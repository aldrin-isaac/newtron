package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	newtlabclient "github.com/aldrin-isaac/newtron/pkg/newtlab/client"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/client"
)

// newClient constructs a newtrun-server client from the persistent
// --newtrun-server flag, the NEWTRUN_SERVER environment variable, and the
// default. The flag wins over the env var; the env var wins over the default.
// TLS posture follows the shared NEWTRON_TLS_CERT/KEY/CA env vars —
// see [httputil.LoadClientTLSConfigFromEnv].
func newClient() *client.Client {
	url := newtrunServerFlag
	if url == "" {
		url = os.Getenv("NEWTRUN_SERVER")
	}
	tlsCfg, err := httputil.LoadClientTLSConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading client TLS config from env: %v\n", err)
		os.Exit(1)
	}
	return client.New(url, client.WithTLS(tlsCfg))
}

// newtlabURL resolves the URL for newtlab-server's HTTP surface from
// the persistent --newtlab-server flag, the NEWTLAB_SERVER env var,
// and the default `http://127.0.0.1:18080` (newt-server's composed
// listen address). Used by stop / status to read lab state and
// destroy topologies through newtlab's HTTP API — newtlab owns
// LabState (§27) so the CLI consults it via the client rather than
// reading state.json from disk.
func newtlabURL() string {
	url := newtlabServerFlag
	if url == "" {
		url = os.Getenv("NEWTLAB_SERVER")
	}
	if url == "" {
		url = "http://127.0.0.1:18080"
	}
	return url
}

// newNewtlabClient constructs a newtlab-server client at newtlabURL()
// with TLS posture from NEWTRON_TLS_CERT/KEY/CA — same env vars the
// other in-repo CLI clients honor (auth-design.md L2a). Used by
// status / stop to consult LabState via newtlab's HTTP surface.
func newNewtlabClient() *newtlabclient.Client {
	tlsCfg, err := httputil.LoadClientTLSConfigFromEnv()
	if err != nil {
		fmt.Fprintf(os.Stderr, "loading client TLS config from env: %v\n", err)
		os.Exit(1)
	}
	return newtlabclient.New(newtlabURL(), newtlabclient.WithTLS(tlsCfg))
}

// requireServer probes the server's health endpoint and returns a clear
// error message pointing the user to start newtrun-server if it isn't up.
// Every CLI command — read or write — calls this before its real work.
// Strict Option A: the CLI never bypasses the server to read state from
// disk; the server is the single source of truth.
func requireServer(ctx context.Context, c *client.Client) error {
	if _, err := c.Health(ctx); err != nil {
		return fmt.Errorf("newtrun-server is not running\n\nstart it with: bin/newtrun-server")
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
