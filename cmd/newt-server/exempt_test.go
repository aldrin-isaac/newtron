package main

import (
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
