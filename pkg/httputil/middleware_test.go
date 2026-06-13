package httputil

import (
	"bytes"
	"log"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestRequestIDSetsHeader(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/x", nil)
	h.ServeHTTP(rec, req)

	if rec.Header().Get("X-Request-ID") == "" {
		t.Error("X-Request-ID header not set")
	}
}

func TestRequestIDsAreMonotonicAcrossCalls(t *testing.T) {
	h := RequestID(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))

	rec1 := httptest.NewRecorder()
	rec2 := httptest.NewRecorder()
	h.ServeHTTP(rec1, httptest.NewRequest(http.MethodGet, "/a", nil))
	h.ServeHTTP(rec2, httptest.NewRequest(http.MethodGet, "/b", nil))

	id1 := rec1.Header().Get("X-Request-ID")
	id2 := rec2.Header().Get("X-Request-ID")
	if id1 == id2 {
		t.Errorf("id1 == id2 (%s); want monotonic", id1)
	}
}

func TestLoggerCapturesStatusAndPath(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	h := Logger(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusTeapot)
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/brew", nil))

	out := buf.String()
	if !strings.Contains(out, "GET /brew 418") {
		t.Errorf("logger output = %q, want to contain 'GET /brew 418'", out)
	}
}

func TestRecoveryConvertsPanicTo500(t *testing.T) {
	var buf bytes.Buffer
	logger := log.New(&buf, "", 0)
	h := Recovery(logger)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("boom")
	}))

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
	if !strings.Contains(rec.Body.String(), "internal server error") {
		t.Errorf("body = %q, want to contain error envelope", rec.Body.String())
	}
	if !strings.Contains(buf.String(), "PANIC: boom") {
		t.Errorf("logger output = %q, want PANIC trace", buf.String())
	}
}

func TestStatusWriterForwardsFlushForSSEPath(t *testing.T) {
	// Compose Logger → handler-that-flushes; the inner Flush should
	// reach the underlying ResponseWriter via statusWriter's Flush
	// forward, otherwise SSE handlers wrapped in Logger are blind.
	rec := httptest.NewRecorder()
	h := Logger(log.New(&bytes.Buffer{}, "", 0))(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, ok := w.(http.Flusher)
		if !ok {
			t.Error("middleware-wrapped ResponseWriter does not implement http.Flusher")
			return
		}
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("hello"))
		f.Flush()
	}))
	h.ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/x", nil))
	if !rec.Flushed {
		t.Error("ResponseRecorder.Flushed = false; statusWriter.Flush did not forward")
	}
}
