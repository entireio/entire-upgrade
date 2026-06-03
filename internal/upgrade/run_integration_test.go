package upgrade

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	fakeStableVersion  = "0.6.1"
	fakeNightlyVersion = "0.6.2-nightly.202605160654.ddf1a331"
)

func TestMain(m *testing.M) {
	if os.Getenv("ENTIRE_UPGRADE_FAKE_COMMANDS") == "1" {
		os.Exit(runFakeCommand())
	}
	os.Exit(m.Run())
}

func TestRunWithFakeHomebrewInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew path detection depends on Unix-style cask symlinks")
	}

	h := newFakeHarness(t)
	brewPrefix := filepath.Join(h.dir, "homebrew")
	h.setBrewPrefix(brewPrefix)

	caskBin := filepath.Join(brewPrefix, "Caskroom", "entire", fakeStableVersion, "entire")
	h.installFakeCommandAt(caskBin)
	if err := os.MkdirAll(filepath.Join(brewPrefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	pathBin := filepath.Join(brewPrefix, "bin", "entire")
	if err := os.Symlink(caskBin, pathBin); err != nil {
		t.Fatal(err)
	}
	h.prependPath(filepath.Join(brewPrefix, "bin"), h.bin)

	h.runUpgrade(t, NightlyChannel)
	h.assertInstalledVersion(t, fakeNightlyVersion)
	h.assertLogContains(t,
		"brew tap entireio/tap",
		"brew update",
		"brew uninstall --cask entire",
		"brew install --cask entire@nightly",
	)
}

func TestRunExplicitStableSwitchesFakeHomebrewInstall(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("Homebrew path detection depends on Unix-style cask symlinks")
	}

	h := newFakeHarness(t)
	brewPrefix := filepath.Join(h.dir, "homebrew")
	h.setBrewPrefix(brewPrefix)
	if err := os.WriteFile(h.version, []byte(fakeNightlyVersion), 0o644); err != nil {
		t.Fatal(err)
	}

	caskBin := filepath.Join(brewPrefix, "Caskroom", "entire@nightly", fakeNightlyVersion, "entire")
	h.installFakeCommandAt(caskBin)
	if err := os.MkdirAll(filepath.Join(brewPrefix, "bin"), 0o755); err != nil {
		t.Fatal(err)
	}
	pathBin := filepath.Join(brewPrefix, "bin", "entire")
	if err := os.Symlink(caskBin, pathBin); err != nil {
		t.Fatal(err)
	}
	h.prependPath(filepath.Join(brewPrefix, "bin"), h.bin)

	h.runUpgradeWithOptions(t, Options{Channel: StableChannel, ExplicitChannel: true})
	h.assertInstalledVersion(t, fakeStableVersion)
	h.assertLogContains(t,
		"brew tap entireio/tap",
		"brew update",
		"brew uninstall --cask entire@nightly",
		"brew install --cask entire",
	)
}

func TestRunWithFakeCurlInstall(t *testing.T) {
	h := newFakeHarness(t)
	home := filepath.Join(h.dir, "home")
	h.setHome(home)

	curlBin := filepath.Join(home, ".local", "bin")
	h.installFakeCommandAt(filepath.Join(curlBin, commandFilename("entire")))
	h.prependPath(curlBin, h.bin)

	h.runUpgrade(t, NightlyChannel)
	h.assertInstalledVersion(t, fakeNightlyVersion)
	h.assertLogContains(t,
		"bash -c set -o pipefail; curl -fsSL https://entire.io/install.sh | bash -s -- --channel nightly",
	)
}

