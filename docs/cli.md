# CLI reference

All subcommands ultimately talk to the Query API on `127.0.0.1:8091` (or
whatever `MAILHOOK_API` points to). `serve` and the daemon commands are the
exception — they manage the process itself.

Exit codes: `0` ok · `1` runtime error · `2` usage error.

## Daemon lifecycle

```
mailhook start    [serve flags]   start in background, write pidfile
mailhook stop                     stop background daemon (+ reap orphans)
mailhook restart  [serve flags]   stop then start
mailhook status                   show pid, RSS, CPU
mailhook logs                     tail -f the daemon log
mailhook serve    [flags]         run in foreground (called by `start`)
```

### `serve` flags

| Flag         | Default              | Description                                  |
| ------------ | -------------------- | -------------------------------------------- |
| `--smtp`     | `127.0.0.1:1025`     | SMTP listen address                          |
| `--hook`     | `127.0.0.1:8090`     | Webhook receiver listen address              |
| `--api`      | `127.0.0.1:8091`     | Query API + Web UI listen address            |
| `--cap`      | `500`                | Ring-buffer capacity per stream (mail/hook)  |

## Mail

```
mailhook mail list   [-j]
mailhook mail show   <id>
mailhook mail delete <id>
mailhook mail clear
```

- `list` prints a table; `-j` emits JSON.
- `show` always pretty-prints JSON.
- `delete` and `clear` print nothing on success, exit `1` on unknown id.

## Hook

```
mailhook hook list   [-j]
mailhook hook show   <id>
mailhook hook delete <id>
mailhook hook clear
```

## Rule (mock responses)

```
mailhook rule add    [-m METHOD] [-p PATH] [-s STATUS] [-b BODY]
                     [-H 'K: V']  [-d MS]  [-f FILE]
mailhook rule list   [-j]
mailhook rule show   <id>
mailhook rule update <id> [same flags as add]
mailhook rule delete <id>
mailhook rule clear
```

### `add` / `update` flags

| Flag         | Default | Description                                                       |
| ------------ | ------- | ----------------------------------------------------------------- |
| `-m METHOD`  | `POST`  | HTTP method to match; `*` matches any                             |
| `-p PATH`    | —       | Path to match. Required. `*` for any, `/api/*` for subpath prefix |
| `-s STATUS`  | `200`   | Response status code                                              |
| `-b BODY`    | —       | Response body (raw string)                                        |
| `-H 'K: V'`  | —       | Response header; repeat for multiple                              |
| `-d MS`      | `0`     | Response delay in milliseconds                                    |
| `-f FILE`    | —       | Read full rule JSON from file (`-` for stdin)                     |

See [`docs/rules.md`](rules.md) for path-matching semantics and examples.

## Tail (live stream)

```
mailhook tail [-t mail|hook|all] [-j]
```

Long-lived. Each event prints one line:

```
[12:34:56] MAIL diss…  is@test.local → user@example.com  Subject…
[12:34:56] HOOK diss…  POST /sms/send  (12B)
```

`-j` emits raw NDJSON (one JSON event per line) — agent-friendly.

## Environment

| Variable        | Default                       | Purpose                                |
| --------------- | ----------------------------- | -------------------------------------- |
| `MAILHOOK_API`  | `http://127.0.0.1:8091`       | Override the API URL the CLI talks to. |
| `MAILHOOK_DIR`  | `$HOME/.local/var/mailhook`   | Override pid + log directory.          |

## Agent flow examples

```bash
# Extract the most recent OTP from mail body.
mailhook mail list -j | jq -r '.[-1].body' | grep -oE '[0-9]{4,8}' | head -1

# Wait for the next webhook on /sms/send and print its body.
mailhook tail -j -t hook \
  | jq -r 'select((.data|fromjson).path=="/sms/send") | (.data|fromjson).body' \
  | head -1

# Mock the SMS provider to return 201 + delay 50 ms.
mailhook rule add -m POST -p /sms/send -s 201 \
  -b '{"messageId":"mock-123","status":"queued"}' \
  -H 'Content-Type: application/json' \
  -d 50
```

Always clear before a fresh test run:

```bash
mailhook mail clear && mailhook hook clear
```
