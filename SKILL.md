---
name: mailhook
description: Capture / inspect outbound email or HTTP webhook calls from a local app under test (WSO2 IS OTP, password reset, SMS publisher, custom HTTP notification). Triggers on "catch the OTP email", "intercept the SMS webhook", "what email did IS send", "mock the SMS endpoint", "wait for a webhook on :8090".
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
mailhook start            # background daemon (idempotent, self-heals orphans)
mailhook status
mailhook stop
```

Endpoints:
- SMTP   `127.0.0.1:1025`
- Hook   `127.0.0.1:8090` (any HTTP method)
- API+UI `http://127.0.0.1:8091/`

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

`Mail`: `id`, `from`, `to[]`, `subject`, `headers{}`, `text`, `html`, `body`, `raw`
`Hook`: `id`, `method`, `path`, `query`, `headers{}`, `body`, `remote`

## OTP extract

```bash
mailhook mail list -j \
  | jq -r '[.[] | select(.subject|test("OTP";"i"))] | last | .body' \
  | grep -oE '[0-9]{4,8}' | head -1
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
