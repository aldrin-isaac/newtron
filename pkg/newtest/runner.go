package newtest

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/newtron-network/newtron/pkg/newtron/network"
	"github.com/newtron-network/newtron/pkg/newtron/network/node"
	"github.com/newtron-network/newtron/pkg/newtlab"
)

// Runner is the top-level newtest orchestrator.
type Runner struct {
	ScenariosDir  string
	TopologiesDir string
	Network       *network.Network
	Lab           *newtlab.Lab
	ChangeSets    map[string]*node.ChangeSet
	HostConns     map[string]*ssh.Client // host device name → SSH client
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
	Verbose   bool
	JUnitPath string

	// Lifecycle fields (set by `start` command, not by `run`)
	Suite     string            // suite name for state tracking; empty disables lifecycle
	Resume    bool              // true when resuming a paused run
	Completed map[string]StepStatus // scenario → status from previous run (resume)
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
	if opts.All && HasRequires(scenarios) {
		sorted, err := ValidateDependencyGraph(scenarios)
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

// scenarioRunner is a callback that executes a single scenario within the
// iteration loop. It receives the resolved topology and platform names.
type scenarioRunner func(ctx context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error)

// iterateScenarios encapsulates the common scenario iteration loop used by both
// runShared and runIndependent. It handles resume, pause, requires checks, and
// progress reporting. The run callback performs the actual per-scenario execution.
// The deployedPlatform parameter specifies the actual platform used for deployment
// (for capability checks in shared mode); empty string means use per-scenario platform.
func (r *Runner) iterateScenarios(ctx context.Context, scenarios []*Scenario, opts RunOptions, deployedPlatform string, run scenarioRunner) ([]*ScenarioResult, error) {
	scenarioStatus := make(map[string]StepStatus)
	var results []*ScenarioResult

	// Seed status map with completed scenarios from previous run (resume)
	for name, st := range opts.Completed {
		scenarioStatus[name] = st
	}

	for i, sc := range scenarios {
		topology := opts.Topology
		if topology == "" {
			topology = sc.Topology
		}
		platform := opts.Platform
		if platform == "" {
			platform = sc.Platform
		}

		// Resume: skip already-completed scenarios
		if opts.Resume {
			if prev, ok := opts.Completed[sc.Name]; ok && prev == StepStatusPassed {
				result := &ScenarioResult{
					Name:       sc.Name,
					Topology:   topology,
					Platform:   platform,
					Status:     StepStatusSkipped,
					SkipReason: "already passed (resumed)",
				}
				results = append(results, result)
				r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, i, len(scenarios)) })
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
				Status:     StepStatusSkipped,
				SkipReason: reason,
			}
			results = append(results, result)
			scenarioStatus[sc.Name] = StepStatusSkipped
			r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, i, len(scenarios)) })
			continue
		}

		// Feature requirements check: skip if platform doesn't support required features
		// In shared mode (deployedPlatform != ""), use that; otherwise use per-scenario platform
		if reason := r.checkPlatformFeatures(sc, deployedPlatform, platform); reason != "" {
			result := &ScenarioResult{
				Name:       sc.Name,
				Topology:   topology,
				Platform:   platform,
				Status:     StepStatusSkipped,
				SkipReason: reason,
			}
			results = append(results, result)
			scenarioStatus[sc.Name] = StepStatusSkipped
			r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, i, len(scenarios)) })
			continue
		}

		r.progress(func(p ProgressReporter) { p.ScenarioStart(sc.Name, i, len(scenarios)) })

		result, err := run(ctx, sc, topology, platform)
		if err != nil {
			return results, err
		}

		results = append(results, result)
		scenarioStatus[sc.Name] = result.Status
		r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, i, len(scenarios)) })
	}

	return results, nil
}

// deployTopology deploys the lab topology using lifecycle mode (EnsureTopology)
// or legacy mode (DeployTopology). It returns a cleanup function that should be
// deferred by the caller; the cleanup is nil when no teardown is needed.
func (r *Runner) deployTopology(ctx context.Context, specDir string, opts RunOptions) (cleanup func(), err error) {
	if opts.Suite != "" {
		lab, _, err := EnsureTopology(ctx, specDir)
		if err != nil {
			return nil, err
		}
		r.Lab = lab
		return nil, nil // lifecycle mode: stop command handles teardown
	}
	lab, err := DeployTopology(ctx, specDir)
	if err != nil {
		return nil, err
	}
	r.Lab = lab
	if !opts.Keep {
		return func() { _ = DestroyTopology(context.Background(), r.Lab) }, nil
	}
	return nil, nil
}

