// Package client provides an HTTP client for the newtrun-server API.
//
// The newtrun CLI uses this package to drive newtrun-server. The newtcon
// browser frontend (server-side adapter) and newtcon-server's adapter to
// the orchestration engine also consume this package.
package client

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtrun"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/api"
)

// DefaultBaseURL is the default newtrun-server URL when neither flag nor
// environment variable supplies one.
const DefaultBaseURL = "http://127.0.0.1:8081"

// Client is the HTTP client for newtrun-server.
type Client struct {
	baseURL string
	// httpClient applies to short-lived request/response calls. The SSE
	// stream uses a separate no-timeout client because the connection is
	// expected to be long-lived; the caller's context controls termination.
	httpClient *http.Client
	streamClient *http.Client
}

// New constructs a client targeting the given base URL. baseURL must not
// have a trailing slash.
func New(baseURL string) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	return &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		streamClient: &http.Client{}, // no timeout for SSE
	}
}

// ServerError is returned by every client method when the server returned
// an error envelope. Wraps the HTTP status and the server's error message.
type ServerError struct {
	StatusCode int
	Message    string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("newtrun-server returned %d: %s", e.StatusCode, e.Message)
}

// Health pings the server. Used by the CLI to produce a clear error when
// the server isn't running.
func (c *Client) Health(ctx context.Context) (api.HealthResponse, error) {
	var resp api.HealthResponse
	err := c.get(ctx, "/api/health", &resp)
	return resp, err
}

// ListRuns returns the summary list of suite-runs known to the server.
func (c *Client) ListRuns(ctx context.Context) ([]api.RunInfo, error) {
	var resp []api.RunInfo
	err := c.get(ctx, "/api/runs", &resp)
	return resp, err
}

// GetRun returns the full RunState for the named suite. Returns nil, nil
// when the server returns 404.
func (c *Client) GetRun(ctx context.Context, suite string) (*newtrun.RunState, error) {
	var resp newtrun.RunState
	err := c.get(ctx, "/api/runs/"+suite, &resp)
	if err != nil {
		var se *ServerError
		if errorsAs(err, &se) && se.StatusCode == http.StatusNotFound {
			return nil, nil
		}
		return nil, err
	}
	return &resp, nil
}

// StartRun starts a server-side run of a file-backed suite.
func (c *Client) StartRun(ctx context.Context, req api.StartRunRequest) (*api.StartRunResponse, error) {
	var resp api.StartRunResponse
	err := c.post(ctx, "/api/runs", req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// PauseRun requests a graceful pause for the named suite's active run.
func (c *Client) PauseRun(ctx context.Context, suite string) error {
	return c.post(ctx, "/api/runs/"+suite+"/pause", nil, nil)
}

// StopRun cancels the named suite's active run.
func (c *Client) StopRun(ctx context.Context, suite string) error {
	return c.post(ctx, "/api/runs/"+suite+"/stop", nil, nil)
}

// DeleteRun removes the persistent state for the named suite. The server
// rejects with 409 if the run is still active; callers should StopRun
// first when transitioning.
func (c *Client) DeleteRun(ctx context.Context, suite string) error {
	return c.do(ctx, http.MethodDelete, "/api/runs/"+suite, nil, nil)
}

// ListSuites returns the suite names discoverable under the server's
// SuitesBase.
func (c *Client) ListSuites(ctx context.Context) ([]string, error) {
	var resp api.SuitesResponse
	if err := c.get(ctx, "/api/suites", &resp); err != nil {
		return nil, err
	}
	return resp.Suites, nil
}

// ListTopologies returns the topology names discoverable under the
// server's TopologiesBase.
func (c *Client) ListTopologies(ctx context.Context) ([]string, error) {
	var resp api.TopologiesResponse
	if err := c.get(ctx, "/api/topologies", &resp); err != nil {
		return nil, err
	}
	return resp.Topologies, nil
}

// StreamEvents subscribes to the SSE event stream for the named suite.
// The handler is called for each event in delivery order. Returns when
// the connection closes (caller cancels the context) or on a network
// error. Heartbeat comment lines are silently skipped.
func (c *Client) StreamEvents(ctx context.Context, suite string, handle func(api.Event)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/runs/"+suite+"/events", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "text/event-stream")
	resp, err := c.streamClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return readServerError(resp)
	}

	scanner := bufio.NewScanner(resp.Body)
	// Allow longer event lines (up to 1 MB) for large payloads.
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	var eventType string
	var dataLines []string
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, ": "):
			// Comment (heartbeat or initial subscribe marker); ignore.
		case strings.HasPrefix(line, "event: "):
			eventType = strings.TrimPrefix(line, "event: ")
		case strings.HasPrefix(line, "data: "):
			dataLines = append(dataLines, strings.TrimPrefix(line, "data: "))
		case line == "":
			// End of event; dispatch.
			if eventType != "" && len(dataLines) > 0 {
				ev := api.Event{Type: api.EventType(eventType)}
				if err := json.Unmarshal([]byte(strings.Join(dataLines, "\n")), &ev.Payload); err == nil {
					handle(ev)
				}
			}
			eventType = ""
			dataLines = nil
		}
	}
	return scanner.Err()
}

// ----- internal helpers -----

func (c *Client) get(ctx context.Context, path string, out any) error {
	return c.do(ctx, http.MethodGet, path, nil, out)
}

func (c *Client) post(ctx context.Context, path string, in, out any) error {
	return c.do(ctx, http.MethodPost, path, in, out)
}

func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var body io.Reader
	if in != nil {
		buf, err := json.Marshal(in)
		if err != nil {
			return fmt.Errorf("marshal request body: %w", err)
		}
		body = bytes.NewReader(buf)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, body)
	if err != nil {
		return err
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("contacting newtrun-server at %s: %w (is newtrun-server running?)", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 400 {
		return readServerError(resp)
	}
	if out == nil {
		return nil
	}
	var envelope api.APIResponse
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	if envelope.Data == nil {
		return nil
	}
	// Re-marshal Data then decode into out — clean way to translate the
	// envelope's any value into the caller's typed target without a second
	// HTTP read.
	buf, err := json.Marshal(envelope.Data)
	if err != nil {
		return fmt.Errorf("re-encoding response data: %w", err)
	}
	if err := json.Unmarshal(buf, out); err != nil {
		return fmt.Errorf("decoding response data into target type: %w", err)
	}
	return nil
}

func readServerError(resp *http.Response) error {
	var envelope api.APIResponse
	_ = json.NewDecoder(resp.Body).Decode(&envelope)
	msg := envelope.Error
	if msg == "" {
		msg = resp.Status
	}
	return &ServerError{StatusCode: resp.StatusCode, Message: msg}
}

// errorsAs avoids importing errors in this file only for the As call.
func errorsAs(err error, target any) bool {
	se, ok := target.(**ServerError)
	if !ok {
		return false
	}
	for err != nil {
		if e, ok := err.(*ServerError); ok {
			*se = e
			return true
		}
		type unwrapper interface{ Unwrap() error }
		if u, ok := err.(unwrapper); ok {
			err = u.Unwrap()
			continue
		}
		return false
	}
	return false
}
