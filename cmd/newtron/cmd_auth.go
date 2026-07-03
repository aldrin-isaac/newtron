package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron/client"
)

// `newtron auth login` / `newtron auth logout` / `newtron auth status`.
// Implements the operator-side of the L2c session-key arc: one
// interactive login per user per machine, the resulting Bearer
// cached in ~/.newtron/sessions/<user>@<host>.json (one file per
// (user, server) pair), every subsequent newtron /
// newtrun / newtlab invocation reuses it without re-prompting.
//
// auth-design.md §L2c "Programmatic clients" sentence explicitly
// motivates this pattern ("A long-running automation embeds a key
// obtained at start-up rather than a password embedded in env vars
// or config"). The CLI flow is the human-operator analog — the
// key is in a 0600 file under $HOME, not env vars; the operator
// re-logs in when it expires.
//
// The login subprocess is the only place in the newtron CLI that
// uses HTTP Basic — the rest of the CLI authenticates via the
// cached Bearer wired through client.WithBearer.

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Authenticate against newtron-server (login / logout / status)",
	Long: `Manage the per-user session-key cache that the newtron, newtrun,
and newtlab CLIs read on every invocation. One login mints a key
that survives shell sessions; logout revokes it both server-side
and on disk.

  newtron auth login     # prompt, POST /auth/login, save to ~/.newtron/sessions/<user>@<host>.json
  newtron auth logout    # POST /auth/logout, delete the cache file
  newtron auth status    # show the cached user / server / expiry

The session file is mode 0600 — only your user account can read
it. A second login replaces any earlier session for the same
server.`,
	GroupID: "meta",
}

var authLoginCmd = &cobra.Command{
	Use:   "login",
	Short: "Mint a session key via POST /auth/login and cache it on disk",
	Long: `Prompt for a username and password, send them to newtron-server's
/auth/login endpoint via HTTP Basic auth, and write the returned
session key to ~/.newtron/sessions/<user>@<host>.json (mode 0600).

Subsequent newtron / newtrun / newtlab invocations read the cache
and carry Authorization: Bearer <key> on every outbound HTTP call
— no further password prompts until logout or expiry.

The --user flag skips the username prompt; the password is always
read interactively (no echo). This command is for human operators at
terminals. Scripted callers can POST directly to
/newt-server/v1/auth/login with HTTP Basic and persist the returned
key wherever they need it (the cache layout under ~/.newtron/sessions/
is documented in docs/newtron/pam-howto.md).`,
	Args: cobra.NoArgs,
	RunE: runAuthLogin,
}

var authLogoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Revoke the cached session key (server-side and on disk)",
	Long: `Read the cached session key, POST /auth/logout to the server it
was minted against, and delete the cached file under
~/.newtron/sessions/.

If the server is unreachable, the local file is still deleted —
the operator's intent ("my key should no longer work from this
machine") is satisfied. The key remains valid server-side until
its TTL expires; an operator who needs immediate server-side
revocation must reach the server some other way.

Idempotent: logout with no cached session is a no-op (exit 0,
prints "no cached session").`,
	Args: cobra.NoArgs,
	RunE: runAuthLogout,
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the cached session (user, server, expiry)",
	Long: `Read the cached session record and print its user, the newtron
server it was minted against, and when it expires. Useful for
"who am I and how much time do I have" before kicking off a
long-running suite.

Exit code 0 when a valid session is cached; exit code 1 when none
is cached (or the cache is expired, which Load() treats the same
way as missing).`,
	Args: cobra.NoArgs,
	RunE: runAuthStatus,
}

var authLoginUser string

func init() {
	authCmd.AddCommand(authLoginCmd, authLogoutCmd, authStatusCmd)
	authLoginCmd.Flags().StringVarP(&authLoginUser, "user", "u", "", "username to log in as (skips the username prompt)")
	authLogoutCmd.Flags().StringVarP(&authLoginUser, "user", "u", "", "log out a specific cached user; required when multiple sessions are cached for the same server")
	rootCmd.AddCommand(authCmd)
}

