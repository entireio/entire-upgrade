package upgrade

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime/debug"
	"strings"
)

// remoteHelperBinary is the git remote helper that ships in every Entire CLI
// release beside entire. Its version is read exactly like entire's — `--version`
// first, Go build info as a fallback (see detectHelperVersion).
const remoteHelperBinary = "git-remote-entire"

type Method string

const (
	MethodHomebrew Method = "homebrew"
	MethodCurl     Method = "curl"
	MethodGo       Method = "go"
)

type Installation struct {
	Method       Method
	BinaryPath   string
	ResolvedPath string
	BrewCask     string
	Version      Version
	// HelperPath is where git-remote-entire is expected to live: beside the
	// entire binary on PATH. HelperVersion is its baked-in version, or a
	// zero (Present=false) Version when the helper is missing or its version
	// can't be read.
	HelperPath    string
	HelperVersion Version
}

type Environment struct {
	Home       string
	BrewPrefix string
	GoBin      string
	GoPath     string
}

func DetectInstallation(ctx context.Context) (Installation, error) {
	binaryPath, err := exec.LookPath("entire")
	if err != nil {
		return Installation{}, fmt.Errorf("entire CLI not found on PATH: %w", err)
	}

	resolvedPath, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		resolvedPath = binaryPath
	}

	home, _ := os.UserHomeDir()
	env := Environment{
		Home:       home,
		BrewPrefix: commandOutput(ctx, "brew", "--prefix"),
		GoBin:      commandOutput(ctx, "go", "env", "GOBIN"),
		GoPath:     commandOutput(ctx, "go", "env", "GOPATH"),
	}

	install, ok := ClassifyInstallation(binaryPath, resolvedPath, env)
	if !ok {
		return Installation{}, fmt.Errorf("unsupported Entire CLI installation at %s; supported update methods are Homebrew, install.sh, and go install", binaryPath)
	}

	versionOut, err := exec.CommandContext(ctx, binaryPath, "--version").CombinedOutput()
	if err != nil {
		return Installation{}, versionCommandError(err, versionOut)
	}
	version, err := resolveVersion(versionOut, resolvedPath)
	if err != nil {
		return Installation{}, err
	}
	install.Version = version

	// git-remote-entire ships beside entire and is resolved by git off PATH
	// from the same directory, so look for it there.
	install.HelperPath = filepath.Join(filepath.Dir(install.BinaryPath), executableName(remoteHelperBinary))
	install.HelperVersion = detectHelperVersion(ctx, install.HelperPath)

	return install, nil
}

// resolveVersion turns a binary's `--version` output into a Version, falling
// back to the Go build info compiled into the binary when the output can't be
// parsed (the go-install build whose --version prints "dev"). entire and
// git-remote-entire share this so both report their version identically.
func resolveVersion(versionOut []byte, resolvedPath string) (Version, error) {
	version, err := ParseVersion(string(versionOut))
	if err == nil {
		return version, nil
	}
	buildVersion, buildErr := versionFromGoBuildInfo(resolvedPath)
	if buildErr != nil {
		return Version{}, fmt.Errorf("%w; Go build info fallback failed: %v", err, buildErr)
	}
	return buildVersion, nil
}

// detectHelperVersion resolves git-remote-entire's version the same way as
// entire — `--version`, then build info — but degrades to an absent Version
// instead of erroring, since the helper may be missing or predate the
// --version flag (older releases print a usage banner and exit non-zero). An
// absent Version signals callers to (re)install it.
func detectHelperVersion(ctx context.Context, helperPath string) Version {
	resolved, err := filepath.EvalSymlinks(helperPath)
	if err != nil {
		resolved = helperPath
	}
	if out, err := exec.CommandContext(ctx, helperPath, "--version").CombinedOutput(); err == nil {
		if version, perr := resolveVersion(out, resolved); perr == nil {
			return version
		}
	}
	// No --version (old helper) or unparseable output: read build info directly.
	if version, err := versionFromGoBuildInfo(resolved); err == nil {
		return version
	}
	return Version{}
}

func versionFromGoBuildInfo(binaryPath string) (Version, error) {
	info, err := buildinfo.ReadFile(binaryPath)
	if err != nil {
		return Version{}, fmt.Errorf("read Go build info from %s: %w", binaryPath, err)
	}
	return versionFromBuildInfo(info)
}

func versionFromBuildInfo(info *debug.BuildInfo) (Version, error) {
	if info == nil {
		return Version{}, fmt.Errorf("missing Go build info")
	}

	// Mirror the CLI's versioninfo.resolve: a GoReleaser `-X ...Version=` stamp
	// wins over the module version. `go build -ldflags=...` records that whole
	// flag string in build settings (and `-s -w` doesn't strip it), so release
	// binaries — Homebrew and install.sh — expose their version here even
	// though buildinfo.Main.Version is "(devel)" for a `go build`.
	if raw, ok := ldflagsVersion(info.Settings); ok {
		if version, err := ParseVersion(raw); err == nil {
			return version, nil
		}
	}

	candidates := []string{}
	if isEntireCLIModulePath(info.Main.Path) {
		candidates = append(candidates, info.Main.Version)
	}
	for _, dep := range info.Deps {
		if dep != nil && isEntireCLIModulePath(dep.Path) {
			candidates = append(candidates, dep.Version)
		}
	}

	for _, candidate := range candidates {
		if candidate == "" || candidate == "(devel)" {
			continue
		}
		if version, err := ParseVersion(candidate); err == nil {
			return version, nil
		}
	}
	return Version{}, fmt.Errorf("Go build info did not include an Entire CLI module version")
}

