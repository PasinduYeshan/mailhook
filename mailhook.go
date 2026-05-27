// mailhook — minimal SMTP catcher + HTTP webhook inspector.
//
// Single process exposes three TCP listeners:
//   SMTP receiver  (default 127.0.0.1:1025) — catches outgoing mail from apps under test.
//   Hook receiver  (default 127.0.0.1:8090) — catches arbitrary HTTP requests.
//   Query API      (default 127.0.0.1:8091) — JSON read/stream interface used by the CLI.
//
// Captured items live in an in-memory ring buffer (default cap 500 each).
// Stdlib only. No UI. Designed for agents/scripts: `-j` flag emits JSON;
// `tail` streams NDJSON over chunked HTTP.

package main

import (
	"bufio"
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net"
	"net/http"
	"net/mail"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"text/tabwriter"
	"time"
)

//go:embed ui.html
var uiHTML []byte

const (
	defaultSMTPAddr = "127.0.0.1:1025"
	defaultHookAddr = "127.0.0.1:8090"
	defaultAPIAddr  = "127.0.0.1:8091"
	defaultCap      = 500
)

// ---------------------------------------------------------------------------
// Models
// ---------------------------------------------------------------------------

type Mail struct {
	ID      string            `json:"id"`
	Time    time.Time         `json:"time"`
	From    string            `json:"from"`
	To      []string          `json:"to"`
	Subject string            `json:"subject"`
	Headers map[string]string `json:"headers"`
	Body    string            `json:"body"`           // preferred display body (text if present, else html)
	Text    string            `json:"text,omitempty"` // text/plain part (decoded)
	HTML    string            `json:"html,omitempty"` // text/html part (decoded)
	Raw     string            `json:"raw"`
}

type Hook struct {
	ID      string              `json:"id"`
	Time    time.Time           `json:"time"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   string              `json:"query"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
	Remote  string              `json:"remote"`
}

// Rule describes a Beeceptor-style mock response: incoming requests matching
// Method+Path get the configured response instead of mailhook's default
// `{"id":..,"received":true}`. Captures still happen unconditionally.
//
// Matching rules:
//   - Method "" or "*" matches any method (case-insensitive otherwise).
//   - Path   "" or "*" matches any path. Trailing "/*" matches any subpath.
//   - Newest-added rule wins on tie.
type Rule struct {
	ID      string            `json:"id"`
	Created time.Time         `json:"created"`
	Method  string            `json:"method"`
	Path    string            `json:"path"`
	Status  int               `json:"status"`
	Headers map[string]string `json:"headers,omitempty"`
	Body    string            `json:"body,omitempty"`
	DelayMs int               `json:"delay_ms,omitempty"`
}

type Event struct {
	Kind string          `json:"kind"` // "mail" | "hook"
	Data json.RawMessage `json:"data"`
}

// ---------------------------------------------------------------------------
// Store — in-memory ring buffer + pub/sub for live tail.
// ---------------------------------------------------------------------------

type Store struct {
	mu       sync.RWMutex
	mails    []Mail
	hooks    []Hook
	rules    []Rule
	capacity int
	subs     map[chan Event]struct{}
}

func NewStore(capacity int) *Store {
	return &Store{
		capacity: capacity,
		subs:     map[chan Event]struct{}{},
	}
}

func (s *Store) AddMail(m Mail) {
	s.mu.Lock()
	s.mails = append(s.mails, m)
	if len(s.mails) > s.capacity {
		s.mails = s.mails[len(s.mails)-s.capacity:]
	}
	s.mu.Unlock()
	s.publish("mail", m)
}

func (s *Store) AddHook(h Hook) {
	s.mu.Lock()
	s.hooks = append(s.hooks, h)
	if len(s.hooks) > s.capacity {
		s.hooks = s.hooks[len(s.hooks)-s.capacity:]
	}
	s.mu.Unlock()
	s.publish("hook", h)
}

func (s *Store) Mails() []Mail {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Mail, len(s.mails))
	copy(out, s.mails)
	return out
}

func (s *Store) Hooks() []Hook {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Hook, len(s.hooks))
	copy(out, s.hooks)
	return out
}

func (s *Store) MailByID(id string) (Mail, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.mails {
		if m.ID == id {
			return m, true
		}
	}
	return Mail{}, false
}

func (s *Store) HookByID(id string) (Hook, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, h := range s.hooks {
		if h.ID == id {
			return h, true
		}
	}
	return Hook{}, false
}

