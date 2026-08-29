// Package cli is btw's command line: the daemon, and the few things an operator needs a
// shell for.
package cli

import (
	"fmt"
	"log/slog"
	"os"

	"github.com/spf13/cobra"

	"btw/internal/app"
	"btw/internal/config"
	"btw/internal/store"
)

func root() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "btw",
		Short: "A place to put a thought down and stop carrying it",
		Long: "btw holds the things you mean to do that have no when, and puts one of them\n" +
			"on your phone at an hour nobody picked.",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	// Cobra's Print helpers write to stderr unless an output is set, which would make
	// `link=$(btw invite)` capture nothing at all — the one thing anybody wants to do with
	// that command.
	cmd.SetOut(os.Stdout)
	cmd.AddCommand(serveCmd(), inviteCmd(), healthcheckCmd(), versionCmd())
	return cmd
}

// Execute runs the command line and returns a process exit code.
func Execute() int {
	if err := root().Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "btw:", err)
		return 1
	}
	return 0
}

// setup is what every command that touches data needs: the environment, a logger, and the
// databases.
func setup() (*config.Config, *store.Store, *slog.Logger, error) {
	cfg, err := config.Load()
	if err != nil {
		return nil, nil, nil, err
	}
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: cfg.LogLevel}))

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		return nil, nil, nil, err
	}
	return cfg, st, log, nil
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print the version this binary was built from",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cmd.Println(app.Version)
			return nil
		},
	}
}
