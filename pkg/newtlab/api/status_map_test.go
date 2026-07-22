package api

import (
	"errors"
	"fmt"
	"net/http"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtlab"
)

// TestStatusFromErr pins the absent-resource → 404 mapping that
// newtlab/api.md documents for status/destroy/resync/node-start/stop.
// Before it, a registered-but-never-deployed lab surfaced as 500.
func TestStatusFromErr(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want int
	}{
		{"lab not deployed (wrapped ErrLabNotFound)",
			fmt.Errorf("status x: %w", fmt.Errorf("newtlab: lab x %w", newtlab.ErrLabNotFound)),
			http.StatusNotFound},
		{"unknown node (wrapped ErrNodeNotFound)",
			fmt.Errorf("stop x/n: %w", fmt.Errorf("newtlab: node %q %w", "n", newtlab.ErrNodeNotFound)),
			http.StatusNotFound},
		{"real failure stays 500", errors.New("parse state.json: boom"),
			http.StatusInternalServerError},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := statusFromErr(c.err); got != c.want {
				t.Errorf("statusFromErr = %d, want %d", got, c.want)
			}
		})
	}
}
