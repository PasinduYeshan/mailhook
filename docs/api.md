# HTTP API

Base URL: `http://127.0.0.1:8091` (override with `MAILHOOK_API`).

The webhook receiver (`:7790`) and SMTP receiver (`:1025`) are write-only
endpoints — apps under test post / send to them. Everything else (read,
delete, mock-rule CRUD, live tail, Web UI) lives on the API port `:8091`.

## Endpoints

| Method | Path                          | Description                                      |
| ------ | ----------------------------- | ------------------------------------------------ |
| GET    | `/healthz`                    | Liveness probe — returns `ok`.                   |
| GET    | `/`, `/ui`                    | Web UI (HTML).                                   |
| GET    | `/mail`                       | List captured mail (JSON array).                 |
| GET    | `/mail/{id}`                  | Read one mail.                                   |
| DELETE | `/mail/{id}`                  | Delete one mail (`204` or `404`).                |
| DELETE | `/mail`                       | Clear all mail (`204`).                          |
| GET    | `/hook`                       | List captured hooks.                             |
| GET    | `/hook/{id}`                  | Read one hook.                                   |
| DELETE | `/hook/{id}`                  | Delete one hook.                                 |
| DELETE | `/hook`                       | Clear all hooks.                                 |
| GET    | `/rule`                       | List mock-response rules.                        |
| GET    | `/rule/{id}`                  | Read one rule.                                   |
| POST   | `/rule`                       | Create a rule (JSON body — see [rules.md](rules.md)). |
| PUT    | `/rule/{id}`                  | Replace a rule.                                  |
| DELETE | `/rule/{id}`                  | Delete one rule.                                 |
| DELETE | `/rule`                       | Clear all rules.                                 |
| GET    | `/tail?kind=mail\|hook\|all`  | NDJSON stream of new events (long-lived).        |

## Webhook receiver response headers

Whenever the receiver captures a request it sets `X-Mailhook-Id: <capture-id>`
so the caller can correlate the request to a record. When a [rule](rules.md)
fires, it also sets `X-Mailhook-Rule: <rule-id>`.

## Mail JSON shape

```json
{
  "id":      "diss5tj5kn7c",
  "time":    "2026-05-27T08:32:00.936545Z",
  "from":    "is@test.local",
  "to":      ["user@example.com"],
  "subject": "OTP 482913",
  "headers": { "From": "...", "Subject": "...", "To": "..." },
  "body":    "Code: 482913",
  "text":    "Code: 482913",
  "html":    "<html>...</html>",
  "raw":     "From: ...\r\n...full MIME source..."
}
```

`text` and `html` are decoded from multipart/alternative messages with
quoted-printable / base64 transfer-encodings unwrapped. `body` is whichever
of `text` or `html` (in that order) is present — for backwards-compatible
plain access.

## Hook JSON shape

```json
{
  "id":      "diss5tj7vmpo",
  "time":    "2026-05-27T08:32:01.306755Z",
  "method":  "POST",
  "path":    "/sms/send",
  "query":   "",
  "headers": { "Content-Type": ["application/json"], "User-Agent": ["..."] },
  "body":    "{\"to\":\"+9477...\",\"text\":\"...\"}",
  "remote":  "127.0.0.1:64450"
}
```

Headers are `map[string][]string` (HTTP allows repeated keys).

## Tail event shape (NDJSON)

```json
{"kind":"mail","data":<embedded JSON of Mail or Hook record>}
```

`/tail?kind=mail` filters to mail events only; `kind=hook` only; `all`
(or omit) for both.

## curl cheat sheet

```bash
# List
curl -s http://127.0.0.1:8091/mail
curl -s http://127.0.0.1:8091/hook

# Read one
curl -s http://127.0.0.1:8091/mail/<id>
curl -s http://127.0.0.1:8091/hook/<id>

# Delete one
curl -X DELETE http://127.0.0.1:8091/mail/<id>
curl -X DELETE http://127.0.0.1:8091/hook/<id>

# Clear all
curl -X DELETE http://127.0.0.1:8091/mail
curl -X DELETE http://127.0.0.1:8091/hook

# Mock rule (Beeceptor-style)
curl -X POST http://127.0.0.1:8091/rule \
  -H 'Content-Type: application/json' \
  -d '{"method":"POST","path":"/sms/send","status":201,
       "body":"{\"queued\":true}",
       "headers":{"Content-Type":"application/json"},
       "delay_ms":50}'

# Live stream
curl -Ns http://127.0.0.1:8091/tail?kind=all
```

## Status-code conventions

| Status | When                                        |
| ------ | ------------------------------------------- |
| `200`  | GET ok                                      |
| `201`  | rule created                                |
| `204`  | delete or clear ok                          |
| `400`  | malformed JSON / missing required field     |
| `404`  | id not found                                |
| `5xx`  | server bug — file a report                  |
