# mailhook

Local SMTP catcher + HTTP webhook inspector. One Go binary, zero external
deps. Built for testing WSO2 IS pack flows that send mail (OTP, password
reset) or POST to a notification endpoint (SMS, custom HTTP), but useful
for any app that emits SMTP or webhooks.

- Catches outbound SMTP on `127.0.0.1:1025` (no auth, no TLS, local-only).
- Catches any HTTP method on `127.0.0.1:8090` — captures stay in memory.
- Optional per-`method`+`path` mock responses (Beeceptor-style).
- Web UI on `127.0.0.1:8091` and a JSON API + live NDJSON tail.
- ~6 MB binary, ~12 MB RSS, ~0% idle CPU.

## Install

Requires **Go 1.22+** (`brew install go` on macOS).

### One-shot

```bash
go install github.com/PasinduYeshan/mailhook/cmd/mailhook@latest
```

Lands in `$(go env GOPATH)/bin` (usually `~/go/bin`). Add it to `PATH` if
not already:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

Verify:

```bash
mailhook help
```

### From source

```bash
git clone https://github.com/PasinduYeshan/mailhook
cd mailhook
make install              # installs to $HOME/.local/bin
```

## Uninstall

```bash
mailhook stop                                  # stop daemon if running
rm "$(go env GOPATH)/bin/mailhook"             # if installed via `go install`
# OR (if installed from source):
cd /path/to/mailhook && make uninstall

rm -rf "$HOME/.local/var/mailhook"             # optional: remove logs + pidfile
```

## Quick start

```bash
mailhook start                     # background daemon
open http://127.0.0.1:8091/        # web UI

# Send mail to the catcher.
swaks --server localhost:1025 \
      --from is@test.local --to user@example.com \
      --header "Subject: OTP 482913" --body "Code: 482913"

# Or a webhook.
curl -X POST http://localhost:8090/sms/send -d '{"text":"hi"}'

# Inspect from CLI.
mailhook mail list
mailhook hook list
mailhook tail

mailhook stop                      # when done
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

## License

MIT.
