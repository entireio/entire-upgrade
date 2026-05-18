package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/entireio/entire-upgrade/internal/upgrade"
	"github.com/spf13/cobra"
)

func execute(t *testing.T, cmd *cobra.Command, args ...string) (string, error) {
	t.Helper()

	var stdout bytes.Buffer
	cmd.SetOut(&stdout)
	cmd.SetErr(&stdout)
	cmd.SetArgs(args)
	err := cmd.Execute()
	return stdout.String(), err
}

func TestRootUpgradeDefaultsToStable(t *testing.T) {
	var got upgrade.Options
	cmd := NewRootCommand(Options{
		Version: "test-version",
		UpgradeRunner: func(_ context.Context, opts upgrade.Options) error {
			got = opts
			return nil
		},
	})

	out, err := execute(t, cmd)
	if err != nil {
		t.Fatalf("execute root: %v\n%s", err, out)
	}
	if got.Channel != upgrade.StableChannel {
		t.Fatalf("channel = %s, want %s", got.Channel, upgrade.StableChannel)
	}
	if got.ExplicitChannel {
		t.Fatal("default stable channel should not be explicit")
	}
}

func TestRootUpgradeNightlyFlag(t *testing.T) {
	var got upgrade.Options
	cmd := NewRootCommand(Options{
		Version: "test-version",
		UpgradeRunner: func(_ context.Context, opts upgrade.Options) error {
			got = opts
			return nil
		},
	})

	out, err := execute(t, cmd, "--nightly")
	if err != nil {
		t.Fatalf("execute root: %v\n%s", err, out)
	}
	if got.Channel != upgrade.NightlyChannel {
		t.Fatalf("channel = %s, want %s", got.Channel, upgrade.NightlyChannel)
	}
	if !got.ExplicitChannel {
		t.Fatal("nightly flag did not mark channel explicit")
	}
}

func TestRootUpgradeStableFlag(t *testing.T) {
	var got upgrade.Options
	cmd := NewRootCommand(Options{
		Version: "test-version",
		UpgradeRunner: func(_ context.Context, opts upgrade.Options) error {
			got = opts
			return nil
		},
	})

	out, err := execute(t, cmd, "--stable")
	if err != nil {
		t.Fatalf("execute root: %v\n%s", err, out)
	}
	if got.Channel != upgrade.StableChannel {
		t.Fatalf("channel = %s, want %s", got.Channel, upgrade.StableChannel)
	}
	if !got.ExplicitChannel {
		t.Fatal("stable flag did not mark channel explicit")
	}
}

func TestRootUpgradeRejectsConflictingChannelFlags(t *testing.T) {
	cmd := NewRootCommand(Options{Version: "test-version"})

	out, err := execute(t, cmd, "--stable", "--nightly")
	if err == nil {
		t.Fatalf("expected conflicting flags to fail\n%s", out)
	}
}

func TestRootUpgradeRejectsPositionalArgs(t *testing.T) {
	cmd := NewRootCommand(Options{Version: "test-version"})

	out, err := execute(t, cmd, "stable")
	if err == nil {
		t.Fatalf("expected positional args to fail\n%s", out)
	}
}

func TestDoctorRequiresPluginDataDir(t *testing.T) {
	cmd := NewRootCommand(Options{Version: "test-version"})
	out, err := execute(t, cmd, "doctor")
	if err == nil {
		t.Fatal("doctor returned nil error without plugin data dir")
	}
	if !strings.Contains(out, "ENTIRE_PLUGIN_DATA_DIR=<unset>") {
		t.Fatalf("doctor output missing environment dump:\n%s", out)
	}
}

func TestDoctorCreatesWritablePluginDataDir(t *testing.T) {
	dataDir := t.TempDir() + "/plugin-data"
	cmd := NewRootCommand(Options{
		Version: "test-version",
		Env:     EntireEnv{PluginDataDir: dataDir},
	})

	out, err := execute(t, cmd, "doctor")
	if err != nil {
		t.Fatalf("doctor: %v", err)
	}
	if !strings.Contains(out, "plugin data dir: writable") {
		t.Fatalf("doctor output missing writable status:\n%s", out)
	}
}

func TestConfigInitAndShow(t *testing.T) {
	dataDir := t.TempDir()
	cmd := NewRootCommand(Options{
		Version: "test-version",
		Env:     EntireEnv{PluginDataDir: dataDir},
	})

	out, err := execute(t, cmd, "config", "init")
	if err != nil {
		t.Fatalf("config init: %v", err)
	}
	if !strings.Contains(out, "config.json") {
		t.Fatalf("config init output missing path:\n%s", out)
	}

	cmd = NewRootCommand(Options{
		Version: "test-version",
		Env:     EntireEnv{PluginDataDir: dataDir},
	})
	out, err = execute(t, cmd, "config", "show")
	if err != nil {
		t.Fatalf("config show: %v", err)
	}
	if !strings.Contains(out, `"greeting": "Hello from an Entire plugin"`) {
		t.Fatalf("config show output missing default greeting:\n%s", out)
	}
}
