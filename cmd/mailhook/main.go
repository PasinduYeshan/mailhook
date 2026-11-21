// mailhook — local SMTP catcher + HTTP webhook inspector.
// Entry point: dispatches to cli or daemon subcommand packages.

package main

import (
	"fmt"
	"log"
	"os"

	"github.com/PasinduYeshan/mailhook/internal/cli"
	"github.com/PasinduYeshan/mailhook/internal/daemon"
)

// Set at release build time via -ldflags "-X main.version=... -X main.commit=... -X main.date=...".
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

func main() {
	log.SetFlags(log.Ltime)
	if len(os.Args) < 2 {
		usage()
		os.Exit(2)
	}
	var err error
	switch os.Args[1] {
	// Daemon lifecycle.
	case "serve":
		err = cli.Serve(os.Args[2:])
	case "start":
		err = daemon.Start(os.Args[2:])
	case "stop":
		err = daemon.Stop(os.Args[2:])
	case "restart":
		err = daemon.Restart(os.Args[2:])
	case "status":
		err = daemon.Status(os.Args[2:])
	case "logs":
		err = daemon.Logs(os.Args[2:])

	// Inspection / control.
	case "mail":
		err = cli.Mail(os.Args[2:])
	case "hook":
		err = cli.Hook(os.Args[2:])
	case "rule":
		err = cli.Rule(os.Args[2:])
	case "tail":
		err = cli.Tail(os.Args[2:])

	case "-v", "--version", "version":
		fmt.Printf("mailhook %s (commit %s, built %s)\n", version, commit, date)
		return

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

func usage() {
	fmt.Fprintln(os.Stderr, `mailhook — local SMTP catcher + HTTP webhook inspector

Default ports:
  SMTP  127.0.0.1:7725
  HOOK  127.0.0.1:7790  (any HTTP method)
  API   127.0.0.1:7791
  UI    http://127.0.0.1:7791/

Daemon lifecycle:
  mailhook start   [serve flags]    start in background
  mailhook stop    [--all]          stop background daemon (--all: every instance)
  mailhook restart [serve flags]    stop then start (keeps current ports)
  mailhook status                   show pid, RSS, CPU, ports
  mailhook logs                     tail -f the daemon log
  mailhook serve   [flags]          run in foreground

Serve flags (address or bare port, e.g. --smtp 2525):
  --smtp ADDR  --hook ADDR  --api ADDR  --cap N

Inspection (talks to a running daemon over HTTP):
  mailhook mail  list  [-j] | show <id> | delete <id> | clear
  mailhook mail  code  [id]   first OTP-like code (default: newest mail)
  mailhook mail  links [id]   URLs in the body, one per line
  mailhook hook  list  [-j] | show <id> | code [id] | links [id] | delete <id> | clear
  mailhook rule  add   [-m METHOD] [-p PATH] [-s STATUS] [-b BODY]
                       [-H 'K: V']  [-d MS]  [-f FILE]
  mailhook rule  list  [-j] | show <id> | update <id> | delete <id> | clear
  mailhook tail        [-t mail|hook|all] [-j]

Other:
  mailhook version                  print version, commit and build date

Docs:
  README.md          install + quick start
  docs/cli.md        full CLI reference
  docs/api.md        HTTP API
  docs/rules.md      mock-response rules
  docs/integration.md  WSO2 IS pack integration`)
}
