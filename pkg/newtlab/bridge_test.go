package newtlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

// TestBuildBridgeConfigCarriesToken pins that the per-lab telemetry token
// threads from BridgePushParams into the serialized bridge config newtlink
// reads — without it newtlink has nothing to present and the enforced server
// 401s every push.
func TestBuildBridgeConfigCarriesToken(t *testing.T) {
	cfg := buildBridgeConfig(nil, BridgePushParams{
		OrchestratorURL: "http://127.0.0.1:18080",
		LabName:         "lab-a",
		Token:           "tok-xyz",
	})
	if cfg.Token != "tok-xyz" {
		t.Errorf("cfg.Token = %q, want tok-xyz", cfg.Token)
	}
}

// TestPushBridgeStatsSendsBearer pins that pushBridgeStats presents the token
// as `Authorization: Bearer <token>` when set, and omits the header when empty.
func TestPushBridgeStatsSendsBearer(t *testing.T) {
	for _, tc := range []struct {
		name, token, wantAuth string
	}{
		{"with token", "tok-xyz", "Bearer tok-xyz"},
		{"empty token omits header", "", ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var gotAuth string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotAuth = r.Header.Get("Authorization")
				w.WriteHeader(http.StatusNoContent)
			}))
			defer srv.Close()

			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			if err := pushBridgeStats(ctx, srv.Client(), srv.URL, tc.token, BridgeStats{}); err != nil {
				t.Fatalf("pushBridgeStats: %v", err)
			}
			if gotAuth != tc.wantAuth {
				t.Errorf("Authorization = %q, want %q", gotAuth, tc.wantAuth)
			}
		})
	}
}

// TestNewTelemetryTokenUniqueAndNonEmpty pins that minted tokens are non-empty
// and don't repeat (crypto/rand, not a fixed/derived value).
func TestNewTelemetryTokenUniqueAndNonEmpty(t *testing.T) {
	seen := map[string]bool{}
	for i := 0; i < 100; i++ {
		tok, err := NewTelemetryToken()
		if err != nil {
			t.Fatalf("NewTelemetryToken: %v", err)
		}
		if tok == "" {
			t.Fatal("token is empty")
		}
		if seen[tok] {
			t.Fatalf("duplicate token minted: %q", tok)
		}
		seen[tok] = true
	}
}
