# HTTP API

Base URL: `http://127.0.0.1:7791` (override with `MAILHOOK_API`).

The webhook receiver (`:7790`) and SMTP receiver (`:7725`) are write-only
endpoints — apps under test post / send to them. Everything else (read,
delete, mock-rule CRUD, live tail, Web UI) lives on the API port `:7791`.

## Endpoints

| Method | Path                          | Description                                      |
| ------ | ----------------------------- | ------------------------------------------------ |
| GET    | `/healthz`                    | Liveness probe — returns `ok`.                   |
| GET    | `/`, `/ui`                    | Web UI (HTML).                                   |
| GET    | `/mail`                       | List captured mail (JSON array).                 |
| GET    | `/mail/{id}`                  | Read one mail.                                   |
| GET    | `/mail/{id}/eml`              | Raw message as a `.eml` download.                |
| GET    | `/mail/{id}/part/{n}`         | Attachment `n` (`?download=1` to save it).       |
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
| GET    | `/tail?kind=mail\|hook\|all`  | NDJSON stream of new events (long-lived). Add `ops=1` to also get delete / clear / rule-change events. |

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
  "raw":     "From: ...\r\n...full MIME source...",
  "codes":   ["482913"],
  "links":   [{ "url": "https://localhost:9443/reset?code=abc&user=bob", "text": "Reset password" }],
  "attachments": [
    { "index": 0, "filename": "logo.png", "content_type": "image/png",
      "size": 178, "content_id": "logo@shop", "inline": true }
  ]
}
```

`from` / `to` are the SMTP envelope (`to` includes Bcc). `headers` values
are decoded (`=?UTF-8?…?=` → text) and repeated keys are joined with `, `.
Bodies are converted to UTF-8 from UTF-8, US-ASCII, ISO-8859-1/-15 and
Windows-1252.

Derived fields (omitted when empty):

- `codes` — likely one-time codes: numbers of 4–10 digits (or `123-456`)
  near words like *code*, *OTP*, *PIN*, *verification*; otherwise any
  6-digit number. Subject and body are searched; URLs are ignored.
- `links` — `<a href>` targets (HTML entities decoded, anchor text in
  `text`), then bare `http(s)` URLs from the text. `#`, `javascript:`,
  `cid:` and `data:` links are skipped. At most 50.
- `attachments` — every MIME leaf that is not the first text/plain or
  text/html body. Inline images referenced as `cid:` have `content_id`.

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
  "remote":  "127.0.0.1:64450",
  "status":  201,
  "rule":    "diss5tj2abcd",
  "sms":     { "to": "+9477...", "from": "WSO2IS", "text": "Your code is 482913" },
  "codes":   ["482913"],
  "links":   []
}
```

Headers are `map[string][]string` (HTTP allows repeated keys). `status` is
what mailhook answered; `rule` is set when a mock rule produced it.

`sms` is present when the request looks like a message. It is read from,
in order: a JSON body (keys such as `text`, `message`, `body`, `content`;
`to`, `phone`, `msisdn`, `destinations[].to`; `from`, `sender`), a form
body (Twilio's `Body` / `To` / `From`), XML elements with those names, a
short plain-text body, then the query string. `codes` come from the SMS
text; `links` from the SMS text, else from the body and query.

## Tail event shape (NDJSON)

```json
{"kind":"mail","data":<embedded JSON of Mail or Hook record>}
```

`/tail?kind=mail` filters to mail events only; `kind=hook` only; `all`
(or omit) for both.

With `ops=1` the stream also carries changes made by other clients, so a
viewer can stay in sync without polling:

```json
{"kind":"mail","op":"delete","data":{"id":"diss5tj5kn7c"}}
{"kind":"hook","op":"clear","data":{"id":""}}
{"kind":"rule","op":"change","data":{"id":""}}
```

Without `ops=1` only captures are sent (no `op` field), as before.

## curl cheat sheet

```bash
# List
curl -s http://127.0.0.1:7791/mail
curl -s http://127.0.0.1:7791/hook

# Read one
curl -s http://127.0.0.1:7791/mail/<id>
curl -s http://127.0.0.1:7791/hook/<id>

# Delete one
curl -X DELETE http://127.0.0.1:7791/mail/<id>
curl -X DELETE http://127.0.0.1:7791/hook/<id>

# Clear all
curl -X DELETE http://127.0.0.1:7791/mail
curl -X DELETE http://127.0.0.1:7791/hook

# Mock rule (Beeceptor-style)
curl -X POST http://127.0.0.1:7791/rule \
  -H 'Content-Type: application/json' \
  -d '{"method":"POST","path":"/sms/send","status":201,
       "body":"{\"queued\":true}",
       "headers":{"Content-Type":"application/json"},
       "delay_ms":50}'

# Live stream
curl -Ns http://127.0.0.1:7791/tail?kind=all
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
