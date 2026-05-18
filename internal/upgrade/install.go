package upgrade

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
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
		return Installation{}, fmt.Errorf("entire CLI not found on PATH")
	}

	resolvedPath, err := filepath.EvalSymlinks(binaryPath)
	if err != nil {
		resolvedPath = binaryPath
	}

	versionOut, err := exec.CommandContext(ctx, binaryPath, "--version").CombinedOutput()
	if err != nil {
		return Installation{}, fmt.Errorf("failed to read installed Entire CLI version: %w", err)
	}
	version, err := ParseVersion(string(versionOut))
	if err != nil {
		return Installation{}, err
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
	install.Version = version
	return install, nil
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

	if env.Home != "" {
		curlPath := filepath.Join(env.Home, ".local", "bin", executableName("entire"))
		if samePath(binaryPath, curlPath) || samePath(resolvedPath, curlPath) {
			install.Method = MethodCurl
			return install, true
		}
	}

	for _, dir := range goBinDirs(env) {
		goPath := filepath.Join(dir, executableName("entire"))
		if samePath(binaryPath, goPath) || samePath(resolvedPath, goPath) {
			install.Method = MethodGo
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
	if env.GoBin != "" {
		dirs = append(dirs, env.GoBin)
	}
	if env.GoPath == "" && env.Home != "" {
		dirs = append(dirs, filepath.Join(env.Home, "go", "bin"))
	}
	for _, p := range filepath.SplitList(env.GoPath) {
		if p == "" {
			continue
		}
		dirs = append(dirs, filepath.Join(p, "bin"))
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