func (s *Store) ClearMails() { s.mu.Lock(); s.mails = nil; s.mu.Unlock() }
func (s *Store) ClearHooks() { s.mu.Lock(); s.hooks = nil; s.mu.Unlock() }

func (s *Store) DeleteMail(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, m := range s.mails {
		if m.ID == id {
			s.mails = append(s.mails[:i], s.mails[i+1:]...)
			return true
		}
	}
	return false
}

func (s *Store) DeleteHook(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, h := range s.hooks {
		if h.ID == id {
			s.hooks = append(s.hooks[:i], s.hooks[i+1:]...)
			return true
		}
	}
	return false
}

func (s *Store) AddRule(r Rule) Rule {
	s.mu.Lock()
	defer s.mu.Unlock()
	r.ID = newID()
	r.Created = time.Now().UTC()
	if r.Status == 0 {
		r.Status = http.StatusOK
	}
	s.rules = append(s.rules, r)
	return r
}

func (s *Store) UpdateRule(r Rule) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.rules {
		if existing.ID == r.ID {
			r.Created = existing.Created // preserve
			if r.Status == 0 {
				r.Status = http.StatusOK
			}
			s.rules[i] = r
			return true
		}
	}
	return false
}

func (s *Store) DeleteRule(id string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, r := range s.rules {
		if r.ID == id {
			s.rules = append(s.rules[:i], s.rules[i+1:]...)
			return true
		}
	}
	return false
}

func (s *Store) ClearRules() { s.mu.Lock(); s.rules = nil; s.mu.Unlock() }

func (s *Store) Rules() []Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Rule, len(s.rules))
	copy(out, s.rules)
	return out
}

func (s *Store) RuleByID(id string) (Rule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// MatchRule returns the newest rule that matches method+path, or nil.
func (s *Store) MatchRule(method, path string) *Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for i := len(s.rules) - 1; i >= 0; i-- {
		r := s.rules[i]
		if !methodMatches(r.Method, method) {
			continue
		}
		if !pathMatches(r.Path, path) {
			continue
		}
		matched := r
		return &matched
	}
	return nil
}

func methodMatches(pattern, actual string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	return strings.EqualFold(pattern, actual)
}

func pathMatches(pattern, actual string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "/*") {
		prefix := strings.TrimSuffix(pattern, "/*")
		return actual == prefix || strings.HasPrefix(actual, prefix+"/")
	}
	return pattern == actual
}

func (s *Store) Subscribe() (chan Event, func()) {
	ch := make(chan Event, 32)
	s.mu.Lock()
	s.subs[ch] = struct{}{}
	s.mu.Unlock()
	cancel := func() {
		s.mu.Lock()
		if _, ok := s.subs[ch]; ok {
			delete(s.subs, ch)
			close(ch)
		}
		s.mu.Unlock()
	}
	return ch, cancel
}

// publish serializes payload and fans out to subscribers. Held under RLock so
// concurrent Subscribe/cancel (write-lock) cannot close a channel mid-send.
func (s *Store) publish(kind string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	e := Event{Kind: kind, Data: b}
	s.mu.RLock()
	for c := range s.subs {
		select {
		case c <- e:
		default:
		}
	}
	s.mu.RUnlock()
}

func newID() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

// ---------------------------------------------------------------------------
// SMTP receiver
// ---------------------------------------------------------------------------

func runSMTP(ctx context.Context, addr string, store *Store) error {
	var lc net.ListenConfig
	ln, err := lc.Listen(ctx, "tcp", addr)
	if err != nil {
		return fmt.Errorf("smtp listen: %w", err)
	}
	go func() { <-ctx.Done(); ln.Close() }()
	log.Printf("smtp  listening on %s", addr)
	for {
		conn, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			continue
		}
		go handleSMTP(conn, store)
	}
}

