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
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
	"github.com/aldrin-isaac/newtron/pkg/newtrun/api"
)

// DefaultBaseURL is the default URL the newtrun CLI dials. Points at
// newtser (port 18080), which fronts every backend by path prefix —
// the CLI's URLs start with /newtrun/v1/ so newtser routes them to
// newtrun-server on its loopback port (:19081). For standalone use
// without newtser, override with --newtrun-server http://127.0.0.1:19081.
const DefaultBaseURL = "http://127.0.0.1:18080"

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
// have a trailing slash. Functional options configure transport-level
// concerns (TLS for L2a inter-service mTLS, etc.) without changing the
// signature for the common case.
func New(baseURL string, opts ...Option) *Client {
	if baseURL == "" {
		baseURL = DefaultBaseURL
	}
	c := &Client{
		baseURL:      strings.TrimRight(baseURL, "/"),
		httpClient:   &http.Client{Timeout: 30 * time.Second},
		streamClient: &http.Client{}, // no timeout for SSE
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Option configures a Client at construction.
type Option func(*Client)

// WithTLS attaches a *tls.Config to both the request/response client
// and the SSE stream client (auth-design.md L2a). nil tlsCfg keeps
// the default plain-HTTP transport — the disabled state. Build the
// config with httputil.LoadClientTLSConfig.
func WithTLS(tlsCfg *tls.Config) Option {
	return func(c *Client) {
		if tlsCfg == nil {
			return
		}
		tr := &http.Transport{TLSClientConfig: tlsCfg}
		c.httpClient.Transport = tr
		c.streamClient.Transport = tr
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
	err := c.get(ctx, "/newtrun/v1/health", &resp)
	return resp, err
}

// ListRuns returns the summary list of suite-runs known to the server.
func (c *Client) ListRuns(ctx context.Context) ([]api.RunInfo, error) {
	var resp []api.RunInfo
	err := c.get(ctx, "/newtrun/v1/runs", &resp)
	return resp, err
}

// GetRun returns the full RunState for the named suite. Returns nil, nil
// when the server returns 404.
func (c *Client) GetRun(ctx context.Context, suite string) (*newtrun.RunState, error) {
	var resp newtrun.RunState
	err := c.get(ctx, "/newtrun/v1/runs/"+suite, &resp)
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
	err := c.post(ctx, "/newtrun/v1/runs", req, &resp)
	if err != nil {
		return nil, err
	}
	return &resp, nil
}

// PauseRun requests a graceful pause for the named suite's active run.
func (c *Client) PauseRun(ctx context.Context, suite string) error {
	return c.post(ctx, "/newtrun/v1/runs/"+suite+"/pause", nil, nil)
}

// StopRun cancels the named suite's active run.
func (c *Client) StopRun(ctx context.Context, suite string) error {
	return c.post(ctx, "/newtrun/v1/runs/"+suite+"/stop", nil, nil)
}

// DeleteRun removes the persistent state for the named suite. The server
// rejects with 409 if the run is still active; callers should StopRun
// first when transitioning.
func (c *Client) DeleteRun(ctx context.Context, suite string) error {
	return c.do(ctx, http.MethodDelete, "/newtrun/v1/runs/"+suite, nil, nil)
}

// ListSuites returns the suite names discoverable under the server's
// SuitesBase.
func (c *Client) ListSuites(ctx context.Context) ([]string, error) {
	var resp api.SuitesResponse
	if err := c.get(ctx, "/newtrun/v1/suites", &resp); err != nil {
		return nil, err
	}
	return resp.Suites, nil
}

// ListSuiteScenarios returns the scenarios in the named suite as
// summaries (name, topology, step count, dependency edges). 404 means
// the suite directory doesn't exist on the server.
func (c *Client) ListSuiteScenarios(ctx context.Context, suite string) (*api.SuiteScenariosResponse, error) {
	var resp api.SuiteScenariosResponse
	if err := c.get(ctx, "/newtrun/v1/suites/"+suite+"/scenarios", &resp); err != nil {
		return nil, err
	}
	return &resp, nil
}

// CreateSuite creates a suite directory + suite.yaml manifest on the
// server. Returns 409 if the suite already exists. Topology is the
// topology this suite targets; the runner uses it as a guard against
// suite/server-topology mismatches at run time.
func (c *Client) CreateSuite(ctx context.Context, name, topology string) error {
	return c.do(ctx, http.MethodPost, "/newtrun/v1/suites", api.CreateSuiteRequest{Name: name, Topology: topology}, nil)
}

// DeleteSuite removes an empty suite directory. Returns 409 if the
// suite still contains scenarios.
func (c *Client) DeleteSuite(ctx context.Context, name string) error {
	return c.do(ctx, http.MethodDelete, "/newtrun/v1/suites/"+name, nil, nil)
}

// GetScenario returns the raw scenario YAML body. 404 if no file
// matches <name>.yaml or *-<name>.yaml in the suite directory.
func (c *Client) GetScenario(ctx context.Context, suite, name string) ([]byte, error) {
	return c.getRaw(ctx, "/newtrun/v1/suites/"+suite+"/scenarios/"+name)
}

// PutScenario creates or updates a scenario. The body must be raw YAML
// whose name: field matches the URL name; ParseScenarioBytes on the
// server is the validation gate.
func (c *Client) PutScenario(ctx context.Context, suite, name string, body []byte) error {
	return c.putRaw(ctx, "/newtrun/v1/suites/"+suite+"/scenarios/"+name, body)
}

// DeleteScenario removes a scenario file.
func (c *Client) DeleteScenario(ctx context.Context, suite, name string) error {
	return c.do(ctx, http.MethodDelete, "/newtrun/v1/suites/"+suite+"/scenarios/"+name, nil, nil)
}

// StreamEvents subscribes to the SSE event stream for the named suite.
// The handler is called for each event in delivery order. Returns when
// the connection closes (caller cancels the context) or on a network
// error. Heartbeat comment lines are silently skipped.
func (c *Client) StreamEvents(ctx context.Context, suite string, handle func(api.Event)) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/newtrun/v1/runs/"+suite+"/events", nil)
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

// getRaw fetches a non-JSON body (e.g., scenario YAML). Surfaces 4xx/5xx
// as ServerError just like do(); successful responses are returned as the
// raw byte slice with no envelope unwrapping.
func (c *Client) getRaw(ctx context.Context, path string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("contacting newtrun-server at %s: %w (is newtrun-server running?)", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return nil, readServerError(resp)
	}
	return io.ReadAll(resp.Body)
}

// putRaw sends a non-JSON body (e.g., scenario YAML for ParseScenarioBytes
// validation). 2xx responses are discarded; 4xx/5xx surface as ServerError.
func (c *Client) putRaw(ctx context.Context, path string, body []byte) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, c.baseURL+path, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/yaml")
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("contacting newtrun-server at %s: %w (is newtrun-server running?)", c.baseURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		return readServerError(resp)
	}
	return nil
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
	var envelope httputil.APIResponse
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
	var envelope httputil.APIResponse
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
