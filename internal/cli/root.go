package cli

import (
	"context"
	"errors"

	"github.com/entireio/entire-upgrade/internal/upgrade"
	"github.com/spf13/cobra"
)

type Options struct {
	Version       string
	Env           EntireEnv
	UpgradeRunner func(context.Context, upgrade.Options) error
}

// Execute runs the plugin root command with the real process environment.
func Execute(version string) error {
	return NewRootCommand(Options{
		Version: version,
		Env:     EnvFromOS(),
	}).Execute()
}

func NewRootCommand(opts Options) *cobra.Command {
	if opts.Version == "" {
		opts.Version = "dev"
	}
	if opts.UpgradeRunner == nil {
		opts.UpgradeRunner = upgrade.Run
	}

	var nightly bool
	var stable bool

	cmd := &cobra.Command{
		Use:           "entire-upgrade",
		Short:         "Upgrade the system-installed Entire CLI",
		Args:          cobra.NoArgs,
		SilenceUsage:  true,
		SilenceErrors: true,
		Long: `entire-upgrade upgrades the Entire CLI binary that is currently on PATH.

It detects whether Entire was installed with Homebrew, install.sh, or go install,
checks the selected release channel, and runs the matching updater only when a
newer build is available.

Examples:
  entire upgrade
  entire upgrade --stable
  entire upgrade --nightly`,
		RunE: func(cmd *cobra.Command, args []string) error {
			if stable && nightly {
				return errors.New("--stable and --nightly cannot be used together")
			}

			channel := upgrade.StableChannel
			explicitChannel := stable
			if nightly {
				channel = upgrade.NightlyChannel
				explicitChannel = true
			}

			return opts.UpgradeRunner(cmd.Context(), upgrade.Options{
				Channel:         channel,
				ExplicitChannel: explicitChannel,
				Stdout:          cmd.OutOrStdout(),
				Stderr:          cmd.ErrOrStderr(),
			})
		},
	}

	cmd.Flags().BoolVar(&stable, "stable", false, "switch back to the latest stable build")
	cmd.Flags().BoolVar(&nightly, "nightly", false, "upgrade to the latest nightly build")
	cmd.AddCommand(newDoctorCommand(opts.Env))
	cmd.AddCommand(newConfigCommand(opts.Env))
	cmd.AddCommand(newVersionCommand(opts.Version))
	return cmd
}
