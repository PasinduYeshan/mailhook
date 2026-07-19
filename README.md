# mailhook

Local SMTP catcher + HTTP webhook inspector. Single static binary, zero external deps. Built for testing WSO2 IS pack flows that send mail (OTP,
password reset) or POST to a notification endpoint (SMS, custom HTTP), but useful for any app that emits SMTP or webhooks.

- Catches outbound SMTP on `127.0.0.1:1025` (no auth, no TLS, local-only).
- Catches any HTTP method on `127.0.0.1:7790` — captures stay in memory.
- Optional per-`method`+`path` mock responses (Beeceptor-style).
- Web UI on `127.0.0.1:8091` and a JSON API + live NDJSON tail.
- ~6 MB binary, ~12 MB RSS, ~0% idle CPU.

![mailhook web UI](docs/assets/mailhook.png)

## Install

```bash
curl -fsSL https://raw.githubusercontent.com/PasinduYeshan/mailhook/main/install.sh | sh
```

Verify:

```bash
mailhook version
```

> Building from source? See [CONTRIBUTING.md](CONTRIBUTING.md).

## Quick start

01. Start the background daemon.

```bash
mailhook start                     # background daemon
```

Then open the web UI at port 8091.

```bash
open http://127.0.0.1:8091/        # web UI
```

02. Send mail to the catcher.

```bash
swaks --server localhost:1025 \
      --from is@test.local --to user@example.com \
      --header "Subject: OTP 482913" --body "Code: 482913"
```

or a webhook.

```bash
curl -X POST http://localhost:7790/sms/send -d '{"text":"hi"}'
```

03. Inspect from CLI.

```bash
mailhook mail list
```

```bash
mailhook hook list
```

```bash
mailhook tail
```

04. Stop the daemon when done.

```bash
mailhook stop
```

## Other commands

| Command                | What it does                                      |
| ---------------------- | ------------------------------------------------- |
| `mailhook start`       | start daemon in background                        |
| `mailhook stop`        | stop daemon, reap orphan processes                |
| `mailhook restart`     | stop then start                                   |
| `mailhook status`      | show pid, RSS, CPU                                |
| `mailhook logs`        | `tail -f` the daemon log                          |
| `mailhook serve`       | run in the foreground                             |
| `mailhook mail …`      | list / show / delete captured mail                |
| `mailhook hook …`      | list / show / delete captured webhook requests    |
| `mailhook rule …`      | add / list / update / delete mock-response rules  |
| `mailhook tail`        | live NDJSON stream of new captures                |

Full reference: see [`docs/cli.md`](docs/cli.md).

## Documentation

| Topic            | File                                            |
| ---------------- | ----------------------------------------------- |
| CLI reference    | [`docs/cli.md`](docs/cli.md)                    |
| HTTP API         | [`docs/api.md`](docs/api.md)                    |
| Mock rules       | [`docs/rules.md`](docs/rules.md)                |
| WSO2 IS pack     | [`docs/integration.md`](docs/integration.md)    |
| Internals        | [`docs/architecture.md`](docs/architecture.md)  |
| Agent skill      | [`SKILL.md`](SKILL.md)                          |
| Contributing     | [`CONTRIBUTING.md`](CONTRIBUTING.md)            |

## License

MIT
