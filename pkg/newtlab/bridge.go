package newtlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// pushInterval is how often newtlink posts a fresh BridgeStats snapshot
// to newtlab-server. The CLI's `--monitor` mode refreshes every 2s, so
// 5s keeps the displayed values within at most one refresh cycle of
// truth while keeping the request rate proportional to lab size
// (one POST per worker host per cycle).
const pushInterval = 5 * time.Second

// BridgeConfig is the serialized link configuration read by the bridge
// process. newtlink no longer listens on any port — it pushes
// BridgeStats snapshots to newtlab-server. LabName + WorkerHost
// identify the (lab, host) slot in the server-side store;
// OrchestratorURL is the base URL of newtlab-server (or newt-server's
// composed listener) reachable from this worker.
type BridgeConfig struct {
	Links []BridgeLink `json:"links"`

	OrchestratorURL string `json:"orchestrator_url"`
	LabName         string `json:"lab_name"`
	WorkerHost      string `json:"worker_host"` // "" for the local worker
	// Token is the per-lab telemetry credential (LabState.TelemetryToken).
	// newtlink presents it as `Authorization: Bearer <token>` on every push so
	// an --enforce-authorization server accepts it without a user session key.
	// Empty ⇒ no header sent (server not gating telemetry).
	Token string `json:"token,omitempty"`
}

// BridgeLink holds the bind/port config for one link's bridge worker.
type BridgeLink struct {
	APort int    `json:"a_port"`
	ZPort int    `json:"z_port"`
	ABind string `json:"a_bind"`
	ZBind string `json:"z_bind"`
	A     string `json:"a"` // display label, e.g. "spine1:Ethernet0"
	Z     string `json:"z"` // display label, e.g. "leaf1:Ethernet0"
}

// LinkStats holds telemetry counters for a single bridge link.
type LinkStats struct {
	A         string `json:"a"`
	Z         string `json:"z"`
	APort     int    `json:"a_port"`
	ZPort     int    `json:"z_port"`
	AToZBytes int64  `json:"a_to_z_bytes"`
	ZToABytes int64  `json:"z_to_a_bytes"`
	Sessions  int64  `json:"sessions"`
	Connected bool   `json:"connected"`
}

// BridgeStats is the telemetry snapshot newtlink pushes to newtlab-server.
type BridgeStats struct {
	Links []LinkStats `json:"links"`
}

// BridgePushParams names the (lab, host, orchestrator URL) tuple needed
// for newtlink to deliver its stats. Threaded into the bridge config
// alongside the link list.
type BridgePushParams struct {
	OrchestratorURL string
	LabName         string
	WorkerHost      string // "" for the local worker
	Token           string // per-lab telemetry token (LabState.TelemetryToken)
}

// WriteBridgeConfig serializes link config to bridge.json in the state dir.
func WriteBridgeConfig(stateDir string, links []*LinkConfig, push BridgePushParams) error {
	cfg := buildBridgeConfig(links, push)
	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("newtlab: marshal bridge config: %w", err)
	}
	return os.WriteFile(filepath.Join(stateDir, "bridge.json"), data, 0644)
}

// injectBridgeToken reads bridge.json at path, sets its Token, and writes it
// back in the same indented form WriteBridgeConfig uses. Resync uses it to give
// a running worker's newtlink a credential without rebuilding the link config
// (the config is already on disk from deploy).
func injectBridgeToken(path, token string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("newtlab: read bridge config %s: %w", path, err)
	}
	var cfg BridgeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("newtlab: parse bridge config %s: %w", path, err)
	}
	cfg.Token = token
	out, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("newtlab: marshal bridge config: %w", err)
	}
	if err := os.WriteFile(path, out, 0644); err != nil {
		return fmt.Errorf("newtlab: write bridge config %s: %w", path, err)
	}
	return nil
}

// reloadBridgeToken re-reads just the Token from a bridge.json file. Used by
// newtlink's SIGHUP handler so a resync can rotate the telemetry credential
// without restarting the bridge workers (and thus without dropping the VM link
// connections newtlink relays).
func reloadBridgeToken(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", err
	}
	var cfg BridgeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return "", err
	}
	return cfg.Token, nil
}

// buildBridgeConfig creates a BridgeConfig from links and push parameters.
func buildBridgeConfig(links []*LinkConfig, push BridgePushParams) BridgeConfig {
	cfg := BridgeConfig{
		Links:           make([]BridgeLink, len(links)),
		OrchestratorURL: push.OrchestratorURL,
		LabName:         push.LabName,
		WorkerHost:      push.WorkerHost,
		Token:           push.Token,
	}
	for i, lc := range links {
		cfg.Links[i] = BridgeLink{
			APort: lc.APort,
			ZPort: lc.ZPort,
			ABind: lc.ABind,
			ZBind: lc.ZBind,
			A:     lc.A.Device + ":" + lc.A.Interface,
			Z:     lc.Z.Device + ":" + lc.Z.Interface,
		}
	}
	return cfg
}

