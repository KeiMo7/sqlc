package cmd

import (
	"context"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/sqlc-dev/sqlc/internal/idempotent"
)

func init() {
	idempotentCmd.Flags().Bool("detail", false, "print the per-file line-level diff in addition to the summary")
	idempotentCmd.Flags().Bool("sources", false, "print the registered nondeterminism sources and how they are stabilized")
}

var idempotentCmd = &cobra.Command{
	Use:   "verify-idempotent",
	Short: "Generate twice and assert byte-for-byte identical output",
	RunE: func(cmd *cobra.Command, args []string) error {
		stderr := cmd.ErrOrStderr()
		dir, name := getConfigPath(stderr, cmd.Flag("file"))

		options := &Options{
			Env:    ParseEnv(cmd),
			Stderr: stderr,
		}
		report, err := idempotent.Check(cmd.Context(), func(ctx context.Context) (map[string]string, error) {
			return Generate(ctx, dir, name, options)
		})
		if err != nil {
			return err
		}

		fmt.Fprintln(stderr, report.Summary())

		showDetail, _ := cmd.Flags().GetBool("detail")
		if showDetail && !report.Stable() {
			fmt.Fprintln(stderr, report.Detail())
		}

		showSources, _ := cmd.Flags().GetBool("sources")
		if showSources {
			fmt.Fprintln(stderr, report.SourceHints())
		}

		if !report.Stable() {
			return fmt.Errorf("generation is not idempotent")
		}
		return nil
	},
}
