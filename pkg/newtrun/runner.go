package newtrun

import (
	"context"
	"crypto/tls"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"time"

	"golang.org/x/crypto/ssh"

	"github.com/aldrin-isaac/newtron/pkg/newtron/client"
)

// Runner is the top-level newtrun orchestrator.
type Runner struct {
	// SuiteDir is the directory of the suite this runner executes —
	// holds suite.yaml plus the scenario *.yaml files. The same
	// concept the HTTP handler (handleStartRun) and ResolveSuiteDir
	// already call "suite dir"; §13 (Same Concept = Same Name).
	SuiteDir       string
	// NetworksBase is the root under which sibling suites live
	// (<base>/<topology>/suites/<name>/); required for run-suite step
	// composition (issue #27) so a step in this suite can invoke a
	// sibling suite without the parent encoding its sibling's
	// per-topology location.
	NetworksBase string
	ServerURL      string         // newtron-server HTTP address
	NetworkID      string         // network identifier for server operations
	// NewtronClientTLS is the TLS config the runner uses when
	// constructing its outbound newtron HTTP client at connectToServer
	// time (auth-design.md L2a). nil keeps the client on plain HTTP —
	// the L2a disabled state. Populated by cmd/newt-server from its
	// own --tls-cert/--tls-key/--tls-ca flags; an mTLS-enforced
	// newtron-server then sees the runner's cert CN as the caller
	// identity (typically configured as a super_user in network.json
	// so the runner has the broad authority test scenarios need).
	NewtronClientTLS *tls.Config

	// OperatorBearer is the L2c session key extracted from the
	// Authorization: Bearer header of the HTTP request that
	// triggered this run (auth-design.md §L2c). The runner forwards
	// it as the default credential on every outbound newtron call,
	// so the operator's identity — verified once by newt-server's
	// outer auth middleware on the inbound request — flows through
	// the runner unchanged. Empty when the run was triggered without
	// a Bearer (cmd/newt-server without --auth-pam-service) — in
	// that case outbound newtron calls carry no Authorization header,
	// which is fine against a newtron engine that doesn't enforce
	// identity. Per-scenario `as: <user>` overrides this default for
	// every call the scenario makes via the UserSessions map.
	OperatorBearer string

	// UserSessions maps a username to the Bearer session key
	// supplied by the operator's CLI at run-start time. Used by
	// per-scenario `as: <user>` impersonation in scenarios that
	// test authorization-by-identity (mallory denied, alice
	// allowed). Populated by the newtrun-server's StartRun handler
	// from StartRunRequest.UserSessions; the CLI loads every
	// cached session from ~/.newtron/sessions/ before submitting
	// (over-supplying is harmless). Empty when the operator has no
	// cached sessions.
	UserSessions map[string]string

	Client       *client.Client // HTTP client for all SONiC operations
	NewtlabURL   string         // newtlab-server HTTP address (deploy/destroy/status via HTTP, not in-process)
	NewtlabClient LabClient    // newtlab HTTP client (satisfied by *pkg/newtlab/client.Client); injected for tests
	HostConns    map[string]*ssh.Client // host device name → SSH client
	Progress     ProgressReporter

	// Populated by connectToServer from the server's registered network.
	Network string // network name (from server)
	Dir  string // network directory (from server)

	// Populated by Run from the loaded suite.yaml. resolvedIterations
	// is the cross-product of suite-level targets after per-run
	// override merging — one entry for embedded-target suites (the
	// single nil binding), one entry per dimension combination for
	// parameterized suites. resolvedParameters carries the merged
	// parameter values (suite defaults + run-time overrides, typed
	// per ParameterSpec.Coerce). Both views are derived from the
	// loaded suite once at Run startup; runScenarioSteps reads them
	// directly without re-querying the suite per scenario.
	suite               *Suite
	resolvedIterations  []map[string]string
	resolvedParameters  map[string]any

	discoveredPlatform string // platform discovered from connected devices

	// captured is the response-capture map scoped to the current
	// scenario iteration. runScenarioSteps resets it at the start
	// of each iteration; newtronExecutor populates it from
	// step.Capture after every successful single-call newtron step;
	// ExpandStep reads it to resolve {{captured.NAME}} references.
	//
	// The map is iteration-scoped (per parameterized iteration when
	// applicable) rather than scenario- or suite-scoped because the
	// dependency graph in requires:/after: has parallel branches:
	// sharing a mutable map across them would create ambiguity about
	// who wrote which value. A scenario that needs cross-step carry
	// runs steps in sequence within itself.
	captured map[string]any

	opts RunOptions

	// scenario is the currently-executing scenario, set by the
	// iterateScenarios loop at the start of each scenario iteration
	// and read by newtronExecutor.doCall for scenario.As. Sequential
	// only — scenarios run one at a time per runner, so no goroutine
	// reader races the writer.
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

	// Targets overrides per-dimension entries of the suite's targets
	// block at run time. Keys must match dimensions declared in
	// suite.yaml; omitted keys inherit the suite default.
	Targets map[string][]string

	// Parameters overrides the suite's parameter defaults at run time.
	// Keys must match parameters declared in suite.yaml; values are
	// validated against each parameter's spec (type, constraints).
	Parameters map[string]any

	// Lifecycle fields (set by `start` command, not by `run`)
	Suite     string                // suite name for state tracking; empty disables lifecycle
	Resume    bool                  // true when resuming a paused run
	Completed map[string]StepStatus // scenario → status from previous run (resume)
}

