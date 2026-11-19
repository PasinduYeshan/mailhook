# WSO2 IS pack integration

Point an IS pack at mailhook so OTP / password-reset / SMS publisher flows
land in the local sink instead of leaving the machine.

## Email — SMTP catcher

Append to `repository/conf/deployment.toml`:

```toml
[output_adapter.email]
from_address = "is@test.local"
username = ""
password = ""
hostname = "127.0.0.1"
port = 7725
enable_start_tls = false
enable_authentication = false
```

Restart the IS pack. All outbound mail (signup verification, OTP, password
reset, etc.) now lands in mailhook:

```bash
mailhook mail list
mailhook mail show <id>          # full JSON incl. text / html parts
```

Or open `http://127.0.0.1:7791/` and click **Mail**.

## Custom SMS / notification webhook

```toml
[output_adapter.sms]
provider = "http"
provider_url = "http://127.0.0.1:7790/sms/send"
```

Now every SMS publisher call ends up captured:

```bash
mailhook hook list
```

If you also want a realistic provider response (status, body, headers),
add a [mock rule](rules.md):

```bash
mailhook rule add -m POST -p /sms/send -s 201 \
  -b '{"messageId":"mock-123","status":"queued"}' \
  -H 'Content-Type: application/json'
```

## Reading the OTP back

For automated tests, take the code or link mailhook already extracted:

```bash
otp=$(mailhook mail code)               # newest mail
sms_otp=$(mailhook hook code)           # newest SMS webhook
link=$(mailhook mail links | head -1)   # e.g. confirmation / reset link
```

This works for HTML-only templates too. To pick a specific mail, filter
the JSON:

```bash
mailhook mail list -j \
  | jq -r '[.[] | select(.to[0]=="bob@example.com")] | last | .codes[0]'
```

In the UI, codes show as chips (click to copy, or press `c`), and each
link lists its query parameters (`confirmation`, `code`, `inviteToken`, …)
with copy buttons.

## Pre-test reset

Always wipe captures before triggering the flow so assertions only see
events from the current run:

```bash
mailhook mail clear
mailhook hook clear
# trigger IS flow ...
# assert ...
```

## Common pitfalls

| Symptom                                | Cause / fix                                                                                |
| -------------------------------------- | ------------------------------------------------------------------------------------------ |
| `mailhook mail list` shows nothing     | IS pack wasn't restarted after `deployment.toml` edit. Restart and trigger the flow again. |
| `address already in use` on start      | Another tool owns the port. Pick a free one, e.g. `mailhook restart --smtp 2525`.          |
| HTML mail looks like raw `--BOUNDARY…` | Older binary without multipart support. Reinstall.                                         |
| Captures get dropped under load        | Default ring-buffer is 500/stream. Bump with `mailhook start --cap 5000`.                  |

## Run on a remote host

mailhook binds to `127.0.0.1` by default. To accept traffic from a remote
IS pack container, bind on all interfaces — **only on a trusted network**,
since there's no auth:

```bash
mailhook start \
  --smtp 0.0.0.0:7725 \
  --hook 0.0.0.0:7790 \
  --api  127.0.0.1:7791   # keep API local
```
