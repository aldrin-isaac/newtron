package sonic

import (
	"context"
	"testing"
	"time"
)

// TestConnectTimeoutFromContext pins the dial-timeout resolution: the default
// when unset, the value when set, and the default for a zero/negative value.
func TestConnectTimeoutFromContext(t *testing.T) {
	if got := connectTimeoutFromContext(context.Background()); got != DefaultConnectTimeout {
		t.Errorf("unset = %v, want default %v", got, DefaultConnectTimeout)
	}
	if got := connectTimeoutFromContext(WithConnectTimeout(context.Background(), 2*time.Second)); got != 2*time.Second {
		t.Errorf("set = %v, want 2s", got)
	}
	if got := connectTimeoutFromContext(WithConnectTimeout(context.Background(), 0)); got != DefaultConnectTimeout {
		t.Errorf("zero = %v, want default", got)
	}
}

// TestNewSSHTunnel_DialRespectsShortTimeout proves a read/liveness path's short
// dial bound is honored: dialing a guaranteed-unroutable host (192.0.2.0/24 is
// TEST-NET-1, RFC 5737 — the SYN gets no reply, so the dial hangs until the
// timeout rather than a fast connection-refused) fails in ~the short timeout,
// not the 30s DefaultConnectTimeout. Without WithConnectTimeout this would block
// for 30s — the /info-during-provision hang this closes.
func TestNewSSHTunnel_DialRespectsShortTimeout(t *testing.T) {
	ctx := WithConnectTimeout(context.Background(), 300*time.Millisecond)
	start := time.Now()
	_, err := NewSSHTunnel(ctx, "192.0.2.1", "u", "p", 22)
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected the dial to a non-routable host to fail")
	}
	if elapsed > 5*time.Second {
		t.Errorf("dial took %v; the short connect timeout was not applied (want ~300ms, far below the 30s default)", elapsed)
	}
}
