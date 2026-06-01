// Package client is the canonical HTTP client for newtlab-server.
// Sibling engines (newtron), CLI tools, and external consumers
// (newtcon) all import this package — per DESIGN_PRINCIPLES §33, the
// called engine owns its public API, and the Go client that consumers
// reach for is part of that public API. There is no separate
// caller-owned copy.
//
// Responses decode into newtlab.LabState directly per §46 ("Wire Shape
// Mirrors Canonical Types"). No translation, no parallel type — the
// canonical type travels.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

// Client talks to newtlab-server. Construct with New.
type Client struct {
	baseURL    string
	httpClient *http.Client
}

// ServerError represents a non-2xx response from newtlab-server.
type ServerError struct {
	StatusCode int
	Message    string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("newtlab-server (%d): %s", e.StatusCode, e.Message)
}

// New constructs a Client targeting newtlab-server at baseURL
// (e.g., "http://127.0.0.1:18080").
func New(baseURL string) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// LabStatus returns the canonical LabState for a deployed topology.
// Calls GET /newtlab/v1/topologies/{name}/status.
func (c *Client) LabStatus(ctx context.Context, topology string) (*newtlab.LabState, error) {
	path := "/newtlab/v1/topologies/" + url.PathEscape(topology) + "/status"
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("newtlabc: build request: %w", err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("newtlabc: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("newtlabc: read body: %w", err)
	}

	var envelope httputil.APIResponse
	if len(body) > 0 {
		if err := json.Unmarshal(body, &envelope); err != nil {
			if resp.StatusCode >= 400 {
				return nil, &ServerError{StatusCode: resp.StatusCode, Message: string(body)}
			}
			return nil, fmt.Errorf("newtlabc: decode envelope: %w", err)
		}
	}

	if resp.StatusCode >= 400 {
		msg := envelope.Error
		if msg == "" {
			msg = resp.Status
		}
		return nil, &ServerError{StatusCode: resp.StatusCode, Message: msg}
	}
	if envelope.Error != "" {
		return nil, &ServerError{StatusCode: resp.StatusCode, Message: envelope.Error}
	}

	if envelope.Data == nil {
		return nil, fmt.Errorf("newtlabc: empty data for topology %q", topology)
	}

	data, err := json.Marshal(envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("newtlabc: re-marshal data: %w", err)
	}
	var state newtlab.LabState
	if err := json.Unmarshal(data, &state); err != nil {
		return nil, fmt.Errorf("newtlabc: decode LabState: %w", err)
	}
	return &state, nil
}