// runShared deploys once, connects once, and runs all scenarios with a shared
// Runner. Skip propagation is applied based on requires.
func (r *Runner) runShared(ctx context.Context, scenarios []*Scenario, topology string, opts RunOptions) ([]*ScenarioResult, error) {
	specDir := filepath.Join(r.TopologiesDir, topology, "specs")

	// Determine the deployed platform (used for all scenarios in shared mode)
	deployedPlatform := opts.Platform
	if deployedPlatform == "" && len(scenarios) > 0 {
		deployedPlatform = scenarios[0].Platform
	}

	// Deploy topology (unless --no-deploy)
	if !opts.NoDeploy {
		fmt.Fprintf(os.Stderr, "newtest: deploying topology %s...\n", topology)
		cleanup, err := r.deployTopology(ctx, specDir, opts)
		if err != nil {
			var results []*ScenarioResult
			for _, sc := range scenarios {
				results = append(results, &ScenarioResult{
					Name:        sc.Name,
					Topology:    topology,
					Platform:    sc.Platform,
					Status:      StepStatusError,
					DeployError: &InfraError{Op: "deploy", Err: err},
				})
			}
			return results, nil
		}
		fmt.Fprintf(os.Stderr, "newtest: topology ready\n")
		if cleanup != nil {
			defer cleanup()
		}
	}

	// SIGINT handling
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	// Connect devices once
	fmt.Fprintf(os.Stderr, "newtest: connecting to devices...\n")
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
				Status:      StepStatusError,
				DeployError: connectErr,
			})
		}
		return results, nil
	}

	r.ChangeSets = make(map[string]*node.ChangeSet)

	// In shared mode, all capability checks use the deployed platform
	return r.iterateScenarios(ctx, scenarios, opts, deployedPlatform, func(ctx context.Context, sc *Scenario, _ string, platform string) (*ScenarioResult, error) {
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
		return result, nil
	})
}

// runIndependent runs each scenario with its own deploy/connect cycle.
// Skip propagation is still applied based on requires.
func (r *Runner) runIndependent(ctx context.Context, scenarios []*Scenario, opts RunOptions) ([]*ScenarioResult, error) {
	// Independent mode: each scenario uses its own platform
	return r.iterateScenarios(ctx, scenarios, opts, "", func(ctx context.Context, sc *Scenario, topology, platform string) (*ScenarioResult, error) {
		return r.runScenario(ctx, sc, opts)
	})
}

