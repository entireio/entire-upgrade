# entire-upgrade

An Entire CLI external-command plugin. It is modeled
after `entire-sandbox`: Go + Cobra, `mise` tasks, CI, devcontainer support, and
the `.codex` / `.entire` project config that Entire-enabled repos normally
carry.

Entire external commands are plain executables named `entire-<name>` on `PATH`.
When a user runs `entire <name>`, the parent CLI dispatches to that binary and
passes the remaining arguments through unchanged.

This plugin builds a binary named `entire-upgrade`, which is invoked as:

```sh
entire upgrade
```

## Quick Start

```sh
mise install
mise run test
mise run build

entire plugin install ./entire-upgrade
entire upgrade doctor
```

For local development without installing the binary, run it directly:

```sh
go run ./cmd/entire-upgrade
```

Some commands, such as `doctor` and `config`, expect to run through the Entire
CLI so `ENTIRE_PLUGIN_DATA_DIR` is present. For standalone testing, set it:

```sh
ENTIRE_PLUGIN_DATA_DIR="$(mktemp -d)" go run ./cmd/entire-upgrade doctor
```

## Layout

```text
cmd/entire-upgrade/           Binary entry point
internal/cli/                 Cobra commands and Entire environment handling
internal/config/              Small durable-state example
mise-tasks/lint/              File-based mise lint tasks
.codex/                       Codex hooks and Entire search agent config
.claude/                      Claude Code hooks and Entire search agent config
.entire/                      Entire repo settings and ignored runtime state
```

## Entire Plugin Contract

The parent CLI supplies these variables when it dispatches a plugin:

| Variable | Meaning |
|---|---|
| `ENTIRE_CLI_VERSION` | Parent CLI version, such as `0.42.0` or `dev`. |
| `ENTIRE_REPO_ROOT` | Absolute git worktree root when invoked inside one. |
| `ENTIRE_PLUGIN_DATA_DIR` | Per-plugin durable storage directory. The plugin should create it before writing. |

The plugin runs in the caller's current working directory. The parent CLI
filters the environment before launching third-party plugins; users can opt
additional variables in with `ENTIRE_PLUGIN_ENV`, for example:

```sh
ENTIRE_PLUGIN_ENV='AWS_*,EDITOR' entire deploy
```

External-command plugins do not use a manifest and do not participate in
checkpoint/session protocols. If you need full agent lifecycle integration, use
the separate external agent plugin protocol instead.

## Useful Commands

```sh
mise run fmt        # gofmt -s -w .
mise run lint       # go vet, gofmt check, go mod tidy check, shellcheck
mise run test       # go test ./...
mise run test:ci    # go test -race ./...
mise run build      # build ./entire-upgrade
mise run build-all  # cross-build common Entire targets
```
