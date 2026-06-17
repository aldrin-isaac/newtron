package newtrun

import (
	"context"
	"errors"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtlab"
	"github.com/aldrin-isaac/newtron/pkg/newtlab/api"
)

// fakeLabClient records calls and lets each one inject a canned
// return. Satisfies the LabClient contract structurally; production
// code uses *newtlabclient.Client.
type fakeLabClient struct {
	statusFn  func(ctx context.Context, topology string) (*newtlab.LabState, error)
	deployFn  func(ctx context.Context, topology string, opts api.DeployRequest) error
	destroyFn func(ctx context.Context, topology string) error

	deployCalls  int
	destroyCalls int
}

func (f *fakeLabClient) LabStatus(ctx context.Context, topology string) (*newtlab.LabState, error) {
	if f.statusFn != nil {
		return f.statusFn(ctx, topology)
	}
	return nil, errors.New("not deployed")
}

func (f *fakeLabClient) Deploy(ctx context.Context, topology string, opts api.DeployRequest) error {
	f.deployCalls++
	if f.deployFn != nil {
		return f.deployFn(ctx, topology, opts)
	}
	return nil
}

func (f *fakeLabClient) Destroy(ctx context.Context, topology string) error {
	f.destroyCalls++
	if f.destroyFn != nil {
		return f.destroyFn(ctx, topology)
	}
	return nil
}

// TestDeployTopology_RoutesThroughClient asserts the §27 contract:
// DeployTopology routes through the LabClient interface, never
// in-process via newtlab.NewLab. A mock satisfying LabClient is
// sufficient; the real production path uses *newtlabclient.Client.
func TestDeployTopology_RoutesThroughClient(t *testing.T) {
	fake := &fakeLabClient{}
	if err := DeployTopology(context.Background(), fake, "topo"); err != nil {
		t.Fatalf("DeployNetwork: %v", err)
	}
	if fake.deployCalls != 1 {
		t.Errorf("deploy calls = %d, want 1", fake.deployCalls)
	}
}

// TestDeployTopology_PropagatesError surfaces server-side failures
// rather than swallowing them — the runner needs the error to set
// scenario status correctly.
func TestDeployTopology_PropagatesError(t *testing.T) {
	want := errors.New("disk full")
	fake := &fakeLabClient{
		deployFn: func(ctx context.Context, topology string, opts api.DeployRequest) error {
			return want
		},
	}
	err := DeployTopology(context.Background(), fake, "topo")
	if err == nil || !errors.Is(err, want) {
		t.Errorf("err = %v, want chain containing %v", err, want)
	}
}

// TestEnsureTopology_ReusesRunningLab asserts that when LabStatus
// reports all nodes running, EnsureTopology skips the redeploy.
// Saves real wall time on lifecycle-mode runs that re-target an
// already-up lab.
func TestEnsureTopology_ReusesRunningLab(t *testing.T) {
	fake := &fakeLabClient{
		statusFn: func(ctx context.Context, topology string) (*newtlab.LabState, error) {
			return &newtlab.LabState{
				Name: topology,
				Nodes: map[string]*newtlab.NodeState{
					"n1": {Status: "running"},
					"n2": {Status: "running"},
				},
			}, nil
		},
	}
	if err := EnsureTopology(context.Background(), fake, "topo"); err != nil {
		t.Fatalf("EnsureNetwork: %v", err)
	}
	if fake.deployCalls != 0 {
		t.Errorf("deploy calls = %d, want 0 (reuse path)", fake.deployCalls)
	}
}

// TestEnsureTopology_RedeploysWhenPartialOrMissing covers the two
// non-reuse paths: (1) LabStatus errors (lab not deployed), and
// (2) LabStatus reports a mix of running and stopped nodes.
func TestEnsureTopology_RedeploysWhenPartialOrMissing(t *testing.T) {
	cases := []struct {
		name    string
		statusFn func(ctx context.Context, topology string) (*newtlab.LabState, error)
	}{
		{
			name: "missing",
			statusFn: func(ctx context.Context, topology string) (*newtlab.LabState, error) {
				return nil, errors.New("404")
			},
		},
		{
			name: "partial",
			statusFn: func(ctx context.Context, topology string) (*newtlab.LabState, error) {
				return &newtlab.LabState{
					Name: topology,
					Nodes: map[string]*newtlab.NodeState{
						"n1": {Status: "running"},
						"n2": {Status: "stopped"},
					},
				}, nil
			},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			fake := &fakeLabClient{statusFn: c.statusFn}
			if err := EnsureTopology(context.Background(), fake, "topo"); err != nil {
				t.Fatalf("EnsureNetwork: %v", err)
			}
			if fake.deployCalls != 1 {
				t.Errorf("deploy calls = %d, want 1 (redeploy path)", fake.deployCalls)
			}
		})
	}
}

// TestDestroyTopology_RoutesThroughClient mirrors the deploy test:
// destroy goes through the HTTP client, not newtlab.DestroyByName.
func TestDestroyTopology_RoutesThroughClient(t *testing.T) {
	fake := &fakeLabClient{}
	if err := DestroyTopology(context.Background(), fake, "topo"); err != nil {
		t.Fatalf("DestroyNetwork: %v", err)
	}
	if fake.destroyCalls != 1 {
		t.Errorf("destroy calls = %d, want 1", fake.destroyCalls)
	}
}
