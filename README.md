# mailhook

Minimal SMTP catcher + HTTP webhook inspector in a single Go binary.
Built for local WSO2 IS pack testing: capture outgoing email (e.g. OTP, password
reset) and custom SMS / notification webhook calls — inspect them from CLI,
HTTP API, or a tiny built-in web UI.

- Zero external dependencies — Go stdlib only.
- One 8.9 MB self-contained binary. No Docker, no Rancher Desktop, no JVM.
- ~11 MB RSS / ~0% CPU at idle.
- Agent-friendly: every command supports JSON output; live tail emits NDJSON.

---

## Features

| Surface          | Default address     | Purpose                                          |
| ---------------- | ------------------- | ------------------------------------------------ |
| SMTP receiver    | `127.0.0.1:1025`    | Catches outbound mail. No auth, no TLS, no relay.|
| Webhook receiver | `127.0.0.1:8090`    | Catches any HTTP method on any path (`GET`/`POST`/`PUT`/`PATCH`/`DELETE`/`HEAD`/`OPTIONS`/...). |
| Mock rules       | configured via API  | Beeceptor-style: assign custom response per `method`+`path`. |
| Query API        | `127.0.0.1:8091`    | Read / clear / live-tail captures + manage rules.|
| Web UI           | `http://127.0.0.1:8091/` | Dark mode, tabs, click to expand, live updates. |

In-memory ring buffer keeps the last 500 mails and 500 hooks (configurable
via `--cap`). Nothing — captures or rules — is persisted to disk; restart
the daemon to wipe.

---

## Install

Requires Go 1.22+.

```bash
git clone <repo> mailhook && cd mailhook
go build -o ~/.local/bin/mailhook .
```

Make sure `~/.local/bin` is on `PATH`.

---

## Quick start

```bash
# Start the daemon (foreground).
mailhook serve

# In another terminal — send some SMTP mail.
swaks --server localhost:1025 \
      --from is@test.local --to user@example.com \
      --header "Subject: OTP 482913" --body "Code: 482913"

# Or a webhook.
curl -X POST http://localhost:8090/sms/send \
     -H 'Content-Type: application/json' \
     -d '{"to":"+94771234567","text":"Your IS OTP is 482913"}'

# Inspect.
mailhook mail list
mailhook hook list
mailhook tail
```

Open `http://127.0.0.1:8091/` in a browser for the UI.

---

## CLI reference

```
mailhook serve [--smtp 127.0.0.1:1025] [--hook 127.0.0.1:8090]
              [--api  127.0.0.1:8091] [--cap 500]

mailhook mail  list  [-j]            # list captured mail (table or JSON)
mailhook mail  show  <id>            # full record as pretty JSON
mailhook mail  clear                 # wipe mail buffer

mailhook hook  list  [-j]            # list captured hooks
mailhook hook  show  <id>            # full record as pretty JSON
mailhook hook  clear                 # wipe hook buffer

mailhook rule  add    [-m POST] [-p /path] [-s 200] [-b BODY] [-H 'K: V']
                     [-d MS] [-f file.json]
mailhook rule  list   [-j]
mailhook rule  show   <id>
mailhook rule  update <id> [same flags as add]
mailhook rule  delete <id>
mailhook rule  clear

mailhook tail        [-t mail|hook|all] [-j]   # live stream (Ctrl-C to exit)
```

Environment:

| Variable        | Default                     | Purpose                                  |
| --------------- | --------------------------- | ---------------------------------------- |
| `MAILHOOK_API`   | `http://127.0.0.1:8091`     | Override the API URL the CLI talks to.   |

Exit codes: `0` ok · `1` runtime error · `2` usage error.

### Agent flow examples

```bash
# Extract the latest OTP for an automated test.
mailhook mail list -j \
  | jq -r '.[-1].body' \
  | grep -oE '[0-9]{6}' | head -1

# Wait until the next SMS webhook arrives, then print its body.
mailhook tail -t hook -j \
  | jq -r 'select(.kind=="hook") | .data | fromjson? // .data | .body' \
  | head -1
```

---

## HTTP API

| Method | Path                          | Description                                      |
| ------ | ----------------------------- | ------------------------------------------------ |
| GET    | `/healthz`                    | Liveness probe (returns `ok`).                   |
| GET    | `/mail`                       | List all captured mail (JSON array).             |
| GET    | `/mail/{id}`                  | Read one mail (JSON object or `404`).            |
| DELETE | `/mail/{id}`                  | Delete one mail (`204` or `404`).                |
| DELETE | `/mail`                       | Clear all mail (`204`).                          |
| GET    | `/hook`                       | List all captured hooks.                         |
| GET    | `/hook/{id}`                  | Read one hook.                                   |
| DELETE | `/hook/{id}`                  | Delete one hook.                                 |
| DELETE | `/hook`                       | Clear all hooks.                                 |
| GET    | `/rule`                       | List configured response-mock rules.             |
| GET    | `/rule/{id}`                  | Read one rule.                                   |
| POST   | `/rule`                       | Create a rule (JSON body, see schema below).     |
| PUT    | `/rule/{id}`                  | Replace a rule.                                  |
| DELETE | `/rule/{id}`                  | Delete one rule.                                 |
| DELETE | `/rule`                       | Clear all rules.                                 |
| GET    | `/tail?kind=mail\|hook\|all`  | NDJSON stream of new events (long-lived).        |
| GET    | `/`, `/ui`                    | Web UI.                                          |

