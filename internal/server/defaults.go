// Package server runs the SMTP catcher, HTTP webhook receiver, and Query API
// in a single process. Public surface: Run, Config, Mail, Hook, Rule, Event.

package server

const (
	DefaultSMTPAddr = "127.0.0.1:1025"
	DefaultHookAddr = "127.0.0.1:8090"
	DefaultAPIAddr  = "127.0.0.1:8091"
	DefaultCap      = 500
)
