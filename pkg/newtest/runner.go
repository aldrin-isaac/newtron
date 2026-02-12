package newtest

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
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
	Progress      ProgressReporter

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

	// Lifecycle fields (set by `start` command, not by `run`)
	Suite     string            // suite name for state tracking; empty disables lifecycle
	Resume    bool              // true when resuming a paused run
	Completed map[string]Status // scenario → status from previous run (resume)
}

// NewRunner creates a new test runner.
func NewRunner(scenariosDir, topologiesDir string) *Runner {
	return &Runner{
		ScenariosDir:  scenariosDir,
		TopologiesDir: topologiesDir,
	}
}

// Run executes one or all scenarios and returns results.
// When running multiple scenarios with a shared topology, it deploys once and
// shares connections. Scenarios with `requires` are sorted by dependency order
// and skipped if a blocker fails.
func (r *Runner) Run(opts RunOptions) ([]*ScenarioResult, error) {
	if opts.Scenario == "" && !opts.All {
		return nil, fmt.Errorf("specify --scenario <name> or --all")
	}

	// Validate --topology override exists
	if opts.Topology != "" {
		topoDir := filepath.Join(r.TopologiesDir, opts.Topology, "specs")
		if _, err := os.Stat(topoDir); os.IsNotExist(err) {
			return nil, fmt.Errorf("topology %q not found: %s does not exist", opts.Topology, topoDir)
		}
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
		path, err := resolveScenarioPath(r.ScenariosDir, opts.Scenario)
		if err != nil {
			return nil, err
		}
		s, err := ParseScenario(path)
		if err != nil {
			return nil, err
		}
		scenarios = []*Scenario{s}
	}

	// Validate and topologically sort if any scenario declares requires
	if opts.All && hasRequires(scenarios) {
		if err := validateDependencyGraph(scenarios); err != nil {
			return nil, err
		}
		sorted, err := topologicalSort(scenarios)
		if err != nil {
			return nil, err
		}
		scenarios = sorted
	}

	r.progress(func(p ProgressReporter) { p.SuiteStart(scenarios) })

	suiteStart := time.Now()

	var results []*ScenarioResult
	var err error

	// If all scenarios share the same topology, deploy once and share connections
	if len(scenarios) > 1 {
		if topology := sharedTopology(scenarios, opts.Topology); topology != "" {
			results, err = r.runShared(context.Background(), scenarios, topology, opts)
			if err != nil {
				return results, err
			}
			r.progress(func(p ProgressReporter) { p.SuiteEnd(results, time.Since(suiteStart)) })
			return results, nil
		}
	}

	// Independent mode: different topologies or single scenario
	results, err = r.runIndependent(context.Background(), scenarios, opts)
	if err != nil {
		return results, err
	}

	if len(scenarios) > 1 {
		r.progress(func(p ProgressReporter) { p.SuiteEnd(results, time.Since(suiteStart)) })
	}
	return results, nil
}

