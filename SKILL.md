---
name: mailhook
description: Capture / inspect outbound email or HTTP webhook calls from a local app under test (WSO2 IS OTP, password reset, SMS publisher, custom HTTP notification). Triggers on "catch the OTP email", "intercept the SMS webhook", "what email did IS send", "mock the SMS endpoint", "wait for a webhook on :7790".
---

## When to use

- Grab an OTP / reset code from email IS sent.
- Verify SMS / notification publisher posted the right body.
- Mock a third-party HTTP response.
- Any flow that emits SMTP or calls an external HTTP endpoint where the
  message must not actually leave the host.

Not for: real mail delivery, TLS, persistence across restarts, multi-host.

## Lifecycle

```bash
mailhook start            # background daemon (idempotent)
mailhook status
mailhook stop
```

Endpoints:
- SMTP   `127.0.0.1:7725`
- Hook   `127.0.0.1:7790` (any HTTP method)
- API+UI `http://127.0.0.1:7791/`

## Core commands

```bash
mailhook mail list [-j]
mailhook mail show <id>
mailhook hook list [-j]
mailhook hook show <id>
mailhook tail
mailhook mail clear && mailhook hook clear   # pre-test reset
```

## Key JSON fields

`Mail`: `id`, `from`, `to[]`, `subject`, `headers{}`, `text`, `html`, `body`, `raw`,
`codes[]`, `links[{url,text}]`, `attachments[]`
`Hook`: `id`, `method`, `path`, `query`, `headers{}`, `body`, `remote`, `status`,
`rule`, `sms{to,from,text}`, `codes[]`, `links[]`

## OTP / link extract

```bash
mailhook mail code              # first code in the newest mail (exit 1 if none)
mailhook hook code              # same for the newest SMS webhook
mailhook mail links | head -1   # first link, entities decoded
mailhook mail list -j | jq -r '[.[] | select(.to[0]=="bob@x.test")] | last | .codes[0]'
```

## Mock response

```bash
mailhook rule add -m POST -p /sms/send -s 201 \
  -b '{"queued":true}' -H 'Content-Type: application/json' -d 50
```

Paths: `/exact`, `/prefix/*`, `*`. Newest rule wins. Response carries
`X-Mailhook-Rule: <id>` for assertion.

## Full reference

| Topic           | File                  |
| --------------- | --------------------- |
| CLI flags       | `docs/cli.md`         |
| HTTP API        | `docs/api.md`         |
| Mock rules      | `docs/rules.md`       |
| IS integration  | `docs/integration.md` |
| Internals       | `docs/architecture.md`|
