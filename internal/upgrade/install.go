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

// anchorBinary is the executable we discover on PATH to locate the install
// (its directory and install method). Every other release binary is resolved
// beside it. remoteHelperBinary is the git remote helper for entire:// URLs.
const (
	anchorBinary       = "entire"
	remoteHelperBinary = "git-remote-entire"
)

// releaseBinaries is the set of executables shipped in an Entire CLI release.
// They're versioned and (re)installed independently — even though releases
// normally ship them in lockstep, a user's bin dir can drift. Add a binary
// here and detection, gating, install, and verification all pick it up.
var releaseBinaries = []struct {
	Name  string // executable base name
	GoPkg string // module path for `go install`
}{
	{Name: anchorBinary, GoPkg: "github.com/entireio/cli/cmd/entire"},
	{Name: remoteHelperBinary, GoPkg: "github.com/entireio/cli/cmd/git-remote-entire"},
}

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
	// Version is the anchor (entire) version, kept for the channel/compare
	// logic. It mirrors Binaries[0].Version.
	Version Version
	// Binaries are all the release executables this install manages, anchor
	// first, each with its independently detected version (Present=false when
	// missing or unreadable).
	Binaries []ManagedBinary
}

// ManagedBinary is one release executable located beside the anchor.
type ManagedBinary struct {
	Name    string
	GoPkg   string
	Path    string
	Version Version
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

	// Resolve every release binary beside the anchor. The anchor is the one we
	// found on PATH, so its version must be readable; the rest may legitimately
	// be missing or predate --version, in which case an absent Version tells
	// callers to (re)install them.
	binDir := filepath.Dir(binaryPath)
	for _, rb := range releaseBinaries {
		bin := ManagedBinary{Name: rb.Name, GoPkg: rb.GoPkg, Path: filepath.Join(binDir, executableName(rb.Name))}
		version, verr := binaryVersion(ctx, bin.Path)
		if verr != nil {
			if rb.Name == anchorBinary {
				return Installation{}, verr
			}
			version = Version{}
		}
		bin.Version = version
		install.Binaries = append(install.Binaries, bin)
	}
	install.Version = install.Binaries[0].Version

	return install, nil
}

// binaryVersion reads a binary's version the same way for every release
// executable: its `--version` output first, then the Go build info baked into
// the binary (the go-install build whose --version prints "dev", or an older
// git-remote-entire predating the flag). Returns an error only when neither
// source yields a version (including a missing binary).
func binaryVersion(ctx context.Context, path string) (Version, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		resolved = path
	}

	out, cmdErr := exec.CommandContext(ctx, path, "--version").CombinedOutput()
	if cmdErr == nil {
		if version, perr := ParseVersion(string(out)); perr == nil {
			return version, nil
		}
	}
	if version, buildErr := versionFromGoBuildInfo(resolved); buildErr == nil {
		return version, nil
	}
	if cmdErr != nil {
		return Version{}, versionCommandError(cmdErr, out)
	}
	return Version{}, fmt.Errorf("could not determine %s version from --version output or Go build info", filepath.Base(path))
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