// runShared deploys once, connects once, and runs all scenarios with a shared
// Runner. Skip propagation is applied based on requires.
func (r *Runner) runShared(ctx context.Context, scenarios []*Scenario, topology string, opts RunOptions) ([]*ScenarioResult, error) {
	specDir := filepath.Join(r.TopologiesDir, topology, "specs")

	// Deploy: lifecycle mode uses EnsureTopology; legacy mode uses DeployTopology
	if !opts.NoDeploy {
		if opts.Suite != "" {
			lab, _, err := EnsureTopology(specDir)
			if err != nil {
				var results []*ScenarioResult
				for _, sc := range scenarios {
					results = append(results, &ScenarioResult{
						Name:        sc.Name,
						Topology:    topology,
						Platform:    sc.Platform,
						Status:      StatusError,
						DeployError: &InfraError{Op: "deploy", Err: err},
					})
				}
				return results, nil
			}
			r.Lab = lab
			// Lifecycle mode: never destroy — stop command handles that
		} else {
			lab, err := DeployTopology(specDir)
			if err != nil {
				var results []*ScenarioResult
				for _, sc := range scenarios {
					results = append(results, &ScenarioResult{
						Name:        sc.Name,
						Topology:    topology,
						Platform:    sc.Platform,
						Status:      StatusError,
						DeployError: &InfraError{Op: "deploy", Err: err},
					})
				}
				return results, nil
			}
			r.Lab = lab
			if !opts.Keep {
				defer func() { _ = DestroyTopology(r.Lab) }()
			}
		}
	}

	// SIGINT handling
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	// Connect devices once
	if err := r.connectDevices(ctx, specDir); err != nil {
		connectErr := err
		if opts.NoDeploy {
			connectErr = fmt.Errorf("%w\nhint: no running lab; deploy first with: newtlab deploy -S <specDir>", err)
		}
		var results []*ScenarioResult
		for _, sc := range scenarios {
			results = append(results, &ScenarioResult{
				Name:        sc.Name,
				Topology:    topology,
				Platform:    sc.Platform,
				Status:      StatusError,
				DeployError: connectErr,
			})
		}
		return results, nil
	}

	r.ChangeSets = make(map[string]*network.ChangeSet)

	scenarioStatus := make(map[string]Status)
	var results []*ScenarioResult

	// Seed status map with completed scenarios from previous run (resume)
	if opts.Completed != nil {
		for name, st := range opts.Completed {
			scenarioStatus[name] = st
		}
	}

	for i, sc := range scenarios {
		platform := opts.Platform
		if platform == "" {
			platform = sc.Platform
		}

		// Resume: skip already-completed scenarios
		if opts.Resume {
			if prev, ok := opts.Completed[sc.Name]; ok && prev == StatusPassed {
				result := &ScenarioResult{
					Name:       sc.Name,
					Topology:   topology,
					Platform:   platform,
					Status:     StatusSkipped,
					SkipReason: "already passed (resumed)",
				}
				results = append(results, result)
				idx := i
				r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, idx, len(scenarios)) })
				continue
			}
		}

		// Pause check: if another process set status to "pausing", stop here
		if opts.Suite != "" && CheckPausing(opts.Suite) {
			return results, &PauseError{Completed: len(results)}
		}

		if reason := checkRequires(sc, scenarioStatus); reason != "" {
			result := &ScenarioResult{
				Name:       sc.Name,
				Topology:   topology,
				Platform:   platform,
				Status:     StatusSkipped,
				SkipReason: reason,
			}
			results = append(results, result)
			scenarioStatus[sc.Name] = StatusSkipped
			idx := i
			r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, idx, len(scenarios)) })
			continue
		}

		idx := i
		r.progress(func(p ProgressReporter) { p.ScenarioStart(sc.Name, idx, len(scenarios)) })

		r.opts = RunOptions{
			Topology: topology,
			Platform: platform,
			NoDeploy: true,
			Keep:     true,
			Verbose:  opts.Verbose,
		}
		r.scenario = sc

		result := &ScenarioResult{
			Name:     sc.Name,
			Topology: topology,
			Platform: platform,
		}
		start := time.Now()
		r.runScenarioSteps(ctx, sc, r.opts, result)
		result.Duration = time.Since(start)

		results = append(results, result)
		scenarioStatus[sc.Name] = result.Status
		r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, idx, len(scenarios)) })
	}

	return results, nil
}

