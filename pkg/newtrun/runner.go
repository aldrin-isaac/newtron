package newtrun

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/newtron-network/newtron/pkg/newtlab"
	"github.com/newtron-network/newtron/pkg/newtron/client"
)

// Runner is the top-level newtrun orchestrator.
type Runner struct {
	ScenariosDir string
	ServerURL    string         // newtron-server HTTP address
	NetworkID    string         // network identifier for server operations
	Client       *client.Client // HTTP client for all SONiC operations
	Lab          *newtlab.Lab
	HostConns    map[string]*ssh.Client // host device name → SSH client
	Progress     ProgressReporter

	// Populated by connectToServer from the server's registered network.
	Topology string // topology name (from server)
	SpecDir  string // spec directory (from server)

	discoveredPlatform string // platform discovered from connected devices

	opts     RunOptions
	scenario *Scenario
}

// RunOptions controls Runner behavior from CLI flags.
type RunOptions struct {
	Scenario  string
	Target    string // run minimal dependency chain to reach this scenario
	All       bool
	Platform  string
	Keep      bool
	NoDeploy  bool
	Verbose   bool
	JUnitPath string

	// Lifecycle fields (set by `start` command, not by `run`)
	Suite     string                 // suite name for state tracking; empty disables lifecycle
	Resume    bool                   // true when resuming a paused run
	Completed map[string]StepStatus  // scenario → status from previous run (resume)
}

// NewRunner creates a new test runner.
func NewRunner(scenariosDir string) *Runner {
	return &Runner{
		ScenariosDir: scenariosDir,
	}
}

// Run executes one or all scenarios and returns results.
// The server determines the topology — scenarios declare compatible topologies
// as guards; mismatches fail immediately.
func (r *Runner) Run(opts RunOptions) ([]*ScenarioResult, error) {
	if opts.Scenario == "" && opts.Target == "" && !opts.All {
		return nil, fmt.Errorf("specify --scenario <name>, --target <name>, or --all")
	}

	var scenarios []*Scenario

	if opts.All || opts.Target != "" {
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
	if (opts.All || opts.Target != "") && HasRequires(scenarios) {
		sorted, err := ValidateDependencyGraph(scenarios)
		if err != nil {
			return nil, err
		}
		scenarios = sorted
	}

	// --target: filter to minimal dependency chain
	if opts.Target != "" {
		chain, err := ComputeTargetChain(scenarios, opts.Target)
		if err != nil {
			return nil, err
		}
		scenarios = chain
	}

	// Connect to server to learn topology
	fmt.Fprintf(os.Stderr, "newtrun: connecting to server %s...\n", r.ServerURL)
	if err := r.connectToServer(); err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "newtrun: server has topology %q (%d nodes)\n", r.Topology, len(r.allDeviceNames()))

	// Guard: all scenarios must be compatible with the server's topology
	for _, sc := range scenarios {
		if sc.Topology != "" && sc.Topology != r.Topology {
			return nil, fmt.Errorf("scenario %q requires topology %q but server has %q loaded",
				sc.Name, sc.Topology, r.Topology)
		}
	}

	r.progress(func(p ProgressReporter) { p.SuiteStart(scenarios) })
	suiteStart := time.Now()

	// Deploy topology (unless --no-deploy)
	if !opts.NoDeploy {
		fmt.Fprintf(os.Stderr, "newtrun: deploying topology %s...\n", r.Topology)
		cleanup, err := r.deployTopology(context.Background(), r.SpecDir, opts)
		if err != nil {
			var results []*ScenarioResult
			for _, sc := range scenarios {
				results = append(results, &ScenarioResult{
					Name:        sc.Name,
					Topology:    r.Topology,
					Platform:    sc.Platform,
					Status:      StepStatusError,
					DeployError: &InfraError{Op: "deploy", Err: err},
				})
			}
			return results, nil
		}
		fmt.Fprintf(os.Stderr, "newtrun: topology ready\n")
		if cleanup != nil {
			defer cleanup()
		}
	}

	// SIGINT handling
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	// Connect host devices (skip in no-deploy mode — no physical devices to connect to).
	if opts.NoDeploy {
		r.HostConns = make(map[string]*ssh.Client)
		fmt.Fprintf(os.Stderr, "newtrun: no-deploy mode — skipping device connections\n")
	} else {
		fmt.Fprintf(os.Stderr, "newtrun: connecting to devices...\n")
		if err := r.connectDevices(); err != nil {
			var results []*ScenarioResult
			for _, sc := range scenarios {
				results = append(results, &ScenarioResult{
					Name:        sc.Name,
					Topology:    r.Topology,
					Platform:    sc.Platform,
					Status:      StepStatusError,
					DeployError: err,
				})
			}
			return results, nil
		}
	}

	// Resolve platform for capability checks
	deployedPlatform := opts.Platform
	if deployedPlatform == "" && len(scenarios) > 0 {
		deployedPlatform = scenarios[0].Platform
	}
	if deployedPlatform == "" {
		deployedPlatform = r.discoveredPlatform
	}

	// Run all scenarios
	results, err := r.iterateScenarios(ctx, scenarios, opts, deployedPlatform, func(ctx context.Context, sc *Scenario, platform string) (*ScenarioResult, error) {
		r.opts = RunOptions{
			Platform: platform,
			NoDeploy: true,
			Keep:     true,
			Verbose:  opts.Verbose,
		}
		r.scenario = sc

		result := &ScenarioResult{
			Name:     sc.Name,
			Topology: r.Topology,
			Platform: platform,
		}
		start := time.Now()
		r.runScenarioSteps(ctx, sc, r.opts, result)
		result.Duration = time.Since(start)
		return result, nil
	})
	if err != nil {
		return results, err
	}

	if len(scenarios) > 1 {
		r.progress(func(p ProgressReporter) { p.SuiteEnd(results, time.Since(suiteStart)) })
	}
	return results, nil
}

