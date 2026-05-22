# entire-upgrade

An Entire CLI plugin that upgrades the system-installed `entire` binary.

This plugin builds a binary named `entire-upgrade`, which is invoked as:

```sh
entire upgrade
```

By default it checks the stable release channel and installs only when the
latest stable build is newer than the current binary. Use `--nightly` to opt
into the latest nightly build:

```sh
entire upgrade --nightly
```

If you are running a nightly build and want to move back to the latest stable
release, use:

```sh
entire upgrade --stable
```

The plugin detects whether the active `entire` binary was installed through
Homebrew, `install.sh`, or `go install`, then runs the matching updater.

## Installation & development

```sh
git clone https://github.com/entireio/entire-upgrade.git
cd entire-upgrade
mise install && mise build
entire plugin install ./entire-upgrade --force
```

For local development without installing the binary, run it directly:

```sh
go run ./cmd/entire-upgrade
```

The `doctor`, `config`, and `version` subcommands are for local
diagnostics and plugin-environment inspection.

