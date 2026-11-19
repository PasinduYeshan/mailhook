// Package server runs the SMTP catcher, HTTP webhook receiver, and Query API
// in a single process. Public surface: Run, Config, Mail, Hook, Rule, Event.

package server

import "strings"

// Defaults sit in the quiet 77xx block, below every OS ephemeral range and
// clear of common dev tools (Mailpit/MailHog 1025/8025, Couchbase 8091).
const (
	DefaultSMTPAddr = "127.0.0.1:7725"
	DefaultHookAddr = "127.0.0.1:7790"
	DefaultAPIAddr  = "127.0.0.1:7791"
	DefaultCap      = 500
)

// NormalizeAddr turns a bare port ("2525") into "127.0.0.1:2525".
func NormalizeAddr(s string) string {
	if s != "" && strings.Trim(s, "0123456789") == "" {
		return "127.0.0.1:" + s
	}
	return s
}
