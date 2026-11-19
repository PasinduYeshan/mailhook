package server

import (
	"encoding/json"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Mail is a captured SMTP message after MIME parsing.
type Mail struct {
	ID          string            `json:"id"`
	Time        time.Time         `json:"time"`
	From        string            `json:"from"` // envelope MAIL FROM
	To          []string          `json:"to"`   // envelope RCPT TO (includes Bcc)
	Subject     string            `json:"subject"`
	Headers     map[string]string `json:"headers"`        // decoded; repeated keys joined
	Body        string            `json:"body"`           // preferred display body (text if present, else html)
	Text        string            `json:"text,omitempty"` // text/plain part (decoded)
	HTML        string            `json:"html,omitempty"` // text/html part (decoded)
	Raw         string            `json:"raw"`
	Attachments []Attachment      `json:"attachments,omitempty"`
	Links       []Link            `json:"links,omitempty"` // URLs in the body, anchors first
	Codes       []string          `json:"codes,omitempty"` // likely one-time codes

	parts [][]byte // attachment bytes, indexed like Attachments
}

// Attachment describes a non-body MIME part; fetch it at /mail/{id}/part/{index}.
type Attachment struct {
	Index       int    `json:"index"`
	Filename    string `json:"filename,omitempty"`
	ContentType string `json:"content_type"`
	Size        int    `json:"size"`
	ContentID   string `json:"content_id,omitempty"`
	Inline      bool   `json:"inline,omitempty"`
}

// Hook is a captured HTTP request.
type Hook struct {
	ID      string              `json:"id"`
	Time    time.Time           `json:"time"`
	Method  string              `json:"method"`
	Path    string              `json:"path"`
	Query   string              `json:"query"`
	Headers map[string][]string `json:"headers"`
	Body    string              `json:"body"`
	Remote  string              `json:"remote"`
	Status  int                 `json:"status,omitempty"` // status mailhook answered with
	Rule    string              `json:"rule,omitempty"`   // id of the rule that answered
	SMS     *SMS                `json:"sms,omitempty"`    // message, if the body looks like one
	Links   []Link              `json:"links,omitempty"`
	Codes   []string            `json:"codes,omitempty"`
}

// Rule is a Beeceptor-style mock response. Incoming hook requests matching
// Method+Path get this response instead of the default. Captures still happen.
//
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

// Event is the wire format used by /tail and the in-process pub/sub bus.
// Captures have no Op. Ops ("delete", "clear", "change") only reach
// subscribers that ask for them (/tail?ops=1), so older readers never see them.
type Event struct {
	Kind string          `json:"kind"` // "mail" | "hook" | "rule"
	Op   string          `json:"op,omitempty"`
	Data json.RawMessage `json:"data"`
}

// store is the in-memory ring buffer that holds captures and rules.
type store struct {
	mu       sync.RWMutex
	mails    []Mail
	hooks    []Hook
	rules    []Rule
	capacity int
	subs     map[chan Event]bool // value: wants ops
}

func newStore(capacity int) *store {
	return &store{
		capacity: capacity,
		subs:     map[chan Event]bool{},
	}
}

func newID() string { return strconv.FormatInt(time.Now().UnixNano(), 36) }

// --- mail ---

func (s *store) addMail(m Mail) {
	s.mu.Lock()
	s.mails = append(s.mails, m)
	if len(s.mails) > s.capacity {
		s.mails = s.mails[len(s.mails)-s.capacity:]
	}
	s.mu.Unlock()
	s.publish("mail", m)
}

func (s *store) mailList() []Mail {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Mail, len(s.mails))
	copy(out, s.mails)
	return out
}

func (s *store) mailByID(id string) (Mail, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, m := range s.mails {
		if m.ID == id {
			return m, true
		}
	}
	return Mail{}, false
}

func (s *store) deleteMail(id string) bool {
	s.mu.Lock()
	ok := false
	for i, m := range s.mails {
		if m.ID == id {
			s.mails = append(s.mails[:i], s.mails[i+1:]...)
			ok = true
			break
		}
	}
	s.mu.Unlock()
	if ok {
		s.publishOp("mail", "delete", id)
	}
	return ok
}

func (s *store) clearMails() {
	s.mu.Lock()
	s.mails = nil
	s.mu.Unlock()
	s.publishOp("mail", "clear", "")
}

// --- hook ---

func (s *store) addHook(h Hook) {
	s.mu.Lock()
	s.hooks = append(s.hooks, h)
	if len(s.hooks) > s.capacity {
		s.hooks = s.hooks[len(s.hooks)-s.capacity:]
	}
	s.mu.Unlock()
	s.publish("hook", h)
}

func (s *store) hookList() []Hook {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Hook, len(s.hooks))
	copy(out, s.hooks)
	return out
}

func (s *store) hookByID(id string) (Hook, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, h := range s.hooks {
		if h.ID == id {
			return h, true
		}
	}
	return Hook{}, false
}

func (s *store) deleteHook(id string) bool {
	s.mu.Lock()
	ok := false
	for i, h := range s.hooks {
		if h.ID == id {
			s.hooks = append(s.hooks[:i], s.hooks[i+1:]...)
			ok = true
			break
		}
	}
	s.mu.Unlock()
	if ok {
		s.publishOp("hook", "delete", id)
	}
	return ok
}

func (s *store) clearHooks() {
	s.mu.Lock()
	s.hooks = nil
	s.mu.Unlock()
	s.publishOp("hook", "clear", "")
}

// --- rule ---

func (s *store) addRule(r Rule) Rule {
	defer s.publishOp("rule", "change", "")
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

func (s *store) updateRule(r Rule) bool {
	defer s.publishOp("rule", "change", "")
	s.mu.Lock()
	defer s.mu.Unlock()
	for i, existing := range s.rules {
		if existing.ID == r.ID {
			r.Created = existing.Created
			if r.Status == 0 {
				r.Status = http.StatusOK
			}
			s.rules[i] = r
			return true
		}
	}
	return false
}

func (s *store) deleteRule(id string) bool {
	defer s.publishOp("rule", "change", "")
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

func (s *store) clearRules() {
	s.mu.Lock()
	s.rules = nil
	s.mu.Unlock()
	s.publishOp("rule", "change", "")
}

func (s *store) ruleList() []Rule {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Rule, len(s.rules))
	copy(out, s.rules)
	return out
}

func (s *store) ruleByID(id string) (Rule, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, r := range s.rules {
		if r.ID == id {
			return r, true
		}
	}
	return Rule{}, false
}

// matchRule returns the newest rule matching method+path, or nil.
func (s *store) matchRule(method, path string) *Rule {
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

// --- pub/sub ---

func (s *store) subscribe(ops bool) (chan Event, func()) {
	ch := make(chan Event, 64)
	s.mu.Lock()
	s.subs[ch] = ops
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

func (s *store) publish(kind string, payload any) {
	b, err := json.Marshal(payload)
	if err != nil {
		return
	}
	s.send(Event{Kind: kind, Data: b})
}

func (s *store) publishOp(kind, op, id string) {
	b, _ := json.Marshal(map[string]string{"id": id})
	s.send(Event{Kind: kind, Op: op, Data: b})
}

func (s *store) send(e Event) {
	s.mu.RLock()
	for c, ops := range s.subs {
		if e.Op != "" && !ops {
			continue
		}
		select {
		case c <- e:
		default:
		}
	}
	s.mu.RUnlock()
}
