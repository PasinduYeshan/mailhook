---
name: mailhook
description: Use this skill when an agent needs to capture or inspect outbound email or HTTP webhook calls from a local app under test — especially WSO2 IS pack OTP / email / custom SMS publisher flows. Provides a single-binary local sink (SMTP :1025 + HTTP :8090) with a JSON query API and live NDJSON tail. Triggers on phrases like "catch the OTP email", "intercept the SMS webhook", "what email did IS send", "wait for a webhook on :8090", "test the password reset mail flow", or any IS pack test that emits mail or HTTP notifications.
---

# mailhook — agent guide

`mailhook` is a local sink for SMTP mail and HTTP webhooks, used as a test
double for any service that would normally send email or call an external
notification endpoint. One Go binary, three listeners, an HTTP API, and an
NDJSON live stream. Everything is in-memory.

Source: `/Users/pasindu/project/personal/webhook`
Binary: `~/.local/bin/mailhook`

## When to use this skill

Use when the task involves any of:

- "Send a password reset / OTP from IS pack and grab the code."
- "Verify the IS notification publisher posts to the SMS webhook with the right body."
- "Confirm the app sends an email on user signup."
- "Wait until the webhook fires, then assert on its payload."
- Any flow that emits SMTP or calls an external HTTP endpoint where you
  don't actually want the message to leave the host.

Do **not** use this skill for:

- Real mail delivery.
- TLS / STARTTLS testing (none implemented).
- Persistent capture across restarts (in-memory only).
- Multi-tenant or networked scenarios (localhost only by default).

## Lifecycle

```bash
# Build (only once, after a code change).
cd /Users/pasindu/project/personal/webhook && go build -o ~/.local/bin/mailhook .

# Start daemon (background).
mailhook-up                        # zshrc helper, writes pid to ~/.local/var/mailhook/run/

# Status / logs.
mailhook-status
mailhook-logs                      # tail -f the daemon log

# Stop.
mailhook-down
```

If `mailhook-up` is not defined in the shell, fall back to:

```bash
nohup mailhook serve > /tmp/mailhook.log 2>&1 &
```

Endpoints once running:

| Surface       | URL                       |
| ------------- | ------------------------- |
| SMTP receiver | `127.0.0.1:1025`          |
| Webhook sink  | `http://127.0.0.1:8090/*` — any HTTP method captured (`GET`/`POST`/`PUT`/`PATCH`/`DELETE`/`HEAD`/`OPTIONS`/...) |
| Query API     | `http://127.0.0.1:8091/`  |
| Web UI        | `http://127.0.0.1:8091/`  |

## Core commands

```
mailhook mail list   [-j]                     # table or JSON of captured mail
mailhook mail show   <id>                     # pretty JSON of one mail
mailhook mail delete <id>                     # remove a single mail
mailhook mail clear                           # wipe mail buffer
mailhook hook list   [-j]                     # captured HTTP hooks
mailhook hook show   <id>
mailhook hook delete <id>
mailhook hook clear
mailhook tail        [-t mail|hook|all] [-j]  # live stream (Ctrl-C to exit)
```

All commands hit the query API on `127.0.0.1:8091`. Override with
`MAILHOOK_API=http://host:port` if running remote.

### Pure-curl equivalents

Every CLI verb maps 1:1 to an HTTP call. Use these when no `mailhook` binary
is reachable (e.g. inside a container that can only see port `8091`):

| CLI                          | curl                                                              |
| ---------------------------- | ----------------------------------------------------------------- |
| `mailhook mail list -j`       | `curl -s http://127.0.0.1:8091/mail`                              |
| `mailhook mail show <id>`     | `curl -s http://127.0.0.1:8091/mail/<id>`                         |
| `mailhook mail delete <id>`   | `curl -s -X DELETE http://127.0.0.1:8091/mail/<id>`               |
| `mailhook mail clear`         | `curl -s -X DELETE http://127.0.0.1:8091/mail`                    |
| `mailhook hook list -j`       | `curl -s http://127.0.0.1:8091/hook`                              |
| `mailhook hook show <id>`     | `curl -s http://127.0.0.1:8091/hook/<id>`                         |
| `mailhook hook delete <id>`   | `curl -s -X DELETE http://127.0.0.1:8091/hook/<id>`               |
| `mailhook hook clear`         | `curl -s -X DELETE http://127.0.0.1:8091/hook`                    |
| `mailhook tail`               | `curl -Ns http://127.0.0.1:8091/tail?kind=all`                    |
| healthcheck                  | `curl -fs http://127.0.0.1:8091/healthz`                          |

