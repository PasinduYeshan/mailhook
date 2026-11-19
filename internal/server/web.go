package server

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"mime"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"
)

//go:embed ui.html
var uiHTML []byte

// ---------------------------------------------------------------------------
// Webhook receiver
// ---------------------------------------------------------------------------

func runHook(ctx context.Context, ln net.Listener, st *store) error {
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
			Status:  http.StatusOK,
		}
		enrichHook(&h)

		// Beeceptor-style: if a rule matches, serve its configured response.
		rule := st.matchRule(r.Method, r.URL.Path)
		if rule != nil {
			h.Rule = rule.ID
			if rule.Status != 0 {
				h.Status = rule.Status
			}
		}
		st.addHook(h)

		if rule != nil {
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
			w.WriteHeader(h.Status)
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
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shCtx)
	}()
	log.Printf("hook  listening on %s", ln.Addr())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// ---------------------------------------------------------------------------
// Query API (also serves the Web UI)
// ---------------------------------------------------------------------------

func runAPI(ctx context.Context, ln net.Listener, cfg Config, st *store) error {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprintln(w, "ok")
	})
	page := strings.NewReplacer(
		"{{SMTP}}", portOf(cfg.SMTPAddr),
		"{{HOOK}}", portOf(cfg.HookAddr),
		"{{API}}", portOf(cfg.APIAddr),
		"{{SMTP_ADDR}}", localAddr(cfg.SMTPAddr),
		"{{HOOK_URL}}", "http://"+localAddr(cfg.HookAddr),
		"{{CAP}}", strconv.Itoa(cfg.Cap),
	).Replace(string(uiHTML))
	serveUI := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		io.WriteString(w, page)
	}
	mux.HandleFunc("GET /ui", serveUI)
	mux.HandleFunc("GET /{$}", serveUI)

	// --- mail ---
	mux.HandleFunc("GET /mail", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, st.mailList())
	})
	mux.HandleFunc("GET /mail/{id}", func(w http.ResponseWriter, r *http.Request) {
		if m, ok := st.mailByID(r.PathValue("id")); ok {
			writeJSON(w, m)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("GET /mail/{id}/eml", func(w http.ResponseWriter, r *http.Request) {
		m, ok := st.mailByID(r.PathValue("id"))
		if !ok {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "message/rfc822")
		w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": m.ID + ".eml"}))
		io.WriteString(w, m.Raw)
	})
	mux.HandleFunc("GET /mail/{id}/part/{n}", func(w http.ResponseWriter, r *http.Request) {
		m, ok := st.mailByID(r.PathValue("id"))
		n, err := strconv.Atoi(r.PathValue("n"))
		if !ok || err != nil || n < 0 || n >= len(m.parts) {
			http.NotFound(w, r)
			return
		}
		a := m.Attachments[n]
		disp := "inline"
		if r.URL.Query().Has("download") {
			disp = "attachment"
		}
		if a.Filename != "" {
			disp = mime.FormatMediaType(disp, map[string]string{"filename": a.Filename})
		}
		w.Header().Set("Content-Type", a.ContentType)
		w.Header().Set("Content-Disposition", disp)
		// Parts come from untrusted mail: never let them run script on this origin.
		w.Header().Set("Content-Security-Policy", "sandbox")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Write(m.parts[n])
	})
	mux.HandleFunc("DELETE /mail", func(w http.ResponseWriter, r *http.Request) {
		st.clearMails()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /mail/{id}", func(w http.ResponseWriter, r *http.Request) {
		if st.deleteMail(r.PathValue("id")) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})

	// --- hook ---
	mux.HandleFunc("GET /hook", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, st.hookList())
	})
	mux.HandleFunc("GET /hook/{id}", func(w http.ResponseWriter, r *http.Request) {
		if h, ok := st.hookByID(r.PathValue("id")); ok {
			writeJSON(w, h)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("DELETE /hook", func(w http.ResponseWriter, r *http.Request) {
		st.clearHooks()
		w.WriteHeader(http.StatusNoContent)
	})
	mux.HandleFunc("DELETE /hook/{id}", func(w http.ResponseWriter, r *http.Request) {
		if st.deleteHook(r.PathValue("id")) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})

	// --- rule ---
	mux.HandleFunc("GET /rule", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, st.ruleList())
	})
	mux.HandleFunc("GET /rule/{id}", func(w http.ResponseWriter, r *http.Request) {
		if rl, ok := st.ruleByID(r.PathValue("id")); ok {
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
			http.Error(w, `rule.path is required (use "*" for any)`, http.StatusBadRequest)
			return
		}
		rl = st.addRule(rl)
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
		if !st.updateRule(rl) {
			http.NotFound(w, r)
			return
		}
		updated, _ := st.ruleByID(rl.ID)
		writeJSON(w, updated)
	})
	mux.HandleFunc("DELETE /rule/{id}", func(w http.ResponseWriter, r *http.Request) {
		if st.deleteRule(r.PathValue("id")) {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		http.NotFound(w, r)
	})
	mux.HandleFunc("DELETE /rule", func(w http.ResponseWriter, r *http.Request) {
		st.clearRules()
		w.WriteHeader(http.StatusNoContent)
	})

	// --- live tail (NDJSON) ---
	mux.HandleFunc("GET /tail", func(w http.ResponseWriter, r *http.Request) {
		kind := r.URL.Query().Get("kind")
		ops := r.URL.Query().Get("ops") == "1"
		flusher, ok := w.(http.Flusher)
		if !ok {
			http.Error(w, "streaming unsupported", http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", "application/x-ndjson")
		w.WriteHeader(http.StatusOK)
		flusher.Flush()

		ch, cancel := st.subscribe(ops)
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
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		<-ctx.Done()
		shCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		srv.Shutdown(shCtx)
	}()
	log.Printf("api   listening on %s", ln.Addr())
	if err := srv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return err
	}
	return nil
}

// localAddr swaps a wildcard host for loopback so the address is dialable.
func localAddr(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if host == "" || host == "0.0.0.0" || host == "::" {
		host = "127.0.0.1"
	}
	return net.JoinHostPort(host, port)
}

// portOf returns ":port" for display.
func portOf(addr string) string {
	if _, p, err := net.SplitHostPort(addr); err == nil {
		return ":" + p
	}
	return addr
}

func writeJSON(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(v)
}
