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
	"bufio"
	"bytes"
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
	"github.com/aldrin-isaac/newtron/pkg/newtlab/api"
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

// ListTopologies returns the names of every lab newtlab knows about.
// Calls GET /newtlab/v1/topologies. Running and stopped labs are both
// included; per-node state requires LabStatus per topology.
func (c *Client) ListTopologies(ctx context.Context) ([]string, error) {
	var items []api.TopologyListItem
	if err := c.doGet(ctx, "/newtlab/v1/topologies", &items); err != nil {
		return nil, err
	}
	names := make([]string, len(items))
	for i, it := range items {
		names[i] = it.Name
	}
	return names, nil
}

// Deploy submits an async deploy of the named topology to newtlab-
// server and blocks until the deploy reaches a terminal event
// (complete / error). The HTTP request itself returns 202 Accepted
// immediately; this method consumes the per-topology SSE stream and
// waits for completion so callers see a synchronous "deploy succeeded
// or failed" outcome — matching the in-process Lab.Deploy semantics
// that this method replaces.
//
// Returns ConflictError when another deploy is already in flight for
// this topology. ctx cancellation aborts the SSE consumer (the
// server-side deploy may still complete).
func (c *Client) Deploy(ctx context.Context, topology string, opts api.DeployRequest) error {
	if topology == "" {
		return fmt.Errorf("newtlab: topology is required")
	}
	deployPath := "/newtlab/v1/topologies/" + url.PathEscape(topology) + "/deploy"
	var resp api.DeployResponse
	if err := c.doPost(ctx, deployPath, opts, &resp); err != nil {
		return err
	}
	return c.waitForTerminalEvent(ctx, topology)
}

// Destroy tears down the named topology synchronously. Calls
// POST /newtlab/v1/topologies/{name}/destroy.
func (c *Client) Destroy(ctx context.Context, topology string) error {
	if topology == "" {
		return fmt.Errorf("newtlab: topology is required")
	}
	path := "/newtlab/v1/topologies/" + url.PathEscape(topology) + "/destroy"
	return c.doPost(ctx, path, nil, nil)
}

// doPost issues a POST with a JSON body against newtlab-server. body
// may be nil for empty-body POSTs (destroy). result may be nil when
// the caller doesn't need the response payload.
func (c *Client) doPost(ctx context.Context, path string, body any, result any) error {
	var reqBody io.Reader
	if body != nil {
		buf, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode %T: %w", body, err)
		}
		reqBody = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+path, reqBody)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	req.Header.Set("Accept", "application/json")
	if reqBody != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.decodeResponse(resp, result)
}

// waitForTerminalEvent subscribes to the per-topology SSE stream and
// blocks until a terminal event (complete or error) arrives. Used by
// Deploy to provide synchronous semantics over an async server.
//
// The events endpoint emits SSE-framed lines:
//
//	event: phase|complete|error
//	data: {"...json..."}
//
// We only care about terminal types; phase events are ignored at the
// client. Callers needing live phase updates should subscribe to the
// events endpoint directly.
func (c *Client) waitForTerminalEvent(ctx context.Context, topology string) error {
	eventsPath := "/newtlab/v1/topologies/" + url.PathEscape(topology) + "/events"
	// SSE consumer needs an http.Client with no overall timeout — the
	// stream is long-lived. Re-use the same Transport so connection
	// pooling and TLS config carry through.
	sseClient := &http.Client{Transport: c.httpClient.Transport}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+eventsPath, nil)
	if err != nil {
		return fmt.Errorf("subscribe events: %w", err)
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := sseClient.Do(req)
	if err != nil {
		return fmt.Errorf("subscribe events: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return &ServerError{StatusCode: resp.StatusCode, Message: resp.Status}
	}
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 4096), 1<<20) // 1 MiB max event size
	var eventType string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			data := strings.TrimPrefix(line, "data: ")
			switch api.EventType(eventType) {
			case api.EventComplete:
				return nil
			case api.EventError:
				var p api.ErrorPayload
				if err := json.Unmarshal([]byte(data), &p); err == nil && p.Message != "" {
					return fmt.Errorf("newtlab deploy: %s", p.Message)
				}
				return fmt.Errorf("newtlab deploy: server reported error")
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("events stream: %w", err)
	}
	return fmt.Errorf("events stream closed before terminal event")
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