func handleSMTP(c net.Conn, store *Store) {
	defer c.Close()
	c.SetDeadline(time.Now().Add(5 * time.Minute))
	br := bufio.NewReader(c)
	bw := bufio.NewWriter(c)

	send := func(line string) {
		bw.WriteString(line)
		bw.WriteString("\r\n")
		bw.Flush()
	}

	send("220 mailhook SMTP ready")

	var from string
	var rcpts []string

	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		upper := strings.ToUpper(line)

		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			send("250 OK")
		case strings.HasPrefix(upper, "MAIL FROM:"):
			from = extractAddr(line[10:])
			send("250 OK")
		case strings.HasPrefix(upper, "RCPT TO:"):
			rcpts = append(rcpts, extractAddr(line[8:]))
			send("250 OK")
		case upper == "DATA":
			send("354 End data with <CR><LF>.<CR><LF>")
			raw, err := readSMTPData(br)
			if err != nil {
				return
			}
			m := parseMail(from, rcpts, raw)
			store.AddMail(m)
			send("250 OK queued " + m.ID)
			from = ""
			rcpts = nil
		case upper == "RSET":
			from = ""
			rcpts = nil
			send("250 OK")
		case upper == "NOOP":
			send("250 OK")
		case upper == "QUIT":
			send("221 Bye")
			return
		default:
			// Accept and ignore unknown verbs — we are a sink, not a real MTA.
			send("250 OK")
		}
	}
}

func extractAddr(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.Index(s, "<"); i >= 0 {
		if j := strings.Index(s[i:], ">"); j > 0 {
			return s[i+1 : i+j]
		}
	}
	return s
}

func readSMTPData(br *bufio.Reader) (string, error) {
	var buf bytes.Buffer
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			return "", err
		}
		if line == ".\r\n" || line == ".\n" {
			break
		}
		// RFC 5321 dot-stuffing: leading "." on data line is escape for ".".
		if strings.HasPrefix(line, "..") {
			line = line[1:]
		}
		buf.WriteString(line)
	}
	return buf.String(), nil
}

func parseMail(from string, to []string, raw string) Mail {
	m := Mail{
		ID:      newID(),
		Time:    time.Now().UTC(),
		From:    from,
		To:      to,
		Raw:     raw,
		Headers: map[string]string{},
	}
	msg, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		m.Body = raw
		return m
	}
	for k, v := range msg.Header {
		m.Headers[k] = strings.Join(v, ", ")
	}
	m.Subject = decodeMIMEHeader(msg.Header.Get("Subject"))

	ct := msg.Header.Get("Content-Type")
	mediaType, params, _ := mime.ParseMediaType(ct)
	collectMIMEParts(msg.Body, mediaType, params, msg.Header.Get("Content-Transfer-Encoding"), &m)

	// Choose a single "body" for backwards-compatible display:
	// prefer plain text; fall back to HTML; finally the undecoded body.
	switch {
	case m.Text != "":
		m.Body = m.Text
	case m.HTML != "":
		m.Body = m.HTML
	}
	if m.Body == "" {
		body, _ := io.ReadAll(msg.Body)
		m.Body = string(body)
	}
	return m
}

// collectMIMEParts walks a single part (or recursively into multipart trees)
// and fills m.Text / m.HTML with the first matching part of each type.
func collectMIMEParts(body io.Reader, mediaType string, params map[string]string, transferEnc string, m *Mail) {
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := params["boundary"]
		if boundary == "" {
			return
		}
		mr := multipart.NewReader(body, boundary)
		for {
			p, err := mr.NextPart()
			if err == io.EOF {
				return
			}
			if err != nil {
				return
			}
			pCT := p.Header.Get("Content-Type")
			pMediaType, pParams, _ := mime.ParseMediaType(pCT)
			pEnc := p.Header.Get("Content-Transfer-Encoding")
			if strings.HasPrefix(pMediaType, "multipart/") {
				collectMIMEParts(p, pMediaType, pParams, pEnc, m)
				continue
			}
			raw, _ := io.ReadAll(p)
			decoded := decodeTransfer(pEnc, raw)
			switch {
			case strings.HasPrefix(pMediaType, "text/html") && m.HTML == "":
				m.HTML = string(decoded)
			case strings.HasPrefix(pMediaType, "text/plain") && m.Text == "":
				m.Text = string(decoded)
			}
		}
	}
	// Single-part body.
	raw, _ := io.ReadAll(body)
	decoded := decodeTransfer(transferEnc, raw)
	text := string(decoded)
	switch {
	case strings.HasPrefix(mediaType, "text/html"):
		m.HTML = text
	case strings.HasPrefix(mediaType, "text/plain"), mediaType == "":
		m.Text = text
	}
}

