package upgrade

import (
	"context"
	"path/filepath"
	"reflect"
	"runtime/debug"
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

func TestVersionFromBuildInfoUsesMainModuleVersion(t *testing.T) {
	got, err := versionFromBuildInfo(&debug.BuildInfo{
		Main: debug.Module{
			Path:    "github.com/entireio/cli/cmd/entire",
			Version: "v0.6.1",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.6.1" {
		t.Fatalf("version = %s, want 0.6.1", got)
	}
}

func TestVersionFromBuildInfoUsesEntireDependencyVersion(t *testing.T) {
	got, err := versionFromBuildInfo(&debug.BuildInfo{
		Main: debug.Module{
			Path:    "github.com/example/not-entire",
			Version: "(devel)",
		},
		Deps: []*debug.Module{
			{Path: "github.com/entireio/cli", Version: "v0.6.2-nightly.202605160654.ddf1a331"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.6.2-nightly.202605160654.ddf1a331" {
		t.Fatalf("version = %s", got)
	}
}

func TestVersionFromBuildInfoPrefersLdflagsStamp(t *testing.T) {
	// Release binaries (Homebrew, install.sh) carry "(devel)" as the module
	// version but a real version in the -ldflags stamp, just like git-remote-entire.
	got, err := versionFromBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Path: "github.com/entireio/cli/cmd/git-remote-entire", Version: "(devel)"},
		Settings: []debug.BuildSetting{
			{Key: "-ldflags", Value: "-s -w -X github.com/entireio/cli/cmd/entire/cli/versioninfo.Version=0.7.4 -X github.com/entireio/cli/cmd/entire/cli/versioninfo.Commit=deadbeef"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.7.4" {
		t.Fatalf("version = %s, want 0.7.4", got)
	}
}

func TestVersionFromBuildInfoLdflagsStampWinsOverModule(t *testing.T) {
	// The stamp is what `entire --version` reports, so it must win over the
	// module version when they differ, matching the CLI's versioninfo.resolve.
	got, err := versionFromBuildInfo(&debug.BuildInfo{
		Main: debug.Module{Path: "github.com/entireio/cli/cmd/entire", Version: "v0.6.1"},
		Settings: []debug.BuildSetting{
			{Key: "-ldflags", Value: "-X github.com/entireio/cli/cmd/entire/cli/versioninfo.Version=0.7.4"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.String() != "0.7.4" {
		t.Fatalf("version = %s, want 0.7.4 (ldflags stamp should win)", got)
	}
}

func TestVersionFromBuildInfoRejectsDevelVersion(t *testing.T) {
	_, err := versionFromBuildInfo(&debug.BuildInfo{
		Main: debug.Module{
			Path:    "github.com/entireio/cli/cmd/entire",
			Version: "(devel)",
		},
	})
	if err == nil {
		t.Fatal("expected devel build info version to fail")
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
		// Go-install command-args verification lives in the integration test
		// (TestRunWithFakeGoInstall), since the production path now stages to a
		// temp dir and renames the resulting binary — operations that need a
		// real subprocess to exercise.
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

func TestInstallCurlUsesScopedGitHubTokenEnv(t *testing.T) {
	t.Setenv(installScriptGitHubTokenEnv, "secret")

	runner := &recordRunner{}
	err := Install(context.Background(), runner, Installation{Method: MethodCurl}, mustVersion(t, "0.6.1"), StableChannel)
	if err != nil {
		t.Fatal(err)
	}

	want := []string{
		"bash -c set -o pipefail; GITHUB_TOKEN=\"$ENTIRE_UPGRADE_GITHUB_TOKEN\" bash -c 'curl -fsSL https://entire.io/install.sh | bash -s -- --channel stable'",
	}
	if !reflect.DeepEqual(runner.commands, want) {
		t.Fatalf("commands = %#v, want %#v", runner.commands, want)
	}
	if strings.Contains(runner.commands[0], "secret") {
		t.Fatalf("command leaked token value: %q", runner.commands[0])
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

type recordRunner struct {
	commands []string
	envs     [][]string
}

func (r *recordRunner) Run(ctx context.Context, name string, args ...string) error {
	return r.RunEnv(ctx, nil, name, args...)
}

func (r *recordRunner) RunEnv(_ context.Context, env []string, name string, args ...string) error {
	r.commands = append(r.commands, strings.Join(append([]string{name}, args...), " "))
	r.envs = append(r.envs, env)
	return nil
}
