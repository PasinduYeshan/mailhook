# Mock rules (Beeceptor-style)

By default the webhook receiver answers every request with
`200 {"id":"…","received":true}`. Rules let you override that on a
per-`method`+`path` basis without giving up the capture — the request is
still recorded.

## Rule JSON

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

`status` defaults to `200` if omitted. `headers` and `body` are optional.
`delay_ms` is a synchronous sleep before the response is written — useful
for testing client timeouts.

## Path matching

| Pattern        | Matches                                                  |
| -------------- | -------------------------------------------------------- |
| `"/sms/send"`  | exact path                                               |
| `"/api/*"`     | `/api`, `/api/users`, `/api/orders/42`, …                |
| `"*"` or `""`  | any path                                                 |

Method matching is the same: literal (case-insensitive), or `"*"` / `""`
for any.

## Tie-breaking

If multiple rules match a request, the **newest-added** rule wins (most
recently created). This makes it easy to override an existing rule for a
specific case by adding a more-specific one on top.

## Response headers added automatically

When a rule fires, mailhook adds:

- `X-Mailhook-Id: <capture-id>` — the captured request's id
- `X-Mailhook-Rule: <rule-id>`  — which rule fired

Both let your test code correlate the request with the capture and the
matched rule. If the rule sets its own `Content-Type` mailhook keeps it;
otherwise mailhook auto-fills `application/json` for bodies that start with
`{` / `[`, else `text/plain; charset=utf-8`.

## CLI examples

```bash
# Mock the SMS provider to return 201 + queued payload, 50 ms delay.
mailhook rule add -m POST -p /sms/send -s 201 \
  -b '{"messageId":"mock-123","status":"queued"}' \
  -H 'Content-Type: application/json' \
  -d 50

# Force every /api/* call to 503 (test client circuit-breaker behaviour).
mailhook rule add -m '*' -p '/api/*' -s 503 -b 'circuit open'

# List rules.
mailhook rule list

# Delete one rule.
mailhook rule delete <id>

# Wipe everything.
mailhook rule clear
```

## curl examples

```bash
# Create.
curl -s -X POST http://127.0.0.1:7791/rule \
  -H 'Content-Type: application/json' \
  -d '{"method":"POST","path":"/sms/send","status":201,
       "body":"{\"queued\":true}",
       "headers":{"Content-Type":"application/json"},
       "delay_ms":50}'

# List.
curl -s http://127.0.0.1:7791/rule | jq

# Delete one.
curl -X DELETE http://127.0.0.1:7791/rule/<id>

# Clear all.
curl -X DELETE http://127.0.0.1:7791/rule
```

## From the Web UI

You never have to declare paths up front: any method on any path is
captured and answered with the default `200`. In **Webhooks**,
captures are grouped by path and each group shows what that path
currently answers (`POST · 200` or `POST · mock 201`). **Mock** opens
the mock response form pre-filled with that method and path; **Edit
mock** opens the rule that answers it. A request's detail view shows the
status it got and links to the rule that produced it. Rules are listed,
edited and deleted under **Mock responses**.

## Limitations

- No regex paths — only exact and `/prefix/*` patterns.
- No request-body matching — only method + path.
- Rules live in memory; restarting the daemon wipes them.
- One static response per rule — no randomization, no templating.

If you need any of those, reach for [Hookdeck](https://hookdeck.com/) or
[wiremock](https://wiremock.org/). For local testing this set is enough.
