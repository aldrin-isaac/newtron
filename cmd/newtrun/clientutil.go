package main

import (
	"context"
	"fmt"
	"os"

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

