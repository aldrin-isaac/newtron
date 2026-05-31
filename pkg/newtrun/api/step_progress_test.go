package api

import (
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/httputil"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// stubOp returns a representative DeviceOp for tests.
func stubOp(seq int, kind, result string) *sonic.DeviceOp {
	return &sonic.DeviceOp{
		Seq:    seq,
		Kind:   kind,
		Table:  "VLAN",
		Key:    "Vlan100",
		Result: result,
		At:     time.Now().UTC(),
	}
}

func TestHTTPReporterStepProgressPublishesEvent(t *testing.T) {
	b := httputil.NewBroker[Event]()
	events, unsub := b.Subscribe("test-suite")
	defer unsub()

	r := NewHTTPReporter(b, "test-suite", nil)
	step := &newtrun.Step{Name: "apply", Action: newtrun.ActionNewtron}

	r.StepProgress("scenario-a", step, stubOp(0, "redis_write", "applied"), 0)

	select {
	case ev := <-events:
		if ev.Type != EventStepProgress {
			t.Errorf("event type: got %q, want %q", ev.Type, EventStepProgress)
		}
		p, ok := ev.Payload.(StepProgressPayload)
		if !ok {
			t.Fatalf("payload type: got %T, want StepProgressPayload", ev.Payload)
		}
		if p.Op.Kind != "redis_write" {
			t.Errorf("Op.Kind: got %q, want redis_write", p.Op.Kind)
		}
		if p.Op.Result != "applied" {
			t.Errorf("Op.Result: got %q, want applied", p.Op.Result)
		}
		if p.Step != "apply" {
			t.Errorf("Step: got %q, want apply", p.Step)
		}
	case <-time.After(time.Second):
		t.Fatal("StepProgress event not published")
	}
}

func TestHTTPReporterStepProgressNilOpNoEvent(t *testing.T) {
	b := httputil.NewBroker[Event]()
	events, unsub := b.Subscribe("test-suite")
	defer unsub()

	r := NewHTTPReporter(b, "test-suite", nil)
	step := &newtrun.Step{Name: "apply", Action: newtrun.ActionNewtron}

	r.StepProgress("scenario-a", step, nil, 0)

	select {
	case ev := <-events:
		t.Errorf("nil op should not publish event; got %+v", ev)
	case <-time.After(100 * time.Millisecond):
		// Expected: no event.
	}
}

func TestHTTPReporterStepProgressChainsToInner(t *testing.T) {
	b := httputil.NewBroker[Event]()
	inner := &capturingReporter{}
	r := NewHTTPReporter(b, "test-suite", inner)
	step := &newtrun.Step{Name: "apply", Action: newtrun.ActionNewtron}

	r.StepProgress("scenario-a", step, stubOp(0, "redis_write", "applied"), 0)

	if inner.stepProgress != 1 {
		t.Errorf("inner.stepProgress: got %d, want 1", inner.stepProgress)
	}
}

func TestStateReporterStepProgressBuffersUntilStepEnd(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	state := &newtrun.RunState{
		Suite:  "test",
		Status: newtrun.SuiteStatusRunning,
		Scenarios: []newtrun.ScenarioState{
			{Name: "scen-a", TotalSteps: 1},
		},
	}
	sr := &newtrun.StateReporter{State: state}
	step := &newtrun.Step{Name: "apply", Action: newtrun.ActionNewtron}

	sr.StepStart("scen-a", step, 0, 1)
	sr.StepProgress("scen-a", step, stubOp(0, "redis_write", "applied"), 0)
	sr.StepProgress("scen-a", step, stubOp(1, "verify_read", "applied"), 0)
	sr.StepEnd("scen-a", &newtrun.StepResult{
		Name:   "apply",
		Action: newtrun.ActionNewtron,
		Status: newtrun.StepStatusPassed,
	}, 0, 1)

	if got := len(state.Scenarios[0].Steps); got != 1 {
		t.Fatalf("scenario steps: got %d, want 1", got)
	}
	step0 := state.Scenarios[0].Steps[0]
	if got := len(step0.DeviceOps); got != 2 {
		t.Fatalf("DeviceOps: got %d events, want 2", got)
	}
	if step0.DeviceOps[0].Kind != "redis_write" {
		t.Errorf("DeviceOps[0].Kind: got %q, want redis_write", step0.DeviceOps[0].Kind)
	}
	if step0.DeviceOps[1].Kind != "verify_read" {
		t.Errorf("DeviceOps[1].Kind: got %q, want verify_read", step0.DeviceOps[1].Kind)
	}
}

func TestStateReporterStepProgressClearedBetweenSteps(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	state := &newtrun.RunState{
		Suite:  "test",
		Status: newtrun.SuiteStatusRunning,
		Scenarios: []newtrun.ScenarioState{
			{Name: "scen-a", TotalSteps: 2},
		},
	}
	sr := &newtrun.StateReporter{State: state}
	step1 := &newtrun.Step{Name: "step1", Action: newtrun.ActionNewtron}
	step2 := &newtrun.Step{Name: "step2", Action: newtrun.ActionNewtron}

	sr.StepStart("scen-a", step1, 0, 2)
	sr.StepProgress("scen-a", step1, stubOp(0, "redis_write", "applied"), 0)
	sr.StepEnd("scen-a", &newtrun.StepResult{Name: "step1", Status: newtrun.StepStatusPassed}, 0, 2)

	sr.StepStart("scen-a", step2, 1, 2)
	// No StepProgress events on step2.
	sr.StepEnd("scen-a", &newtrun.StepResult{Name: "step2", Status: newtrun.StepStatusPassed}, 1, 2)

	if got := len(state.Scenarios[0].Steps); got != 2 {
		t.Fatalf("scenario steps: got %d, want 2", got)
	}
	if got := len(state.Scenarios[0].Steps[0].DeviceOps); got != 1 {
		t.Errorf("step1 DeviceOps: got %d, want 1", got)
	}
	if got := len(state.Scenarios[0].Steps[1].DeviceOps); got != 0 {
		t.Errorf("step2 DeviceOps should be empty (no events sent); got %d", got)
	}
}

func TestStateReporterStepProgressChainsToInner(t *testing.T) {
	inner := &capturingReporter{}
	state := &newtrun.RunState{
		Suite:     "test",
		Status:    newtrun.SuiteStatusRunning,
		Scenarios: []newtrun.ScenarioState{{Name: "s", TotalSteps: 1}},
	}
	t.Setenv("HOME", t.TempDir())
	sr := &newtrun.StateReporter{State: state, Inner: inner}
	step := &newtrun.Step{Name: "apply", Action: newtrun.ActionNewtron}

	sr.StepProgress("s", step, stubOp(0, "redis_write", "applied"), 0)

	if inner.stepProgress != 1 {
		t.Errorf("inner.stepProgress: got %d, want 1", inner.stepProgress)
	}
}
