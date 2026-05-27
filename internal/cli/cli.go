package cli

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"text/tabwriter"

	"mailhook/internal/server"
)

// ---------------------------------------------------------------------------
// Serve (foreground; used by daemon.Start to spawn the daemon process)
// ---------------------------------------------------------------------------

func Serve(args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	cfg := server.Config{}
	fs.StringVar(&cfg.SMTPAddr, "smtp", server.DefaultSMTPAddr, "SMTP listen address")
	fs.StringVar(&cfg.HookAddr, "hook", server.DefaultHookAddr, "Webhook receiver listen address")
	fs.StringVar(&cfg.APIAddr, "api", server.DefaultAPIAddr, "Query API listen address")
	fs.IntVar(&cfg.Cap, "cap", server.DefaultCap, "Per-stream ring-buffer capacity")
	fs.Parse(args)

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()
	return server.Run(ctx, cfg)
}

// ---------------------------------------------------------------------------
// Mail
// ---------------------------------------------------------------------------

func Mail(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mailhook mail {list|show <id>|delete <id>|clear}")
	}
	switch args[0] {
	case "list", "ls":
		fs := flag.NewFlagSet("mail-list", flag.ExitOnError)
		asJSON := fs.Bool("j", false, "JSON output")
		fs.Parse(args[1:])
		var mails []server.Mail
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
		var m server.Mail
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

// ---------------------------------------------------------------------------
// Hook
// ---------------------------------------------------------------------------

func Hook(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mailhook hook {list|show <id>|delete <id>|clear}")
	}
	switch args[0] {
	case "list", "ls":
		fs := flag.NewFlagSet("hook-list", flag.ExitOnError)
		asJSON := fs.Bool("j", false, "JSON output")
		fs.Parse(args[1:])
		var hooks []server.Hook
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
		var h server.Hook
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

// ---------------------------------------------------------------------------
// Rule
// ---------------------------------------------------------------------------

func Rule(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: mailhook rule {add|list|show <id>|update <id>|delete <id>|clear}")
	}
	switch args[0] {
	case "add":
		return ruleWrite(args[1:], "")
	case "update":
		if len(args) < 2 {
			return errors.New("usage: mailhook rule update <id> [flags]")
		}
		return ruleWrite(args[2:], args[1])
	case "list", "ls":
		fs := flag.NewFlagSet("rule-list", flag.ExitOnError)
		asJSON := fs.Bool("j", false, "JSON output")
		fs.Parse(args[1:])
		var rules []server.Rule
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
		var r server.Rule
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

// ruleWrite handles both `rule add` (id="") and `rule update <id>`.
func ruleWrite(args []string, id string) error {
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

	var rule server.Rule
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

// ---------------------------------------------------------------------------
// Live tail
// ---------------------------------------------------------------------------

func Tail(args []string) error {
	fs := flag.NewFlagSet("tail", flag.ExitOnError)
	kind := fs.String("t", "all", "filter: mail | hook | all")
	asJSON := fs.Bool("j", false, "raw NDJSON (no human formatting)")
	fs.Parse(args)

	resp, err := http.Get(apiURL() + "/tail?kind=" + *kind)
	if err != nil {
		return fmt.Errorf("api unreachable (is `mailhook start` running?): %w", err)
	}
	defer resp.Body.Close()

	dec := json.NewDecoder(resp.Body)
	for {
		var e server.Event
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
			var m server.Mail
			if err := json.Unmarshal(e.Data, &m); err != nil {
				continue
			}
			fmt.Printf("[%s] MAIL %s  %s → %s  %s\n",
				m.Time.Local().Format("15:04:05"), m.ID, m.From,
				strings.Join(m.To, ","), truncate(m.Subject, 60))
		case "hook":
			var h server.Hook
			if err := json.Unmarshal(e.Data, &h); err != nil {
				continue
			}
			fmt.Printf("[%s] HOOK %s  %s %s  (%dB)\n",
				h.Time.Local().Format("15:04:05"), h.ID, h.Method, h.Path, len(h.Body))
		}
	}
}
