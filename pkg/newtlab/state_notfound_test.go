package newtlab

import (
	"errors"
	"fmt"
	"testing"
)

func TestSentinelMessagesMatchLegacyText(t *testing.T) {
	lab := fmt.Errorf("newtlab: lab %s %w", "2node-vs", ErrLabNotFound)
	if lab.Error() != "newtlab: lab 2node-vs not found (no state.json)" {
		t.Errorf("lab msg = %q", lab.Error())
	}
	if !errors.Is(lab, ErrLabNotFound) {
		t.Error("lab err does not unwrap to ErrLabNotFound")
	}
	node := fmt.Errorf("newtlab: node %q %w", "switch9", ErrNodeNotFound)
	if node.Error() != `newtlab: node "switch9" not found` {
		t.Errorf("node msg = %q", node.Error())
	}
	if !errors.Is(node, ErrNodeNotFound) {
		t.Error("node err does not unwrap to ErrNodeNotFound")
	}
}

// TestLoadStateMissingIsErrLabNotFound pins the typed not-found: a lab dir
// with no state.json (never deployed) unwraps to ErrLabNotFound so the API
// maps it to 404 rather than 500.
func TestLoadStateMissingIsErrLabNotFound(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	resetHomeDir()
	_, err := LoadState("never-deployed")
	if err == nil {
		t.Fatal("expected error for missing state.json")
	}
	if !errors.Is(err, ErrLabNotFound) {
		t.Errorf("err = %v; want errors.Is(_, ErrLabNotFound)", err)
	}
}
