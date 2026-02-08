package newtest

import (
	"context"
	"fmt"
	"os/signal"
	"os"
	"path/filepath"
	"time"

	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/newtlab"
)

// Runner is the top-level newtest orchestrator.
type Runner struct {
	ScenariosDir  string
	TopologiesDir string
	Network       *network.Network
	Lab           *newtlab.Lab
	ChangeSets    map[string]*network.ChangeSet
	Verbose       bool

	opts     RunOptions
	scenario *Scenario
}

// RunOptions controls Runner behavior from CLI flags.
type RunOptions struct {
	Scenario  string
	All       bool
	Topology  string
	Platform  string
	Keep      bool
	NoDeploy  bool
	Parallel  int
	Verbose   bool
	JUnitPath string
}

// NewRunner creates a new test runner.
func NewRunner(scenariosDir, topologiesDir string) *Runner {
	return &Runner{
		ScenariosDir:  scenariosDir,
		TopologiesDir: topologiesDir,
	}
}

// Run executes one or all scenarios and returns results.
func (r *Runner) Run(opts RunOptions) ([]*ScenarioResult, error) {
	if opts.Scenario == "" && !opts.All {
		return nil, fmt.Errorf("specify --scenario <name> or --all")
	}

	var scenarios []*Scenario

	if opts.All {
		var err error
		scenarios, err = ParseAllScenarios(r.ScenariosDir)
		if err != nil {
			return nil, err
		}
		if len(scenarios) == 0 {
			return nil, fmt.Errorf("no scenarios found in %s", r.ScenariosDir)
		}
	} else {
		path := filepath.Join(r.ScenariosDir, opts.Scenario+".yaml")
		s, err := ParseScenario(path)
		if err != nil {
			return nil, err
		}
		scenarios = []*Scenario{s}
	}

	var results []*ScenarioResult
	for _, s := range scenarios {
		result, err := r.RunScenario(context.Background(), s, opts)
		if err != nil {
			return results, err
		}
		results = append(results, result)
	}
	return results, nil
}

// RunScenario executes a single scenario end-to-end.
func (r *Runner) RunScenario(ctx context.Context, scenario *Scenario, opts RunOptions) (*ScenarioResult, error) {
	r.opts = opts
	r.scenario = scenario

	result := &ScenarioResult{
		Name:     scenario.Name,
		Topology: scenario.Topology,
		Platform: scenario.Platform,
	}
	start := time.Now()

	// Resolve topology spec dir
	topology := opts.Topology
	if topology == "" {
		topology = scenario.Topology
	}
	specDir := filepath.Join(r.TopologiesDir, topology, "specs")

	// Deploy topology (unless --no-deploy)
	if !opts.NoDeploy {
		lab, err := DeployTopology(specDir)
		if err != nil {
			result.DeployError = err
			result.Status = StatusError
			result.Duration = time.Since(start)
			return result, nil
		}
		r.Lab = lab

		if !opts.Keep {
			defer func() { _ = DestroyTopology(r.Lab) }()
		}
	}

	// SIGINT handling
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	// Connect to all devices
	if err := r.connectDevices(ctx, specDir); err != nil {
		result.DeployError = err
		result.Status = StatusError
		result.Duration = time.Since(start)
		return result, nil
	}

	// Execute steps sequentially
	r.ChangeSets = make(map[string]*network.ChangeSet)
	for i, step := range scenario.Steps {
		output := r.executeStep(ctx, &step, i, len(scenario.Steps), opts)

		// Merge ChangeSets (last-write-wins)
		for name, cs := range output.ChangeSets {
			r.ChangeSets[name] = cs
		}

		result.Steps = append(result.Steps, *output.Result)

		// Fail-fast
		if output.Result.Status == StatusFailed || output.Result.Status == StatusError {
			break
		}
	}

	result.Status = computeOverallStatus(result.Steps)
	result.Duration = time.Since(start)

	return result, nil
}

// connectDevices builds the Network OO hierarchy and connects all devices.
func (r *Runner) connectDevices(ctx context.Context, specDir string) error {
	net, err := network.NewNetwork(specDir)
	if err != nil {
		return fmt.Errorf("loading specs: %w", err)
	}
	r.Network = net

	for _, name := range net.ListDevices() {
		dev, err := net.GetDevice(name)
		if err != nil {
			return fmt.Errorf("getting device %s: %w", name, err)
		}
		if err := dev.Connect(ctx); err != nil {
			return fmt.Errorf("connecting to %s: %w", name, err)
		}
	}

	return nil
}

// executeStep dispatches a step to its executor.
func (r *Runner) executeStep(ctx context.Context, step *Step, index, total int, opts RunOptions) *StepOutput {
	executor, ok := executors[step.Action]
	if !ok {
		return &StepOutput{
			Result: &StepResult{
				Name:    step.Name,
				Action:  step.Action,
				Status:  StatusError,
				Message: fmt.Sprintf("unknown action: %s", step.Action),
			},
		}
	}

	if opts.Verbose {
		fmt.Printf("  [%d/%d] %s\n", index+1, total, step.Name)
	}

	start := time.Now()
	output := executor.Execute(ctx, r, step)
	output.Result.Duration = time.Since(start)
	output.Result.Name = step.Name
	output.Result.Action = step.Action

	return output
}

// allDeviceNames returns sorted names of all topology devices.
func (r *Runner) allDeviceNames() []string {
	return r.Network.ListDevices()
}

// resolveDevices resolves step.Devices to concrete device names.
func (r *Runner) resolveDevices(step *Step) []string {
	return step.Devices.Resolve(r.allDeviceNames())
}

// hasDataplane checks if the scenario platform supports dataplane forwarding.
func (r *Runner) hasDataplane() bool {
	platformName := r.scenario.Platform
	if r.opts.Platform != "" {
		platformName = r.opts.Platform
	}
	p, err := r.Network.GetPlatform(platformName)
	if err != nil {
		return false
	}
	return p.Dataplane != ""
}

// computeOverallStatus computes overall scenario status from step results.
func computeOverallStatus(steps []StepResult) Status {
	hasError := false
	for _, s := range steps {
		if s.Status == StatusError {
			hasError = true
		}
		if s.Status == StatusFailed {
			return StatusFailed
		}
	}
	if hasError {
		return StatusError
	}
	return StatusPassed
}
