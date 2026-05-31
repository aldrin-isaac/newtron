package httputil

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestWriteJSONWrapsDataInEnvelope(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteJSON(rec, http.StatusOK, map[string]string{"hello": "world"})

	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", rec.Code)
	}
	if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("Content-Type = %q, want application/json", ct)
	}
	var env APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error != "" {
		t.Errorf("Error = %q, want empty", env.Error)
	}
	data, ok := env.Data.(map[string]any)
	if !ok {
		t.Fatalf("Data = %T, want map", env.Data)
	}
	if data["hello"] != "world" {
		t.Errorf("Data.hello = %v, want world", data["hello"])
	}
}

func TestWriteErrorWrapsErrorMessage(t *testing.T) {
	rec := httptest.NewRecorder()
	WriteError(rec, http.StatusNotFound, errors.New("nope"))

	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rec.Code)
	}
	var env APIResponse
	if err := json.NewDecoder(rec.Body).Decode(&env); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if env.Error != "nope" {
		t.Errorf("Error = %q, want nope", env.Error)
	}
	if env.Data != nil {
		t.Errorf("Data = %v, want nil", env.Data)
	}
}

func TestDecodeJSONEmptyBodyReturnsNil(t *testing.T) {
	r := httptest.NewRequest(http.MethodPost, "/x", nil)
	var v struct{ A int }
	if err := DecodeJSON(r, &v); err != nil {
		t.Errorf("DecodeJSON empty: %v, want nil", err)
	}
	if v.A != 0 {
		t.Errorf("v.A = %d, want 0 (untouched)", v.A)
	}
}

func TestDecodeJSONValidBodyPopulatesTarget(t *testing.T) {
	body := strings.NewReader(`{"a": 42}`)
	r := httptest.NewRequest(http.MethodPost, "/x", body)
	r.ContentLength = int64(len(`{"a": 42}`))
	var v struct{ A int }
	if err := DecodeJSON(r, &v); err != nil {
		t.Fatalf("DecodeJSON: %v", err)
	}
	if v.A != 42 {
		t.Errorf("v.A = %d, want 42", v.A)
	}
}

func TestDecodeJSONRejectsUnknownFields(t *testing.T) {
	body := strings.NewReader(`{"unknown": 1}`)
	r := httptest.NewRequest(http.MethodPost, "/x", body)
	r.ContentLength = int64(len(`{"unknown": 1}`))
	var v struct{ Known int }
	if err := DecodeJSON(r, &v); err == nil {
		t.Fatal("DecodeJSON accepted unknown field")
	}
}

func TestDecodeJSONMalformedReturnsWrappedError(t *testing.T) {
	body := io.NopCloser(bytes.NewReader([]byte(`{not json`)))
	r := httptest.NewRequest(http.MethodPost, "/x", body)
	r.ContentLength = 9
	var v struct{}
	err := DecodeJSON(r, &v)
	if err == nil {
		t.Fatal("DecodeJSON accepted malformed body")
	}
	if !strings.Contains(err.Error(), "malformed JSON body") {
		t.Errorf("err = %q, want wrapped 'malformed JSON body'", err)
	}
}

// fmt is needed by tests above
var _ = fmt.Errorf
