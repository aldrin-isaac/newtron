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
	var state newtlab.LabState
	path := "/newtlab/v1/topologies/" + url.PathEscape(topology) + "/status"
	if err := c.doGet(ctx, path, &state); err != nil {
		return nil, err
	}
	return &state, nil
}

// doGet issues a GET against the newtlab-server, unwraps the
// `{"data": ...}` envelope, and decodes the data into result.
//
// Mirrors the helper pattern used by pkg/newtron/client/client.go so
// both engine clients have the same shape for envelope handling.
func (c *Client) doGet(ctx context.Context, path string, result any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.decodeResponse(resp, result)
}

// decodeResponse unwraps the {"data": ...} envelope returned by every
// newtlab-server response and decodes the data into result. Errors
// from the server (envelope.Error or non-2xx status) become *ServerError.
func (c *Client) decodeResponse(resp *http.Response, result any) error {
	var envelope httputil.APIResponse
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}

	if len(body) == 0 {
		if resp.StatusCode >= 400 {
			return &ServerError{StatusCode: resp.StatusCode, Message: resp.Status}
		}
		return nil
	}

	if err := json.Unmarshal(body, &envelope); err != nil {
		if resp.StatusCode >= 400 {
			return &ServerError{StatusCode: resp.StatusCode, Message: string(body)}
		}
		return fmt.Errorf("decode response: %w", err)
	}

	if resp.StatusCode >= 400 {
		msg := envelope.Error
		if msg == "" {
			msg = resp.Status
		}
		return &ServerError{StatusCode: resp.StatusCode, Message: msg}
	}
	if envelope.Error != "" {
		return &ServerError{StatusCode: resp.StatusCode, Message: envelope.Error}
	}

	if result != nil && envelope.Data != nil {
		data, err := json.Marshal(envelope.Data)
		if err != nil {
			return fmt.Errorf("re-marshal data: %w", err)
		}
		if err := json.Unmarshal(data, result); err != nil {
			return fmt.Errorf("decode data into %T: %w", result, err)
		}
	}

	return nil
}