// RunBridgeFromFile reads a bridge config JSON file, runs bridge workers,
// and pushes BridgeStats snapshots to newtlab-server every pushInterval
// until the process receives SIGTERM/SIGINT. The stateDir for the pid
// file is derived from the config file's directory.
//
// One first push is sent synchronously after the workers start so a
// subsequent CLI status call returns telemetry without waiting a full
// interval. A final push fires on shutdown so the operator sees a
// terminal snapshot rather than stale counters.
func RunBridgeFromFile(configPath string) error {
	stateDir := filepath.Dir(configPath)
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("newtlab: read bridge config: %w", err)
	}

	var cfg BridgeConfig
	if err := json.Unmarshal(data, &cfg); err != nil {
		return fmt.Errorf("newtlab: parse bridge config: %w", err)
	}
	if cfg.OrchestratorURL == "" {
		return fmt.Errorf("newtlab: bridge config missing orchestrator_url")
	}
	if cfg.LabName == "" {
		return fmt.Errorf("newtlab: bridge config missing lab_name")
	}

	links := make([]*LinkConfig, len(cfg.Links))
	for i, bl := range cfg.Links {
		aDevice, aIface, err := splitLinkEndpoint(bl.A)
		if err != nil {
			return fmt.Errorf("newtlab: bridge link %d A-side: %w", i, err)
		}
		zDevice, zIface, err := splitLinkEndpoint(bl.Z)
		if err != nil {
			return fmt.Errorf("newtlab: bridge link %d Z-side: %w", i, err)
		}
		links[i] = &LinkConfig{
			A:     LinkEndpoint{Device: aDevice, Interface: aIface},
			Z:     LinkEndpoint{Device: zDevice, Interface: zIface},
			APort: bl.APort,
			ZPort: bl.ZPort,
			ABind: bl.ABind,
			ZBind: bl.ZBind,
		}
	}

	bridge, err := StartBridgeWorkers(links)
	if err != nil {
		return err
	}

	// Signal readiness by writing pid to bridge.pid
	pidFile := filepath.Join(stateDir, "bridge.pid")
	if err := os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644); err != nil {
		fmt.Fprintf(os.Stderr, "warning: write bridge pid file: %v\n", err)
	}

	// Stats push loop. Each tick posts the current Bridge.Stats()
	// snapshot to newtlab-server. Push errors log and continue —
	// they're not fatal because the next tick retries. Inlined as a
	// minimal HTTP POST rather than using pkg/newtlab/client because
	// client imports pkg/newtlab (cycle); newtlink doesn't need the
	// client's envelope-decoding machinery — it just fires-and-forgets.
	pushURL := pushURLFor(cfg.OrchestratorURL, cfg.LabName, cfg.WorkerHost)
	pushHTTPClient := &http.Client{Timeout: pushInterval}
	pushCtx, cancelPush := context.WithCancel(context.Background())
	defer cancelPush()

	// The push token can be hot-reloaded on SIGHUP (resync injects a fresh token
	// into bridge.json and signals us) WITHOUT restarting the bridge workers —
	// so the VM link connections newtlink relays stay up. Guard it: the push
	// loop reads it concurrently with the SIGHUP handler that rewrites it.
	var tokMu sync.Mutex
	curToken := cfg.Token
	pushToken := func() string { tokMu.Lock(); defer tokMu.Unlock(); return curToken }

	push := func(ctx context.Context) {
		if err := pushBridgeStats(ctx, pushHTTPClient, pushURL, pushToken(), bridge.Stats()); err != nil {
			fmt.Fprintf(os.Stderr, "newtlink: push stats to %s: %v\n", pushURL, err)
		}
	}

	// First push happens before the loop so a CLI status call right
	// after deploy doesn't have to wait pushInterval to see data.
	push(pushCtx)

	pushDone := make(chan struct{})
	go func() {
		defer close(pushDone)
		ticker := time.NewTicker(pushInterval)
		defer ticker.Stop()
		for {
			select {
			case <-pushCtx.Done():
				return
			case <-ticker.C:
				push(pushCtx)
			}
		}
	}()

	// SIGHUP hot-reloads the telemetry token (resync path); SIGTERM/SIGINT shut
	// down. On SIGHUP we re-read just the token from bridge.json and keep the
	// bridge workers running, so the VM connections newtlink relays are never
	// dropped — the whole point of resync-without-redeploy.
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT, syscall.SIGHUP)
	for {
		sig := <-sigCh
		if sig != syscall.SIGHUP {
			break // SIGTERM / SIGINT → shut down
		}
		tok, err := reloadBridgeToken(configPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "newtlink: SIGHUP token reload: %v\n", err)
			continue
		}
		tokMu.Lock()
		curToken = tok
		tokMu.Unlock()
		fmt.Fprintln(os.Stderr, "newtlink: reloaded telemetry token on SIGHUP")
		push(pushCtx) // push immediately so authenticated stats land without waiting a tick
	}

	cancelPush()
	<-pushDone
	// Final terminal push so the operator's last status view shows
	// the bridge's true stopped state rather than the second-to-last
	// snapshot from the loop.
	finalCtx, finalCancel := context.WithTimeout(context.Background(), pushInterval)
	defer finalCancel()
	push(finalCtx)

	bridge.Stop()
	return nil
}

