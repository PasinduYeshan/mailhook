// mailhook — local SMTP catcher + HTTP webhook inspector.
// Entry point: dispatches to cli or daemon subcommand packages.

package main

import (
	"fmt"
	"log"
	"os"

	"github.com/PasinduYeshan/mailhook/internal/cli"
	"github.com/PasinduYeshan/mailhook/internal/daemon"
	"github.com/PasinduYeshan/mailhook/internal/version"
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

	case "-h", "--help", "help":
		usage()
		return
	case "-v", "--version", "version":
		fmt.Println("mailhook " + version.Version)
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

Daemon lifecycle:
  mailhook start   [serve flags]    start in background
  mailhook stop                     stop background daemon
  mailhook restart [serve flags]    stop then start
  mailhook status                   show pid, RSS, CPU
  mailhook logs                     tail -f the daemon log
  mailhook serve   [flags]          run in foreground

Inspection (talks to a running daemon over HTTP):
  mailhook mail  list  [-j] | show <id> | delete <id> | clear
  mailhook hook  list  [-j] | show <id> | delete <id> | clear
  mailhook rule  add   [-m METHOD] [-p PATH] [-s STATUS] [-b BODY]
                       [-H 'K: V']  [-d MS]  [-f FILE]
  mailhook rule  list  [-j] | show <id> | update <id> | delete <id> | clear
  mailhook tail        [-t mail|hook|all] [-j]
  mailhook version                  print version

Docs:
  README.md          install + quick start
  docs/cli.md        full CLI reference
  docs/api.md        HTTP API
  docs/rules.md      mock-response rules
  docs/integration.md  WSO2 IS pack integration`)
}
