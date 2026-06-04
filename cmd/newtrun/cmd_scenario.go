package main

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/spf13/cobra"
)

// newScenarioCmd is the parent for scenario CRUD subcommands. The
// subcommands mirror the HTTP surface 1:1 so an operator can do
// anything newtcon does over the same endpoints from a terminal.
func newScenarioCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "scenario",
		Short: "Create, read, update, and delete scenarios via newtrun-server",
		Long: `Author scenarios on a running newtrun-server. Mirrors the HTTP
surface used by newtcon (the browser frontend):

  newtrun scenario list <suite>                         # list scenarios in a suite
  newtrun scenario get <suite> <name>                   # print scenario YAML to stdout
  newtrun scenario put <suite> <name> --file foo.yaml   # create or update from a file
  cat foo.yaml | newtrun scenario put <suite> <name>    # ...or from stdin
  newtrun scenario delete <suite> <name>                # delete a scenario file

Every write is validated against ParseScenarioBytes on the server; bad
YAML is rejected before the file is touched.`,
	}
	cmd.AddCommand(
		newScenarioListCmd(),
		newScenarioGetCmd(),
		newScenarioPutCmd(),
		newScenarioDeleteCmd(),
	)
	return cmd
}

func newScenarioListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list <suite>",
		Short: "List scenarios in a suite (alias of `newtrun list <suite>`)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}
			return listScenarios(ctx, args[0])
		},
	}
}

func newScenarioGetCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "get <suite> <name>",
		Short: "Print a scenario's YAML to stdout",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}
			body, err := c.GetScenario(ctx, args[0], args[1])
			if err != nil {
				return err
			}
			_, err = os.Stdout.Write(body)
			return err
		},
	}
}

func newScenarioPutCmd() *cobra.Command {
	var file string
	cmd := &cobra.Command{
		Use:   "put <suite> <name>",
		Short: "Create or update a scenario from a YAML file or stdin",
		Long: `Send the YAML body of a scenario to newtrun-server, which
validates it via ParseScenarioBytes and persists it atomically. The
body's name: field must match the URL name argument; mismatches are
rejected before the write.`,
		Args: cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			body, err := readScenarioBody(file)
			if err != nil {
				return err
			}
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}
			if err := c.PutScenario(ctx, args[0], args[1], body); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "wrote scenario %s/%s (%d bytes)\n", args[0], args[1], len(body))
			return nil
		},
	}
	cmd.Flags().StringVarP(&file, "file", "f", "", "YAML file to read (default: stdin)")
	return cmd
}

func newScenarioDeleteCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "delete <suite> <name>",
		Short: "Delete a scenario file",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			c := newClient()
			ctx := cmd.Context()
			if err := requireServer(ctx, c); err != nil {
				return err
			}
			if err := c.DeleteScenario(ctx, args[0], args[1]); err != nil {
				return err
			}
			fmt.Fprintf(os.Stderr, "deleted scenario %s/%s\n", args[0], args[1])
			return nil
		},
	}
}

func readScenarioBody(file string) ([]byte, error) {
	if file == "" || file == "-" {
		return io.ReadAll(os.Stdin)
	}
	return os.ReadFile(file)
}

// newSuiteCmd handles suite-level create/delete. Hidden-aliased to
// `suites` would conflate names; instead the user-facing verb is
// `suite` (singular) for create/delete and the historic `suites`
// (plural, hidden) stays an alias of `list`.
func newSuiteCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "suite",
		Short: "Create or delete suites via newtrun-server",
	}
	cmd.AddCommand(
		newSuiteCreateCmd(),
		&cobra.Command{
			Use:   "delete <name>",
			Short: "Delete an empty suite directory (refuses if scenarios remain)",
			Args:  cobra.ExactArgs(1),
			RunE: func(cmd *cobra.Command, args []string) error {
				return deleteSuite(cmd.Context(), args[0])
			},
		},
	)
	return cmd
}

func newSuiteCreateCmd() *cobra.Command {
	var topology string
	cmd := &cobra.Command{
		Use:   "create <name>",
		Short: "Create a suite directory + suite.yaml manifest on the server",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if topology == "" {
				return fmt.Errorf("--topology is required")
			}
			return createSuite(cmd.Context(), args[0], topology)
		},
	}
	cmd.Flags().StringVar(&topology, "topology", "", "topology name this suite targets (required)")
	_ = cmd.MarkFlagRequired("topology")
	return cmd
}

func createSuite(ctx context.Context, name, topology string) error {
	c := newClient()
	if err := requireServer(ctx, c); err != nil {
		return err
	}
	if err := c.CreateSuite(ctx, name, topology); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "created suite %s (topology=%s)\n", name, topology)
	return nil
}

func deleteSuite(ctx context.Context, name string) error {
	c := newClient()
	if err := requireServer(ctx, c); err != nil {
		return err
	}
	if err := c.DeleteSuite(ctx, name); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "deleted suite %s\n", name)
	return nil
}