// scenarioRunner is a callback that executes a single scenario within the
// iteration loop. It receives the resolved platform name.
type scenarioRunner func(ctx context.Context, sc *Scenario, platform string) (*ScenarioResult, error)

// iterateScenarios encapsulates the scenario iteration loop. It handles resume,
// pause, requires checks, and progress reporting. The run callback performs the
// actual per-scenario execution.
func (r *Runner) iterateScenarios(ctx context.Context, scenarios []*Scenario, opts RunOptions, deployedPlatform string, run scenarioRunner) ([]*ScenarioResult, error) {
	scenarioStatus := make(map[string]StepStatus)
	var results []*ScenarioResult

	// Seed status map with completed scenarios from previous run (resume)
	for name, st := range opts.Completed {
		scenarioStatus[name] = st
	}

	for i, sc := range scenarios {
		platform := opts.Platform
		if platform == "" {
			platform = sc.Platform
		}

		// Resume: skip already-completed scenarios
		if opts.Resume {
			if prev, ok := opts.Completed[sc.Name]; ok && prev == StepStatusPassed {
				result := &ScenarioResult{
					Name:       sc.Name,
					Topology:   r.Topology,
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
				Topology:   r.Topology,
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
		if reason := r.checkPlatformFeatures(sc, deployedPlatform, platform); reason != "" {
			result := &ScenarioResult{
				Name:       sc.Name,
				Topology:   r.Topology,
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

		result, err := run(ctx, sc, platform)
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
// or standalone mode (DeployTopology). It returns a cleanup function that should
// be deferred by the caller; the cleanup is nil when no teardown is needed.
func (r *Runner) deployTopology(ctx context.Context, specDir string, opts RunOptions) (cleanup func(), err error) {
	if opts.Suite != "" {
		lab, err := EnsureTopology(ctx, specDir)
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

// connectToServer queries the server for the registered network's info.
// Populates r.Topology, r.SpecDir, and creates the HTTP client.
func (r *Runner) connectToServer() error {
	r.Client = client.New(r.ServerURL, r.NetworkID)

	info, err := r.Client.GetNetworkInfo()
	if err != nil {
		return &InfraError{Op: "connect", Err: fmt.Errorf("querying server: %w (is the network registered?)", err)}
	}

	r.SpecDir = info.SpecDir
	r.Topology = info.Topology

	if r.Topology == "" {
		r.Topology = "(unknown)"
	}

	return nil
}

// connectDevices connects host devices via SSH and discovers the platform.
// SONiC devices are not pre-connected; the server connects on demand per-request.
func (r *Runner) connectDevices() error {
	r.HostConns = make(map[string]*ssh.Client)

	deviceNames, err := r.Client.TopologyDeviceNames()
	if err != nil || deviceNames == nil {
		return &InfraError{Op: "connect", Err: fmt.Errorf("no topology.json found")}
	}

	for _, name := range deviceNames {
		isHost, err := r.Client.IsHostDevice(name)
		if err != nil {
			return &InfraError{Op: "connect", Device: name, Err: err}
		}
		if isHost {
			sshClient, err := connectHostSSH(r.Client, name)
			if err != nil {
				return &InfraError{Op: "connect", Device: name, Err: err}
			}
			r.HostConns[name] = sshClient
		}
	}

	// Discover platform from the first non-host device's profile.
	for _, name := range deviceNames {
		if _, isHost := r.HostConns[name]; isHost {
			continue
		}
		info, err := r.Client.DeviceInfo(name)
		if err == nil && info.Platform != "" {
			r.discoveredPlatform = info.Platform
			break
		}
	}

	return nil
}

// runScenarioSteps executes the steps of a scenario, appending results to result.
// When scenario.Repeat > 1, all steps are executed in a loop for the specified
// number of iterations. Execution stops on the first failed iteration.
func (r *Runner) runScenarioSteps(ctx context.Context, scenario *Scenario, opts RunOptions, result *ScenarioResult) {
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

// connectHostSSH establishes a plain SSH connection to a host device.
func connectHostSSH(c *client.Client, name string) (*ssh.Client, error) {
	profile, err := c.GetHostProfile(name)
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

	// Handle expect_failure: invert pass/fail logic
	if step.ExpectFailure {
		output.Result = applyExpectFailure(output.Result, step)
	}

	return output
}

// applyExpectFailure inverts the pass/fail result for steps with expect_failure: true.
// If the step failed/errored → passes (expected). If it passed → fails (unexpected success).
// When expect.contains is set, the error message must contain that substring.
func applyExpectFailure(result *StepResult, step *Step) *StepResult {
	switch result.Status {
	case StepStatusFailed, StepStatusError:
		// Step failed as expected — check error message if contains is specified
		if step.Expect != nil && step.Expect.Contains != "" {
			if !strings.Contains(result.Message, step.Expect.Contains) {
				result.Status = StepStatusFailed
				result.Message = fmt.Sprintf("expected failure containing %q, got: %s",
					step.Expect.Contains, result.Message)
				return result
			}
		}
		result.Status = StepStatusPassed
		result.Message = fmt.Sprintf("expected failure: %s", result.Message)
		// Also flip device-level details so aggregation works
		for i := range result.Details {
			if result.Details[i].Status == StepStatusFailed || result.Details[i].Status == StepStatusError {
				result.Details[i].Status = StepStatusPassed
			}
		}
	case StepStatusPassed:
		result.Status = StepStatusFailed
		result.Message = "expected failure but step succeeded"
	}
	return result
}

// progress calls fn with the ProgressReporter if one is set.
func (r *Runner) progress(fn func(ProgressReporter)) {
	if r.Progress != nil {
		fn(r.Progress)
	}
}

// allDeviceNames returns sorted names of all topology devices (including hosts).
func (r *Runner) allDeviceNames() []string {
	names, _ := r.Client.TopologyDeviceNames()
	return names
}

// resolveDevices resolves step.Devices to concrete device names.
func (r *Runner) resolveDevices(step *Step) []string {
	return step.Devices.Resolve(r.allDeviceNames())
}

// hasDataplane checks if the scenario platform supports dataplane forwarding.
func (r *Runner) hasDataplane() bool {
	platformName := r.resolvePlatform()
	if platformName == "" {
		return false
	}
	p, err := r.Client.ShowPlatform(platformName)
	if err != nil {
		return false
	}
	return p.Dataplane != ""
}

// resolvePlatform returns the platform name from CLI override, scenario YAML,
// or device discovery (in that priority order).
func (r *Runner) resolvePlatform() string {
	if r.opts.Platform != "" {
		return r.opts.Platform
	}
	if r.scenario != nil && r.scenario.Platform != "" {
		return r.scenario.Platform
	}
	return r.discoveredPlatform
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
func (r *Runner) checkPlatformFeatures(sc *Scenario, deployedPlatform, scenarioPlatform string) string {
	if len(sc.RequiresFeatures) == 0 {
		return "" // No feature requirements
	}

	if r.Client == nil {
		return "" // Cannot check features without server connection (proceed and let operations fail)
	}

	// Use deployed platform, then per-scenario, then discovered
	platformName := deployedPlatform
	if platformName == "" {
		platformName = scenarioPlatform
	}
	if platformName == "" {
		platformName = r.discoveredPlatform
	}

	var unsupported []string
	for _, feature := range sc.RequiresFeatures {
		supported, err := r.Client.PlatformSupportsFeature(platformName, feature)
		if err != nil || !supported {
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