DELETE responses: `204` on success, `404` if `id` is unknown. GET responses:
`200` + JSON, or `404` for unknown id.

## Mail record schema

```json
{
  "id":      "disqgh9r3dog",
  "time":    "2026-05-26T16:02:00.936545Z",
  "from":    "is@test.local",
  "to":      ["user@example.com"],
  "subject": "OTP Code 482913",
  "headers": { "From": "...", "Subject": "...", "To": "..." },
  "body":    "Your verification code: 482913.\r\n",
  "raw":     "From: ...\r\n..."
}
```

## Hook record schema

```json
{
  "id":      "disqghfviabc",
  "time":    "2026-05-26T16:02:01.306755Z",
  "method":  "POST",
  "path":    "/sms/send",
  "query":   "",
  "headers": { "Content-Type": ["application/json"], ... },
  "body":    "{\"to\":\"+94771234567\",\"text\":\"Your IS OTP is 482913\"}",
  "remote":  "127.0.0.1:64450"
}
```

Headers on hooks are `map[string][]string` (HTTP allows repeats). Headers on
mail are flattened `map[string]string` (joined with `, `).

## Agent workflows

### 1. Extract an OTP from the most recent mail

```bash
mailhook mail list -j \
  | jq -r '.[-1].body' \
  | grep -oE '[0-9]{4,8}' | head -1
```

For richer scoping (subject must contain "OTP"):

```bash
mailhook mail list -j \
  | jq -r '[.[] | select(.subject|test("OTP";"i"))] | last | .body' \
  | grep -oE '[0-9]{4,8}' | head -1
```

### 2. Wait for a specific webhook to arrive

```bash
mailhook tail -t hook -j | while IFS= read -r line; do
  body=$(echo "$line" | jq -r '.data | fromjson? // empty | .body // ""')
  if echo "$body" | grep -q '"event":"user.created"'; then
    echo "matched: $body"
    break
  fi
done
```

`mailhook tail` is NDJSON — one JSON event per line. Each event is
`{"kind":"mail"|"hook","data":<payload-as-base64-encoded-json>}`. With
`-j`, just print the raw `data` field of each event.

### 3. Pre-test reset

Always clear before a test run so assertions see only events produced by
that run:

```bash
mailhook mail clear
mailhook hook clear
# ... trigger the IS pack flow ...
# ... then assert ...
```

### 4. Polling for the latest capture (no live tail)

```bash
for i in $(seq 1 30); do
  count=$(curl -s http://127.0.0.1:8091/mail | jq 'length')
  if [ "$count" -gt 0 ]; then break; fi
  sleep 0.5
done
```

### 5. Use directly via HTTP API (skip the CLI)

```bash
curl -s http://127.0.0.1:8091/mail | jq '.[].subject'
curl -s -X DELETE http://127.0.0.1:8091/hook
```

Useful when running inside a container that doesn't have the `mailhook`
binary but can reach the host's port `8091`.

## Mock responses (Beeceptor-style)

Define a rule and the webhook receiver returns the configured response
instead of the default `{"received":true}`. Captures still happen.

```bash
# Mock the SMS provider to return 201 with a queued payload, 50ms delay.
mailhook rule add -m POST -p /sms/send -s 201 \
  -b '{"messageId":"mock-123","status":"queued"}' \
  -H 'Content-Type: application/json' \
  -d 50

# Force /api/* to return 503 (test IS pack's circuit-breaker behavior).
mailhook rule add -m '*' -p '/api/*' -s 503 -b 'circuit open'

# List / show / delete.
mailhook rule list
mailhook rule delete <id>
mailhook rule clear
```

Curl equivalents:

```bash
curl -s -X POST http://127.0.0.1:8091/rule \
  -H 'Content-Type: application/json' \
  -d '{"method":"POST","path":"/sms/send","status":201,
       "body":"{\"queued\":true}",
       "headers":{"Content-Type":"application/json"},
       "delay_ms":50}'

curl -s http://127.0.0.1:8091/rule | jq          # list
curl -s -X DELETE http://127.0.0.1:8091/rule/<id>
curl -s -X DELETE http://127.0.0.1:8091/rule     # clear
```

