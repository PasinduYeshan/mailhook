# Architecture

Single Go process, three TCP listeners, in-memory ring buffer, optional
mock-rule engine. Stdlib only.

## Process layout

```
mailhook (one process)
  ├─ SMTP listener        :7725   handleSMTP → store.addMail
  ├─ Hook listener        :7790   net/http handler → store.addHook
  │                                          ↘ matchRule → custom response
  └─ Query API + Web UI   :7791   net/http handler
                                  ├─ /mail, /hook, /rule  (REST)
                                  ├─ /tail               (NDJSON stream)
                                  └─ /, /ui              (embedded HTML)
```

Three goroutines, one per listener, started by `server.Run`. All share a
single `*store` value (in-process — no IPC, no DB).

## Storage

In-memory ring buffer:

- `mails []Mail` — capped at `Cap` (default 500)
- `hooks []Hook` — capped at `Cap`
- `rules []Rule` — uncapped (user-managed)
- `subs map[chan Event]struct{}` — live-tail subscribers

A single `sync.RWMutex` protects everything. Hot paths (capture, list) use
RLock; writes (add / delete / clear / sub-cancel) use Lock. Pub/sub fan-out
happens under RLock with non-blocking sends to subscribers — slow / dead
subscribers drop events instead of stalling the producer.

Restarting the daemon wipes the buffer. Nothing is persisted to disk.

## Daemon control

`mailhook start` self-spawns `mailhook serve` detached:

1. Resolve own executable path via `os.Executable`.
2. `exec.Command(exe, "serve", …)` with `Setsid: true` (new session →
   immune to parent shell exit).
3. Redirect stdout/stderr to `~/.local/var/mailhook/log/mailhook.log`.
4. Write child pid to `~/.local/var/mailhook/run/mailhook.pid`.
5. Wait until `serve` writes `run/mailhook.json` (its bound ports), or
   report the log if the child exits first (e.g. a busy port).

`stop` only signals the pid in the pidfile (or `mailhook.json`), and only
after `ps` confirms it is still a `mailhook serve` process, so a stale pid
reused by another program is never killed. It sends `SIGTERM`, waits up to
5 s for exit, then `SIGKILL`s. Other `mailhook serve` processes (other state
dirs, hand-run `serve`) are listed but left alone unless `stop --all`.

## Resource footprint

Measured idle, macOS arm64:

| Metric | Value |
| ------ | ----- |
| Binary | 6.5 MB (stripped, `-s -w` ldflags) |
| RSS    | ~12 MB |
| CPU    | ~0% |
| FDs    | 3 listeners + per-connection |

Almost all RSS is Go runtime (scheduler, GC, goroutine stacks). Capture
ring buffers contribute a few hundred KB at peak.

## Repo layout

```
mailhook/
├── README.md               install + quick start
├── Makefile                build / install / uninstall
├── go.mod                  module declaration (stdlib only)
├── .gitignore
├── cmd/
│   └── mailhook/
│       └── main.go         entry point, subcommand dispatch
├── internal/
│   ├── server/             SMTP + webhook + API + UI (package server)
│   │   ├── defaults.go     default ports / capacity
│   │   ├── store.go        Mail/Hook/Rule types, ring buffer, pub/sub
│   │   ├── smtp.go         SMTP receiver + MIME parsing (charsets, attachments)
│   │   ├── extract.go      links, one-time codes, SMS fields, charset tables
│   │   ├── web.go          webhook receiver, Query API, go:embed UI
│   │   ├── serve.go        Run() — starts the three listeners
│   │   └── ui.html         embedded Web UI
│   ├── cli/                user-facing subcommands (package cli)
│   │   ├── cli.go          mail / hook / rule / tail / serve handlers
│   │   └── client.go       HTTP client helpers, formatting
│   └── daemon/             background-process control (package daemon)
│       └── daemon.go       start / stop / restart / status / logs
├── docs/
│   ├── cli.md              full CLI reference
│   ├── api.md              HTTP API
│   ├── rules.md            mock-rule semantics
│   ├── integration.md      WSO2 IS pack recipes
│   └── architecture.md     this file
└── SKILL.md                agent-facing skill manifest
```

Total Go source: ~2400 lines across 9 files, plus tests for parsing and
extraction. Web UI: ~1100 lines of self-contained HTML/CSS/JS (no
framework, no build step).

The UI renders HTML mail in an iframe with `sandbox="allow-same-origin
allow-popups allow-popups-to-escape-sandbox"`: no script runs, links open
in a new tab, and the parent can size the frame to its content. `cid:`
images are rewritten to `/mail/{id}/part/{n}`, which is served with
`Content-Security-Policy: sandbox` so an attachment can never run script
on the API origin.

## Dependency graph

```
cmd/mailhook → cli  → server
              └→ daemon
```

- `cmd/mailhook` is the thin entry point — args parsing + dispatch.
- `internal/cli` is the user-facing CLI. Imports `internal/server` for
  shared types (`server.Mail`, `server.Rule`, …) and for the `Serve`
  entry point that starts the daemon in foreground.
- `internal/daemon` is process-level glue: spawn / kill / inspect. Has
  no dependency on `internal/server` — it talks to the daemon via the
  binary path and pidfile only.

## Why stdlib only

No external Go modules. Builds in any Go 1.22+ environment without network
access. `go.mod` is two lines. This is on purpose: the tool exists so test
flows don't pull in a Docker daemon or Rancher Desktop. Adding deps would
defeat that.

The only embedded asset is `ui.html`, packed at compile time with
`//go:embed`. No filesystem dependency at runtime.

## Threading model

- One goroutine per accepted TCP connection (SMTP / hook / API).
- Server handlers are short-lived; no background goroutines.
- The `/tail` handler holds one goroutine per live subscriber for the
  duration of the streaming HTTP connection.
- The store's pub/sub uses non-blocking channel sends — a stuck subscriber
  loses events rather than back-pressuring the producer.

No worker pools, no rate limits, no admission control. This is fine for
local testing; do not put mailhook on a network you don't trust.
