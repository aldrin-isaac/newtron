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

	"github.com/newtron-network/newtron/pkg/newtron"
	"github.com/newtron-network/newtron/pkg/newtron/api"
)

// Client is an HTTP client for the newtron-server API.
type Client struct {
	baseURL    string
	networkID  string
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

// RegisterNetwork registers a network with the server. Returns nil if
// the network is already registered (409 is treated as success).
func (c *Client) RegisterNetwork(specDir string) error {
	body := api.RegisterNetworkRequest{
		ID:      c.networkID,
		SpecDir: specDir,
	}
	err := c.doPost("/network", body, nil)
	if err != nil {
		if se, ok := err.(*ServerError); ok && se.StatusCode == http.StatusConflict {
			return nil // already registered — idempotent
		}
		return err
	}
	return nil
}

// UnregisterNetwork removes a registered network from the server.
// Returns nil if the network is not registered (404/500 treated as success).
func (c *Client) UnregisterNetwork() error {
	err := c.doDelete(c.networkPath(), nil)
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
	return "/network/" + url.PathEscape(c.networkID)
}

// nodePath returns the base path for node-scoped endpoints.
func (c *Client) nodePath(device string) string {
	return c.networkPath() + "/node/" + url.PathEscape(device)
}

// interfacePath returns the base path for interface-scoped endpoints.
func (c *Client) interfacePath(device, iface string) string {
	return c.nodePath(device) + "/interface/" + url.PathEscape(iface)
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

// doGet performs a GET request and decodes the response data into result.
func (c *Client) doGet(path string, result any) error {
	resp, err := c.httpClient.Get(c.baseURL + path)
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
	resp, err := c.httpClient.Post(c.baseURL+path, "application/json", bodyReader)
	if err != nil {
		return fmt.Errorf("POST %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.decodeResponse(resp, result)
}

// doDelete performs a DELETE request.
func (c *Client) doDelete(path string, result any) error {
	req, err := http.NewRequest(http.MethodDelete, c.baseURL+path, nil)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", path, err)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("DELETE %s: %w", path, err)
	}
	defer resp.Body.Close()
	return c.decodeResponse(resp, result)
}

// decodeResponse unwraps the APIResponse envelope.
func (c *Client) decodeResponse(resp *http.Response, result any) error {
	var envelope api.APIResponse
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
