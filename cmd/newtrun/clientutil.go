package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	newtlabclient "github.com/aldrin-isaac/newtron/pkg/newtlab/client"
	newtronclient "github.com/aldrin-isaac/newtron/pkg/newtron/client"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/client"
)

// newClient constructs a newtrun-server client from the persistent
// --newtrun-server flag, the NEWTRUN_SERVER environment variable, and the
// default. The flag wins over the env var; the env var wins over the default.
//
// TLS posture follows the shared NEWTRON_TLS_CERT/KEY/CA env vars —
// see [httputil.LoadClientTLSConfigFromEnv].
//
// Operator identity comes from the single owner of CLI bearer resolution,
// [newtronclient.ResolveCLIBearer] (NEWTRON_BEARER over the session cache,
// resolved against NEWTRON_USER). Empty key when no identity resolves →
// WithBearer is a no-op, preserving the unenforced quickstart path (#184).
func newClient() *client.Client {
	url := newtrunServerURL()
	tlsCfg, err := httputil.LoadClientTLSConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading client TLS config from env: %v\n", err)
		os.Exit(1)
	}
	// Resolve the operator's identity through the single owner (§27) — same
	// NEWTRON_BEARER / session-cache precedence every in-repo CLI now shares.
	// An empty key reduces WithBearer to a no-op (the no-auth quickstart path).
	bearerKey, err := newtronclient.ResolveCLIBearer(url)
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	// Order matters: WithTLS before WithBearer so the bearer
	// round-tripper wraps the TLS transport rather than being
	// clobbered by it. See client.WithTLS docstring.
	return client.New(url, client.WithTLS(tlsCfg), client.WithBearer(bearerKey))
}

// newtrunServerURL resolves newtrun-server's URL from the standard CLI ladder:
// --newtrun-server flag > NEWTRUN_SERVER env > the default newt-server address.
// The one place the newtrun-server URL ladder lives (§27) — newClient and
// newNewtronClient both resolve through it.
func newtrunServerURL() string {
	return httputil.ResolveServerURL(newtrunServerFlag, "NEWTRUN_SERVER", client.DefaultBaseURL)
}

// newtlabURL resolves the URL for newtlab-server's HTTP surface:
// --newtlab-server flag > NEWTLAB_SERVER env > the default newt-server address
// (its composed listen address). Used by stop / status to read lab state and
// destroy topologies through newtlab's HTTP API — newtlab owns LabState (§27)
// so the CLI consults it via the client rather than reading state.json from disk.
func newtlabURL() string {
	return httputil.ResolveServerURL(newtlabServerFlag, "NEWTLAB_SERVER", client.DefaultBaseURL)
}

// newNewtlabClient constructs a newtlab-server client at newtlabURL()
// through the single owner of the newtlab CLI client build (§27), which
// resolves both identity (NEWTRON_BEARER / session cache) and TLS posture
// (NEWTRON_TLS_CERT/KEY/CA) from the environment. Used by status / stop to
// consult LabState via newtlab's HTTP surface — those reads are gated under
// --enforce-authorization, so the client must carry the operator's identity.
func newNewtlabClient() *newtlabclient.Client {
	c, err := newtlabclient.NewCLIClient(newtlabURL())
	if err != nil {
		fmt.Fprintf(os.Stderr, "%v\n", err)
		os.Exit(1)
	}
	return c
}

// requireServer probes the server's health endpoint and returns a clear
// error message pointing the user to start newtrun-server if it isn't up.
// Every CLI command — read or write — calls this before its real work.
// Strict Option A: the CLI never bypasses the server to read state from
// disk; the server is the single source of truth.
func requireServer(ctx context.Context, c *client.Client) error {
	if _, err := c.Health(ctx); err != nil {
		return fmt.Errorf("newt-server is not running\n\nstart it with: bin/newt-server &")
	}
	return nil
}

// notFoundIsNil returns true when err is a server 404 — used by GET-style
// commands that want to treat absence gracefully (e.g., `status <suite>`
// where the suite has no recorded run).
func notFoundIsNil(err error) bool {
	var se *client.ServerError
	if errors.As(err, &se) {
		return se.StatusCode == 404
	}
	return false
}

// fetchRunStateViaClient is the server-mediated equivalent of
// newtrun.LoadRunState for status / list-class commands. Returns (nil,
// nil) on 404 to match LoadRunState's "absent file" contract — the only
// other place the CLI's status code path special-cases missing state.
func fetchRunStateViaClient(suite string) (*newtrun.RunState, error) {
	c := newClient()
	ctx := context.Background()
	state, err := c.GetRun(ctx, suite)
	if notFoundIsNil(err) {
		return nil, nil
	}
	return state, err
}

// listSuiteNamesViaClient is the server-mediated equivalent of
// newtrun.ListSuiteStates. Returns the suite names known to the server.
func listSuiteNamesViaClient() ([]string, error) {
	c := newClient()
	ctx := context.Background()
	infos, err := c.ListRuns(ctx)
	if err != nil {
		return nil, err
	}
	names := make([]string, len(infos))
	for i, info := range infos {
		names[i] = info.Suite
	}
	return names, nil
}
