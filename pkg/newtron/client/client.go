// Package client provides an HTTP client for the newtron-server API.
// Both the CLI and newtrun use this package instead of importing
// pkg/newtron directly for operations.
package client

import (
	"bytes"
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

// AlreadyRegisteredError is returned by RegisterNetwork when the network ID
// is already registered for a different spec_dir. True-idempotent
// re-registration (same id + same spec_dir) returns nil instead, since the
// observable state matches what the caller asked for.
type AlreadyRegisteredError struct {
	ID               string
	RequestedSpecDir string
	ExistingSpecDir  string
}

func (e *AlreadyRegisteredError) Error() string {
	return fmt.Sprintf(
		"network '%s' is already registered with spec_dir '%s'; "+
			"unregister it first or use a different network ID to register %q alongside",
		e.ID, e.ExistingSpecDir, e.RequestedSpecDir,
	)
}

// New creates a new Client.
func New(baseURL, networkID string) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		networkID: networkID,
		httpClient: &http.Client{
			Timeout: 6 * time.Minute, // slightly above server's 5min write timeout
		},
	}
}

// NetworkID returns the network identifier used for API paths.
func (c *Client) NetworkID() string {
	return c.networkID
}

// RegisterNetwork registers a network with the server.
//
// Returns nil on true-idempotent re-registration (the network is already
// registered for the same spec_dir — the observable state already matches).
// Returns *AlreadyRegisteredError when the network is registered for a
// different spec_dir, so callers can distinguish "you already did this" from
// "someone else owns this slot." Other server errors come back as
// *ServerError.
//
// The 409 response envelope carries an api.AlreadyRegisteredErrorInfo in
// Data with the existing spec_dir; this method decodes it to make the
// comparison.
func (c *Client) RegisterNetwork(specDir string) error {
	body := api.RegisterNetworkRequest{
		ID:      c.networkID,
		SpecDir: specDir,
	}
	bodyBytes, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshal body: %w", err)
	}
	resp, err := c.httpClient.Post(c.baseURL+"/newtron/v1/networks", "application/json", bytes.NewReader(bodyBytes))
	if err != nil {
		return fmt.Errorf("POST /newtron/v1/networks: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 400 {
		return nil
	}

	respBody, _ := io.ReadAll(resp.Body)
	var envelope httputil.APIResponse
	_ = json.Unmarshal(respBody, &envelope)

	if resp.StatusCode == http.StatusConflict {
		var info api.AlreadyRegisteredErrorInfo
		dataParsed := false
		if envelope.Data != nil {
			if dataBytes, err := json.Marshal(envelope.Data); err == nil {
				if err := json.Unmarshal(dataBytes, &info); err == nil {
					dataParsed = true
				}
			}
		}
		// True idempotent only when the server actually told us the
		// existing spec_dir AND it matches what the caller asked for.
		// Without the dataParsed guard, an empty/unparseable Data
		// payload would collapse to ExistingSpecDir == "" and could
		// match a (degenerate) empty specDir — best to fail loud.
		if dataParsed && info.ExistingSpecDir == specDir {
			return nil
		}
		return &AlreadyRegisteredError{
			ID:               c.networkID,
			RequestedSpecDir: specDir,
			ExistingSpecDir:  info.ExistingSpecDir,
		}
	}

	msg := envelope.Error
	if msg == "" {
		msg = resp.Status
	}
	return &ServerError{StatusCode: resp.StatusCode, Message: msg}
}

// ScaffoldNetwork creates an empty spec layout at specDir (three zero-valued
// spec files + an empty profiles/ subdirectory) and registers it under the
// client's network ID. description seeds topology.json.
//
// Unlike RegisterNetwork, a 409 here is meaningful — specDir already
// contains specs — and is returned to the caller so the operator can choose
// to register the existing layout or pick a different path.
func (c *Client) ScaffoldNetwork(specDir, description string) error {
	body := api.RegisterNetworkRequest{
		ID:          c.networkID,
		SpecDir:     specDir,
		Scaffold:    true,
		Description: description,
	}
	return c.doPost("/newtron/v1/networks", body, nil)
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


// RawRequest performs an HTTP request and returns the response Data as raw JSON.
// It unwraps the APIResponse envelope and returns the Data field without decoding
// it into a typed struct — the caller receives the raw JSON for flexible evaluation.
func (c *Client) RawRequest(method, path string, body any) (json.RawMessage, error) {
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