// runIndependent runs each scenario with its own deploy/connect cycle.
// Skip propagation is still applied based on requires.
func (r *Runner) runIndependent(ctx context.Context, scenarios []*Scenario, opts RunOptions) ([]*ScenarioResult, error) {
	scenarioStatus := make(map[string]Status)
	var results []*ScenarioResult

	// Seed status map with completed scenarios from previous run (resume)
	if opts.Completed != nil {
		for name, st := range opts.Completed {
			scenarioStatus[name] = st
		}
	}

	for i, s := range scenarios {
		topology := opts.Topology
		if topology == "" {
			topology = s.Topology
		}
		platform := opts.Platform
		if platform == "" {
			platform = s.Platform
		}

		// Resume: skip already-completed scenarios
		if opts.Resume {
			if prev, ok := opts.Completed[s.Name]; ok && prev == StatusPassed {
				result := &ScenarioResult{
					Name:       s.Name,
					Topology:   topology,
					Platform:   platform,
					Status:     StatusSkipped,
					SkipReason: "already passed (resumed)",
				}
				results = append(results, result)
				idx := i
				r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, idx, len(scenarios)) })
				continue
			}
		}

		// Pause check
		if opts.Suite != "" && CheckPausing(opts.Suite) {
			return results, &PauseError{Completed: len(results)}
		}

		if reason := checkRequires(s, scenarioStatus); reason != "" {
			result := &ScenarioResult{
				Name:       s.Name,
				Topology:   topology,
				Platform:   platform,
				Status:     StatusSkipped,
				SkipReason: reason,
			}
			results = append(results, result)
			scenarioStatus[s.Name] = StatusSkipped
			idx := i
			r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, idx, len(scenarios)) })
			continue
		}

		idx := i
		r.progress(func(p ProgressReporter) { p.ScenarioStart(s.Name, idx, len(scenarios)) })

		result, err := r.RunScenario(ctx, s, opts)
		if err != nil {
			return results, err
		}
		results = append(results, result)
		scenarioStatus[s.Name] = result.Status
		r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, idx, len(scenarios)) })
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
		if opts.Suite != "" {
			lab, _, err := EnsureTopology(specDir)
			if err != nil {
				result.DeployError = &InfraError{Op: "deploy", Err: err}
				result.Status = StatusError
				result.Duration = time.Since(start)
				return result, nil
			}
			r.Lab = lab
		} else {
			lab, err := DeployTopology(specDir)
			if err != nil {
				result.DeployError = &InfraError{Op: "deploy", Err: err}
				result.Status = StatusError
				result.Duration = time.Since(start)
				return result, nil
			}
			r.Lab = lab
			if !opts.Keep {
				defer func() { _ = DestroyTopology(r.Lab) }()
			}
		}
	}

	// SIGINT handling
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	// Connect to all devices
	if err := r.connectDevices(ctx, specDir); err != nil {
		if opts.NoDeploy {
			result.DeployError = fmt.Errorf("%w\nhint: no running lab; deploy first with: newtlab deploy -S <specDir>", err)
		} else {
			result.DeployError = err
		}
		result.Status = StatusError
		result.Duration = time.Since(start)
		return result, nil
	}

	// Execute steps sequentially
	r.runScenarioSteps(ctx, scenario, opts, result)
	result.Duration = time.Since(start)

	return result, nil
}

// runScenarioSteps executes the steps of a scenario, appending results to result.
// When scenario.Repeat > 1, all steps are executed in a loop for the specified
// number of iterations. Execution stops on the first failed iteration.
func (r *Runner) runScenarioSteps(ctx context.Context, scenario *Scenario, opts RunOptions, result *ScenarioResult) {
	if r.ChangeSets == nil {
		r.ChangeSets = make(map[string]*network.ChangeSet)
	}

	repeat := scenario.Repeat
	if repeat <= 1 {
		repeat = 1
	}
	result.Repeat = scenario.Repeat

	for iter := 1; iter <= repeat; iter++ {
		iterFailed := false
		for i, step := range scenario.Steps {
			stepCopy := step
			idx := i
			total := len(scenario.Steps)
			r.progress(func(p ProgressReporter) { p.StepStart(scenario.Name, &stepCopy, idx, total) })

			output := r.executeStep(ctx, &step, i, len(scenario.Steps), opts)

			// Merge ChangeSets (last-write-wins)
			for name, cs := range output.ChangeSets {
				r.ChangeSets[name] = cs
			}

			sr := *output.Result
			if repeat > 1 {
				sr.Iteration = iter
			}
			result.Steps = append(result.Steps, sr)

			srCopy := sr
			r.progress(func(p ProgressReporter) { p.StepEnd(scenario.Name, &srCopy, idx, total) })

			// Fail-fast within iteration
			if output.Result.Status == StatusFailed || output.Result.Status == StatusError {
				iterFailed = true
				break
			}
		}

		if iterFailed {
			if repeat > 1 {
				result.FailedIteration = iter
			}
			break
		}
	}

	result.Status = computeOverallStatus(result.Steps)
}

// connectDevices builds the Network OO hierarchy and connects all devices.
func (r *Runner) connectDevices(ctx context.Context, specDir string) error {
	net, err := network.NewNetwork(specDir)
	if err != nil {
		return &InfraError{Op: "connect", Err: fmt.Errorf("loading specs: %w", err)}
	}
	r.Network = net

	topo := net.GetTopology()
	if topo == nil {
		return &InfraError{Op: "connect", Err: fmt.Errorf("no topology.json found")}
	}

	for _, name := range topo.DeviceNames() {
		dev, err := net.GetDevice(name)
		if err != nil {
			return &InfraError{Op: "connect", Device: name, Err: err}
		}
		if err := dev.Connect(ctx); err != nil {
			return &InfraError{Op: "connect", Device: name, Err: err}
		}
	}

	return nil
}

