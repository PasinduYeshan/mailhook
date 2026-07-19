# Contributing

## Build from source

Requires **Go 1.22+** (`brew install go` on macOS).

```bash
git clone https://github.com/PasinduYeshan/mailhook
cd mailhook
make build       # compiles ./mailhook
make install     # installs to $HOME/.local/bin (override with PREFIX=/opt/local)
```

Or, without cloning:

```bash
go install github.com/PasinduYeshan/mailhook/cmd/mailhook@latest
```

Lands in `$(go env GOPATH)/bin` (usually `~/go/bin`) — add it to `PATH` if
not already:

```bash
export PATH="$(go env GOPATH)/bin:$PATH"
```

## Makefile targets

| Target            | What it does                                  |
| ----------------- | ---------------------------------------------- |
| `make build`      | compile `./mailhook`                           |
| `make install`    | build + copy to `$PREFIX/bin`                  |
| `make reinstall`  | uninstall + install                            |
| `make uninstall`  | remove installed binary (state dir kept)       |
| `make run`        | build + run in foreground                      |
| `make vet`        | `go vet ./...`                                 |
| `make fmt`        | `go fmt ./...`                                 |
| `make test`       | `go test ./...`                                |
| `make clean`      | remove local build artifact                    |

## Uninstall (source install)

```bash
mailhook stop           # stop daemon if running
make uninstall
rm -rf "$HOME/.local/var/mailhook"   # optional: remove logs + pidfile
```

## Releasing

Releases are cut via the `Release` GitHub Action
([`.github/workflows/release.yml`](.github/workflows/release.yml)):

1. Go to **Actions → Release → Run workflow**.
2. Pick a bump type: `patch`, `minor`, or `major`.
3. The workflow computes the next semver tag from the latest `vX.Y.Z` tag,
   pushes it, cross-compiles binaries for `linux` and `darwin` (`amd64` +
   `arm64`), and publishes a GitHub Release with the binaries attached.