func TestRunWithFakeGoInstall(t *testing.T) {
	h := newFakeHarness(t)
	goBin := filepath.Join(h.dir, "go-bin")
	h.setGoBin(goBin)

	h.installFakeCommandAt(filepath.Join(goBin, commandFilename("entire")))
	h.prependPath(goBin, h.bin)

	h.runUpgrade(t, NightlyChannel)
	h.assertInstalledVersion(t, fakeNightlyVersion)
	h.assertLogContains(t,
		"go install github.com/entireio/cli/cmd/entire@v"+fakeNightlyVersion,
		"go install github.com/entireio/cli/cmd/git-remote-entire@v"+fakeNightlyVersion,
	)

	// git-remote-entire ships alongside entire and must land in the same dir.
	gitRemote := filepath.Join(goBin, commandFilename("git-remote-entire"))
	if _, err := os.Stat(gitRemote); err != nil {
		t.Fatalf("git-remote-entire not installed beside entire at %s: %v", gitRemote, err)
	}
}

// TestRunInstallsMissingHelperWhenEntireCurrent reproduces the reported bug:
// entire is already at the latest version, but git-remote-entire is absent. The
// upgrade must still run and install only the missing helper — not bail with
// "already up to date" and not needlessly reinstall entire.
func TestRunInstallsMissingHelperWhenEntireCurrent(t *testing.T) {
	h := newFakeHarness(t)
	goBin := filepath.Join(h.dir, "go-bin")
	h.setGoBin(goBin)

	// entire already at the latest nightly; no git-remote-entire beside it.
	if err := os.WriteFile(h.version, []byte(fakeNightlyVersion), 0o644); err != nil {
		t.Fatal(err)
	}
	h.installFakeCommandAt(filepath.Join(goBin, commandFilename("entire")))
	h.prependPath(goBin, h.bin)

	h.runUpgrade(t, NightlyChannel)
	h.assertInstalledVersion(t, fakeNightlyVersion)

	// Only the helper should be (re)built; entire is already at the target.
	h.assertLogContains(t, "go install github.com/entireio/cli/cmd/git-remote-entire@v"+fakeNightlyVersion)
	if log := h.readLog(t); strings.Contains(log, "go install github.com/entireio/cli/cmd/entire@") {
		t.Fatalf("entire was reinstalled despite being current:\n%s", log)
	}
	if _, err := os.Stat(filepath.Join(goBin, commandFilename("git-remote-entire"))); err != nil {
		t.Fatalf("git-remote-entire not installed beside entire: %v", err)
	}
}

