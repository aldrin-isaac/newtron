package httputil

import (
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
)

// resp builds a minimal *http.Response with the given status and body.
func resp(status int, body string) *http.Response {
	return &http.Response{
		StatusCode: status,
		Status:     http.StatusText(status),
		Body:       io.NopCloser(strings.NewReader(body)),
	}
}

func TestServerError_Error(t *testing.T) {
	withServer := (&ServerError{Server: "newtron-server", StatusCode: 404, Message: "nope"}).Error()
	if !strings.Contains(withServer, "newtron-server") || !strings.Contains(withServer, "404") || !strings.Contains(withServer, "nope") {
		t.Errorf("labeled Error() = %q, want it to name the server, code, and message", withServer)
	}
	bare := (&ServerError{StatusCode: 500, Message: "boom"}).Error()
	if strings.Contains(bare, "returned") == false || !strings.Contains(bare, "500") {
		t.Errorf("unlabeled Error() = %q, want a generic server-error phrasing with the code", bare)
	}
}

func TestDecodeAPIResponse(t *testing.T) {
	type payload struct {
		Name string `json:"name"`
	}

	t.Run("2xx decodes Data into out", func(t *testing.T) {
		var out payload
		if err := DecodeAPIResponse(resp(200, `{"data":{"name":"leaf1"}}`), &out, "newtron-server"); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if out.Name != "leaf1" {
			t.Errorf("out.Name = %q, want leaf1", out.Name)
		}
	})

	t.Run("non-2xx yields a labeled *ServerError", func(t *testing.T) {
		err := DecodeAPIResponse(resp(500, `{"error":"kaboom"}`), nil, "newtlab-server")
		var se *ServerError
		if !errors.As(err, &se) {
			t.Fatalf("err = %T, want *ServerError", err)
		}
		if se.StatusCode != 500 || se.Server != "newtlab-server" || se.Message != "kaboom" {
			t.Errorf("got %+v, want {newtlab-server 500 kaboom}", se)
		}
	})

	t.Run("2xx envelope error still yields *ServerError", func(t *testing.T) {
		err := DecodeAPIResponse(resp(200, `{"error":"soft failure"}`), nil, "newtrun-server")
		var se *ServerError
		if !errors.As(err, &se) || se.Message != "soft failure" {
			t.Fatalf("err = %v, want a *ServerError carrying the envelope error", err)
		}
	})

	t.Run("empty 2xx body is a no-op", func(t *testing.T) {
		if err := DecodeAPIResponse(resp(204, ``), nil, "newtron-server"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})

	t.Run("empty non-2xx body yields *ServerError from the status", func(t *testing.T) {
		err := DecodeAPIResponse(resp(404, ``), nil, "newtron-server")
		var se *ServerError
		if !errors.As(err, &se) || se.StatusCode != 404 {
			t.Fatalf("err = %v, want a 404 *ServerError", err)
		}
	})

	t.Run("nil out with data present is a no-op success", func(t *testing.T) {
		if err := DecodeAPIResponse(resp(200, `{"data":{"name":"x"}}`), nil, "newtron-server"); err != nil {
			t.Errorf("unexpected error: %v", err)
		}
	})
}