// runAuthLogin prompts for credentials, calls /auth/login, and
// persists the returned record. The username comes from --user if
// set; the password is always read via term.ReadPassword (no
// echo, no scrollback exposure).
func runAuthLogin(cmd *cobra.Command, _ []string) error {
	server := app.serverURL
	if server == "" {
		server = httputil.DefaultServerURL
	}
	user := authLoginUser
	if user == "" {
		prompt, err := promptLine(os.Stderr, fmt.Sprintf("Username (for %s): ", server))
		if err != nil {
			return fmt.Errorf("reading username: %w", err)
		}
		user = strings.TrimSpace(prompt)
	}
	if user == "" {
		return fmt.Errorf("username is required")
	}
	pass, err := promptPassword(os.Stderr, "Password: ")
	if err != nil {
		return fmt.Errorf("reading password: %w", err)
	}
	if pass == "" {
		return fmt.Errorf("password is required")
	}

	rec, err := minAuthLogin(server, user, pass)
	if err != nil {
		return err
	}
	if err := client.SaveSessionFor(rec); err != nil {
		return fmt.Errorf("saving session: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Logged in as %s (server %s); session expires %s.\n",
		rec.User, rec.Server, rec.ExpiresAt.Local().Format(time.RFC1123))
	fmt.Fprintf(os.Stderr, "Session cached at %s (mode 0600).\n", client.SessionPath(rec.User, rec.Server))
	return nil
}

// runAuthLogout revokes server-side then deletes the local cache.
// Server unreachability does not block local deletion (the
// operator's "log me out from this machine" intent is unconditional).
//
// Which session to log out is resolved via LoadCLISession: --user
// X picks that user's cache; with no flag, the single cached
// session is logged out, or all-cached-from-server when there are
// multiple but only one matches the named server. Multiple
// ambiguous sessions force the operator to disambiguate with
// --user so we never log out the wrong identity.
func runAuthLogout(cmd *cobra.Command, _ []string) error {
	rec, err := client.LoadCLISession(authLoginUser, app.serverURL)
	if err != nil {
		return fmt.Errorf("reading session: %w", err)
	}
	if rec == nil {
		fmt.Fprintln(os.Stderr, "No cached session.")
		return nil
	}
	if err := postAuthLogout(rec.Server, rec.Key); err != nil {
		fmt.Fprintf(os.Stderr, "WARNING: server-side revoke failed (%v); deleting local cache anyway.\n", err)
	}
	if err := client.DeleteSessionFor(rec.User, rec.Server); err != nil {
		return fmt.Errorf("deleting session file: %w", err)
	}
	fmt.Fprintf(os.Stderr, "Logged out %s@%s.\n", rec.User, rec.Server)
	return nil
}

// runAuthStatus prints every cached session record or reports
// none. Exit code 1 on the no-session path so shell scripts can
// branch on "$( newtron auth status >/dev/null; echo $? )".
func runAuthStatus(cmd *cobra.Command, _ []string) error {
	all, problems, err := client.ListSessions()
	if err != nil {
		return fmt.Errorf("listing sessions: %w", err)
	}
	for _, p := range problems {
		// Surface cache-file problems even when other sessions
		// loaded cleanly — most importantly the insecure-permissions
		// case where a Bearer may already have leaked. Operator
		// inspects + chmods or re-logs in.
		fmt.Fprintf(os.Stderr, "WARNING: %s: %v\n", p.Path, p.Err)
	}
	if len(all) == 0 {
		if len(problems) == 0 {
			fmt.Fprintln(os.Stderr, "No cached sessions.")
		}
		os.Exit(1)
	}
	if len(problems) > 0 {
		fmt.Fprintln(os.Stderr)
	}
	for i, rec := range all {
		if i > 0 {
			fmt.Fprintln(os.Stderr)
		}
		fmt.Fprintf(os.Stderr, "User:       %s\n", rec.User)
		fmt.Fprintf(os.Stderr, "Server:     %s\n", rec.Server)
		fmt.Fprintf(os.Stderr, "Expires:    %s (in %s)\n",
			rec.ExpiresAt.Local().Format(time.RFC1123),
			time.Until(rec.ExpiresAt).Round(time.Second))
		fmt.Fprintf(os.Stderr, "Cached at:  %s\n", client.SessionPath(rec.User, rec.Server))
	}
	return nil
}

// minAuthLogin drives POST /auth/login with HTTP Basic and decodes
// the {data, error} envelope. Bypasses pkg/newtron/client's higher
// layers (no envelope helper here) because the call is one-shot
// and predates any cached Bearer — using the regular client would
// fight the cache-priming use case. Logs the raw error.
func minAuthLogin(server, user, pass string) (*client.SessionRecord, error) {
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(server, "/")+"/newt-server/v1/auth/login",
		bytes.NewReader(nil))
	if err != nil {
		return nil, fmt.Errorf("building login request: %w", err)
	}
	req.SetBasicAuth(user, pass)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("login: HTTP %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	var env struct {
		Data struct {
			Key       string    `json:"key"`
			ExpiresAt time.Time `json:"expires_at"`
			User      string    `json:"user"`
		} `json:"data"`
		Error string `json:"error"`
	}
	if err := json.Unmarshal(body, &env); err != nil {
		return nil, fmt.Errorf("decoding login response: %w", err)
	}
	if env.Error != "" {
		return nil, fmt.Errorf("login: %s", env.Error)
	}
	if env.Data.Key == "" {
		return nil, fmt.Errorf("login: server returned empty key")
	}
	return &client.SessionRecord{
		Server:    server,
		User:      env.Data.User,
		Key:       env.Data.Key,
		ExpiresAt: env.Data.ExpiresAt,
	}, nil
}

