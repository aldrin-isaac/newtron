package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/spf13/cobra"
)

// sshCredentialsCmd authors the device SSH login (ssh_user / ssh_pass) at any
// scope — network, zone, or node — the single authoring path for the login (§27,
// mirroring POST .../set-ssh-credentials). Network-scoped: needs -N <network>.
// The login resolves node > zone > network > platform default > "admin" at
// connect time; usually it is set once at network scope.
var sshCredentialsCmd = &cobra.Command{
	Use:   "ssh-credentials",
	Short: "Author the device SSH login at network/zone/node scope",
	Long: `Author the device SSH login (ssh_user / ssh_pass) at any scope.

The login is a scalar per scope; it resolves node > zone > network, then the
platform default, then "admin". Usually set once at network scope — a zone or
node override rests on that base (a scoped override requires a network login).

ssh_pass may be a ${secret:KEY} reference — store the value with 'newtron
secrets' (or POST .../secrets) and reference it here, so the password never
lands in shell history or the spec files.

Examples:
  # network-wide login (the common case)
  newtron -N mynet ssh-credentials set --ssh-user admin --ssh-pass '${secret:net_ssh}'

  # a per-node override
  newtron -N mynet ssh-credentials set --scope node --scope-instance switch1 \
      --ssh-user admin --ssh-pass '${secret:switch1_ssh}'

  # read the login authored at a scope (ssh_pass masked)
  newtron -N mynet ssh-credentials show --scope node --scope-instance switch1

  # remove an override (falls back to the network login)
  newtron -N mynet ssh-credentials clear --scope node --scope-instance switch1

  # read ssh_pass from stdin instead of the command line
  echo -n "$PW" | newtron -N mynet ssh-credentials set --ssh-user admin --ssh-pass -`,
}

var (
	sshCredScope    string
	sshCredInstance string
	sshCredUser     string
	sshCredPass     string
)

func init() {
	sshCredentialsCmd.PersistentFlags().StringVar(&sshCredScope, "scope", "network", "scope: network | zone | node")
	sshCredentialsCmd.PersistentFlags().StringVar(&sshCredInstance, "scope-instance", "", "zone or node name (required for zone/node scope)")

	sshCredSetCmd.Flags().StringVar(&sshCredUser, "ssh-user", "", "SSH username (empty inherits from the next scope up)")
	sshCredSetCmd.Flags().StringVar(&sshCredPass, "ssh-pass", "", "SSH password or ${secret:KEY} reference; \"-\" reads from stdin")

	sshCredentialsCmd.AddCommand(sshCredSetCmd)
	sshCredentialsCmd.AddCommand(sshCredShowCmd)
	sshCredentialsCmd.AddCommand(sshCredClearCmd)
}

var sshCredSetCmd = &cobra.Command{
	Use:   "set",
	Short: "Set the device SSH login at the given scope (upsert)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		pass := sshCredPass
		if pass == "-" {
			b, err := io.ReadAll(bufio.NewReader(os.Stdin))
			if err != nil {
				return fmt.Errorf("reading ssh-pass from stdin: %w", err)
			}
			pass = strings.TrimRight(string(b), "\r\n")
		}
		if err := app.client.SetSSHCredentials(sshCredScopeArg(), sshCredInstance, sshCredUser, pass); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "set ssh login at %s\n", sshCredScopeLabel())
		return nil
	},
}

var sshCredShowCmd = &cobra.Command{
	Use:   "show",
	Short: "Show the device SSH login authored at the given scope (ssh_pass masked)",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		v, err := app.client.ShowSSHCredentials(sshCredScopeArg(), sshCredInstance)
		if err != nil {
			return err
		}
		if app.jsonOutput {
			return json.NewEncoder(os.Stdout).Encode(v)
		}
		fmt.Fprintf(cmd.OutOrStdout(), "scope:     %s\n", sshCredScopeLabel())
		fmt.Fprintf(cmd.OutOrStdout(), "ssh_user:  %s\n", sshCredOrDash(v.SSHUser))
		fmt.Fprintf(cmd.OutOrStdout(), "ssh_pass:  %s\n", sshCredOrDash(v.SSHPass))
		return nil
	},
}

var sshCredClearCmd = &cobra.Command{
	Use:   "clear",
	Short: "Remove the device SSH login override at the given scope",
	Args:  cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		if err := app.client.ClearSSHCredentials(sshCredScopeArg(), sshCredInstance); err != nil {
			return err
		}
		fmt.Fprintf(cmd.OutOrStdout(), "cleared ssh login at %s\n", sshCredScopeLabel())
		return nil
	},
}

// sshCredScopeArg maps the --scope flag to the wire value: "network" (the
// default) becomes "" so the server applies its network default and the wire
// stays minimal; "zone"/"node" pass through.
func sshCredScopeArg() string {
	if sshCredScope == "network" {
		return ""
	}
	return sshCredScope
}

func sshCredScopeLabel() string {
	if sshCredInstance == "" {
		return sshCredScope
	}
	return sshCredScope + " '" + sshCredInstance + "'"
}

func sshCredOrDash(s string) string {
	if s == "" {
		return "- (inherits / not set at this scope)"
	}
	return s
}