Path patterns:
- `"/exact"` — exact match.
- `"/api/*"` — prefix match (any subpath under `/api`).
- `"*"` or `""` — any path.

Method `"*"` or `""` matches any method. Newest rule wins on overlap.

A request that fires a rule has these response headers added:
`X-Mailhook-Id: <capture-id>` and `X-Mailhook-Rule: <rule-id>`. Use them
to assert in tests which rule matched.

## Pointing an IS pack at mailhook

Append to `repository/conf/deployment.toml`:

```toml
[output_adapter.email]
from_address = "is@test.local"
username = ""
password = ""
hostname = "127.0.0.1"
port = 1025
enable_start_tls = false
enable_authentication = false

[output_adapter.sms]
provider = "http"
provider_url = "http://127.0.0.1:8090/sms/send"
```

Restart the IS pack after editing.

## Sanity checks before reporting failure

If a test that depends on mailhook fails, verify in this order:

1. **Daemon is up.** `curl -fs http://127.0.0.1:8091/healthz` returns `ok`.
2. **Buffer has entries.** `mailhook mail list` / `mailhook hook list` shows
   the expected count. If empty, the app under test never sent anything —
   the bug is upstream, not in mailhook.
3. **IS deployment.toml was reloaded.** IS reads `deployment.toml` only at
   startup; toml edits without a restart will not take effect.
4. **Port collisions.** `lsof -nP -iTCP:1025 -sTCP:LISTEN` — only one
   listener should be on each port. macOS ssh sometimes squats on
   `:1025`; pass `--smtp 127.0.0.1:1026` if needed.
5. **Buffer eviction.** Default cap is 500 per stream — under heavy load
   the OTP you want may have already been evicted. Bump with
   `mailhook serve --cap 5000`.

## Hard rules for agents

- Always `mailhook mail clear` / `mailhook hook clear` before running a
  flow you intend to assert on. Stale captures cause false positives.
- Treat `id` as opaque. Order in `list` is insertion order; the most
  recent entry is the last element of the array.
- Do not parse the human table output. Use `-j` everywhere for parsing.
- Do not poll `/mail` faster than ~500 ms; use `/tail` or `mailhook tail`
  for low-latency waits.
- The daemon is **localhost-only by default**. If `--smtp 0.0.0.0:1025`
  is required (e.g. capturing from a container), call out the security
  implication explicitly — anyone on the network can drop mail.
- mailhook has no auth. Never run it on a shared host.

## One-liner cheat sheet

```bash
mailhook-up                                                 # start
mailhook mail clear && mailhook hook clear                   # pre-test reset
mailhook tail                                               # watch
mailhook mail list -j | jq -r '.[-1].body'                  # latest mail body
mailhook hook list -j | jq '.[-1] | {method,path,body}'     # latest hook summary
mailhook mail delete <id>                                   # remove one captured mail
mailhook hook delete <id>                                   # remove one captured hook
curl -s http://127.0.0.1:8091/healthz                      # liveness
mailhook-down                                               # stop
```

## Curl-only mode (no CLI available)

```bash
# Pre-test: wipe everything via API.
curl -s -X DELETE http://127.0.0.1:8091/mail
curl -s -X DELETE http://127.0.0.1:8091/hook

# Wait for a mail, grab its OTP, then delete that one mail.
ID=$(while :; do
  ID=$(curl -s http://127.0.0.1:8091/mail | jq -r '.[-1].id // empty')
  [ -n "$ID" ] && { echo "$ID"; break; }
  sleep 0.3
done)
curl -s http://127.0.0.1:8091/mail/$ID | jq -r '.body' | grep -oE '[0-9]{6}'
curl -s -X DELETE http://127.0.0.1:8091/mail/$ID

# Wait for a webhook hitting a known path, print its body, then delete it.
ID=$(while :; do
  ID=$(curl -s 'http://127.0.0.1:8091/hook' | jq -r '[.[] | select(.path=="/sms/send")] | last.id // empty')
  [ -n "$ID" ] && { echo "$ID"; break; }
  sleep 0.3
done)
curl -s http://127.0.0.1:8091/hook/$ID | jq '.body'
curl -s -X DELETE http://127.0.0.1:8091/hook/$ID
```
