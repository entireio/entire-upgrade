package upgrade

import (
	"context"
	"debug/buildinfo"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"strings"
)

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
	version, err := ParseVersion(string(versionOut))
	if err != nil {
		if install.Method != MethodGo {
			return Installation{}, err
		}
		parseErr := err
		version, err = versionFromGoBuildInfo(resolvedPath)
		if err != nil {
			return Installation{}, fmt.Errorf("%w; Go build info fallback failed: %v", parseErr, err)
		}
	}
	install.Version = version
	return install, nil
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