// NewRunner creates a new test runner bound to the given suite
// directory (which holds suite.yaml + the scenario YAMLs).
func NewRunner(suiteDir string) *Runner {
	return &Runner{
		SuiteDir: suiteDir,
	}
}

// Run executes one or all scenarios and returns results.
// The server determines the topology — the suite declares its target
// topology in suite.yaml; mismatches fail immediately.
//
// The supplied context cancels the run between scenario boundaries. SIGINT
// handling is layered on top so an interactive CLI run still responds to
// Ctrl-C even when the caller supplied context.Background(). Server-side
// runs use this to cancel in-flight runners when the server shuts down or
// when an operator POSTs to the stop endpoint.
func (r *Runner) Run(ctx context.Context, opts RunOptions) (results []*ScenarioResult, err error) {
	if opts.Scenario == "" && opts.Target == "" && !opts.All {
		return nil, fmt.Errorf("specify --scenario <name>, --target <name>, or --all")
	}

	// Load the suite: suite.yaml + every scenario file in the dir.
	// LoadSuite validates all template references against suite-level
	// declarations and rejects scenarios that set topology/platform.
	suite, err := LoadSuite(r.SuiteDir)
	if err != nil {
		return nil, err
	}
	if len(suite.Scenarios) == 0 {
		return nil, fmt.Errorf("no scenarios found in %s", r.SuiteDir)
	}
	r.suite = suite

	// Resolve effective targets and parameters from suite defaults +
	// per-run overrides. Failures here are 400-class — bad request.
	effTargets, err := suite.EffectiveTargets(opts.Targets)
	if err != nil {
		return nil, err
	}
	effParams, err := suite.EffectiveParameters(opts.Parameters)
	if err != nil {
		return nil, err
	}
	// EffectiveTargets returns the suite defaults too; we need a temp
	// Suite snapshot to drive TargetIterations against the resolved
	// targets without mutating the loaded suite.
	resolved := *suite
	resolved.Targets = effTargets
	r.resolvedIterations = resolved.TargetIterations()
	r.resolvedParameters = effParams

	// Filter scenarios by --scenario / --target / --all.
	var scenarios []*Scenario
	switch {
	case opts.All:
		scenarios = suite.Scenarios
	case opts.Target != "":
		chain, err := ComputeTargetChain(suite.Scenarios, opts.Target)
		if err != nil {
			return nil, err
		}
		scenarios = chain
	default:
		scenarios = nil
		for _, sc := range suite.Scenarios {
			if sc.Name == opts.Scenario {
				scenarios = []*Scenario{sc}
				break
			}
		}
		if len(scenarios) == 0 {
			return nil, fmt.Errorf("scenario %q not found in %s", opts.Scenario, r.SuiteDir)
		}
	}

	// Connect to server to learn topology
	fmt.Fprintf(os.Stderr, "newtrun: connecting to server %s...\n", r.ServerURL)
	if err := r.connectToServer(); err != nil {
		return nil, err
	}
	fmt.Fprintf(os.Stderr, "newtrun: server has topology %q (%d nodes)\n", r.Network, len(r.allDeviceNames()))

	// Guard: the suite's topology must match the server's.
	if suite.Network != "" && suite.Network != r.Network {
		return nil, fmt.Errorf("suite %q requires topology %q but server has %q loaded",
			suite.Name, suite.Network, r.Network)
	}

	r.progress(func(p ProgressReporter) { p.SuiteStart(suite.Network, suite.Platform, scenarios) })
	suiteStart := time.Now()

	// Emit SuiteEnd from every path that returns after SuiteStart. The
	// CLI client (cmd_start.go) cancels its SSE stream on the SuiteEnd
	// event — when an early-return path (deploy failure, connect
	// failure) skipped the SuiteEnd emission, the SSE stream stayed
	// open forever and `newtrun start` hung indefinitely after the
	// suite already finalized state.json. The defer-with-named-returns
	// pattern guarantees every post-SuiteStart exit produces SuiteEnd
	// with the right (results, err) tuple.
	defer func() {
		if len(scenarios) > 0 {
			status := SuiteStatusFromOutcome(err, results)
			r.progress(func(p ProgressReporter) { p.SuiteEnd(results, status, time.Since(suiteStart)) })
		}
	}()

	// Deploy topology (unless --no-deploy)
	if !opts.NoDeploy {
		fmt.Fprintf(os.Stderr, "newtrun: deploying topology %s...\n", r.Network)
		cleanup, deployErr := r.deployTopology(ctx, r.Dir, opts)
		if deployErr != nil {
			for _, sc := range scenarios {
				results = append(results, &ScenarioResult{
					Name:        sc.Name,
					Network:    r.Network,
					Platform:    sc.Platform,
					Status:      StepStatusError,
					DeployError: &InfraError{Op: "deploy", Err: deployErr},
				})
			}
			return results, nil
		}
		fmt.Fprintf(os.Stderr, "newtrun: topology ready\n")
		if cleanup != nil {
			defer cleanup()
		}
	}

	// SIGINT handling layered on top of the caller's context.
	ctx, cancel := signal.NotifyContext(ctx, os.Interrupt)
	defer cancel()

	// Connect host devices (skip in no-deploy mode — no physical devices to connect to).
	if opts.NoDeploy {
		r.HostConns = make(map[string]*ssh.Client)
		fmt.Fprintf(os.Stderr, "newtrun: no-deploy mode — skipping device connections\n")
	} else {
		fmt.Fprintf(os.Stderr, "newtrun: connecting to devices...\n")
		if connErr := r.connectDevices(); connErr != nil {
			for _, sc := range scenarios {
				results = append(results, &ScenarioResult{
					Name:        sc.Name,
					Network:    r.Network,
					Platform:    sc.Platform,
					Status:      StepStatusError,
					DeployError: connErr,
				})
			}
			return results, nil
		}
	}

	// Resolve platform for capability checks
	deployedPlatform := opts.Platform
	if deployedPlatform == "" {
		deployedPlatform = suite.Platform
	}
	if deployedPlatform == "" {
		deployedPlatform = r.discoveredPlatform
	}

	// Run all scenarios. Assigns the named-return results and err so
	// the deferred SuiteEnd emission (registered just after SuiteStart)
	// computes its status from the final tuple.
	results, err = r.iterateScenarios(ctx, scenarios, opts, deployedPlatform, func(ctx context.Context, sc *Scenario, platform string) (*ScenarioResult, error) {
		r.opts = RunOptions{
			Platform: platform,
			NoDeploy: true,
			Keep:     true,
			Verbose:  opts.Verbose,
		}
		r.scenario = sc

		result := &ScenarioResult{
			Name:     sc.Name,
			Network: r.Network,
			Platform: platform,
		}
		start := time.Now()
		r.runScenarioSteps(ctx, sc, r.opts, result)
		result.Duration = time.Since(start)
		return result, nil
	})

	return results, err
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
		// Server-shutdown / external cancellation check. When the
		// run's context is canceled (server SIGTERM cancelling the
		// registry, or an admin call into the future), stop iterating
		// instead of running the remainder as guaranteed failures.
		// Without this, every remaining scenario emits a FAIL event
		// (its subprocess fails because ctx is dead), the SSE wire
		// fills with phantom failures, and the operator sees
		// "N failed" that looks indistinguishable from a real bad
		// suite. Returning ctx.Err() here routes through
		// SuiteStatusFromOutcome to the aborted status.
		if err := ctx.Err(); err != nil {
			return results, err
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
					Network:   r.Network,
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
				Network:   r.Network,
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
				Network:   r.Network,
				Platform:   platform,
				Status:     StepStatusSkipped,
				SkipReason: reason,
			}
			results = append(results, result)
			scenarioStatus[sc.Name] = StepStatusSkipped
			r.progress(func(p ProgressReporter) { p.ScenarioEnd(result, i, len(scenarios)) })
			continue
		}

		// Parameter requirements check: skip when a required suite
		// parameter is unset or empty. The mechanism is "operator
		// opted in" — a scenario that needs runtime credentials
		// (typically secrets the suite cannot ship in the repo)
		// names them here and skips quietly when the operator did
		// not supply them at start time.
		if reason := r.checkRequiredParams(sc); reason != "" {
			result := &ScenarioResult{
				Name:       sc.Name,
				Network:   r.Network,
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

// deployTopology deploys the lab topology by calling newtlab-server.
// Lifecycle mode (opts.Suite set, e.g. `newtrun start ...`) uses
// EnsureTopology — reuse an already-running lab, redeploy otherwise.
// Standalone mode (legacy direct Run.Run path) uses DeployTopology
// and registers a cleanup func that calls DestroyTopology unless
// opts.Keep is set. Per §27 (Single Owner) every touch of LabState
// goes through newtlab-server's HTTP client; no in-process
// newtlab.NewLab.
//
// dir registers the network with newtron-server so newtlab can
// query specs from there during deploy.
func (r *Runner) deployTopology(ctx context.Context, dir string, opts RunOptions) (cleanup func(), err error) {
	if dir != "" {
		if regErr := r.Client.RegisterNetwork(dir); regErr != nil {
			fmt.Fprintf(os.Stderr, "newtrun: register %s with newtron: %v (continuing)\n", dir, regErr)
		}
	}
	if r.NewtlabClient == nil {
		return nil, fmt.Errorf("newtrun: no newtlab client configured (set Runner.NewtlabClient or use --newtlab-server)")
	}
	if opts.Suite != "" {
		if err := EnsureTopology(ctx, r.NewtlabClient, r.Network); err != nil {
			return nil, err
		}
		return nil, nil // lifecycle mode: stop command handles teardown
	}
	if err := DeployTopology(ctx, r.NewtlabClient, r.Network); err != nil {
		return nil, err
	}
	if !opts.Keep {
		topo := r.Network
		client := r.NewtlabClient
		return func() { _ = DestroyTopology(context.Background(), client, topo) }, nil
	}
	return nil, nil
}

// connectToServer queries the server for the registered network's info.
// Populates r.Network, r.Dir, and creates the HTTP client.
//
// The HTTP client honors r.NewtronClientTLS so the runner can
// authenticate against an mTLS-enforced newtron-server via its peer
// cert CN (auth-design.md L2a). When NewtronClientTLS is nil, the
// client falls back to plain HTTP — the L2a disabled state.
//
// When r.OperatorBearer is set (typical: a session key extracted
// from the incoming /newtrun/v1/runs request's Authorization
// header), the client attaches Authorization: Bearer <key> on
// every outbound newtron call that doesn't already carry one. The
// operator's identity, verified once by newt-server's outer auth
// middleware on the inbound request, flows through the runner
// unchanged. Per-scenario `as: <user>` overrides the default for
// every call the scenario makes via the UserSessions map (see
// steps_newtron.go).
func (r *Runner) connectToServer() error {
	opts := []client.Option{
		client.WithTLS(r.NewtronClientTLS),
		client.WithBearer(r.OperatorBearer),
	}
	r.Client = client.New(r.ServerURL, r.NetworkID, opts...)

	info, err := r.Client.GetNetworkInfo()
	if err != nil {
		return &InfraError{Op: "connect", Err: fmt.Errorf("querying server: %w (is the network registered?)", err)}
	}

	r.Dir = info.Dir
	// NetworkInfo.Topology is the basename of dir's parent (e.g.
	// "1node-vs"); under the new vocabulary this IS the network's own
	// name, and NetworkInfo's field reads as a redundancy with ID
	// in the simple case. The wire field is kept as-is for now
	// (separate wire-shape concern; tracked in #200).
	r.Network = info.Topology

	if r.Network == "" {
		r.Network = "(unknown)"
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

// runScenarioSteps executes the steps of a scenario, appending results
// to result.
//
// Three nested loops, outer to inner:
//
//   - Repeat: when scenario.Repeat > 1, every step runs that many
//     times. Repeat is fail-fast — the first failed repeat-iteration
//     records FailedIteration and stops further repeats.
//   - Target iteration: for parameterized scenarios, the runner
//     enumerates the cross-product of suite-level Targets and binds
//     {{target.X}} / {{param.X}} per iteration. Target iterations are
//     continue-on-failure — one failing binding does not skip the
//     remaining bindings, so a production rollout sees every failing
//     target. Embedded-target scenarios collapse this loop to one
//     nil-binding pass so the step list runs once.
//   - Steps: the original step list. Steps are fail-fast within one
//     target iteration: a failure stops the rest of that iteration's
//     steps, then the outer loop moves to the next binding
//     (parameterized) or stops (embedded-target).
func (r *Runner) runScenarioSteps(ctx context.Context, scenario *Scenario, opts RunOptions, result *ScenarioResult) {
	repeat := scenario.Repeat
	if repeat <= 1 {
		repeat = 1
	}
	result.Repeat = scenario.Repeat

	// A scenario is parameterized when any of its steps references
	// {{target.X}} or {{param.X}}. Parameterized scenarios iterate
	// the suite's resolved target cross-product; embedded-target
	// scenarios run once with a nil binding. Both shapes coexist in
	// one suite (a parameterized rollout alongside embedded-target
	// provision/verify scenarios) so the choice is per-scenario, not
	// per-suite.
	isParameterized := ScenarioIsParameterized(scenario)
	var iterations []map[string]string
	var effectiveParams map[string]any
	if isParameterized {
		iterations = r.resolvedIterations
		effectiveParams = r.resolvedParameters
	}
	if iterations == nil {
		iterations = []map[string]string{nil}
	}

	for repeatIter := 1; repeatIter <= repeat; repeatIter++ {
		anyIterFailed := false

		for _, binding := range iterations {
			iterFailed := false

			// Fresh per-iteration captured map. Same-iteration
			// step order in scenario.Steps fixes write-then-read so a
			// {{captured.NAME}} reference in step N can see what step
			// N-1 captured.
			r.captured = map[string]any{}

			for i, step := range scenario.Steps {
				stepToRun := step
				var expandErr error
				if isParameterized || len(r.captured) > 0 || stepReferencesCaptured(step) {
					stepToRun, expandErr = ExpandStep(step, binding, effectiveParams, r.captured)
				}
				if expandErr != nil {
					sr := StepResult{
						Name:          step.Name,
						Action:        step.Action,
						Status:        StepStatusError,
						Message:       fmt.Sprintf("template expansion: %v", expandErr),
						TargetBinding: binding,
					}
					if repeat > 1 {
						sr.Iteration = repeatIter
					}
					result.Steps = append(result.Steps, sr)
					iterFailed = true
					break
				}

				stepCopy := stepToRun
				r.progress(func(p ProgressReporter) { p.StepStart(scenario.Name, &stepCopy, i, len(scenario.Steps)) })

				output := r.executeStep(ctx, &stepToRun, i, len(scenario.Steps), opts)

				sr := *output.Result
				if repeat > 1 {
					sr.Iteration = repeatIter
				}
				sr.TargetBinding = binding
				result.Steps = append(result.Steps, sr)

				srCopy := sr
				r.progress(func(p ProgressReporter) { p.StepEnd(scenario.Name, &srCopy, i, len(scenario.Steps)) })

				if output.Result.Status == StepStatusFailed || output.Result.Status == StepStatusError {
					iterFailed = true
					break
				}
			}

			if iterFailed {
				anyIterFailed = true
				// Embedded-target scenarios have only one (nil) binding,
				// so this break is moot. Parameterized scenarios
				// continue to the next binding so the operator sees
				// every failing target in one run before the repeat
				// pass terminates.
				if !isParameterized {
					break
				}
			}
		}

		// Repeat is fail-fast regardless of scenario shape: if any
		// iteration in this repeat pass failed, record the index
		// (when Repeat > 1 it's a useful provenance marker for soak
		// runs) and stop further repeats.
		if anyIterFailed {
			if repeat > 1 {
				result.FailedIteration = repeatIter
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

// checkRequiredParams reports a skip reason when a scenario's
// requires_params: lists any name that resolves to an empty / zero
// value in the runner's effective parameter map. Empty list (the
// default) returns "" — no requirement, no skip.
//
// Semantics of "missing": the parameter is absent from the effective
// map, OR its value is the zero value of its declared type (empty
// string, 0, false). The intent is "operator opted in", so a
// parameter the operator left at its default-empty value is treated
// the same as one they omitted entirely.
func (r *Runner) checkRequiredParams(sc *Scenario) string {
	if len(sc.RequiresParams) == 0 {
		return ""
	}
	for _, name := range sc.RequiresParams {
		v, ok := r.resolvedParameters[name]
		if !ok {
			return fmt.Sprintf("requires parameter %q (not set by operator)", name)
		}
		switch t := v.(type) {
		case string:
			if t == "" {
				return fmt.Sprintf("requires parameter %q (operator left it empty)", name)
			}
		case int:
			if t == 0 {
				return fmt.Sprintf("requires parameter %q (operator left it at 0)", name)
			}
		case int64:
			if t == 0 {
				return fmt.Sprintf("requires parameter %q (operator left it at 0)", name)
			}
		case float64:
			if t == 0 {
				return fmt.Sprintf("requires parameter %q (operator left it at 0)", name)
			}
		case bool:
			if !t {
				return fmt.Sprintf("requires parameter %q (operator left it false)", name)
			}
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

