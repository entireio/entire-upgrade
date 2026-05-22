package upgrade

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

type Options struct {
	Channel         Channel
	ExplicitChannel bool
	Stdout          io.Writer
	Stderr          io.Writer
	Stdin           io.Reader
	// Yes skips the interactive confirmation prompt.
	Yes bool
}

// ErrAborted is returned when the user declines the confirmation prompt.
var ErrAborted = errors.New("upgrade aborted by user")

type Runner interface {
	Run(ctx context.Context, name string, args ...string) error
	// RunEnv runs the command with extra env entries appended to the process env.
	// Each entry is a "KEY=value" string; later entries override earlier ones.
	RunEnv(ctx context.Context, env []string, name string, args ...string) error
}

type ExecRunner struct {
	Stdout io.Writer
	Stderr io.Writer
}

const installScriptGitHubTokenEnv = "ENTIRE_UPGRADE_GITHUB_TOKEN"

func (r ExecRunner) Run(ctx context.Context, name string, args ...string) error {
	return r.RunEnv(ctx, nil, name, args...)
}

func (r ExecRunner) RunEnv(ctx context.Context, env []string, name string, args ...string) error {
	cmd := exec.CommandContext(ctx, name, args...)
	cmd.Stdout = r.Stdout
	cmd.Stderr = r.Stderr
	cmd.Stdin = os.Stdin
	cmd.Env = append(commandEnvWithoutGitHubToken(os.Environ()), env...)
	return cmd.Run()
}

