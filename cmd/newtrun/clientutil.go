package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/client"
)

// newClient constructs a newtrun-server client from the persistent
// --newtrun-server flag, the NEWTRUN_SERVER environment variable, and the
// default. The flag wins over the env var; the env var wins over the default.
func newClient() *client.Client {
	url := newtrunServerFlag
	if url == "" {
		url = os.Getenv("NEWTRUN_SERVER")
	}
	return client.New(url)
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
