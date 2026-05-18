package upgrade

import (
	"context"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestClassifyInstallation(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "Users", "tester")
	goPath := filepath.Join(home, "go")
	exe := commandFilename("entire")
	env := Environment{
		Home:       home,
		BrewPrefix: "/opt/homebrew",
		GoPath:     goPath,
	}

	tests := []struct {
		name     string
		path     string
		resolved string
		method   Method
		cask     string
	}{
		{
			name:     "homebrew nightly",
			path:     "/opt/homebrew/bin/entire",
			resolved: "/opt/homebrew/Caskroom/entire@nightly/0.6.2-nightly.202605160654.ddf1a331/entire",
			method:   MethodHomebrew,
			cask:     "entire@nightly",
		},
		{
			name:     "homebrew stable",
			path:     "/opt/homebrew/bin/entire",
			resolved: "/opt/homebrew/Caskroom/entire/0.6.1/entire",
			method:   MethodHomebrew,
			cask:     "entire",
		},
		{
			name:     "curl install.sh",
			path:     filepath.Join(home, ".local", "bin", exe),
			resolved: filepath.Join(home, ".local", "bin", exe),
			method:   MethodCurl,
		},
		{
			name:     "go install",
			path:     filepath.Join(goPath, "bin", exe),
			resolved: filepath.Join(goPath, "bin", exe),
			method:   MethodGo,
		},
		{
			name:     "go install symlinked from local bin",
			path:     filepath.Join(home, ".local", "bin", exe),
			resolved: filepath.Join(goPath, "bin", exe),
			method:   MethodGo,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := ClassifyInstallation(tt.path, tt.resolved, env)
			if !ok {
				t.Fatal("expected install to be classified")
			}
			if got.Method != tt.method {
				t.Fatalf("method = %s, want %s", got.Method, tt.method)
			}
			if got.BrewCask != tt.cask {
				t.Fatalf("cask = %q, want %q", got.BrewCask, tt.cask)
			}
		})
	}
}

func TestGoBinDirsDeduplicates(t *testing.T) {
	home := filepath.Join(string(filepath.Separator), "Users", "tester")
	goPath := filepath.Join(home, "go")
	got := goBinDirs(Environment{
		Home:   home,
		GoBin:  filepath.Join(goPath, "bin"),
		GoPath: goPath,
	})
	want := []string{filepath.Join(goPath, "bin")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dirs = %#v, want %#v", got, want)
	}
}

func TestInstallCommands(t *testing.T) {
	target := mustVersion(t, "0.6.2-nightly.202605160654.ddf1a331")

	tests := []struct {
		name    string
		install Installation
		channel Channel
		want    []string
	}{
		{
			name:    "homebrew same cask upgrades",
			install: Installation{Method: MethodHomebrew, BrewCask: "entire@nightly"},
			channel: NightlyChannel,
			want: []string{
				"brew tap entireio/tap",
				"brew update",
				"brew upgrade --cask entire@nightly",
			},
		},
		{
			name:    "homebrew switches cask",
			install: Installation{Method: MethodHomebrew, BrewCask: "entire"},
			channel: NightlyChannel,
			want: []string{
				"brew tap entireio/tap",
				"brew update",
				"brew uninstall --cask entire",
				"brew install --cask entire@nightly",
			},
		},
		{
			name:    "homebrew switches back to stable cask",
			install: Installation{Method: MethodHomebrew, BrewCask: "entire@nightly"},
			channel: StableChannel,
			want: []string{
				"brew tap entireio/tap",
				"brew update",
				"brew uninstall --cask entire@nightly",
				"brew install --cask entire",
			},
		},
		{
			name:    "curl uses selected channel",
			install: Installation{Method: MethodCurl},
			channel: NightlyChannel,
			want: []string{
				"bash -c set -o pipefail; curl -fsSL https://entire.io/install.sh | bash -s -- --channel nightly",
			},
		},
		{
			name:    "go installs exact target",
			install: Installation{Method: MethodGo},
			channel: NightlyChannel,
			want: []string{
				"go install github.com/entireio/cli/cmd/entire@v0.6.2-nightly.202605160654.ddf1a331",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runner := &recordRunner{}
			if err := Install(context.Background(), runner, tt.install, target, tt.channel); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(runner.commands, tt.want) {
				t.Fatalf("commands = %#v, want %#v", runner.commands, tt.want)
			}
		})
	}
}

func TestInstallRejectsUnsupportedChannel(t *testing.T) {
	runner := &recordRunner{}
	err := Install(context.Background(), runner, Installation{Method: MethodCurl}, mustVersion(t, "0.6.1"), Channel("stable; evil"))
	if err == nil {
		t.Fatal("expected unsupported channel to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %#v, want none", runner.commands)
	}
}

func TestInstallRejectsEmptyGoTarget(t *testing.T) {
	runner := &recordRunner{}
	err := Install(context.Background(), runner, Installation{Method: MethodGo}, Version{}, StableChannel)
	if err == nil {
		t.Fatal("expected empty go target to fail")
	}
	if len(runner.commands) != 0 {
		t.Fatalf("commands = %#v, want none", runner.commands)
	}
}

func TestCommandEnvWithoutGitHubToken(t *testing.T) {
	got := commandEnvWithoutGitHubToken([]string{
		"PATH=/bin",
		"GITHUB_TOKEN=secret",
		"HOME=/tmp/home",
	})
	want := []string{"PATH=/bin", "HOME=/tmp/home"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("env = %#v, want %#v", got, want)
	}
}

func TestVersionCommandErrorIncludesOutput(t *testing.T) {
	err := versionCommandError(errors.New("exit status 1"), []byte("broken install\n"))
	if !strings.Contains(err.Error(), "broken install") {
		t.Fatalf("error = %q, want command output", err)
	}
}

type recordRunner struct {
	commands []string
}

func (r *recordRunner) Run(_ context.Context, name string, args ...string) error {
	r.commands = append(r.commands, strings.Join(append([]string{name}, args...), " "))
	return nil
}