func commandEnvWithoutGitHubToken(env []string) []string {
	filtered := env[:0]
	for _, entry := range env {
		if strings.HasPrefix(entry, "GITHUB_TOKEN=") {
			continue
		}
		filtered = append(filtered, entry)
	}
	return filtered
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
	stdin := opts.Stdin
	if stdin == nil {
		stdin = os.Stdin
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

	compare := latest.Compare(install.Version)
	channelSwitch := opts.ExplicitChannel && !installationMatchesChannel(install, channel)
	if compare <= 0 && !channelSwitch {
		fmt.Fprintf(stdout, "Entire CLI is already up to date for the %s channel.\n", channel)
		return nil
	}

	action := "upgrade"
	actionLabel := "Upgrading"
	if compare < 0 || channelSwitch {
		action = "switch"
		actionLabel = "Switching"
	}

	fmt.Fprintf(stdout, "We will now %s Entire CLI from %s to %s using the %s installer.\n", action, install.Version, latest, install.Method)
	if !opts.Yes {
		ok, err := confirm(stdout, stdin, "Continue? [Y/n] ")
		if err != nil {
			return err
		}
		if !ok {
			fmt.Fprintln(stdout, "Aborted.")
			return ErrAborted
		}
	}

	fmt.Fprintf(stdout, "%s Entire CLI from %s to %s...\n", actionLabel, install.Version, latest)
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
	if !installationMatchesChannel(verified, channel) {
		return fmt.Errorf("upgrade command completed, but Entire CLI is still on the %s channel; expected %s", installedChannel(verified), channel)
	}

	fmt.Fprintf(stdout, "Entire CLI upgrade complete: entire %s installed to %s (via %s).\n", verified.Version, verified.BinaryPath, verified.Method)
	return nil
}

// confirm reads a yes/no answer from r, defaulting to yes on an empty line.
// EOF is treated as a decline so non-interactive callers without --yes do not
// silently proceed.
func confirm(w io.Writer, r io.Reader, prompt string) (bool, error) {
	fmt.Fprint(w, prompt)
	scanner := bufio.NewScanner(r)
	if !scanner.Scan() {
		if err := scanner.Err(); err != nil {
			return false, fmt.Errorf("read confirmation: %w", err)
		}
		fmt.Fprintln(w)
		return false, nil
	}
	answer := strings.ToLower(strings.TrimSpace(scanner.Text()))
	switch answer {
	case "", "y", "yes":
		return true, nil
	default:
		return false, nil
	}
}

func installationMatchesChannel(install Installation, channel Channel) bool {
	return installedChannel(install) == channel
}

func installedChannel(install Installation) Channel {
	if install.Method == MethodHomebrew {
		switch install.BrewCask {
		case "entire@nightly":
			return NightlyChannel
		case "entire":
			return StableChannel
		}
	}
	if install.Version.IsNightly() {
		return NightlyChannel
	}
	return StableChannel
}

func Install(ctx context.Context, runner Runner, install Installation, target Version, channel Channel) error {
	if err := validateChannel(channel); err != nil {
		return err
	}

	switch install.Method {
	case MethodHomebrew:
		return installWithHomebrew(ctx, runner, install, channel)
	case MethodCurl:
		return installWithCurl(ctx, runner, channel)
	case MethodGo:
		return installWithGo(ctx, runner, install, target)
	default:
		return fmt.Errorf("unsupported install method %q", install.Method)
	}
}

func validateChannel(channel Channel) error {
	switch channel {
	case StableChannel, NightlyChannel:
		return nil
	default:
		return fmt.Errorf("unsupported release channel %q", channel)
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
	command := installScriptCommand(channel)
	if err := runner.Run(ctx, "bash", "-c", command); err != nil {
		return fmt.Errorf("install.sh %s install: %w", channel, err)
	}
	return nil
}

func installScriptCommand(channel Channel) string {
	command := "curl -fsSL https://entire.io/install.sh | bash -s -- --channel " + string(channel)
	if os.Getenv(installScriptGitHubTokenEnv) != "" {
		return "set -o pipefail; GITHUB_TOKEN=\"$" + installScriptGitHubTokenEnv + "\" bash -c '" + command + "'"
	}
	return "set -o pipefail; " + command
}

func installWithGo(ctx context.Context, runner Runner, install Installation, target Version) error {
	if !target.Present {
		return fmt.Errorf("go install target version is empty")
	}
	module := "github.com/entireio/cli/cmd/entire@" + target.Tag()

	// Route `go install` to a private staging dir so we can place the new
	// binary exactly where the existing one lives, regardless of the user's
	// GOBIN/GOPATH config. Otherwise `go install` would land it in $GOBIN,
	// which may not be the directory their PATH resolves `entire` from.
	stagingDir, err := os.MkdirTemp("", "entire-upgrade-go-*")
	if err != nil {
		return fmt.Errorf("create staging dir: %w", err)
	}
	defer os.RemoveAll(stagingDir)

	if err := runner.RunEnv(ctx, []string{"GOBIN=" + stagingDir}, "go", "install", module); err != nil {
		return fmt.Errorf("go install %s: %w", module, err)
	}

	stagedBinary := filepath.Join(stagingDir, executableName("entire"))
	if _, err := os.Stat(stagedBinary); err != nil {
		return fmt.Errorf("go install %s completed but produced no binary at %s: %w", module, stagedBinary, err)
	}

	if err := replaceBinary(stagedBinary, install.BinaryPath); err != nil {
		return fmt.Errorf("replace %s: %w", install.BinaryPath, err)
	}
	return nil
}

// replaceBinary atomically replaces dst with the contents of src. The new
// file is staged as a sibling of dst so the rename stays on one filesystem.
func replaceBinary(src, dst string) error {
	dir := filepath.Dir(dst)
	tmp, err := os.CreateTemp(dir, ".entire-upgrade-*")
	if err != nil {
		return fmt.Errorf("create temp file in %s: %w", dir, err)
	}
	tmpPath := tmp.Name()
	cleanup := func() { _ = os.Remove(tmpPath) }

	srcFile, err := os.Open(src)
	if err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	defer srcFile.Close()

	if _, err := io.Copy(tmp, srcFile); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Chmod(0o755); err != nil {
		_ = tmp.Close()
		cleanup()
		return err
	}
	if err := tmp.Close(); err != nil {
		cleanup()
		return err
	}
	if err := os.Rename(tmpPath, dst); err != nil {
		cleanup()
		return err
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
