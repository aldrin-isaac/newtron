package main

import (
	"context"
	"errors"
	"fmt"
	"os"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/client"
)

// newClient constructs a newtrun-server client from the persistent --server
// flag, the NEWTRUN_SERVER environment variable, and the default. The flag
// wins over the env var; the env var wins over the default.
func newClient() *client.Client {
	url := serverFlag
	if url == "" {
		url = os.Getenv("NEWTRUN_SERVER")
	}
	return client.New(url)
}

// requireServer probes the server's health endpoint and returns a clear
// error message pointing the user to start newtrun-server if it isn't up.
// Every state-changing CLI command calls this before its real work.
func requireServer(ctx context.Context, c *client.Client) error {
	if _, err := c.Health(ctx); err != nil {
		// Wrap with the migration hint per the Option A directive: the CLI
		// requires newtrun-server, so a connection refusal is an operator
		// fix, not a tool bug.
		return fmt.Errorf("%w\n\nstart it with: bin/newtrun-server", err)
	}
	return nil
}

// notFoundIsNil checks the error chain for a ServerError with 404 and
// returns true. Used by GET-style commands that want to treat absence
// gracefully.
func notFoundIsNil(err error) bool {
	var se *client.ServerError
	if errors.As(err, &se) {
		return se.StatusCode == 404
	}
	return false
}

// fetchRunStateViaClient is a server-aware drop-in replacement for
// newtrun.LoadRunState used by status/list-class commands. Returns (nil,
// nil) on 404 to match LoadRunState's "absent file" contract.
func fetchRunStateViaClient(suite string) (*newtrun.RunState, error) {
	c := newClient()
	ctx := context.Background()
	return c.GetRun(ctx, suite)
}

// listSuiteNamesViaClient is a server-aware replacement for
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
