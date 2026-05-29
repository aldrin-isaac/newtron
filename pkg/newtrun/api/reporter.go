package api

import (
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// HTTPReporter implements newtrun.ProgressReporter and publishes each
// callback as an Event to the EventBroker, keyed by the run's identity.
//
// One HTTPReporter is constructed per server-side run. The RunKey identifies
// which run this reporter belongs to so the broker can route events to the
// right SSE subscribers. For file-backed suite runs the RunKey is the suite
// name (matches the GET /api/runs/{suite}/events path parameter). For inline
// runs introduced in PR 3, the RunKey is the run's UUID.
//
// The decorator pattern from StateReporter applies here too: HTTPReporter is
// typically wrapped around the existing StateReporter + consoleProgress chain
// so events flow to all three sinks (state file, terminal, HTTP).
type HTTPReporter struct {
	Broker *EventBroker
	RunKey string
	Inner  newtrun.ProgressReporter // optional pass-through for chaining
}

// NewHTTPReporter constructs a reporter that publishes events for runKey to
// the given broker, optionally forwarding to inner.
func NewHTTPReporter(broker *EventBroker, runKey string, inner newtrun.ProgressReporter) *HTTPReporter {
	return &HTTPReporter{
		Broker: broker,
		RunKey: runKey,
		Inner:  inner,
	}
}

func (r *HTTPReporter) SuiteStart(scenarios []*newtrun.Scenario) {
	summaries := make([]ScenarioSummary, 0, len(scenarios))
	for _, s := range scenarios {
		summaries = append(summaries, scenarioSummaryFrom(s))
	}
	r.Broker.Publish(r.RunKey, Event{
		Type:    EventSuiteStart,
		Payload: SuiteStartPayload{Scenarios: summaries},
	})
	if r.Inner != nil {
		r.Inner.SuiteStart(scenarios)
	}
}

func (r *HTTPReporter) ScenarioStart(name string, index, total int) {
	r.Broker.Publish(r.RunKey, Event{
		Type: EventScenarioStart,
		Payload: ScenarioStartPayload{
			Name:  name,
			Index: index,
			Total: total,
		},
	})
	if r.Inner != nil {
		r.Inner.ScenarioStart(name, index, total)
	}
}

func (r *HTTPReporter) ScenarioEnd(result *newtrun.ScenarioResult, index, total int) {
	r.Broker.Publish(r.RunKey, Event{
		Type:    EventScenarioEnd,
		Payload: scenarioEndFrom(result, index, total),
	})
	if r.Inner != nil {
		r.Inner.ScenarioEnd(result, index, total)
	}
}

func (r *HTTPReporter) StepStart(scenario string, step *newtrun.Step, index, total int) {
	r.Broker.Publish(r.RunKey, Event{
		Type: EventStepStart,
		Payload: StepStartPayload{
			Scenario: scenario,
			Name:     step.Name,
			Action:   step.Action,
			Index:    index,
			Total:    total,
		},
	})
	if r.Inner != nil {
		r.Inner.StepStart(scenario, step, index, total)
	}
}

func (r *HTTPReporter) StepProgress(scenario string, step *newtrun.Step, op *sonic.DeviceOp, index int) {
	if op == nil {
		return
	}
	r.Broker.Publish(r.RunKey, Event{
		Type: EventStepProgress,
		Payload: StepProgressPayload{
			Scenario: scenario,
			Step:     step.Name,
			Action:   step.Action,
			Index:    index,
			Op:       *op,
		},
	})
	if r.Inner != nil {
		r.Inner.StepProgress(scenario, step, op, index)
	}
}

func (r *HTTPReporter) StepEnd(scenario string, result *newtrun.StepResult, index, total int) {
	r.Broker.Publish(r.RunKey, Event{
		Type: EventStepEnd,
		Payload: StepEndPayload{
			Scenario: scenario,
			Result:   stepResultFrom(result),
			Index:    index,
			Total:    total,
		},
	})
	if r.Inner != nil {
		r.Inner.StepEnd(scenario, result, index, total)
	}
}

func (r *HTTPReporter) SuiteEnd(results []*newtrun.ScenarioResult, duration time.Duration) {
	payloads := make([]ScenarioEndPayload, 0, len(results))
	for i, res := range results {
		payloads = append(payloads, scenarioEndFrom(res, i, len(results)))
	}
	r.Broker.Publish(r.RunKey, Event{
		Type: EventSuiteEnd,
		Payload: SuiteEndPayload{
			Results:  payloads,
			Duration: durationString(duration),
		},
	})
	if r.Inner != nil {
		r.Inner.SuiteEnd(results, duration)
	}
}