func isEntireCLIModulePath(path string) bool {
	return path == "github.com/entireio/cli" || strings.HasPrefix(path, "github.com/entireio/cli/")
}

// ldflagsVersionRE pulls the version out of the CLI's versioninfo stamp, as it
// appears in the `-ldflags` build setting. GoReleaser emits `-X <path>=<value>`;
// `go build` may render the linker flag as either `-X path=val` or `-X=path=val`.
var ldflagsVersionRE = regexp.MustCompile(`-X[ =]github\.com/entireio/cli/cmd/entire/cli/versioninfo\.Version=(\S+)`)

// ldflagsVersion extracts the versioninfo.Version stamp from the recorded
// `-ldflags` build setting, if present.
func ldflagsVersion(settings []debug.BuildSetting) (string, bool) {
	for _, setting := range settings {
		if setting.Key != "-ldflags" {
			continue
		}
		if m := ldflagsVersionRE.FindStringSubmatch(setting.Value); m != nil {
			return strings.Trim(m[1], `"'`), true
		}
	}
	return "", false
}

func versionCommandError(err error, output []byte) error {
	message := strings.TrimSpace(string(output))
	if message == "" {
		return fmt.Errorf("failed to read installed Entire CLI version: %w", err)
	}
	return fmt.Errorf("failed to read installed Entire CLI version: %w: %s", err, message)
}

func ClassifyInstallation(binaryPath, resolvedPath string, env Environment) (Installation, bool) {
	binaryPath = filepath.Clean(binaryPath)
	resolvedPath = filepath.Clean(resolvedPath)

	install := Installation{
		BinaryPath:   binaryPath,
		ResolvedPath: resolvedPath,
	}

	if cask, ok := brewCaskForPath(resolvedPath, env.BrewPrefix); ok {
		install.Method = MethodHomebrew
		install.BrewCask = cask
		return install, true
	}

	for _, dir := range goBinDirs(env) {
		goPath := filepath.Join(dir, executableName("entire"))
		if samePath(binaryPath, goPath) || samePath(resolvedPath, goPath) {
			install.Method = MethodGo
			return install, true
		}
	}

	if env.Home != "" {
		curlPath := filepath.Join(env.Home, ".local", "bin", executableName("entire"))
		if samePath(binaryPath, curlPath) || samePath(resolvedPath, curlPath) {
			install.Method = MethodCurl
			return install, true
		}
	}

	return Installation{}, false
}

func brewCaskForPath(path, brewPrefix string) (string, bool) {
	type candidate struct {
		dir  string
		cask string
	}
	candidates := []candidate{}
	if brewPrefix != "" {
		candidates = append(candidates,
			candidate{dir: filepath.Join(brewPrefix, "Caskroom", "entire@nightly"), cask: "entire@nightly"},
			candidate{dir: filepath.Join(brewPrefix, "Caskroom", "entire"), cask: "entire"},
		)
	}
	candidates = append(candidates,
		candidate{dir: string(filepath.Separator) + "opt" + string(filepath.Separator) + "homebrew" + string(filepath.Separator) + "Caskroom" + string(filepath.Separator) + "entire@nightly", cask: "entire@nightly"},
		candidate{dir: string(filepath.Separator) + "opt" + string(filepath.Separator) + "homebrew" + string(filepath.Separator) + "Caskroom" + string(filepath.Separator) + "entire", cask: "entire"},
		candidate{dir: string(filepath.Separator) + "usr" + string(filepath.Separator) + "local" + string(filepath.Separator) + "Caskroom" + string(filepath.Separator) + "entire@nightly", cask: "entire@nightly"},
		candidate{dir: string(filepath.Separator) + "usr" + string(filepath.Separator) + "local" + string(filepath.Separator) + "Caskroom" + string(filepath.Separator) + "entire", cask: "entire"},
	)

	for _, candidate := range candidates {
		for _, dir := range pathAliases(candidate.dir) {
			if isSubpath(path, dir) {
				return candidate.cask, true
			}
		}
	}
	return "", false
}

func pathAliases(path string) []string {
	aliases := []string{path}
	if resolved, err := filepath.EvalSymlinks(path); err == nil && resolved != path {
		aliases = append(aliases, resolved)
	}
	return aliases
}

func goBinDirs(env Environment) []string {
	var dirs []string
	seen := map[string]struct{}{}
	addDir := func(dir string) {
		dir = filepath.Clean(dir)
		if _, ok := seen[dir]; ok {
			return
		}
		seen[dir] = struct{}{}
		dirs = append(dirs, dir)
	}
	if env.GoBin != "" {
		addDir(env.GoBin)
	}
	if env.GoPath == "" && env.Home != "" {
		addDir(filepath.Join(env.Home, "go", "bin"))
	}
	for _, p := range filepath.SplitList(env.GoPath) {
		if p == "" {
			continue
		}
		addDir(filepath.Join(p, "bin"))
	}
	return dirs
}

func commandOutput(ctx context.Context, name string, args ...string) string {
	out, err := exec.CommandContext(ctx, name, args...).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func isSubpath(path, dir string) bool {
	path = filepath.Clean(path)
	dir = filepath.Clean(dir)
	rel, err := filepath.Rel(dir, path)
	if err != nil {
		return false
	}
	return rel == "." || (rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)))
}

func samePath(a, b string) bool {
	return filepath.Clean(a) == filepath.Clean(b)
}

func executableName(name string) string {
	if filepath.Ext(name) == "" && os.PathSeparator == '\\' {
		return name + ".exe"
	}
	return name
}