// runScenario executes a single scenario end-to-end.
func (r *Runner) runScenario(ctx context.Context, scenario *Scenario, opts RunOptions) (*ScenarioResult, error) {
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
		fmt.Fprintf(os.Stderr, "newtest: deploying topology %s...\n", topology)
		cleanup, err := r.deployTopology(ctx, specDir, opts)
		if err != nil {
			result.DeployError = &InfraError{Op: "deploy", Err: err}
			result.Status = StepStatusError
			result.Duration = time.Since(start)
			return result, nil
		}
		fmt.Fprintf(os.Stderr, "newtest: topology ready\n")
		if cleanup != nil {
			defer cleanup()
		}
	}

	// SIGINT handling
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	// Connect to all devices
	fmt.Fprintf(os.Stderr, "newtest: connecting to devices...\n")
	if err := r.connectDevices(ctx, specDir); err != nil {
		if opts.NoDeploy {
			result.DeployError = fmt.Errorf("%w\nhint: no running lab; deploy first with: newtlab deploy -S <specDir>", err)
		} else {
			result.DeployError = err
		}
		result.Status = StepStatusError
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
		r.ChangeSets = make(map[string]*node.ChangeSet)
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
			r.progress(func(p ProgressReporter) { p.StepStart(scenario.Name, &stepCopy, i, len(scenario.Steps)) })

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
			r.progress(func(p ProgressReporter) { p.StepEnd(scenario.Name, &srCopy, i, len(scenario.Steps)) })

			// Fail-fast within iteration
			if output.Result.Status == StepStatusFailed || output.Result.Status == StepStatusError {
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
// Host devices are connected via plain SSH (no Redis tunnel).
func (r *Runner) connectDevices(ctx context.Context, specDir string) error {
	net, err := network.NewNetwork(specDir)
	if err != nil {
		return &InfraError{Op: "connect", Err: fmt.Errorf("loading specs: %w", err)}
	}
	r.Network = net
	r.HostConns = make(map[string]*ssh.Client)

	topo := net.GetTopology()
	if topo == nil {
		return &InfraError{Op: "connect", Err: fmt.Errorf("no topology.json found")}
	}

	for _, name := range topo.DeviceNames() {
		if net.IsHostDevice(name) {
			client, err := connectHostSSH(net, name)
			if err != nil {
				return &InfraError{Op: "connect", Device: name, Err: err}
			}
			r.HostConns[name] = client
			continue
		}
		dev, err := net.GetNode(name)
		if err != nil {
			return &InfraError{Op: "connect", Device: name, Err: err}
		}
		if err := dev.Connect(ctx); err != nil {
			return &InfraError{Op: "connect", Device: name, Err: err}
		}
	}

	return nil
}

// connectHostSSH establishes a plain SSH connection to a host device.
func connectHostSSH(net *network.Network, name string) (*ssh.Client, error) {
	profile, err := net.GetHostProfile(name)
	if err != nil {
		return nil, fmt.Errorf("loading host profile: %w", err)
	}

	user := profile.SSHUser
	if user == "" {
		user = "root"
	}
	pass := profile.SSHPass
	port := profile.SSHPort
	if port == 0 {
		port = 22
	}

	config := &ssh.ClientConfig{
		User: user,
		Auth: []ssh.AuthMethod{
			ssh.Password(pass),
		},
		HostKeyCallback: ssh.InsecureIgnoreHostKey(),
		Timeout:         10 * time.Second,
	}

	addr := fmt.Sprintf("%s:%d", profile.MgmtIP, port)
	client, err := ssh.Dial("tcp", addr, config)
	if err != nil {
		return nil, fmt.Errorf("SSH dial %s: %w", addr, err)
	}
	return client, nil
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
				Status:  StepStatusError,
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
			if d.Status != StepStatusPassed && d.Message != "" {
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

// allDeviceNames returns sorted names of all topology devices (including hosts).
func (r *Runner) allDeviceNames() []string {
	if topo := r.Network.GetTopology(); topo != nil {
		return topo.DeviceNames()
	}
	return r.Network.ListNodes()
}

// allSwitchDeviceNames returns sorted names of all non-host topology devices.
func (r *Runner) allSwitchDeviceNames() []string {
	all := r.allDeviceNames()
	var switches []string
	for _, name := range all {
		if !r.Network.IsHostDevice(name) {
			switches = append(switches, name)
		}
	}
	return switches
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
func computeOverallStatus(steps []StepResult) StepStatus {
	hasError := false
	for _, s := range steps {
		if s.Status == StepStatusError {
			hasError = true
		}
		if s.Status == StepStatusFailed {
			return StepStatusFailed
		}
	}
	if hasError {
		return StepStatusError
	}
	return StepStatusPassed
}

// HasRequires returns true if any scenario declares dependencies (requires or after).
func HasRequires(scenarios []*Scenario) bool {
	for _, s := range scenarios {
		if len(s.Requires) > 0 || len(s.After) > 0 {
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
// or "" if all requirements are satisfied. A required scenario that has not
// been run yet is treated as not passed.
func checkRequires(sc *Scenario, status map[string]StepStatus) string {
	for _, req := range sc.Requires {
		st, ok := status[req]
		if !ok {
			return fmt.Sprintf("requires '%s' which has not run yet", req)
		}
		if st != StepStatusPassed {
			return fmt.Sprintf("requires '%s' which %s", req, statusVerb(st))
		}
	}
	return ""
}

// checkPlatformFeatures checks if the platform supports all required features.
// Returns a skip reason if any required feature is unsupported, empty string otherwise.
// If deployedPlatform is non-empty (shared mode), it is used instead of scenarioPlatform.
func (r *Runner) checkPlatformFeatures(sc *Scenario, deployedPlatform, scenarioPlatform string) string {
	if len(sc.RequiresFeatures) == 0 {
		return "" // No feature requirements
	}

	if r.Network == nil {
		return "" // Cannot check features without network (proceed and let operations fail)
	}

	// Use deployed platform (shared mode) if provided, otherwise use per-scenario platform
	platformName := deployedPlatform
	if platformName == "" {
		platformName = scenarioPlatform
	}

	platform, err := r.Network.GetPlatform(platformName)
	if err != nil {
		return "" // Cannot get platform spec (proceed and let operations fail)
	}

	var unsupported []string
	for _, feature := range sc.RequiresFeatures {
		if !platform.SupportsFeature(feature) {
			unsupported = append(unsupported, feature)
		}
	}

	if len(unsupported) > 0 {
		return fmt.Sprintf("platform '%s' does not support required features: %v", platformName, unsupported)
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
	var found string
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
			if found != "" {
				return "", fmt.Errorf("ambiguous scenario name %q: found in %s and %s", name, filepath.Base(found), e.Name())
			}
			found = path
		}
	}
	if found != "" {
		return found, nil
	}

	return "", fmt.Errorf("scenario %q not found in %s", name, dir)
}
