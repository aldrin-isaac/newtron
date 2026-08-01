package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestIsBridgeStatsPush pins the auth-exemption matcher: only
// POST /newtlab/v1/labs/{lab}/bridges/{host}/stats is routed around the
// user-facing sessionkey/PAM chain. A too-broad matcher would expose other
// endpoints unauthenticated; a too-narrow one would 401 newtlink.
func TestIsBridgeStatsPush(t *testing.T) {
	cases := []struct {
		name, method, path string
		want               bool
	}{
		{"push local", "POST", "/newtlab/v1/labs/lab-a/bridges/local/stats", true},
		{"push named host", "POST", "/newtlab/v1/labs/lab-a/bridges/host2/stats", true},
		{"read view is GET (6 segments)", "GET", "/newtlab/v1/labs/lab-a/bridges/stats", false},
		{"read view even as POST", "POST", "/newtlab/v1/labs/lab-a/bridges/stats", false},
		{"lab status not exempt", "POST", "/newtlab/v1/labs/lab-a/status", false},
		{"newtron path not exempt", "POST", "/newtron/v1/networks/n/create-service", false},
		{"deploy not exempt", "POST", "/newtlab/v1/labs/lab-a/deploy", false},
		{"trailing junk not exempt", "POST", "/newtlab/v1/labs/lab-a/bridges/local/stats/extra", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := httptest.NewRequest(tc.method, tc.path, nil)
			if got := isBridgeStatsPush(r); got != tc.want {
				t.Errorf("isBridgeStatsPush(%s %s) = %v, want %v", tc.method, tc.path, got, tc.want)
			}
		})
	}
}

// TestIsHealthProbe pins the exemption's scope (#476). The health surface is
// reachable without a credential, so the predicate must match exactly one
// method and path — a loose match would hand the same free pass to
// /newt-server/v1/auth/login or an engine route.
func TestIsHealthProbe(t *testing.T) {
	cases := []struct {
		method, path string
		want         bool
	}{
		{"GET", "/newt-server/v1/health", true},
		{"GET", "newt-server/v1/health", true},   // no leading slash
		{"GET", "/newt-server/v1/health/", true}, // trailing slash

		// Same prefix, must stay authenticated.
		{"GET", "/newt-server/v1/auth/login", false},
		{"POST", "/newt-server/v1/auth/login", false},
		{"GET", "/newt-server/v1/healthz", false},
		{"GET", "/newt-server/v1/health/detail", false},
		// Engine routes must never be exempt.
		{"GET", "/newtron/v1/networks", false},
		{"GET", "/newtlab/v1/labs", false},
		{"GET", "/newtrun/v1/health", false},
		// Only GET — a write to the health path is not a probe.
		{"POST", "/newt-server/v1/health", false},
		{"DELETE", "/newt-server/v1/health", false},
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			req := httptest.NewRequest(c.method, "/", nil)
			req.Method = c.method
			req.URL.Path = c.path
			if got := isHealthProbe(req); got != c.want {
				t.Errorf("isHealthProbe(%s %s) = %v, want %v", c.method, c.path, got, c.want)
			}
		})
	}
}

// TestExemptUnauthenticatedRoutesOnlyTheExemptions is the guard that the
// exemption did not widen the unauthenticated surface: the health probe and the
// telemetry push reach the engine mux directly, and everything else — including
// every engine route and the login endpoint — still goes through the auth chain.
func TestExemptUnauthenticatedRoutesOnlyTheExemptions(t *testing.T) {
	var reachedMux, reachedAuth bool
	mux := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reachedMux = true })
	authed := http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reachedAuth = true })
	h := exemptUnauthenticated(mux, authed)

	cases := []struct {
		method, path string
		wantExempt   bool
	}{
		{"GET", "/newt-server/v1/health", true},
		{"POST", "/newtlab/v1/labs/lab1/bridges/host1/stats", true},
		{"GET", "/newtron/v1/networks", false},
		{"POST", "/newtron/v1/networks/n/nodes/d/create-vlan", false},
		{"POST", "/newt-server/v1/auth/login", false},
		{"GET", "/newtlab/v1/labs/lab1/bridges/stats", false}, // the read view
	}
	for _, c := range cases {
		t.Run(c.method+" "+c.path, func(t *testing.T) {
			reachedMux, reachedAuth = false, false
			h.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(c.method, c.path, nil))
			if c.wantExempt && !reachedMux {
				t.Errorf("%s %s did not bypass auth — an unauthenticated caller cannot reach it", c.method, c.path)
			}
			if !c.wantExempt && !reachedAuth {
				t.Errorf("%s %s bypassed the auth chain — the exemption is too wide", c.method, c.path)
			}
		})
	}
}
