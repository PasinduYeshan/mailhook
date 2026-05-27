// Package cli implements the user-facing subcommands (mail, hook, rule, tail,
// serve). Read commands talk to the running daemon over HTTP; the `Serve`
// command starts the daemon in the foreground.

package cli

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"

	"mailhook/internal/server"
)

// apiURL is the Query API base URL the CLI talks to.
func apiURL() string {
	if v := os.Getenv("MAILHOOK_API"); v != "" {
		return strings.TrimRight(v, "/")
	}
	return "http://" + server.DefaultAPIAddr
}

func httpGet(path string, out any) error {
	resp, err := http.Get(apiURL() + path)
	if err != nil {
		return fmt.Errorf("api unreachable (is `mailhook start` running?): %w", err)
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

func httpPostJSON(path string, body any) (*http.Response, error) {
	b, err := json.Marshal(body)
	if err != nil {
		return nil, err
	}
	resp, err := http.Post(apiURL()+path, "application/json", bytes.NewReader(b))
	if err != nil {
		return nil, fmt.Errorf("api unreachable (is `mailhook start` running?): %w", err)
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
		return nil, fmt.Errorf("api unreachable (is `mailhook start` running?): %w", err)
	}
	return resp, nil
}

func httpDelete(path string) error {
	req, _ := http.NewRequest(http.MethodDelete, apiURL()+path, nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("api unreachable (is `mailhook start` running?): %w", err)
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

// stringSlice is a repeatable flag value (used by `rule add -H ...`).
type stringSlice []string

func (s *stringSlice) String() string     { return strings.Join(*s, ",") }
func (s *stringSlice) Set(v string) error { *s = append(*s, v); return nil }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

func orStar(s string) string {
	if s == "" {
		return "*"
	}
	return s
}