// postAuthLogout drives POST /auth/logout. Returns nil on 204
// (the documented success status) or any 2xx; non-2xx + transport
// errors propagate so the caller can warn the operator.
func postAuthLogout(server, key string) error {
	req, err := http.NewRequest(http.MethodPost,
		strings.TrimRight(server, "/")+"/newt-server/v1/auth/logout",
		bytes.NewReader(nil))
	if err != nil {
		return fmt.Errorf("building logout request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+key)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("logout: HTTP %s: %s", resp.Status, strings.TrimSpace(string(body)))
	}
	return nil
}

// promptLine reads a single line from stdin, echoing the prompt
// to w. Used for non-secret inputs (username); the password path
// goes through promptPassword which disables echo.
func promptLine(w io.Writer, prompt string) (string, error) {
	fmt.Fprint(w, prompt)
	var line string
	if _, err := fmt.Scanln(&line); err != nil {
		return "", err
	}
	return line, nil
}

// promptPassword reads a password — interactively from the tty
// when stdin is a terminal (echo disabled via x/term), or one
// line from stdin when stdin is not a tty (scripted operator
// setup, e.g. a CI script that pre-populates the session cache
// for the 1node-vs-auth test suite via `printf '%s\n' "$pw" |
// newtron auth login --user alice`).
//
// The non-tty path intentionally trims a trailing newline only —
// any leading whitespace or other shell garbage in the piped
// input is the operator's problem. Operators who pipe whitespace-
// padded passwords get a no-match-against-PAM failure, which is
// the right surface error for "your shell quoting is broken."
func promptPassword(w io.Writer, prompt string) (string, error) {
	if term.IsTerminal(int(syscall.Stdin)) {
		fmt.Fprint(w, prompt)
		pw, err := term.ReadPassword(int(syscall.Stdin))
		fmt.Fprintln(w)
		if err != nil {
			return "", err
		}
		return string(pw), nil
	}
	// stdin is not a tty — read one line. No prompt is printed
	// (it would be invisible to the script feeding stdin).
	var line string
	if _, err := fmt.Fscanln(os.Stdin, &line); err != nil && line == "" {
		return "", err
	}
	return line, nil
}