// pushURLFor builds the full POST target. workerHost == "" is encoded
// as the literal "local" path segment (URL paths can't carry empty
// segments) — handlePushBridgeStats maps "local" back to "" before
// storing.
func pushURLFor(baseURL, labName, workerHost string) string {
	segment := workerHost
	if segment == "" {
		segment = "local"
	}
	return strings.TrimRight(baseURL, "/") +
		"/newtlab/v1/labs/" + url.PathEscape(labName) +
		"/bridges/" + url.PathEscape(segment) + "/stats"
}

// pushBridgeStats POSTs the snapshot to newtlab-server. Reads and
// discards the body so the underlying connection can be reused. When token
// is non-empty it is sent as `Authorization: Bearer <token>` — the per-lab
// telemetry credential an --enforce-authorization server validates against
// the lab's stored TelemetryToken (handlePushBridgeStats).
func pushBridgeStats(ctx context.Context, c *http.Client, url, token string, stats BridgeStats) error {
	body, err := json.Marshal(stats)
	if err != nil {
		return fmt.Errorf("marshal stats: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	resp, err := c.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	io.Copy(io.Discard, resp.Body)
	if resp.StatusCode >= 400 {
		return fmt.Errorf("server returned %s", resp.Status)
	}
	return nil
}

// startBridgeProcess spawns a newtlink bridge process locally.
func startBridgeProcess(labName, stateDir string) (int, error) {
	configPath := filepath.Join(stateDir, "bridge.json")

	p := findSiblingBinary("newtlink")
	if p == "newtlink" {
		return 0, fmt.Errorf("newtlink binary not found next to %s; run 'make build'", os.Args[0])
	}
	cmd := exec.Command(p, configPath)

	logPath := filepath.Join(stateDir, "logs", "bridge.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return 0, fmt.Errorf("create bridge log: %w", err)
	}

	cmd.Stdout = logFile
	cmd.Stderr = logFile
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}

	if err := cmd.Start(); err != nil {
		logFile.Close()
		return 0, fmt.Errorf("start bridge process: %w", err)
	}

	pid := cmd.Process.Pid

	// Release the process so it runs independently
	go func() {
		cmd.Wait()
		logFile.Close()
	}()

	return pid, nil
}

// startBridgeProcessRemote starts a bridge process on a remote host via SSH.
// It uploads the newtlink binary if needed, copies the bridge config JSON,
// then starts newtlink via nohup. Returns the remote PID.
func startBridgeProcessRemote(labName, hostIP string, configJSON []byte) (int, error) {
	newtlinkPath, err := uploadNewtlink(hostIP)
	if err != nil {
		return 0, fmt.Errorf("newtlab: upload newtlink to %s: %w", hostIP, err)
	}

	rawStateDir := fmt.Sprintf("~/.newtlab/labs/%s", labName)
	stateDir := shellQuote(rawStateDir)
	configPath := shellQuote(rawStateDir + "/bridge.json")

	mkdirCmd := fmt.Sprintf("mkdir -p %s/logs && cat > %s", stateDir, configPath)
	cmd := sshCommand(hostIP, mkdirCmd)
	cmd.Stdin = bytes.NewReader(configJSON)
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("newtlab: setup remote bridge dir on %s: %w\n%s", hostIP, err, out)
	}

	startCmd := fmt.Sprintf("nohup %s %s > %s/logs/bridge.log 2>&1 & echo $!",
		shellQuote(newtlinkPath), configPath, stateDir)
	cmd = sshCommand(hostIP, startCmd)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		return 0, fmt.Errorf("newtlab: start remote bridge on %s: %w", hostIP, err)
	}

	pidStr := strings.TrimSpace(stdout.String())
	pid, err := strconv.Atoi(pidStr)
	if err != nil {
		return 0, fmt.Errorf("newtlab: parse remote bridge PID on %s: %w (output: %q)", hostIP, err, pidStr)
	}

	return pid, nil
}

// stopBridgeProcessRemote kills a bridge process on a remote host via SSH.
func stopBridgeProcessRemote(pid int, hostIP string) error {
	return StopNodeRemote(pid, hostIP)
}

// waitForPort polls until a TCP port is accepting connections.
func waitForPort(host string, port int, timeout time.Duration) error {
	addr := fmt.Sprintf("%s:%d", host, port)
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 1*time.Second)
		if err == nil {
			conn.Close()
			return nil
		}
		time.Sleep(100 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}
