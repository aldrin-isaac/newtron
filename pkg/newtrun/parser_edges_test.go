package newtrun

import (
	"strings"
	"testing"
)

// Parser validation for the runner-edge features: device-scoped capture
// bounds, cleanup-step validation, and the poll sanity rule.

func parseOne(t *testing.T, yaml string) error {
	t.Helper()
	_, err := ParseScenarioBytes([]byte(yaml))
	return err
}

func TestParse_CaptureOnMultiDeviceRejected(t *testing.T) {
	err := parseOne(t, `name: s
description: d
steps:
  - name: bad
    action: newtron
    devices: [a, b]
    url: /nodes/{{device}}/x
    capture: {v: .x}
`)
	if err == nil || !strings.Contains(err.Error(), "exactly one device") {
		t.Fatalf("want exactly-one-device rejection, got %v", err)
	}
}

func TestParse_CaptureOnSingleDeviceAllowed(t *testing.T) {
	if err := parseOne(t, `name: s
description: d
steps:
  - name: ok
    action: newtron
    devices: [a]
    url: /nodes/{{device}}/x
    capture: {v: .x}
`); err != nil {
		t.Fatalf("single-device capture should parse, got %v", err)
	}
}

func TestParse_CleanupStepsValidated(t *testing.T) {
	err := parseOne(t, `name: s
description: d
steps:
  - name: main
    action: newtron
    url: /x
cleanup:
  - name: bad
    action: newtron
`)
	if err == nil || !strings.Contains(err.Error(), "url or batch") {
		t.Fatalf("want cleanup step validation error, got %v", err)
	}
}

func TestParse_CleanupTargetRefRejected(t *testing.T) {
	err := parseOne(t, `name: s
description: d
steps:
  - name: main
    action: newtron
    url: /x
cleanup:
  - name: bad
    action: newtron
    url: /nodes/{{target.device}}/x
`)
	if err == nil || !strings.Contains(err.Error(), "{{target.X}}") {
		t.Fatalf("want target-ref rejection in cleanup, got %v", err)
	}
}

func TestParse_PollRequiresBothKnobs(t *testing.T) {
	err := parseOne(t, `name: s
description: d
steps:
  - name: bad
    action: newtron
    url: /x
    poll:
      timeout: 30s
`)
	if err == nil || !strings.Contains(err.Error(), "poll requires timeout and interval") {
		t.Fatalf("want poll sanity rejection, got %v", err)
	}
}

func TestParse_HostExecPollAllowed(t *testing.T) {
	if err := parseOne(t, `name: s
description: d
steps:
  - name: ok
    action: host-exec
    devices: [host1]
    command: "ping -c 1 10.0.0.1"
    poll:
      timeout: 30s
      interval: 5s
    expect:
      success_rate: 1.0
`); err != nil {
		t.Fatalf("host-exec with poll should parse, got %v", err)
	}
}
