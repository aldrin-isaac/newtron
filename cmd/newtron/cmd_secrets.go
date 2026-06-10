package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/aldrin-isaac/newtron/pkg/newtron/secret"
)

// secretsCmd is the operator-facing CLI for managing the secret store
// configured by --secret-store on newtron-server / newt-server
// (auth-design.md L0). The store is a JSON map of key→plaintext value
// at a file path the operator chooses; this command edits it.
//
// All subcommands require --store=PATH. The path is the same file
// the server is configured against — the server doesn't watch for
// changes, so a server restart (or ReloadNetwork on each affected
// network) is required for additions to take effect.
var secretsCmd = &cobra.Command{
	Use:   "secrets",
	Short: "Manage the operator-configured secret store (auth-design.md L0)",
	Long: `Manage the secret store referenced by ${secret:KEY} in spec values.

The store is a JSON file at --store=PATH with mode 0600. Server
processes (newtron-server, newt-server) open the same file with
--secret-store=PATH and resolve references at network load.

Examples:
  newtron secrets --store ~/.newtron/secrets.json put switch1-ssh YourPaSsWoRd
  newtron secrets --store ~/.newtron/secrets.json list
  newtron secrets --store ~/.newtron/secrets.json get switch1-ssh
  newtron secrets --store ~/.newtron/secrets.json delete switch1-ssh

When the value would be visible on the operator's shell history,
use the - sentinel to read from stdin instead:

  echo -n "$SECRET" | newtron secrets --store ~/.newtron/secrets.json put switch1-ssh -

The server doesn't auto-reload the store. After editing, either
restart the server or call ReloadNetwork on each affected network.`,
}

// storePath is the --store flag value, populated by cobra. Each
// secretsCmd subcommand reads it at runtime; the FileStore validates
// the path and refuses to open with broader-than-0600 permissions.
var secretsStorePath string

func init() {
	secretsCmd.PersistentFlags().StringVar(&secretsStorePath, "store", "", "path to the secret store JSON file (required)")
	secretsCmd.MarkPersistentFlagRequired("store") //nolint:errcheck // never errors at init time

	secretsCmd.AddCommand(secretsPutCmd)
	secretsCmd.AddCommand(secretsGetCmd)
	secretsCmd.AddCommand(secretsListCmd)
	secretsCmd.AddCommand(secretsDeleteCmd)
}

var secretsPutCmd = &cobra.Command{
	Use:   "put KEY VALUE",
	Short: "Set a secret value at KEY",
	Long: `Set the value at KEY. Overwrites any existing value.

When VALUE is the single character "-", the value is read from stdin
instead — preferred when the secret would otherwise appear in shell
history. Stdin is read until EOF; trailing newlines are stripped.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := secret.NewFileStore(secretsStorePath)
		if err != nil {
			return err
		}
		key := args[0]
		value := args[1]
		if value == "-" {
			b, err := io.ReadAll(bufio.NewReader(os.Stdin))
			if err != nil {
				return fmt.Errorf("reading value from stdin: %w", err)
			}
			value = strings.TrimRight(string(b), "\r\n")
		}
		if err := store.Set(key, value); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "set %s\n", key)
		return nil
	},
}

var secretsGetCmd = &cobra.Command{
	Use:   "get KEY",
	Short: "Print the value at KEY",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := secret.NewFileStore(secretsStorePath)
		if err != nil {
			return err
		}
		v, err := store.Get(args[0])
		if err != nil {
			return err
		}
		// No trailing newline — for pipe-into-other-command use.
		fmt.Fprint(cmd.OutOrStdout(), v)
		return nil
	},
}

var secretsListCmd = &cobra.Command{
	Use:   "list",
	Short: "List secret keys (values are not printed)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		store, err := secret.NewFileStore(secretsStorePath)
		if err != nil {
			return err
		}
		keys, err := store.List()
		if err != nil {
			return err
		}
		for _, k := range keys {
			fmt.Fprintln(cmd.OutOrStdout(), k)
		}
		return nil
	},
}

var secretsDeleteCmd = &cobra.Command{
	Use:   "delete KEY",
	Short: "Remove the value at KEY",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		store, err := secret.NewFileStore(secretsStorePath)
		if err != nil {
			return err
		}
		if err := store.Delete(args[0]); err != nil {
			var nf *secret.ErrNotFound
			if errors.As(err, &nf) {
				fmt.Fprintf(cmd.OutOrStdout(), "%s (was not set)\n", args[0])
				return nil
			}
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "deleted %s\n", args[0])
		return nil
	},
}
