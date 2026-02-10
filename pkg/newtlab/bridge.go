package newtlab

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"
)

// BridgeConfig is the serialized link configuration read by the bridge process.
type BridgeConfig struct {
	Links     []BridgeLink `json:"links"`
	StatsAddr string       `json:"stats_addr,omitempty"` // TCP listen addr for remote stats queries
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

// BridgeStats is the telemetry snapshot returned over the Unix socket.
type BridgeStats struct {
	Links []LinkStats `json:"links"`
}

// WriteBridgeConfig serializes link config to bridge.json in the state dir.
func WriteBridgeConfig(stateDir string, links []*LinkConfig, statsAddr string) error {
	cfg := buildBridgeConfig(links, statsAddr)
	data, err := json.MarshalIndent(cfg, "", "    ")
	if err != nil {
		return fmt.Errorf("newtlab: marshal bridge config: %w", err)
	}
	return os.WriteFile(filepath.Join(stateDir, "bridge.json"), data, 0644)
}

// buildBridgeConfig creates a BridgeConfig from links and a stats address.
func buildBridgeConfig(links []*LinkConfig, statsAddr string) BridgeConfig {
	cfg := BridgeConfig{
		Links:     make([]BridgeLink, len(links)),
		StatsAddr: statsAddr,
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

// RunBridge reads bridge.json from the lab's state dir and runs bridge workers
// until the process receives SIGTERM/SIGINT. This is called by the hidden
// "newtlab bridge" subcommand.
func RunBridge(labName string) error {
	return RunBridgeFromFile(filepath.Join(LabDir(labName), "bridge.json"))
}

// RunBridgeFromFile reads a bridge config JSON file and runs bridge workers
// until the process receives SIGTERM/SIGINT. The stateDir for pid/socket files
// is derived from the config file's directory.
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

	// Convert to LinkConfig for StartBridgeWorkers
	links := make([]*LinkConfig, len(cfg.Links))
	for i, bl := range cfg.Links {
		aDevice, aIface, _ := splitLinkEndpoint(bl.A)
		zDevice, zIface, _ := splitLinkEndpoint(bl.Z)
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
	os.WriteFile(pidFile, []byte(fmt.Sprintf("%d", os.Getpid())), 0644)

	// Stats query handler: encode stats JSON and close.
	handleStatsConn := func(conn net.Conn) {
		json.NewEncoder(conn).Encode(bridge.Stats())
		conn.Close()
	}

	// Start Unix socket for stats queries (local)
	sockPath := filepath.Join(stateDir, "bridge.sock")
	os.Remove(sockPath) // remove stale socket
	unixLn, err := net.Listen("unix", sockPath)
	if err != nil {
		bridge.Stop()
		return fmt.Errorf("newtlab: stats socket: %w", err)
	}

	go func() {
		for {
			conn, err := unixLn.Accept()
			if err != nil {
				return // listener closed
			}
			go handleStatsConn(conn)
		}
	}()

	// Start TCP listener for stats queries (remote)
	var tcpLn net.Listener
	if cfg.StatsAddr != "" {
		tcpLn, err = net.Listen("tcp", cfg.StatsAddr)
		if err != nil {
			unixLn.Close()
			bridge.Stop()
			return fmt.Errorf("newtlab: stats tcp listener %s: %w", cfg.StatsAddr, err)
		}
		go func() {
			for {
				conn, err := tcpLn.Accept()
				if err != nil {
					return
				}
				go handleStatsConn(conn)
			}
		}()
	}

	// Block until killed
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	<-sigCh

	unixLn.Close()
	os.Remove(sockPath)
	if tcpLn != nil {
		tcpLn.Close()
	}
	bridge.Stop()
	return nil
}

// QueryBridgeStats connects to a running bridge's stats endpoint and returns
// a snapshot of per-link telemetry counters. The addr is either a Unix socket
// path (starts with "/") or a TCP address ("host:port").
func QueryBridgeStats(addr string) (*BridgeStats, error) {
	network := "tcp"
	if strings.HasPrefix(addr, "/") {
		network = "unix"
	}
	conn, err := net.DialTimeout(network, addr, 5*time.Second)
	if err != nil {
		return nil, fmt.Errorf("newtlab: connect to bridge stats (%s): %w", addr, err)
	}
	defer conn.Close()

	var stats BridgeStats
	if err := json.NewDecoder(conn).Decode(&stats); err != nil {
		return nil, fmt.Errorf("newtlab: decode bridge stats: %w", err)
	}
	return &stats, nil
}

// QueryAllBridgeStats aggregates stats from all bridge processes in a lab.
// It queries each bridge via its StatsAddr (TCP for remote, Unix socket for local fallback).
func QueryAllBridgeStats(labName string) (*BridgeStats, error) {
	state, err := LoadState(labName)
	if err != nil {
		return nil, err
	}

	merged := &BridgeStats{}

	// New multi-bridge state
	if len(state.Bridges) > 0 {
		for host, bs := range state.Bridges {
			addr := bs.StatsAddr
			// For local bridge, prefer Unix socket if available
			if host == "" || bs.HostIP == "" {
				sockPath := filepath.Join(LabDir(labName), "bridge.sock")
				if _, err := os.Stat(sockPath); err == nil {
					addr = sockPath
				}
			}
			stats, err := QueryBridgeStats(addr)
			if err != nil {
				return nil, fmt.Errorf("newtlab: query bridge on host %q: %w", host, err)
			}
			merged.Links = append(merged.Links, stats.Links...)
		}
		return merged, nil
	}

	// Legacy fallback: single bridge via Unix socket
	sockPath := filepath.Join(LabDir(labName), "bridge.sock")
	return QueryBridgeStats(sockPath)
}

// startBridgeProcess spawns a bridge process locally.
// It prefers the newtlink binary next to the current executable; falls back to
// "newtlab bridge <labName>" for backward compatibility.
func startBridgeProcess(labName, stateDir string) (int, error) {
	configPath := filepath.Join(stateDir, "bridge.json")

	// Prefer newtlink binary next to current executable; fall back to newtlab bridge
	var cmd *exec.Cmd
	if p := findSiblingBinary("newtlink"); p != "newtlink" {
		cmd = exec.Command(p, configPath)
	} else if exe, err := os.Executable(); err == nil {
		cmd = exec.Command(exe, "bridge", labName)
	} else {
		return 0, fmt.Errorf("resolve executable: %w", err)
	}

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
	// Upload newtlink binary (skip if version matches)
	newtlinkPath, err := uploadNewtlink(hostIP)
	if err != nil {
		return 0, fmt.Errorf("newtlab: upload newtlink to %s: %w", hostIP, err)
	}

	stateDir := fmt.Sprintf("~/.newtlab/labs/%s", labName)
	configPath := stateDir + "/bridge.json"

	// Create remote state dir and write bridge config
	mkdirCmd := fmt.Sprintf("mkdir -p %s/logs && cat > %s", stateDir, configPath)
	cmd := exec.Command("ssh", "-o", "StrictHostKeyChecking=no", hostIP, mkdirCmd)
	cmd.Stdin = bytes.NewReader(configJSON)
	if out, err := cmd.CombinedOutput(); err != nil {
		return 0, fmt.Errorf("newtlab: setup remote bridge dir on %s: %w\n%s", hostIP, err, out)
	}

	// Start newtlink with config file
	startCmd := fmt.Sprintf("nohup %s %s > %s/logs/bridge.log 2>&1 & echo $!", newtlinkPath, configPath, stateDir)
	cmd = exec.Command("ssh", "-o", "StrictHostKeyChecking=no", hostIP, startCmd)
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
