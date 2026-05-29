package newtrun

import (
	"bytes"
	"strings"
	"testing"
	"time"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
)

func TestConsoleProgressStepProgressVerboseRenders(t *testing.T) {
	var buf bytes.Buffer
	p := &consoleProgress{W: &buf, Verbose: true}
	step := &Step{Name: "apply", Action: ActionNewtron}
	op := &sonic.DeviceOp{
		Seq:    0,
		Kind:   "redis_write",
		Table:  "VLAN",
		Key:    "Vlan100",
		Result: "applied",
		At:     time.Now().UTC(),
	}

	p.StepProgress("scen-a", step, op, 0)

	out := buf.String()
	if !strings.Contains(out, "redis_write") {
		t.Errorf("expected output to contain kind; got %q", out)
	}
	if !strings.Contains(out, "VLAN") {
		t.Errorf("expected output to contain table; got %q", out)
	}
	if !strings.Contains(out, "Vlan100") {
		t.Errorf("expected output to contain key; got %q", out)
	}
}

func TestConsoleProgressStepProgressNonVerboseSilent(t *testing.T) {
	var buf bytes.Buffer
	p := &consoleProgress{W: &buf, Verbose: false}
	step := &Step{Name: "apply", Action: ActionNewtron}
	op := &sonic.DeviceOp{Seq: 0, Kind: "redis_write", Result: "applied"}

	p.StepProgress("scen-a", step, op, 0)

	if buf.Len() > 0 {
		t.Errorf("non-verbose should be silent; got %q", buf.String())
	}
}

func TestConsoleProgressStepProgressRejectedHighlighted(t *testing.T) {
	var buf bytes.Buffer
	p := &consoleProgress{W: &buf, Verbose: true}
	step := &Step{Name: "apply", Action: ActionNewtron}
	op := &sonic.DeviceOp{
		Seq:    0,
		Kind:   "redis_write",
		Table:  "VLAN",
		Key:    "Vlan100",
		Result: "rejected",
	}

	p.StepProgress("scen-a", step, op, 0)

	out := buf.String()
	if !strings.Contains(out, "rejected") {
		t.Errorf("rejected result should be visible; got %q", out)
	}
}

func TestConsoleProgressStepProgressNilOpSilent(t *testing.T) {
	var buf bytes.Buffer
	p := &consoleProgress{W: &buf, Verbose: true}
	step := &Step{Name: "apply", Action: ActionNewtron}

	p.StepProgress("scen-a", step, nil, 0)

	if buf.Len() > 0 {
		t.Errorf("nil op should produce no output; got %q", buf.String())
	}
}
