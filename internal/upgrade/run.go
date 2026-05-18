package upgrade

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
)

type Options struct {
	Channel Channel
	Stdout  io.Writer
	Stderr  io.Writer
}

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
}

type ExecRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

func (r ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	cmd.Stdin = os.Stdin
	return cmd.Run()
}

func Run(ctx context.Context, opts Options) error {
	channel := opts.Channel
	if channel == "" {
		channel = StableChannel
	}
	stdout := opts.Stdout
	if stdout == nil {
		stdout = os.Stdout
	}
	stderr := opts.Stderr
	if stderr == nil {
		stderr = os.Stderr
	}

	install, err := DetectInstallation(ctx)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Detected Entire CLI %s at %s (%s install)\n", install.Version, install.BinaryPath, install.Method)

	latest, err := (ReleaseChecker{}).Latest(ctx, channel)
	if err != nil {
		return err
	}
	fmt.Fprintf(stdout, "Latest %s build is %s\n", channel, latest)

	if latest.Compare(install.Version) <= 0 {
		fmt.Fprintf(stdout, "Entire CLI is already up to date for the %s channel.\n", channel)
		return nil
	}

	fmt.Fprintf(stdout, "Upgrading Entire CLI from %s to %s...\n", install.Version, latest)
	if err := Install(ctx, ExecRunner{Stdout: stdout, Stderr: stderr}, install, latest, channel); err != nil {
		return err
	}

	verified, err := DetectInstallation(ctx)
	if err != nil {
		return fmt.Errorf("upgrade command completed, but verification failed: %w", err)
	}
	if verified.Version.Compare(latest) < 0 {
		return fmt.Errorf("upgrade command completed, but Entire CLI still reports %s; expected at least %s", verified.Version, latest)
	}

	fmt.Fprintf(stdout, "Entire CLI upgrade complete. Now running %s.\n", verified.Version)
	return nil
}

func Install(ctx context.Context, runner Runner, install Installation, target Version, channel Channel) error {
	switch install.Method {
	case MethodHomebrew:
		return installWithHomebrew(ctx, runner, install, channel)
	case MethodCurl:
		return installWithCurl(ctx, runner, channel)
	case MethodGo:
		return installWithGo(ctx, runner, target)
	default:
		return fmt.Errorf("unsupported install method %q", install.Method)
	}
}

func installWithHomebrew(ctx context.Context, runner Runner, install Installation, channel Channel) error {
	targetCask := "entire"
	if channel == NightlyChannel {
		targetCask = "entire@nightly"
	}

	if err := runner.Run(ctx, "brew", "tap", "entireio/tap"); err != nil {
		return fmt.Errorf("brew tap entireio/tap: %w", err)
	}
	if err := runner.Run(ctx, "brew", "update"); err != nil {
		return fmt.Errorf("brew update: %w", err)
	}

	if install.BrewCask != "" && install.BrewCask != targetCask {
		if err := runner.Run(ctx, "brew", "uninstall", "--cask", install.BrewCask); err != nil {
			return fmt.Errorf("brew uninstall --cask %s: %w", install.BrewCask, err)
		}
		if err := runner.Run(ctx, "brew", "install", "--cask", targetCask); err != nil {
			return fmt.Errorf("brew install --cask %s: %w", targetCask, err)
		}
		return nil
	}

	if err := runner.Run(ctx, "brew", "upgrade", "--cask", targetCask); err != nil {
		return fmt.Errorf("brew upgrade --cask %s: %w", targetCask, err)
	}
	return nil
}

func installWithCurl(ctx context.Context, runner Runner, channel Channel) error {
	command := "curl -fsSL https://entire.io/install.sh | bash -s -- --channel " + string(channel)
	if err := runner.Run(ctx, "bash", "-c", command); err != nil {
		return fmt.Errorf("install.sh %s install: %w", channel, err)
	}
	return nil
}

func installWithGo(ctx context.Context, runner Runner, target Version) error {
	module := "github.com/entireio/cli/cmd/entire@" + target.Tag()
	if err := runner.Run(ctx, "go", "install", module); err != nil {
		return fmt.Errorf("go install %s: %w", module, err)
	}
	return nil
}

func ChannelFromNightlyFlag(nightly bool) Channel {
	if nightly {
		return NightlyChannel
	}
	return StableChannel
}

func ParseChannel(s string) (Channel, error) {
	switch Channel(strings.ToLower(strings.TrimSpace(s))) {
	case StableChannel:
		return StableChannel, nil
	case NightlyChannel:
		return NightlyChannel, nil
	default:
		return "", fmt.Errorf("unsupported channel %q", s)
	}
}