// executeStep dispatches a step to its executor.
func (r *Runner) executeStep(ctx context.Context, step *Step, index, total int, opts RunOptions) *StepOutput {
	executor, ok := executors[step.Action]
	if !ok {
		err := &StepError{
			Step:   step.Name,
			Action: step.Action,
			Err:    fmt.Errorf("unknown action: %s", step.Action),
		}
		return &StepOutput{
			Result: &StepResult{
				Name:    step.Name,
				Action:  step.Action,
				Status:  StatusError,
				Message: err.Error(),
			},
		}
	}

	start := time.Now()
	output := executor.Execute(ctx, r, step)
	output.Result.Duration = time.Since(start)
	output.Result.Name = step.Name
	output.Result.Action = step.Action

	// Aggregate per-device error details into Message when executors only set Details
	if output.Result.Message == "" && len(output.Result.Details) > 0 {
		var msgs []string
		for _, d := range output.Result.Details {
			if d.Status != StatusPassed && d.Message != "" {
				msgs = append(msgs, d.Device+": "+d.Message)
			}
		}
		if len(msgs) > 0 {
			output.Result.Message = strings.Join(msgs, "; ")
		}
	}

	return output
}

// progress calls fn with the ProgressReporter if one is set.
func (r *Runner) progress(fn func(ProgressReporter)) {
	if r.Progress != nil {
		fn(r.Progress)
	}
}

// allDeviceNames returns sorted names of all topology devices.
func (r *Runner) allDeviceNames() []string {
	if topo := r.Network.GetTopology(); topo != nil {
		return topo.DeviceNames()
	}
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

// hasRequires returns true if any scenario declares dependencies.
func hasRequires(scenarios []*Scenario) bool {
	for _, s := range scenarios {
		if len(s.Requires) > 0 {
			return true
		}
	}
	return false
}

// sharedTopology returns the common topology if all scenarios use the same one,
// or the override if set. Returns "" if topologies are mixed.
func sharedTopology(scenarios []*Scenario, override string) string {
	if override != "" {
		return override
	}
	if len(scenarios) == 0 {
		return ""
	}
	topo := scenarios[0].Topology
	for _, s := range scenarios[1:] {
		if s.Topology != topo {
			return ""
		}
	}
	return topo
}

// checkRequires returns a skip reason if any required scenario did not pass,
// or "" if all requirements are satisfied.
func checkRequires(sc *Scenario, status map[string]Status) string {
	for _, req := range sc.Requires {
		if st, ok := status[req]; ok && st != StatusPassed {
			return fmt.Sprintf("requires '%s' which %s", req, statusVerb(st))
		}
	}
	return ""
}

// resolveScenarioPath resolves a scenario name to a YAML file path.
// Tries in order:
//  1. Exact match: <dir>/<name>.yaml
//  2. Numbered prefix: <dir>/*-<name>.yaml
//  3. Scan files for matching name: field
func resolveScenarioPath(dir, name string) (string, error) {
	// 1. Exact match
	exact := filepath.Join(dir, name+".yaml")
	if _, err := os.Stat(exact); err == nil {
		return exact, nil
	}

	// 2. Numbered prefix glob: *-<name>.yaml
	matches, _ := filepath.Glob(filepath.Join(dir, "*-"+name+".yaml"))
	if len(matches) == 1 {
		return matches[0], nil
	}

	// 3. Scan all YAML files for matching name: field
	entries, err := os.ReadDir(dir)
	if err != nil {
		return "", fmt.Errorf("scenario %q not found: %w", name, err)
	}
	for _, e := range entries {
		if e.IsDir() || filepath.Ext(e.Name()) != ".yaml" {
			continue
		}
		path := filepath.Join(dir, e.Name())
		s, err := ParseScenario(path)
		if err != nil {
			continue
		}
		if s.Name == name {
			return path, nil
		}
	}

	return "", fmt.Errorf("scenario %q not found in %s", name, dir)
}