func decodeTransfer(enc string, body []byte) []byte {
	switch strings.ToLower(strings.TrimSpace(enc)) {
	case "quoted-printable":
		if decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body))); err == nil {
			return decoded
		}
	case "base64":
		cleaned := strings.Map(func(r rune) rune {
			if r == '\n' || r == '\r' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(body))
		if decoded, err := base64.StdEncoding.DecodeString(cleaned); err == nil {
			return decoded
		}
	}
	return body
}

func decodeMIMEHeader(s string) string {
	dec := new(mime.WordDecoder)
	if out, err := dec.DecodeHeader(s); err == nil {
		return out
	}
	return s
}

// ---------------------------------------------------------------------------
// Hook (HTTP) receiver
// ---------------------------------------------------------------------------

func runHook(ctx context.Context, addr string, store *Store) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(http.MaxBytesReader(w, r.Body, 5<<20)) // 5 MiB cap
		r.Body.Close()
		h := Hook{
			ID:      newID(),
			Time:    time.Now().UTC(),
			Method:  r.Method,
			Path:    r.URL.Path,
			Query:   r.URL.RawQuery,
			Headers: r.Header,
			Body:    string(body),
			Remote:  r.RemoteAddr,
		}
		store.AddHook(h)

		// Beeceptor-style: if a rule matches, serve its configured response.
		if rule := store.MatchRule(r.Method, r.URL.Path); rule != nil {
			if rule.DelayMs > 0 {
				time.Sleep(time.Duration(rule.DelayMs) * time.Millisecond)
			}
			for k, v := range rule.Headers {
				w.Header().Set(k, v)
			}
			if w.Header().Get("Content-Type") == "" && rule.Body != "" {
				trimmed := strings.TrimSpace(rule.Body)
				if strings.HasPrefix(trimmed, "{") || strings.HasPrefix(trimmed, "[") {
					w.Header().Set("Content-Type", "application/json")
				} else {
					w.Header().Set("Content-Type", "text/plain; charset=utf-8")
				}
			}
			w.Header().Set("X-Mailhook-Id", h.ID)
			w.Header().Set("X-Mailhook-Rule", rule.ID)
			status := rule.Status
			if status == 0 {
				status = http.StatusOK
			}
			w.WriteHeader(status)
			io.WriteString(w, rule.Body)
			return
		}

		// Default response when no rule matches.
		w.Header().Set("Content-Type", "application/json")
		w.Header().Set("X-Mailhook-Id", h.ID)
		w.WriteHeader(http.StatusOK)
		fmt.Fprintf(w, `{"id":%q,"received":true}`+"\n", h.ID)
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shCtx)
	}()
	log.Printf("hook  listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Query API (CLI talks to this over localhost)
// ---------------------------------------------------------------------------

func runAPI(ctx context.Context, addr string, store *Store) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	serveUI := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Write(uiHTML)
	}
	mux.HandleFunc("GET /ui", serveUI)
	mux.HandleFunc("GET /{$}", serveUI)
	mux.HandleFunc("GET /mail", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, store.Mails())
	})
	mux.HandleFunc("GET /mail/{id}", func(w http.ResponseWriter, r *http.Request) {
		if m, ok := store.MailByID(r.PathValue("id")); ok {
			writeJSON(w, m)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("DELETE /mail", func(w http.ResponseWriter, r *http.Request) {
		store.ClearMails()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /mail/{id}", func(w http.ResponseWriter, r *http.Request) {
		if store.DeleteMail(r.PathValue("id")) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /hook", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, store.Hooks())
	})
	mux.HandleFunc("GET /hook/{id}", func(w http.ResponseWriter, r *http.Request) {
		if h, ok := store.HookByID(r.PathValue("id")); ok {
			writeJSON(w, h)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("DELETE /hook", func(w http.ResponseWriter, r *http.Request) {
		store.ClearHooks()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /hook/{id}", func(w http.ResponseWriter, r *http.Request) {
		if store.DeleteHook(r.PathValue("id")) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /rule", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, store.Rules())
	})
	mux.HandleFunc("GET /rule/{id}", func(w http.ResponseWriter, r *http.Request) {
		if rl, ok := store.RuleByID(r.PathValue("id")); ok {
			writeJSON(w, rl)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("POST /rule", func(w http.ResponseWriter, r *http.Request) {
		var rl Rule
		if err := json.NewDecoder(r.Body).Decode(&rl); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		if rl.Path == "" {
			http.Error(w, "rule.path is required (use \"*\" for any)", http.StatusBadRequest)
			return
		}
		rl = store.AddRule(rl)
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		json.NewEncoder(w).Encode(rl)
	})
	mux.HandleFunc("PUT /rule/{id}", func(w http.ResponseWriter, r *http.Request) {
		var rl Rule
		if err := json.NewDecoder(r.Body).Decode(&rl); err != nil {
			http.Error(w, "bad json: "+err.Error(), http.StatusBadRequest)
			return
		}
		rl.ID = r.PathValue("id")
		if !store.UpdateRule(rl) {
			http.NotFound(w, r)
			return
		}
		updated, _ := store.RuleByID(rl.ID)
		writeJSON(w, updated)
	})
	mux.HandleFunc("DELETE /rule/{id}", func(w http.ResponseWriter, r *http.Request) {
		if store.DeleteRule(r.PathValue("id")) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("DELETE /rule", func(w http.ResponseWriter, r *http.Request) {
		store.ClearRules()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("GET /tail", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		ch, cancel := store.Subscribe()
		defer cancel()
		enc := json.NewEncoder(w)

		for {
			select {
			case <-r.Context().Done():
				return
			case e, ok := <-ch:
				if !ok {
					return
				}
				if kind != "" && kind != "all" && kind != e.Kind {
					continue
				}
				if err := enc.Encode(e); err != nil {
					return
				}
				flusher.Flush()
			}
		}
	})

	srv := &http.Server{
		Addr:              addr,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shCtx)
	}()
	log.Printf("api   listening on %s", addr)
	if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}

// ---------------------------------------------------------------------------
// CLI client
// ---------------------------------------------------------------------------

func apiURL() string {
	if v := os.Getenv("MAILHOOK_API"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://" + defaultAPIAddr
}

func httpGet(path string, out any) error {
	resp, err := http.Get(apiURL() + path)
	if err != nil {
		return fmt.Errorf("api unreachable (is `mailhook serve` running?): %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("not found")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("api: %s", resp.Status)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// stringSlice is a repeatable flag value used for -H headers on `rule add`.
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func httpPostJSON(path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := http.Post(apiURL()+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("api unreachable (is `mailhook serve` running?): %w", err)
	}
	return resp, nil
}

func httpPutJSON(path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	req, _ := http.NewRequest(http.MethodPut, apiURL()+path, bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("api unreachable (is `mailhook serve` running?): %w", err)
	}
	return resp, nil
}

func httpDelete(path string) error {
	req, _ := http.NewRequest(http.MethodDelete, apiURL()+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("api unreachable (is `mailhook serve` running?): %w", err)
	}
	resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("not found")
	}
	if resp.StatusCode >= 400 {
		return fmt.Errorf("api: %s", resp.Status)
	}
	return nil
}

func cmdServe(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	smtpAddr := fs.String("smtp", defaultSMTPAddr, "SMTP listen address")
	hookAddr := fs.String("hook", defaultHookAddr, "Webhook receiver listen address")
	apiAddr := fs.String("api", defaultAPIAddr, "Query API listen address")
	capacity := fs.Int("cap", defaultCap, "Per-stream ring-buffer capacity")
	fs.Parse(args)

	store := NewStore(*capacity)
	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	errCh := make(chan error, 3)
	go func() { errCh <- runSMTP(ctx, *smtpAddr, store) }()
	go func() { errCh <- runHook(ctx, *hookAddr, store) }()
	go func() { errCh <- runAPI(ctx, *apiAddr, store) }()

	var firstErr error
	for i := 0; i < 3; i++ {
		if err := <-errCh; err != nil && firstErr == nil {
			firstErr = err
			cancel() // trigger shutdown of the other two
		}
	}
	return firstErr
}

func cmdMail(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mailhook mail {list|show <id>|delete <id>|clear}")
	}
	switch args[0] {
	case "list", "ls":
		fs := flag.NewFlagSet("mail-list", flag.ExitOnError)
		asJSON := fs.Bool("j", false, "JSON output")
		fs.Parse(args[1:])
		var mails []Mail
		if err := httpGet("/mail", &mails); err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(mails)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tTIME\tFROM\tTO\tSUBJECT")
		for _, m := range mails {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\n",
				m.ID, m.Time.Local().Format("15:04:05"), m.From,
				strings.Join(m.To, ","), truncate(m.Subject, 60))
		}
		return tw.Flush()
	case "show":
		if len(args) < 2 {
			return errors.New("usage: mailhook mail show <id>")
		}
		var m Mail
		if err := httpGet("/mail/"+args[1], &m); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(m)
	case "delete", "rm":
		if len(args) < 2 {
			return errors.New("usage: mailhook mail delete <id>")
		}
		return httpDelete("/mail/" + args[1])
	case "clear":
		return httpDelete("/mail")
	}
	return fmt.Errorf("unknown subcommand: %s", args[0])
}

func cmdHook(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mailhook hook {list|show <id>|delete <id>|clear}")
	}
	switch args[0] {
	case "list", "ls":
		fs := flag.NewFlagSet("hook-list", flag.ExitOnError)
		asJSON := fs.Bool("j", false, "JSON output")
		fs.Parse(args[1:])
		var hooks []Hook
		if err := httpGet("/hook", &hooks); err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(hooks)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tTIME\tMETHOD\tPATH\tQUERY\tBYTES")
		for _, h := range hooks {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%s\t%s\t%d\n",
				h.ID, h.Time.Local().Format("15:04:05"), h.Method,
				truncate(h.Path, 40), truncate(h.Query, 30), len(h.Body))
		}
		return tw.Flush()
	case "show":
		if len(args) < 2 {
			return errors.New("usage: mailhook hook show <id>")
		}
		var h Hook
		if err := httpGet("/hook/"+args[1], &h); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(h)
	case "delete", "rm":
		if len(args) < 2 {
			return errors.New("usage: mailhook hook delete <id>")
		}
		return httpDelete("/hook/" + args[1])
	case "clear":
		return httpDelete("/hook")
	}
	return fmt.Errorf("unknown subcommand: %s", args[0])
}

func cmdRule(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mailhook rule {add|list|show <id>|update <id>|delete <id>|clear}")
	}
	switch args[0] {
	case "add":
		return cmdRuleWrite(args[1:], "")
	case "update":
		if len(args) < 2 {
			return errors.New("usage: mailhook rule update <id> [flags]")
		}
		return cmdRuleWrite(args[2:], args[1])
	case "list", "ls":
		fs := flag.NewFlagSet("rule-list", flag.ExitOnError)
		asJSON := fs.Bool("j", false, "JSON output")
		fs.Parse(args[1:])
		var rules []Rule
		if err := httpGet("/rule", &rules); err != nil {
			return err
		}
		if *asJSON {
			return json.NewEncoder(os.Stdout).Encode(rules)
		}
		tw := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(tw, "ID\tMETHOD\tPATH\tSTATUS\tDELAY\tBODY")
		for _, r := range rules {
			fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%dms\t%s\n",
				r.ID, orStar(r.Method), orStar(r.Path),
				r.Status, r.DelayMs, truncate(r.Body, 50))
		}
		return tw.Flush()
	case "show":
		if len(args) < 2 {
			return errors.New("usage: mailhook rule show <id>")
		}
		var r Rule
		if err := httpGet("/rule/"+args[1], &r); err != nil {
			return err
		}
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(r)
	case "delete", "rm":
		if len(args) < 2 {
			return errors.New("usage: mailhook rule delete <id>")
		}
		return httpDelete("/rule/" + args[1])
	case "clear":
		return httpDelete("/rule")
	}
	return fmt.Errorf("unknown subcommand: %s", args[0])
}

// cmdRuleWrite handles both `rule add` (id="") and `rule update <id>`.
func cmdRuleWrite(args []string, id string) error {
	fs := flag.NewFlagSet("rule-write", flag.ExitOnError)
	method := fs.String("m", "POST", "HTTP method to match (or '*' for any)")
	path := fs.String("p", "", "path to match (e.g. /sms/send, /api/* or '*')")
	status := fs.Int("s", 200, "response status code")
	body := fs.String("b", "", "response body (raw string)")
	delayMs := fs.Int("d", 0, "response delay in milliseconds")
	file := fs.String("f", "", "read full rule JSON from file ('-' for stdin)")
	var headers stringSlice
	fs.Var(&headers, "H", "response header 'Key: Value' (repeatable)")
	fs.Parse(args)

	var rule Rule
	if *file != "" {
		var r io.Reader
		if *file == "-" {
			r = os.Stdin
		} else {
			f, err := os.Open(*file)
			if err != nil {
				return err
			}
			defer f.Close()
			r = f
		}
		if err := json.NewDecoder(r).Decode(&rule); err != nil {
			return fmt.Errorf("bad rule file: %w", err)
		}
	} else {
		rule.Method = *method
		rule.Path = *path
		rule.Status = *status
		rule.Body = *body
		rule.DelayMs = *delayMs
		if len(headers) > 0 {
			rule.Headers = map[string]string{}
			for _, h := range headers {
				k, v, ok := strings.Cut(h, ":")
				if !ok {
					return fmt.Errorf("bad header (need 'Key: Value'): %s", h)
				}
				rule.Headers[strings.TrimSpace(k)] = strings.TrimSpace(v)
			}
		}
	}
	if rule.Path == "" {
		return errors.New("rule path is required (use '-p \"*\"' for any path)")
	}

	var resp *http.Response
	var err error
	if id == "" {
		resp, err = httpPostJSON("/rule", rule)
	} else {
		resp, err = httpPutJSON("/rule/"+id, rule)
	}
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusNotFound {
		return errors.New("not found")
	}
	if resp.StatusCode >= 400 {
		msg, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("api %s: %s", resp.Status, strings.TrimSpace(string(msg)))
	}
	io.Copy(os.Stdout, resp.Body)
	return nil
}

func orStar(s string) string {
	if s == "" {
		return "*"
	}
	return s
}

func cmdTail(args []string) error {
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	kind := fs.String("t", "all", "filter: mail | hook | all")
	asJSON := fs.Bool("j", false, "raw NDJSON (no human formatting)")
	fs.Parse(args)

	resp, err := http.Get(apiURL() + "/tail?kind=" + *kind)
	if err != nil {
		return fmt.Errorf("api unreachable (is `mailhook serve` running?): %w", err)
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	for {
		var e Event
		if err := dec.Decode(&e); err != nil {
			if errors.Is(err, io.EOF) {
				return nil
			}
			return err
		}
		if *asJSON {
			os.Stdout.Write(e.Data)
			os.Stdout.Write([]byte{'\n'})
			continue
		}
		switch e.Kind {
		case "mail":
			var m Mail
			if err := json.Unmarshal(e.Data, &m); err != nil {
				continue
			}
			fmt.Printf("[%s] MAIL %s  %s → %s  %s\n",
				m.Time.Local().Format("15:04:05"), m.ID, m.From,
				strings.Join(m.To, ","), truncate(m.Subject, 60))
		case "hook":
			var h Hook
			if err := json.Unmarshal(e.Data, &h); err != nil {
				continue
			}
			fmt.Printf("[%s] HOOK %s  %s %s  (%dB)\n",
				h.Time.Local().Format("15:04:05"), h.ID, h.Method, h.Path, len(h.Body))
		}
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func usage() {
	fmt.Fprintln(os.Stderr, `mailhook — minimal SMTP catcher + webhook inspector (stdlib only)

Usage:
  mailhook serve [--smtp 127.0.0.1:1025] [--hook 127.0.0.1:8090] [--api 127.0.0.1:8091] [--cap 500]
  mailhook mail  list   [-j]
  mailhook mail  show   <id>
  mailhook mail  delete <id>
  mailhook mail  clear
  mailhook hook  list   [-j]
  mailhook hook  show   <id>
  mailhook hook  delete <id>
  mailhook hook  clear
  mailhook rule  add    [-m METHOD] [-p PATH] [-s STATUS] [-b BODY] [-H 'K: V'] [-d MS] [-f FILE]
  mailhook rule  list   [-j]
  mailhook rule  show   <id>
  mailhook rule  update <id> [same flags as add]
  mailhook rule  delete <id>
  mailhook rule  clear
  mailhook tail         [-t mail|hook|all] [-j]

Env:
  MAILHOOK_API   override the query-API URL (default http://127.0.0.1:8091)

Rules let you mock responses for the webhook receiver (Beeceptor-style):
incoming requests matching method+path get the configured response instead
of the default {"received":true}. Captures still happen regardless.
Path supports '*' (any) and '/prefix/*' (subpath match). Newest rule wins.`)
}

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	case "serve":
		err = cmdServe(os.Args[2:])
	case "mail":
		err = cmdMail(os.Args[2:])
	case "hook":
		err = cmdHook(os.Args[2:])
	case "rule":
		err = cmdRule(os.Args[2:])
	case "tail":
		err = cmdTail(os.Args[2:])
	case "-h", "--help", "help":
		usage()
		return
	default:
		usage()
		os.Exit(2)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}
