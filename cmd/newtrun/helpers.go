package main

import (
	"github.com/aldrin-isaac/newtron/pkg/newtrun"
)

// resolveTopologyFromState returns the topology name carried by the
// suite's run state. The server already populates this field when it
// starts a run, so the CLI no longer needs a filesystem fallback. An
// empty return value means the run state itself doesn't know — caller
// should display "unknown" rather than silently fall back to on-disk
// reads of scenario YAMLs.
func resolveTopologyFromState(state *newtrun.RunState) string {
	return state.Network
}
