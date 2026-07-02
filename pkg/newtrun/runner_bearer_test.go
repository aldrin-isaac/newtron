package newtrun

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestScenarioBearer pins the single owner of per-scenario identity: an absent
// `as:` yields the operator's own Bearer; `as:` yields that cached user's key;
// an `as:` with no cached session fails fast with the login hint; and a run
// with no credential at all yields "".
func TestScenarioBearer(t *testing.T) {
	cases := []struct {
		name    string
		r       *Runner
		want    string
		wantErr string // substring; "" = expect no error
	}{
		{"no scenario → operator", &Runner{OperatorBearer: "op-key"}, "op-key", ""},
		{"scenario without as → operator", &Runner{OperatorBearer: "op-key", scenario: &Scenario{Name: "s"}}, "op-key", ""},
		{"as → that user's session", &Runner{OperatorBearer: "op-key", UserSessions: map[string]string{"alice": "alice-key"}, scenario: &Scenario{As: "alice"}}, "alice-key", ""},
		{"as with no session → fail-fast", &Runner{UserSessions: map[string]string{}, scenario: &Scenario{As: "bob"}}, "", "bob"},
		{"unenforced run → empty", &Runner{}, "", ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := tc.r.scenarioBearer()
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want it to mention %q", err, tc.wantErr)
				}
				if !strings.Contains(err.Error(), "newtron auth login") {
					t.Errorf("err %q should suggest the login remediation", err)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tc.want {
				t.Errorf("scenarioBearer() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestRunCLI_ForwardsBearerViaEnv proves the fix's mechanism end to end at the
// exec seam: runCLI must hand the resolved Bearer to the child via
// NEWTRON_BEARER (env, not argv), so the exec'd CLI authenticates as the
// operator without reading the session cache. A stub `newtron` on PATH records
// the env var it received.
func TestRunCLI_ForwardsBearerViaEnv(t *testing.T) {
	dir := t.TempDir()
	outFile := filepath.Join(dir, "bearer.out")
	stub := filepath.Join(dir, "newtron")
	// Write NEWTRON_BEARER (empty if unset) to outFile, then exit 0.
	script := "#!/bin/sh\nprintf '%s' \"$NEWTRON_BEARER\" > " + outFile + "\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	r := &Runner{ServerURL: "http://127.0.0.1:0", OperatorBearer: "op-key-123"}
	step := &Step{Action: ActionNewtronCLI, Command: "service list"}
	out := (&newtronCLIExecutor{}).Execute(t.Context(), r, step)
	if out.Result.Status != StepStatusPassed {
		t.Fatalf("status = %v, message = %q", out.Result.Status, out.Result.Message)
	}
	got, err := os.ReadFile(outFile)
	if err != nil {
		t.Fatalf("read stub output: %v", err)
	}
	if string(got) != "op-key-123" {
		t.Errorf("child NEWTRON_BEARER = %q, want op-key-123", got)
	}
}
