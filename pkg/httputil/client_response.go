package httputil

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

// ServerError is the error every in-repo API client returns when a server
// answers with a non-2xx status (or a 2xx body carrying an error envelope).
//
// It is ONE type across the newtron / newtlab / newtrun clients — each aliases
// it (`type ServerError = httputil.ServerError`) rather than declaring its own
// — so a cross-engine caller can errors.As a single shape no matter which
// client produced the error. Server names which server answered, preserving the
// diagnostic the three separate types used to carry in their Error() prefixes.
type ServerError struct {
	Server     string // e.g. "newtron-server"; "" when the producer didn't label it
	StatusCode int
	Message    string
}

func (e *ServerError) Error() string {
	if e.Server != "" {
		return fmt.Sprintf("%s returned %d: %s", e.Server, e.StatusCode, e.Message)
	}
	return fmt.Sprintf("server returned %d: %s", e.StatusCode, e.Message)
}

// UnwrapAPIResponse is the shared client-side counterpart to WriteJSON /
// WriteError: it reads resp and either surfaces the server's error or returns
// the envelope's Data as raw JSON. Specifically:
//
//   - a non-2xx status, or a 2xx body whose envelope carries a non-empty
//     `error`, yields a *ServerError labeled with server;
//   - otherwise it returns the envelope's Data re-marshaled to json.RawMessage
//     (nil when the response carried no data).
//
// It is the single owner of "decode a newtron-project API response" (§27): the
// three engine clients duplicated this envelope/error handling. DecodeAPIResponse
// layers typed decoding on top; a raw-passthrough caller (newtron's RawRequest)
// uses the json.RawMessage directly. The caller owns resp.Body.Close.
func UnwrapAPIResponse(resp *http.Response, server string) (json.RawMessage, error) {
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("reading response: %w", err)
	}
	if len(body) == 0 {
		if resp.StatusCode >= 400 {
			return nil, &ServerError{Server: server, StatusCode: resp.StatusCode, Message: resp.Status}
		}
		return nil, nil
	}
	var envelope APIResponse
	if err := json.Unmarshal(body, &envelope); err != nil {
		if resp.StatusCode >= 400 {
			return nil, &ServerError{Server: server, StatusCode: resp.StatusCode, Message: string(body)}
		}
		return nil, fmt.Errorf("decode response envelope: %w", err)
	}
	if resp.StatusCode >= 400 {
		msg := envelope.Error
		if msg == "" {
			msg = resp.Status
		}
		return nil, &ServerError{Server: server, StatusCode: resp.StatusCode, Message: msg}
	}
	if envelope.Error != "" {
		return nil, &ServerError{Server: server, StatusCode: resp.StatusCode, Message: envelope.Error}
	}
	if envelope.Data == nil {
		return nil, nil
	}
	data, err := json.Marshal(envelope.Data)
	if err != nil {
		return nil, fmt.Errorf("re-marshal response data: %w", err)
	}
	return data, nil
}

// DecodeAPIResponse unwraps resp's envelope (see UnwrapAPIResponse) and decodes
// the Data field into out. out may be nil (the caller only cares whether the
// call succeeded), and the response may carry no data — either is a no-op decode
// returning nil. A *ServerError from the unwrap is passed through unchanged.
func DecodeAPIResponse(resp *http.Response, out any, server string) error {
	data, err := UnwrapAPIResponse(resp, server)
	if err != nil {
		return err
	}
	if out == nil || data == nil {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decode response data into target: %w", err)
	}
	return nil
}