// TestRunUpToDateWhenBothBinariesCurrent confirms the helper-aware gate doesn't
// over-trigger: when entire and git-remote-entire are both at the latest
// version, the upgrade reports "already up to date" and installs nothing.
func TestRunUpToDateWhenBothBinariesCurrent(t *testing.T) {
	h := newFakeHarness(t)
	goBin := filepath.Join(h.dir, "go-bin")
	h.setGoBin(goBin)

	if err := os.WriteFile(h.version, []byte(fakeNightlyVersion), 0o644); err != nil {
		t.Fatal(err)
	}
	h.installFakeCommandAt(filepath.Join(goBin, commandFilename("entire")))
	h.installFakeCommandAt(filepath.Join(goBin, commandFilename("git-remote-entire")))
	h.prependPath(goBin, h.bin)

	var out bytes.Buffer
	if err := Run(context.Background(), Options{Channel: NightlyChannel, Yes: true, Stdout: &out, Stderr: &out}); err != nil {
		t.Fatalf("Run() error = %v\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "already up to date") {
		t.Fatalf("expected up-to-date message, got:\n%s", out.String())
	}
	if log := h.readLog(t); strings.Contains(log, "go install") {
		t.Fatalf("installer ran despite both binaries being current:\n%s", log)
	}
}

func TestRunWithFakeGoInstallReplacesBinaryWhenGobinDiffers(t *testing.T) {
	// When the existing entire lives in $GOPATH/bin but GOBIN points
	// elsewhere (e.g. a mise-managed Go), a plain `go install` would land
	// the new binary in GOBIN and leave the existing one stale. The
	// staging-then-rename path should instead replace the binary at its
	// current location and leave GOBIN alone.
	h := newFakeHarness(t)

	goPath := filepath.Join(h.dir, "gopath")
	existingBinDir := filepath.Join(goPath, "bin")
	t.Setenv("ENTIRE_UPGRADE_FAKE_GOPATH", goPath)
	t.Setenv("GOPATH", goPath)

	foreignGoBin := filepath.Join(h.dir, "foreign-gobin")
	if err := os.MkdirAll(foreignGoBin, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("ENTIRE_UPGRADE_FAKE_GOBIN", foreignGoBin)
	t.Setenv("GOBIN", foreignGoBin)

	existingBinary := filepath.Join(existingBinDir, commandFilename("entire"))
	h.installFakeCommandAt(existingBinary)
	h.prependPath(existingBinDir, h.bin)

	h.runUpgrade(t, NightlyChannel)
	h.assertInstalledVersion(t, fakeNightlyVersion)

	for _, name := range []string{"entire", "git-remote-entire"} {
		if _, err := os.Stat(filepath.Join(foreignGoBin, commandFilename(name))); !os.IsNotExist(err) {
			t.Fatalf("%s leaked into GOBIN at %s; want the existing bin dir written only (stat err: %v)", name, foreignGoBin, err)
		}
	}
	gitRemote := filepath.Join(existingBinDir, commandFilename("git-remote-entire"))
	if _, err := os.Stat(gitRemote); err != nil {
		t.Fatalf("git-remote-entire not installed beside entire at %s: %v", gitRemote, err)
	}
}

func TestRunPromptDeclineAborts(t *testing.T) {
	h := newFakeHarness(t)
	goBin := filepath.Join(h.dir, "go-bin")
	h.setGoBin(goBin)

	h.installFakeCommandAt(filepath.Join(goBin, commandFilename("entire")))
	h.prependPath(goBin, h.bin)

	var out bytes.Buffer
	err := Run(context.Background(), Options{
		Channel: NightlyChannel,
		Stdout:  &out,
		Stderr:  &out,
		Stdin:   strings.NewReader("n\n"),
	})
	if !errors.Is(err, ErrAborted) {
		t.Fatalf("Run() error = %v, want ErrAborted\noutput:\n%s", err, out.String())
	}
	if !strings.Contains(out.String(), "Upgrade Entire CLI from "+fakeStableVersion+" to "+fakeNightlyVersion) {
		t.Fatalf("output missing upgrade prompt:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "Aborted.") {
		t.Fatalf("output missing abort message:\n%s", out.String())
	}
	// Verify the installer was never invoked.
	if log := h.readLog(t); strings.Contains(log, "go install") {
		t.Fatalf("installer ran despite decline:\n%s", log)
	}
	h.assertInstalledVersion(t, fakeStableVersion)
}

func TestRunPromptAcceptProceeds(t *testing.T) {
	h := newFakeHarness(t)
	goBin := filepath.Join(h.dir, "go-bin")
	h.setGoBin(goBin)

	h.installFakeCommandAt(filepath.Join(goBin, commandFilename("entire")))
	h.prependPath(goBin, h.bin)

	var out bytes.Buffer
	err := Run(context.Background(), Options{
		Channel: NightlyChannel,
		Stdout:  &out,
		Stderr:  &out,
		Stdin:   strings.NewReader("\n"),
	})
	if err != nil {
		t.Fatalf("Run() error = %v\noutput:\n%s", err, out.String())
	}
	h.assertInstalledVersion(t, fakeNightlyVersion)
	if !strings.Contains(out.String(), "installer? [Y/n]") {
		t.Fatalf("output missing confirmation prompt:\n%s", out.String())
	}
}

type fakeHarness struct {
	t       *testing.T
	dir     string
	bin     string
	logPath string
	version string
}

func newFakeHarness(t *testing.T) *fakeHarness {
	t.Helper()

	dir := t.TempDir()
	h := &fakeHarness{
		t:       t,
		dir:     dir,
		bin:     filepath.Join(dir, "bin"),
		logPath: filepath.Join(dir, "commands.log"),
		version: filepath.Join(dir, "version.txt"),
	}
	if err := os.MkdirAll(h.bin, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(h.version, []byte(fakeStableVersion), 0o644); err != nil {
		t.Fatal(err)
	}

	h.installFakeCommandAt(filepath.Join(h.bin, commandFilename("brew")))
	h.installFakeCommandAt(filepath.Join(h.bin, commandFilename("go")))
	h.installFakeCommandAt(filepath.Join(h.bin, commandFilename("bash")))

	t.Setenv("ENTIRE_UPGRADE_FAKE_COMMANDS", "1")
	t.Setenv("ENTIRE_UPGRADE_FAKE_STATE_DIR", dir)
	t.Setenv("ENTIRE_UPGRADE_FAKE_STABLE_VERSION", fakeStableVersion)
	t.Setenv("ENTIRE_UPGRADE_FAKE_NIGHTLY_VERSION", fakeNightlyVersion)
	t.Setenv("GITHUB_TOKEN", "")

	server := fakeReleaseServer(t)
	t.Setenv(githubAPIBaseEnv, server.URL)

	return h
}

func (h *fakeHarness) installFakeCommandAt(path string) {
	h.t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		h.t.Fatal(err)
	}

	src, err := os.Open(os.Args[0])
	if err != nil {
		h.t.Fatal(err)
	}
	defer src.Close()

	dst, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		h.t.Fatal(err)
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		h.t.Fatal(err)
	}
	if err := dst.Close(); err != nil {
		h.t.Fatal(err)
	}
}

func (h *fakeHarness) setBrewPrefix(prefix string) {
	h.t.Helper()
	h.t.Setenv("ENTIRE_UPGRADE_FAKE_BREW_PREFIX", prefix)
}

func (h *fakeHarness) setHome(home string) {
	h.t.Helper()
	h.t.Setenv("HOME", home)
	if runtime.GOOS == "windows" {
		h.t.Setenv("USERPROFILE", home)
	}
}

func (h *fakeHarness) setGoBin(goBin string) {
	h.t.Helper()
	h.t.Setenv("ENTIRE_UPGRADE_FAKE_GOBIN", goBin)
	h.t.Setenv("GOBIN", goBin)
	h.t.Setenv("ENTIRE_UPGRADE_FAKE_GOPATH", filepath.Join(h.dir, "gopath"))
}

func (h *fakeHarness) prependPath(dirs ...string) {
	h.t.Helper()
	parts := append([]string{}, dirs...)
	if path := os.Getenv("PATH"); path != "" {
		parts = append(parts, path)
	}
	h.t.Setenv("PATH", strings.Join(parts, string(os.PathListSeparator)))
}

func (h *fakeHarness) runUpgrade(t *testing.T, channel Channel) {
	t.Helper()
	h.runUpgradeWithOptions(t, Options{Channel: channel})
}

func (h *fakeHarness) runUpgradeWithOptions(t *testing.T, opts Options) {
	t.Helper()

	var out bytes.Buffer
	opts.Stdout = &out
	opts.Stderr = &out
	opts.Yes = true
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run() error = %v\noutput:\n%s\nlog:\n%s", err, out.String(), h.readLog(t))
	}
	if !strings.Contains(out.String(), "Entire CLI upgrade complete") {
		t.Fatalf("output did not report completion:\n%s", out.String())
	}
	if !strings.Contains(out.String(), "installed to ") {
		t.Fatalf("output did not report install path:\n%s", out.String())
	}
}

func (h *fakeHarness) assertInstalledVersion(t *testing.T, want string) {
	t.Helper()
	got, err := os.ReadFile(h.version)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != want {
		t.Fatalf("installed version = %q, want %q", strings.TrimSpace(string(got)), want)
	}
}

func (h *fakeHarness) assertLogContains(t *testing.T, wants ...string) {
	t.Helper()
	log := h.readLog(t)
	for _, want := range wants {
		if !strings.Contains(log, want) {
			t.Fatalf("command log missing %q\nlog:\n%s", want, log)
		}
	}
}

func (h *fakeHarness) readLog(t *testing.T) string {
	t.Helper()
	data, err := os.ReadFile(h.logPath)
	if err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	return string(data)
}

func fakeReleaseServer(t *testing.T) *httptest.Server {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/releases/latest":
			_, _ = fmt.Fprintf(w, `{"tag_name":"v%s"}`, fakeStableVersion)
		case "/releases":
			_, _ = fmt.Fprintf(w, `[
				{"tag_name":"v%s"},
				{"tag_name":"v0.6.2-nightly.202605150717.11da3db0"},
				{"tag_name":"v%s"}
			]`, fakeStableVersion, fakeNightlyVersion)
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(server.Close)
	return server
}

func runFakeCommand() int {
	name := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	args := os.Args[1:]
	stateDir := os.Getenv("ENTIRE_UPGRADE_FAKE_STATE_DIR")
	if stateDir == "" {
		fmt.Fprintln(os.Stderr, "missing ENTIRE_UPGRADE_FAKE_STATE_DIR")
		return 2
	}

	switch name {
	case "entire":
		return fakeEntire(stateDir)
	case "git-remote-entire":
		return fakeGitRemoteEntire(stateDir, args)
	case "brew":
		return fakeBrew(stateDir, args)
	case "go":
		return fakeGo(stateDir, args)
	case "bash":
		return fakeBash(stateDir, args)
	default:
		fmt.Fprintf(os.Stderr, "unknown fake command %q\n", name)
		return 2
	}
}

func fakeEntire(stateDir string) int {
	version, err := os.ReadFile(filepath.Join(stateDir, "version.txt"))
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Entire CLI %s (fake)\n", strings.TrimSpace(string(version)))
	return 0
}

// fakeGitRemoteEntire mimics a modern git-remote-entire: `--version` prints a
// parseable line (sharing entire's version, as the real binaries ship in
// lockstep); any other invocation is the remote-helper protocol it doesn't
// model, so it errors like an old binary lacking the flag.
func fakeGitRemoteEntire(stateDir string, args []string) int {
	if len(args) == 1 && args[0] == "--version" {
		version, err := os.ReadFile(filepath.Join(stateDir, "version.txt"))
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		fmt.Printf("git-remote-entire %s (fake)\n", strings.TrimSpace(string(version)))
		return 0
	}
	fmt.Fprintln(os.Stderr, "usage: git-remote-entire <remote-name> <url>")
	return 128
}

func fakeBrew(stateDir string, args []string) int {
	if len(args) == 1 && args[0] == "--prefix" {
		fmt.Println(os.Getenv("ENTIRE_UPGRADE_FAKE_BREW_PREFIX"))
		return 0
	}

	if err := appendFakeLog(stateDir, "brew", args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(args) >= 3 && (args[0] == "install" || args[0] == "upgrade") && args[1] == "--cask" {
		if err := fakeSetVersionForCask(stateDir, args[2]); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
}

func fakeGo(stateDir string, args []string) int {
	if len(args) == 2 && args[0] == "env" {
		switch args[1] {
		case "GOBIN":
			fmt.Println(os.Getenv("ENTIRE_UPGRADE_FAKE_GOBIN"))
		case "GOPATH":
			fmt.Println(os.Getenv("ENTIRE_UPGRADE_FAKE_GOPATH"))
		}
		return 0
	}

	if err := appendFakeLog(stateDir, "go", args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if len(args) == 2 && args[0] == "install" {
		spec := args[1]
		at := strings.LastIndex(spec, "@")
		version := strings.TrimPrefix(spec[at+1:], "v")
		binName := commandFilename(filepath.Base(spec[:at]))
		// The entire binary backs `entire --version`, used for verification.
		// git-remote-entire shares the same release version, so writing it
		// for either package keeps version.txt correct.
		if err := os.WriteFile(filepath.Join(stateDir, "version.txt"), []byte(version), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		// Mirror real `go install`: write the produced binary into $GOBIN.
		// The production code routes this through a staging directory and
		// renames the result over the existing binary.
		if goBin := os.Getenv("GOBIN"); goBin != "" {
			if err := os.MkdirAll(goBin, 0o755); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			executable, err := os.Executable()
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
			if err := copyFakeCommand(executable, filepath.Join(goBin, binName)); err != nil {
				fmt.Fprintln(os.Stderr, err)
				return 1
			}
		}
	}
	return 0
}

func fakeBash(stateDir string, args []string) int {
	if err := appendFakeLog(stateDir, "bash", args); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	channel := StableChannel
	if strings.Contains(strings.Join(args, " "), "--channel nightly") {
		channel = NightlyChannel
	}
	if err := fakeSetVersionForChannel(stateDir, channel); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	// install.sh unpacks git-remote-entire beside entire in ~/.local/bin.
	if home := os.Getenv("HOME"); home != "" {
		executable, err := os.Executable()
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
		helper := filepath.Join(home, ".local", "bin", commandFilename(remoteHelperBinary))
		if err := copyFakeCommand(executable, helper); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}
	return 0
}

func fakeSetVersionForCask(stateDir, cask string) error {
	channel := StableChannel
	if cask == "entire@nightly" {
		channel = NightlyChannel
	}
	if err := fakeSetVersionForChannel(stateDir, channel); err != nil {
		return err
	}
	return fakeInstallBrewCask(cask, fakeVersionForChannel(channel))
}

func fakeSetVersionForChannel(stateDir string, channel Channel) error {
	return os.WriteFile(filepath.Join(stateDir, "version.txt"), []byte(fakeVersionForChannel(channel)), 0o644)
}

func fakeVersionForChannel(channel Channel) string {
	if channel == NightlyChannel {
		return os.Getenv("ENTIRE_UPGRADE_FAKE_NIGHTLY_VERSION")
	}
	return os.Getenv("ENTIRE_UPGRADE_FAKE_STABLE_VERSION")
}

func fakeInstallBrewCask(cask, version string) error {
	prefix := os.Getenv("ENTIRE_UPGRADE_FAKE_BREW_PREFIX")
	if prefix == "" {
		return nil
	}

	executable, err := os.Executable()
	if err != nil {
		return err
	}

	// The cask ships entire and git-remote-entire together; install both so
	// the upgrade's "helper landed beside entire" check sees what a real cask
	// would produce.
	for _, name := range []string{"entire", remoteHelperBinary} {
		caskBin := filepath.Join(prefix, "Caskroom", cask, version, commandFilename(name))
		if err := copyFakeCommand(executable, caskBin); err != nil {
			return err
		}

		pathBin := filepath.Join(prefix, "bin", commandFilename(name))
		if err := os.MkdirAll(filepath.Dir(pathBin), 0o755); err != nil {
			return err
		}
		if err := os.Remove(pathBin); err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.Symlink(caskBin, pathBin); err != nil {
			return err
		}
	}
	return nil
}

func copyFakeCommand(srcPath, dstPath string) error {
	if err := os.MkdirAll(filepath.Dir(dstPath), 0o755); err != nil {
		return err
	}

	src, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer src.Close()

	dst, err := os.OpenFile(dstPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(dst, src); err != nil {
		_ = dst.Close()
		return err
	}
	return dst.Close()
}

func appendFakeLog(stateDir, name string, args []string) error {
	line := strings.Join(append([]string{name}, args...), " ") + "\n"
	path := filepath.Join(stateDir, "commands.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = f.WriteString(line)
	return err
}

func commandFilename(name string) string {
	if runtime.GOOS == "windows" {
		return name + ".exe"
	}
	return name
}
