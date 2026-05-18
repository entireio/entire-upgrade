package upgrade

import (
	"bytes"
	"context"
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
	)
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
	if err := Run(context.Background(), opts); err != nil {
		t.Fatalf("Run() error = %v\noutput:\n%s\nlog:\n%s", err, out.String(), h.readLog(t))
	}
	if !strings.Contains(out.String(), "Entire CLI upgrade complete") {
		t.Fatalf("output did not report completion:\n%s", out.String())
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
		version := args[1][strings.LastIndex(args[1], "@")+1:]
		version = strings.TrimPrefix(version, "v")
		if err := os.WriteFile(filepath.Join(stateDir, "version.txt"), []byte(version), 0o644); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
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

	caskBin := filepath.Join(prefix, "Caskroom", cask, version, commandFilename("entire"))
	if err := copyFakeCommand(executable, caskBin); err != nil {
		return err
	}

	pathBin := filepath.Join(prefix, "bin", commandFilename("entire"))
	if err := os.MkdirAll(filepath.Dir(pathBin), 0o755); err != nil {
		return err
	}
	if err := os.Remove(pathBin); err != nil && !os.IsNotExist(err) {
		return err
	}
	return os.Symlink(caskBin, pathBin)
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
