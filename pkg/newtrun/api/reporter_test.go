package api

import (
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

func TestReporterCallbacksProduceCorrectEventTypes(t *testing.T) {
	b := NewEventBroker()
	events, unsub := b.Subscribe("test-suite")
	defer unsub()

	r := NewHTTPReporter(b, "test-suite", nil)

	scenarios := []*newtrun.Scenario{
		{Name: "s1", Topology: "topo", Steps: []newtrun.Step{{Name: "step1"}}},
	}
	scenarioResult := &newtrun.ScenarioResult{
		Name:     "s1",
		Topology: "topo",
		Status:   newtrun.StepStatusPassed,
		Duration: 2 * time.Second,
	}
	step := &newtrun.Step{Name: "step1", Action: newtrun.ActionWait}
	stepResult := &newtrun.StepResult{
		Name:     "step1",
		Action:   newtrun.ActionWait,
		Status:   newtrun.StepStatusPassed,
		Duration: time.Second,
	}

	r.SuiteStart(scenarios)
	r.ScenarioStart("s1", 0, 1)
	r.StepStart("s1", step, 0, 1)
	r.StepEnd("s1", stepResult, 0, 1)
	r.ScenarioEnd(scenarioResult, 0, 1)
	r.SuiteEnd([]*newtrun.ScenarioResult{scenarioResult}, newtrun.SuiteStatusComplete, 3*time.Second)

	want := []EventType{
		EventSuiteStart,
		EventScenarioStart,
		EventStepStart,
		EventStepEnd,
		EventScenarioEnd,
		EventSuiteEnd,
	}
	for i, expected := range want {
		select {
		case ev := <-events:
			if ev.Type != expected {
				t.Errorf("event %d: got %q, want %q", i, ev.Type, expected)
			}
		case <-time.After(time.Second):
			t.Fatalf("timeout waiting for event %d (%q)", i, expected)
		}
	}
}

// TestReporterSuiteEndCarriesStatus is the wire-side guard for the
// shutdown-honesty fix: an aborted run must serialize as status=aborted
// in the SSE SuiteEnd payload so the CLI can distinguish it from a
// real test failure.
func TestReporterSuiteEndCarriesStatus(t *testing.T) {
	cases := []newtrun.SuiteStatus{
		newtrun.SuiteStatusComplete,
		newtrun.SuiteStatusFailed,
		newtrun.SuiteStatusAborted,
		newtrun.SuiteStatusPaused,
	}
	for _, status := range cases {
		t.Run(string(status), func(t *testing.T) {
			b := NewEventBroker()
			events, unsub := b.Subscribe("test-suite")
			defer unsub()
			r := NewHTTPReporter(b, "test-suite", nil)
			r.SuiteEnd(nil, status, time.Second)
			select {
			case ev := <-events:
				p, ok := ev.Payload.(SuiteEndPayload)
				if !ok {
					t.Fatalf("payload type: got %T, want SuiteEndPayload", ev.Payload)
				}
				if p.Status != status {
					t.Errorf("payload.Status: got %v, want %v", p.Status, status)
				}
			case <-time.After(time.Second):
				t.Fatal("timeout waiting for SuiteEnd")
			}
		})
	}
}

func TestReporterChainsToInner(t *testing.T) {
	b := NewEventBroker()
	inner := &capturingReporter{}
	r := NewHTTPReporter(b, "test-suite", inner)

	r.ScenarioStart("s1", 0, 1)
	r.SuiteEnd(nil, newtrun.SuiteStatusComplete, time.Second)

	if inner.scenarioStarts != 1 {
		t.Errorf("inner.scenarioStarts: got %d, want 1", inner.scenarioStarts)
	}
	if inner.suiteEnds != 1 {
		t.Errorf("inner.suiteEnds: got %d, want 1", inner.suiteEnds)
	}
}

func TestReporterScenarioEndCarriesResultFields(t *testing.T) {
	b := NewEventBroker()
	events, unsub := b.Subscribe("test-suite")
	defer unsub()
	r := NewHTTPReporter(b, "test-suite", nil)

	result := &newtrun.ScenarioResult{
		Name:     "scenario-x",
		Topology: "topo-y",
		Status:   newtrun.StepStatusFailed,
		Duration: 5 * time.Second,
		Steps: []newtrun.StepResult{
			{Name: "step-a", Action: newtrun.ActionNewtron, Status: newtrun.StepStatusFailed, Message: "boom"},
		},
	}
	r.ScenarioEnd(result, 2, 5)

	select {
	case ev := <-events:
		p, ok := ev.Payload.(ScenarioEndPayload)
		if !ok {
			t.Fatalf("payload type: got %T, want ScenarioEndPayload", ev.Payload)
		}
		if p.Name != "scenario-x" {
			t.Errorf("Name: got %q, want %q", p.Name, "scenario-x")
		}
		if p.Status != newtrun.StepStatusFailed {
			t.Errorf("Status: got %q, want %q", p.Status, newtrun.StepStatusFailed)
		}
		if p.Index != 2 || p.Total != 5 {
			t.Errorf("Index/Total: got %d/%d, want 2/5", p.Index, p.Total)
		}
		if len(p.Steps) != 1 || p.Steps[0].Message != "boom" {
			t.Errorf("Steps: got %+v", p.Steps)
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

// capturingReporter is a test double that counts callbacks.
type capturingReporter struct {
	suiteStarts    int
	scenarioStarts int
	scenarioEnds   int
	stepStarts     int
	stepProgress   int
	stepEnds       int
	suiteEnds      int
}

func (c *capturingReporter) SuiteStart(scenarios []*newtrun.Scenario) {
	c.suiteStarts++
}
func (c *capturingReporter) ScenarioStart(name string, index, total int) {
	c.scenarioStarts++
}
func (c *capturingReporter) ScenarioEnd(result *newtrun.ScenarioResult, index, total int) {
	c.scenarioEnds++
}
func (c *capturingReporter) StepStart(scenario string, step *newtrun.Step, index, total int) {
	c.stepStarts++
}
func (c *capturingReporter) StepProgress(scenario string, step *newtrun.Step, op *sonic.DeviceOp, index int) {
	c.stepProgress++
}
func (c *capturingReporter) StepEnd(scenario string, result *newtrun.StepResult, index, total int) {
	c.stepEnds++
}
func (c *capturingReporter) SuiteEnd(results []*newtrun.ScenarioResult, _ newtrun.SuiteStatus, duration time.Duration) {
	c.suiteEnds++
}