### Mock rules (Beeceptor-style)

Define `method`+`path` rules and mailhook returns the configured response
instead of the default `{"received":true}`. Captures still happen.

Rule JSON:

```json
{
  "method":   "POST",                  // "*" or "" = any method
  "path":     "/sms/send",             // "*" = any; "/api/*" = subpath
  "status":   201,
  "headers":  {"Content-Type": "application/json"},
  "body":     "{\"messageId\":\"abc\",\"queued\":true}",
  "delay_ms": 50
}
```

When a rule fires, mailhook adds two headers so the caller can correlate:
- `X-Mailhook-Id`   — capture ID of this request
- `X-Mailhook-Rule` — ID of the rule that matched

Newest-added rule wins on overlap.

The webhook receiver always returns `200` with `{"id":"...","received":true}`
and sets `X-Mailhook-Id: <id>` so the caller (or test code) can correlate.

### curl cheat sheet

```bash
# List
curl -s http://127.0.0.1:8091/mail | jq
curl -s http://127.0.0.1:8091/hook | jq

# Read one
curl -s http://127.0.0.1:8091/mail/<id> | jq
curl -s http://127.0.0.1:8091/hook/<id> | jq

# Delete one
curl -s -X DELETE http://127.0.0.1:8091/mail/<id>
curl -s -X DELETE http://127.0.0.1:8091/hook/<id>

# Clear all
curl -s -X DELETE http://127.0.0.1:8091/mail
curl -s -X DELETE http://127.0.0.1:8091/hook

# Live stream (NDJSON)
curl -Ns http://127.0.0.1:8091/tail?kind=all

# Send something to be captured
swaks --server localhost:1025 --to u@x --from is@test.local \
      --header "Subject: hi" --body "hello"
curl -X POST http://127.0.0.1:8090/sms/send -d '{"to":"+9477...","text":"x"}'
```

---

## WSO2 IS pack integration

`deployment.toml`:

```toml
# Email — point IS at the local SMTP sink.
[output_adapter.email]
from_address = "is@test.local"
username = ""
password = ""
hostname = "127.0.0.1"
port = 1025
enable_start_tls = false
enable_authentication = false

# Custom SMS notifications — publish to the webhook receiver.
[output_adapter.sms]
provider = "http"
provider_url = "http://127.0.0.1:8090/sms/send"
```

The IS pack should now send all OTP / password-reset emails to
`localhost:1025`, and all SMS publisher calls to `localhost:8090`. Capture
them with `mailhook mail list` / `mailhook hook list` or watch them live in the
UI.

---

## Resource footprint

Measured on macOS arm64 (M-series), idle daemon, no traffic:

| Metric | Value      |
| ------ | ---------- |
| Binary | 8.9 MB     |
| RSS    | ~11 MB     |
| CPU    | ~0%        |
| FDs    | 3 listeners + per-connection |

For comparison: Rancher Desktop with k3s enabled reserves 3–6 GB RAM and
3–8% CPU at idle. `mailhook` removes that overhead for the SMTP+webhook
use case entirely.

---

## Limitations

- **No TLS.** SMTP STARTTLS and HTTPS are not implemented — bind to
  localhost only.
- **No SMTP auth.** Any client can drop mail.
- **No persistence.** Captures live in memory; restart wipes the buffer.
- **Single-process ring buffer.** Capacity capped per stream (default 500);
  oldest entries are evicted.
- **Single-part bodies.** MIME multipart messages are stored verbatim in
  `raw` and `body`; no part extraction.
- **Localhost only by default.** Pass `--smtp 0.0.0.0:1025` /
  `--hook 0.0.0.0:8090` to bind on all interfaces — only do this on a
  trusted network.

These are deliberate trade-offs for a local test sink. Reach for Mailpit /
webhook.site / Hookdeck when you need any of the above.

---

## Repo layout

```
.
├── go.mod          # module declaration, no deps
├── mailhook.go      # all server + CLI logic, ~720 lines
├── ui.html         # embedded web UI (go:embed)
└── README.md
```

Single-file Go on purpose: easy to read, easy to fork.

---

## License

MIT.
