package cmd

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/sqlc-dev/sqlc/internal/config"
	"github.com/sqlc-dev/sqlc/internal/schemadrift"
)

var ErrSchemaDriftGate = errors.New("schema drift gate failed")

var schemaDriftCmd = &cobra.Command{
	Use:     "schema-drift",
	Aliases: []string{"drift"},
	Short:   "Compare development, staging, and production schemas",
	RunE: func(cmd *cobra.Command, args []string) error {
		stderr := cmd.ErrOrStderr()
		dir, name := getConfigPath(stderr, cmd.Flag("file"))
		gate, err := cmd.Flags().GetBool("gate")
		if err != nil {
			return err
		}
		return runSchemaDriftCheck(cmd.Context(), dir, name, &Options{
			Env:    ParseEnv(cmd),
			Stderr: stderr,
		}, cmd.OutOrStdout(), gate, true)
	},
}

func init() {
	schemaDriftCmd.Flags().Bool("gate", false, "only report differences incompatible with the production schema")
}

func preflightSchemaDrift(ctx context.Context, conf *config.Config, dir string, stderr io.Writer) error {
	for i := range conf.SQL {
		if conf.SQL[i].SchemaDrift != nil && conf.SQL[i].SchemaDrift.Enabled {
			return runSchemaDriftWithConfig(ctx, conf, dir, stderr, false, false)
		}
	}
	return nil
}

func runSchemaDriftWithConfig(ctx context.Context, conf *config.Config, dir string, output io.Writer, forceGate, verbose bool) error {
	_ = ctx
	enabled := 0
	reportBase := "schema-drift-report.md"
	if len(conf.SQL) > 1 {
		reportBase = ""
	}

	for i := range conf.SQL {
		sql := conf.SQL[i]
		if sql.SchemaDrift == nil || !sql.SchemaDrift.Enabled {
			continue
		}
		enabled++

		development := sql.SchemaDrift.Development
		if len(development) == 0 {
			development = sql.Schema
		}
		report, err := schemadrift.Compare(schemadrift.Options{
			Engine: sql.Engine,
			Environments: []schemadrift.Environment{
				{Name: "development", Schema: joinPaths(dir, development)},
				{Name: "staging", Schema: joinPaths(dir, sql.SchemaDrift.Staging)},
				{Name: "production", Schema: joinPaths(dir, sql.SchemaDrift.Production)},
			},
			SummaryThreshold: sql.SchemaDrift.SummaryThreshold,
		})
		if err != nil {
			return err
		}

		reportFile := sql.SchemaDrift.Report
		if reportFile == "" {
			if reportBase == "" {
				reportFile = fmt.Sprintf("schema-drift-%d-report.md", i+1)
			} else {
				reportFile = reportBase
			}
		}
		if strings.HasSuffix(strings.ToLower(reportFile), ".sql") {
			return fmt.Errorf("schema drift: report path %q must not be a schema file", reportFile)
		}
		reportPath := reportFile
		if !filepath.IsAbs(reportPath) {
			reportPath = filepath.Join(dir, reportPath)
		}
		if err := os.MkdirAll(filepath.Dir(reportPath), 0755); err != nil {
			return fmt.Errorf("schema drift: create report directory: %w", err)
		}
		for _, schemaPath := range append(append(joinPaths(dir, development), joinPaths(dir, sql.SchemaDrift.Staging)...), joinPaths(dir, sql.SchemaDrift.Production)...) {
			if filepath.Clean(reportPath) == filepath.Clean(schemaPath) {
				return fmt.Errorf("schema drift: report path %q must not overwrite a schema file", reportFile)
			}
		}
		if err := os.WriteFile(reportPath, []byte(report.RenderComplete()), 0644); err != nil {
			return fmt.Errorf("schema drift: write report: %w", err)
		}

		gated := forceGate || sql.SchemaDrift.Gate
		if gated {
			fmt.Fprint(output, report.RenderGate())
		} else if verbose {
			fmt.Fprint(output, report.RenderFull())
		} else {
			fmt.Fprintf(output, "%s; full report: %s\n", report.Summary(), reportPath)
		}

		if gated && report.HasIncompatible() {
			name := sql.Name
			if name == "" {
				name = fmt.Sprintf("package %d", i+1)
			}
			return fmt.Errorf("%w: %s has %d incompatible difference(s); full report: %s",
				ErrSchemaDriftGate, name, report.IncompatibleCount(), reportPath)
		}
	}
	if enabled == 0 {
		return fmt.Errorf("schema drift is not enabled for any SQL package")
	}
	return nil
}

func runSchemaDriftCheck(ctx context.Context, dir, filename string, o *Options, output io.Writer, forceGate, verbose bool) error {
	_, conf, err := o.ReadConfig(dir, filename)
	if err != nil {
		return err
	}
	if err := config.Validate(conf); err != nil {
		fmt.Fprintf(o.Stderr, "error validating schema drift configuration: %s\n", err)
		return err
	}
	if err := o.Env.Validate(conf); err != nil {
		fmt.Fprintf(o.Stderr, "error validating schema drift configuration: %s\n", err)
		return err
	}
	return runSchemaDriftWithConfig(ctx, conf, dir, output, forceGate, verbose)
}

func joinPaths(dir string, paths config.Paths) []string {
	out := make([]string, 0, len(paths))
	for _, path := range paths {
		out = append(out, filepath.Join(dir, path))
	}
	return out
}
