# Entire Upgrade

An [Entire CLI](https://github.com/entireio/cli) plugin that upgrades the system-installed `entire` binary.

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
Homebrew, `install.sh`, or `go install`, then runs the matching updater. Each
release ships both `entire` and `git-remote-entire`, and the upgrade replaces
both. The Homebrew and `install.sh` paths unpack the two binaries together; the
`go install` path builds and installs each one beside the existing `entire`.

## Installation

```sh
git clone https://github.com/entireio/entire-upgrade.git
cd entire-upgrade
mise build
entire plugin install ./entire-upgrade --force
```

## Development

This project uses [mise](https://mise.jdx.dev/) for task automation and dependency management.

### Prerequisites

- [mise](https://mise.jdx.dev/) - Install with `curl https://mise.run | sh`

### Getting Started

```
# Clone the repository
git clone https://github.com/entireio/entire-upgrade.git
cd cli

# Trust the mise configuration (required on first setup)
mise trust

# Install dependencies (including Go)
mise install

# Build the plugin
mise run build
```

The `doctor`, `config`, and `version` subcommands help with local diagnostics and plugin-environment inspection.
