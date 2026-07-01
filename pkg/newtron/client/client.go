// Package client provides an HTTP client for the newtron-server API.
// Both the CLI and newtrun use this package instead of importing
// pkg/newtron directly for operations.
package client

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/api"
)

// Client is an HTTP client for the newtron-server API.
type Client struct {
	baseURL    string
	networkID  string
	Mode       api.Mode // if set, appended as ?mode= to all requests
	httpClient *http.Client
}

// ServerError represents an error response from the server.
type ServerError struct {
	StatusCode int
	Message    string
}

func (e *ServerError) Error() string {
	return fmt.Sprintf("server error (%d): %s", e.StatusCode, e.Message)
}

// New creates a new Client. Functional options configure transport-
// level concerns (TLS for L2a inter-service mTLS, etc.) without
// changing the signature for the common case.
func New(baseURL, networkID string, opts ...Option) *Client {
	c := &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		networkID: networkID,
		httpClient: &http.Client{
			Timeout: 6 * time.Minute, // slightly above server's 5min write timeout
		},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

// Option configures a Client at construction.
type Option func(*Client)

// WithTLS attaches a *tls.Config to the client's HTTP transport
// (auth-design.md L2a). When tlsCfg is nil the client stays on plain
// HTTP — the disabled state. Build the config with
// httputil.LoadClientTLSConfig from the operator's --tls-cert /
// --tls-key / --tls-ca flags.
func WithTLS(tlsCfg *tls.Config) Option {
	return func(c *Client) {
		if tlsCfg == nil {
			return
		}
		c.httpClient.Transport = &http.Transport{TLSClientConfig: tlsCfg}
	}
}

// WithBearer attaches a static Authorization: Bearer <key> header
// to every outbound request whose Authorization header isn't
// already set (auth-design.md §L2c). Two consumers:
//
//   - The newtron / newtrun / newtlab CLIs after
//     `newtron auth login` has minted a key and persisted it under
//     ~/.newtron/sessions/. The CLI reads the cache via
//     LoadCLISession and passes the key here.
//   - The newtrun runner, which forwards the session key it
//     extracted from the operator's inbound /newtrun/v1/runs
//     request on its own outbound newtron calls (auth-design.md
//     §L2c "Identity forwarding through engines").
//
// Purely static — no /auth/login wire call, no auto-refresh on
// 401. The caller catches 401 responses and surfaces a "session
// expired; run `newtron auth login` again" message, matching the
// human-operator UX. Calls to /auth/login and /auth/logout pass
// through unchanged: the round-tripper respects a caller-set
// Authorization header so the auth endpoints can carry their own
// credentials (Basic at login; Bearer at logout — possibly a
// different, soon-to-be-revoked key than this one).
//
// Empty key is a no-op — the transport is left as-is, no Bearer
// is attached. Useful for the "operator hasn't logged in yet"
// path: the CLI calls WithBearer(record.Key) unconditionally and
// passes "" when LoadSession returned nil.
func WithBearer(key string) Option {
	return func(c *Client) {
		c.httpClient.Transport = httputil.BearerTransport(c.httpClient.Transport, key)
	}
}

// NetworkID returns the network identifier used for API paths.
func (c *Client) NetworkID() string {
	return c.networkID
}

// CreateNetwork ensures the network is registered. The operator names
// the topology by id; the server resolves the on-disk path under its
// --networks-base. If the slot at <networks-base>/<id> is empty, the
// server creates the empty spec layout; if it's already populated, the
// server registers it as-is. Either way the call is idempotent — the
// server returns 201 on first create and 200 on subsequent calls; this
// client surface treats both as success.
//
// description seeds topology.json when the slot is empty. Ignored on
// existing slots — no rewrite of authored specs.
//
// Returns the resolved NetworkInfo so callers can display the picked
// path without re-fetching.
func (c *Client) CreateNetwork(description string) (*api.NetworkInfo, error) {
	body := api.CreateNetworkRequest{
		ID:          c.networkID,
		Description: description,
	}
	var info api.NetworkInfo
	if err := c.doPost("/newtron/v1/networks", body, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// UnregisterNetwork removes a registered network from the server.
// Returns nil if the network is not registered (404/500 treated as success).
func (c *Client) UnregisterNetwork() error {
	err := c.doPost(c.networkPath()+"/unregister", nil, nil)
	if err != nil {
		if se, ok := err.(*ServerError); ok && (se.StatusCode == http.StatusNotFound || se.StatusCode == http.StatusInternalServerError) {
			return nil // not registered — nothing to do
		}
		return err
	}
	return nil
}

// ReloadNetwork reloads the network's specs from disk without restarting the server.
func (c *Client) ReloadNetwork() error {
	return c.doPost(c.networkPath()+"/reload", nil, nil)
}

// ============================================================================
// HTTP helpers
// ============================================================================

// networkPath returns the base path for network-scoped endpoints.
func (c *Client) networkPath() string {
	return "/newtron/v1/networks/" + url.PathEscape(c.networkID)
}

// nodePath returns the base path for node-scoped endpoints.
func (c *Client) nodePath(device string) string {
	return c.networkPath() + "/nodes/" + url.PathEscape(device)
}

// interfacePath returns the base path for interface-scoped endpoints.
func (c *Client) interfacePath(device, iface string) string {
	return c.nodePath(device) + "/interfaces/" + url.PathEscape(iface)
}

// execQuery returns URL query params for ExecOpts.
func execQuery(opts newtron.ExecOpts) string {
	var parts []string
	if !opts.Execute {
		parts = append(parts, "dry_run=true")
	}
	if opts.NoSave {
		parts = append(parts, "no_save=true")
	}
	if len(parts) == 0 {
		return ""
	}
	return "?" + strings.Join(parts, "&")
}

// withForce appends force=true to a path's query string when force is set,
// choosing ? or & based on whether the path already carries parameters.
// Used by the cascade-capable deletes (nodeSpec, spec bindings).
func withForce(path string, force bool) string {
	if !force {
		return path
	}
	if strings.Contains(path, "?") {
		return path + "&force=true"
	}
	return path + "?force=true"
}

// withMode appends ?mode= to a path if the client has a Mode set.
// Handles paths that already have query parameters.
func (c *Client) withMode(path string) string {
	if c.Mode == "" || c.Mode == api.ModeIntent {
		return path
	}
	if strings.Contains(path, "?") {
		return path + "&mode=" + string(c.Mode)
	}
	return path + "?mode=" + string(c.Mode)
}

// doGet performs a GET request and decodes the response data into result.
func (c *Client) doGet(path string, result any) error {
	resp, err := c.httpClient.Get(c.baseURL + c.withMode(path))
	if err != nil {
		return fmt.Errorf("GET %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.decodeResponse(resp, result)
}

// doPost performs a POST request with a JSON body.
func (c *Client) doPost(path string, body any, result any) error {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}
	resp, err := c.httpClient.Post(c.baseURL+c.withMode(path), "application/json", bodyReader)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.decodeResponse(resp, result)
}

// RequestOption modifies an outbound *http.Request before send. Use
// WithHeader (and other future option constructors) to attach
// per-call concerns — typically caller identity headers
// (X-Newtron-Caller, HTTP Basic) the auth-design.md L1/L2 surfaces
// pick up at the server.
type RequestOption func(*http.Request)

// WithHeader sets the named HTTP header on the outbound request.
// Repeated calls with the same key overwrite — last value wins —
// matching http.Header.Set semantics.
func WithHeader(key, value string) RequestOption {
	return func(req *http.Request) {
		req.Header.Set(key, value)
	}
}

// RawRequest performs an HTTP request and returns the response Data as raw JSON.
// It unwraps the APIResponse envelope and returns the Data field without decoding
// it into a typed struct — the caller receives the raw JSON for flexible evaluation.
//
// Per-call RequestOptions (typically WithHeader for caller identity)
// run after the Content-Type default, so a passed-in Content-Type
// override wins — useful for batch + content-type composition.
func (c *Client) RawRequest(method, path string, body any, opts ...RequestOption) (json.RawMessage, error) {
	var bodyReader io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return nil, fmt.Errorf("marshal body: %w", err)
		}
		bodyReader = bytes.NewReader(data)
	}

	req, err := http.NewRequest(method, c.baseURL+c.withMode(path), bodyReader)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for _, opt := range opts {
		opt(req)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}

	if len(respBody) == 0 {
		if resp.StatusCode >= 400 {
			return nil, &ServerError{StatusCode: resp.StatusCode, Message: resp.Status}
		}
		return nil, nil
	}

	var envelope httputil.APIResponse
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		if resp.StatusCode >= 400 {
			return nil, &ServerError{StatusCode: resp.StatusCode, Message: string(respBody)}
		}
		return nil, fmt.Errorf("decode response: %w", err)
	}

	if envelope.Error != "" {
		return nil, &ServerError{StatusCode: resp.StatusCode, Message: envelope.Error}
	}

	if envelope.Data == nil {
		return nil, nil
	}

	data, err := json.Marshal(envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("re-marshal data: %w", err)
	}
	return data, nil
}

// decodeResponse unwraps the APIResponse envelope.
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

	if envelope.Error != "" {
		return &ServerError{StatusCode: resp.StatusCode, Message: envelope.Error}
	}

	if result != nil && envelope.Data != nil {
		// Re-marshal data and decode into the typed result
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
