//go:build e2e

package testutil

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/go-redis/redis/v8"

	"github.com/newtron-network/newtron/pkg/device"
	"github.com/newtron-network/newtron/pkg/network"
)

// SkipIfNoLab skips the test if no containerlab topology is running.
func SkipIfNoLab(t *testing.T) {
	t.Helper()

	name := LabTopologyName()
	if name == "" {
		t.Skip("no lab topology running: run 'make lab-start' first")
	}

	// Verify at least one node is reachable
	nodes := LabNodes(t)
	if len(nodes) == 0 {
		t.Skip("lab topology has no reachable nodes")
	}
}

// LabTopologyName returns the name of the running lab topology.
// It checks NEWTRON_LAB_TOPOLOGY env var first, then the .lab-state file.
func LabTopologyName() string {
	if name := os.Getenv("NEWTRON_LAB_TOPOLOGY"); name != "" {
		return name
	}

	stateFile := filepath.Join(testlabDir(), ".generated", ".lab-state")
	data, err := os.ReadFile(stateFile)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}

// LabSpecsDir returns the path to the generated specs directory for the running lab.
func LabSpecsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(testlabDir(), ".generated", "specs")
}

// LabNode contains information about a running lab node.
type LabNode struct {
	Name string
	IP   string
}

// LabNodes discovers running lab nodes and their IPs via containerlab inspect.
func LabNodes(t *testing.T) []LabNode {
	t.Helper()

	topoName := LabTopologyName()
	if topoName == "" {
		return nil
	}

	generatedDir := filepath.Join(testlabDir(), ".generated")
	clabFile := filepath.Join(generatedDir, topoName+".clab.yml")

	out, err := exec.Command("containerlab", "inspect",
		"-t", clabFile, "--format", "json").Output()
	if err != nil {
		t.Logf("containerlab inspect failed: %v", err)
		return nil
	}

	// containerlab inspect JSON format: { "<topo_name>": [ { "name": "...", "ipv4_address": "..." }, ... ] }
	var result map[string][]struct {
		Name        string `json:"name"`
		IPv4Address string `json:"ipv4_address"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		t.Logf("parsing containerlab inspect output: %v", err)
		return nil
	}

	var nodes []LabNode
	for _, containers := range result {
		for _, c := range containers {
			ip := c.IPv4Address
			if idx := strings.Index(ip, "/"); idx > 0 {
				ip = ip[:idx]
			}
			// Strip clab-<topo>- prefix to get node name
			name := strings.TrimPrefix(c.Name, "clab-"+topoName+"-")
			nodes = append(nodes, LabNode{Name: name, IP: ip})
		}
	}
	return nodes
}

// LabSonicNodes returns only non-server lab nodes (SONiC devices that have
// profiles and run Redis). Server nodes are filtered out by checking whether
// a profile file exists for the node name.
func LabSonicNodes(t *testing.T) []LabNode {
	t.Helper()

	allNodes := LabNodes(t)
	profilesDir := filepath.Join(testlabDir(), ".generated", "specs", "profiles")

	var sonicNodes []LabNode
	for _, n := range allNodes {
		profilePath := filepath.Join(profilesDir, n.Name+".json")
		if _, err := os.Stat(profilePath); err == nil {
			sonicNodes = append(sonicNodes, n)
		}
	}
	return sonicNodes
}

// =========================================================================
// SSH Tunnel Pool for Lab Redis Access
// =========================================================================

var (
	labTunnelsMu sync.Mutex
	labTunnels   map[string]*device.SSHTunnel
)

// labSSHConfig reads SSH credentials from the patched profile JSON for a node.
func labSSHConfig(t *testing.T, nodeName string) (user, pass string) {
	t.Helper()
	profilePath := filepath.Join(testlabDir(), ".generated", "specs", "profiles", nodeName+".json")
	data, err := os.ReadFile(profilePath)
	if err != nil {
		return "", ""
	}
	var profile struct {
		SSHUser string `json:"ssh_user"`
		SSHPass string `json:"ssh_pass"`
	}
	if err := json.Unmarshal(data, &profile); err != nil {
		return "", ""
	}
	return profile.SSHUser, profile.SSHPass
}

// labTunnelAddr returns a Redis address for a lab node.
// If SSH credentials are present in the profile, it returns the local address
// of a shared SSH tunnel. Otherwise it falls back to direct "<ip>:6379".
func labTunnelAddr(t *testing.T, nodeName, nodeIP string) string {
	t.Helper()

	user, pass := labSSHConfig(t, nodeName)
	if user == "" || pass == "" {
		return nodeIP + ":6379"
	}

	labTunnelsMu.Lock()
	defer labTunnelsMu.Unlock()

	if labTunnels == nil {
		labTunnels = make(map[string]*device.SSHTunnel)
	}

	if tun, ok := labTunnels[nodeName]; ok {
		return tun.LocalAddr()
	}

	tun, err := device.NewSSHTunnel(nodeIP, user, pass)
	if err != nil {
		t.Fatalf("SSH tunnel to %s (%s): %v", nodeName, nodeIP, err)
	}
	labTunnels[nodeName] = tun
	return tun.LocalAddr()
}

// CloseLabTunnels closes all shared SSH tunnels. Call from TestMain after m.Run().
func CloseLabTunnels() {
	labTunnelsMu.Lock()
	defer labTunnelsMu.Unlock()

	for _, tun := range labTunnels {
		tun.Close()
	}
	labTunnels = nil
}

// LabNetwork returns a network.Network loaded from the generated lab specs.
func LabNetwork(t *testing.T) *network.Network {
	t.Helper()
	SkipIfNoLab(t)

	specsDir := LabSpecsDir(t)
	net, err := network.NewNetwork(specsDir)
	if err != nil {
		t.Fatalf("creating lab network: %v", err)
	}
	return net
}

// LabConnectedDevice connects to a lab node via the normal network path.
func LabConnectedDevice(t *testing.T, name string) *network.Device {
	t.Helper()

	net := LabNetwork(t)
	ctx := LabContext(t)

	dev, err := net.ConnectDevice(ctx, name)
	if err != nil {
		t.Fatalf("connecting to lab device %s: %v", name, err)
	}

	t.Cleanup(func() {
		dev.Disconnect()
	})

	return dev
}

// TryLabConnectedDevice connects to a lab node, returning the device and any error.
// Unlike LabConnectedDevice, it does not fatal on connection failure.
func TryLabConnectedDevice(t *testing.T, name string) (*network.Device, error) {
	t.Helper()

	net := LabNetwork(t)
	ctx := LabContext(t)

	dev, err := net.ConnectDevice(ctx, name)
	if err != nil {
		return nil, err
	}

	t.Cleanup(func() {
		dev.Disconnect()
	})

	return dev, nil
}

// LabLockedDevice connects to and locks a lab node.
func LabLockedDevice(t *testing.T, name string) *network.Device {
	t.Helper()

	dev := LabConnectedDevice(t, name)
	ctx := LabContext(t)
	if err := dev.Lock(ctx); err != nil {
		t.Fatalf("locking lab device %s: %v", name, err)
	}

	t.Cleanup(func() {
		dev.Unlock()
	})

	return dev
}

// LabContext returns a context with a 2-minute timeout suitable for SONiC-VS operations.
func LabContext(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	t.Cleanup(cancel)
	return ctx
}

// LabRedisClient returns a raw Redis client for a lab node, useful for verification.
// Uses an SSH tunnel when SSH credentials are available in the node's profile.
func LabRedisClient(t *testing.T, name string, db int) *redis.Client {
	t.Helper()

	nodes := LabNodes(t)
	for _, n := range nodes {
		if n.Name == name {
			addr := labTunnelAddr(t, name, n.IP)
			client := redis.NewClient(&redis.Options{
				Addr: addr,
				DB:   db,
			})
			t.Cleanup(func() { client.Close() })
			return client
		}
	}

	t.Fatalf("lab node %q not found", name)
	return nil
}

// AssertConfigDBEntry verifies that a Redis hash in CONFIG_DB (DB 4) has the expected fields.
func AssertConfigDBEntry(t *testing.T, name, table, key string, expectedFields map[string]string) {
	t.Helper()

	client := LabRedisClient(t, name, 4)
	ctx := context.Background()

	redisKey := table + "|" + key
	fields, err := client.HGetAll(ctx, redisKey).Result()
	if err != nil {
		t.Fatalf("reading %s on %s: %v", redisKey, name, err)
	}

	if len(fields) == 0 {
		t.Fatalf("%s on %s: entry not found", redisKey, name)
	}

	for k, want := range expectedFields {
		got, ok := fields[k]
		if !ok {
			t.Errorf("%s on %s: field %q not found", redisKey, name, k)
			continue
		}
		if got != want {
			t.Errorf("%s on %s: field %q = %q, want %q", redisKey, name, k, got, want)
		}
	}
}

// LabNodeIP returns the management IP for a named lab node.
func LabNodeIP(t *testing.T, name string) string {
	t.Helper()

	nodes := LabNodes(t)
	for _, n := range nodes {
		if n.Name == name {
			return n.IP
		}
	}

	t.Fatalf("lab node %q not found", name)
	return ""
}

// WaitForLabRedis waits until Redis is reachable on all SONiC lab nodes.
// Uses SSH tunnels when SSH credentials are available in the node's profile.
func WaitForLabRedis(t *testing.T, timeout time.Duration) {
	t.Helper()

	nodes := LabSonicNodes(t)
	deadline := time.Now().Add(timeout)

	for _, node := range nodes {
		user, pass := labSSHConfig(t, node.Name)
		for time.Now().Before(deadline) {
			if user != "" && pass != "" {
				tun, err := device.NewSSHTunnel(node.IP, user, pass)
				if err != nil {
					time.Sleep(2 * time.Second)
					continue
				}
				ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
				client := redis.NewClient(&redis.Options{Addr: tun.LocalAddr()})
				err = client.Ping(ctx).Err()
				client.Close()
				cancel()
				tun.Close()
				if err == nil {
					break
				}
			} else {
				ctx, cancel := context.WithTimeout(context.Background(), 1*time.Second)
				client := redis.NewClient(&redis.Options{Addr: node.IP + ":6379"})
				err := client.Ping(ctx).Err()
				client.Close()
				cancel()
				if err == nil {
					break
				}
			}
			time.Sleep(2 * time.Second)
		}
	}
}

// LabNodeNames returns the names of all nodes from the running topology.
func LabNodeNames(t *testing.T) []string {
	t.Helper()

	nodes := LabNodes(t)
	names := make([]string, len(nodes))
	for i, n := range nodes {
		names[i] = n.Name
	}
	return names
}

// AssertConfigDBEntryExists checks that a key exists in CONFIG_DB on the named node.
func AssertConfigDBEntryExists(t *testing.T, name, table, key string) {
	t.Helper()

	client := LabRedisClient(t, name, 4)
	ctx := context.Background()

	redisKey := table + "|" + key
	n, err := client.Exists(ctx, redisKey).Result()
	if err != nil {
		t.Fatalf("checking %s on %s: %v", redisKey, name, err)
	}
	if n == 0 {
		t.Fatalf("%s on %s: entry does not exist", redisKey, name)
	}
}

// AssertConfigDBEntryAbsent checks that a key does NOT exist in CONFIG_DB.
func AssertConfigDBEntryAbsent(t *testing.T, name, table, key string) {
	t.Helper()

	client := LabRedisClient(t, name, 4)
	ctx := context.Background()

	redisKey := table + "|" + key
	n, err := client.Exists(ctx, redisKey).Result()
	if err != nil {
		t.Fatalf("checking %s on %s: %v", redisKey, name, err)
	}
	if n > 0 {
		t.Fatalf("%s on %s: entry should not exist but does", redisKey, name)
	}
}

// LabStateDBEntry reads a hash from STATE_DB (DB 6) on the named lab node.
func LabStateDBEntry(t *testing.T, name, table, key string) map[string]string {
	t.Helper()

	client := LabRedisClient(t, name, 6)
	ctx := context.Background()

	redisKey := table + "|" + key
	fields, err := client.HGetAll(ctx, redisKey).Result()
	if err != nil {
		t.Fatalf("reading %s on %s: %v", redisKey, name, err)
	}
	return fields
}

// LabCleanupChanges registers a cleanup function that creates a fresh locked device
// connection and applies the changeset returned by fn. This is used to undo
// operations after a test, using a fresh device connection (since the test's
// device may have a stale configDB cache after Apply).
func LabCleanupChanges(t *testing.T, nodeName string, fn func(ctx context.Context, dev *network.Device) (*network.ChangeSet, error)) {
	t.Helper()
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()

		specsDir := LabSpecsDir(t)
		net, err := network.NewNetwork(specsDir)
		if err != nil {
			t.Logf("cleanup: load network: %v", err)
			return
		}

		dev, err := net.ConnectDevice(ctx, nodeName)
		if err != nil {
			t.Logf("cleanup: connect %s: %v", nodeName, err)
			return
		}
		defer dev.Disconnect()

		if err := dev.Lock(ctx); err != nil {
			t.Logf("cleanup: lock %s: %v", nodeName, err)
			return
		}
		defer dev.Unlock()

		cs, err := fn(ctx, dev)
		if err != nil {
			t.Logf("cleanup: %v", err)
			return
		}
		if cs != nil {
			if err := cs.Apply(dev); err != nil {
				t.Logf("cleanup apply: %v", err)
			}
		}
	})
}

// PollStateDB polls a STATE_DB entry until the expected field has the expected value,
// or the context deadline is exceeded.
func PollStateDB(ctx context.Context, t *testing.T, name, table, key, field, want string) error {
	t.Helper()

	client := LabRedisClient(t, name, 6)

	redisKey := table + "|" + key
	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for %s field %q = %q on %s", redisKey, field, want, name)
		default:
		}

		val, err := client.HGet(ctx, redisKey, field).Result()
		if err == nil && val == want {
			return nil
		}
		time.Sleep(2 * time.Second)
	}
}

// WaitForASICVLAN polls ASIC_DB for a VLAN entry with the given vid.
// It returns nil once a SAI_OBJECT_TYPE_VLAN entry with a matching
// SAI_VLAN_ATTR_VLAN_ID is found, or an error if the context expires.
// This provides a convergence check: CONFIG_DB entries have been processed
// by orchagent and programmed into the ASIC.
func WaitForASICVLAN(ctx context.Context, t *testing.T, name string, vlanID int) error {
	t.Helper()

	client := LabRedisClient(t, name, 1) // ASIC_DB
	want := fmt.Sprintf("%d", vlanID)

	for {
		select {
		case <-ctx.Done():
			return fmt.Errorf("timeout waiting for VLAN %d in ASIC_DB on %s", vlanID, name)
		default:
		}

		keys, err := client.Keys(ctx, "ASIC_STATE:SAI_OBJECT_TYPE_VLAN:*").Result()
		if err == nil {
			for _, key := range keys {
				vid, _ := client.HGet(ctx, key, "SAI_VLAN_ATTR_VLAN_ID").Result()
				if vid == want {
					return nil
				}
			}
		}
		time.Sleep(1 * time.Second)
	}
}

// staleE2EKeys lists CONFIG_DB keys that E2E tests may create.
// These are deleted from all SONiC nodes before the test suite runs
// to prevent stale state from causing vxlanmgrd/orchagent crashes.
var staleE2EKeys = []string{
	// DataPlane_L2Bridged
	"VXLAN_TUNNEL_MAP|vtep1|map_10700_Vlan700",
	"VLAN_MEMBER|Vlan700|Ethernet2",
	"VLAN|Vlan700",
	// DataPlane_IRBSymmetric
	"VLAN_INTERFACE|Vlan800|10.80.0.1/24",
	"VLAN_INTERFACE|Vlan800",
	"VXLAN_TUNNEL_MAP|vtep1|map_10800_Vlan800",
	"VLAN_MEMBER|Vlan800|Ethernet2",
	"VLAN|Vlan800",
	"VRF|Vrf_e2e_irb",
	// DataPlane_L3Routed
	"INTERFACE|Ethernet2|10.90.1.1/30",
	"INTERFACE|Ethernet2|10.90.2.1/30",
	"INTERFACE|Ethernet2",
	"VRF|Vrf_e2e_l3",
	// Operations: VLAN tests
	"VLAN|Vlan500", "VLAN|Vlan501", "VLAN|Vlan502", "VLAN|Vlan503",
	"VLAN_MEMBER|Vlan502|Ethernet2", "VLAN_MEMBER|Vlan503|Ethernet2",
	// Operations: SVI test
	"VLAN_INTERFACE|Vlan504|10.99.1.1/24", "VLAN_INTERFACE|Vlan504",
	"VLAN|Vlan504", "VRF|Vrf_e2e_svi",
	// Operations: EVPN tests
	"VXLAN_TUNNEL_MAP|vtep1|map_10505_Vlan505", "SUPPRESS_VLAN_NEIGH|Vlan505", "VLAN|Vlan505",
	"VXLAN_TUNNEL_MAP|vtep1|map_10506_Vlan506", "SUPPRESS_VLAN_NEIGH|Vlan506", "VLAN|Vlan506",
	"VXLAN_TUNNEL_MAP|vtep1|map_99999_Vrf_e2e_test", "VRF|Vrf_e2e_test",
	"VRF|Vrf_e2e_delete",
	// Operations: VRF/interface tests
	"VRF|Vrf_e2e_iface", "VRF|Vrf_e2e_l3",
	// Operations: ACL tests
	"ACL_RULE|E2E_RULE_ACL|RULE_200", "ACL_TABLE|E2E_RULE_ACL",
	"ACL_RULE|E2E_DELRULE_ACL|RULE_300", "ACL_TABLE|E2E_DELRULE_ACL",
	"ACL_TABLE|E2E_TEST_ACL", "ACL_TABLE|E2E_DELTABLE_ACL", "ACL_TABLE|E2E_BIND_ACL",
	// Operations: VTEP test (on spine — nvo1 cleanup is handled by the test itself;
	// leaves use nvo1 as infrastructure so it must NOT be in stale keys)
	"VXLAN_TUNNEL|e2e_vtep",
	// Operations: LAG tests
	"PORTCHANNEL_MEMBER|PortChannel200|Ethernet2", "PORTCHANNEL|PortChannel200",
	"PORTCHANNEL_MEMBER|PortChannel201|Ethernet2", "PORTCHANNEL|PortChannel201",
	"PORTCHANNEL_MEMBER|PortChannel202|Ethernet2", "PORTCHANNEL|PortChannel202",
	"PORTCHANNEL_MEMBER|PortChannel203|Ethernet2", "PORTCHANNEL|PortChannel203",
	// Health tests
	"VLAN|Vlan510",
	// Audit tests
	"VLAN|Vlan520",
	// Service tests: L2/IRB (Vlan100 from macvpn servers-vlan100)
	"VLAN|Vlan100",
	"VLAN_MEMBER|Vlan100|Ethernet2", "VLAN_MEMBER|Vlan100|Ethernet3",
	"VLAN_INTERFACE|Vlan100", "VLAN_INTERFACE|Vlan100|10.1.100.1/24",
	"VXLAN_TUNNEL_MAP|vtep1|map_10100_Vlan100",
	"SUPPRESS_VLAN_NEIGH|Vlan100",
	"SAG_GLOBAL|IPv4",
	"NEWTRON_SERVICE_BINDING|Ethernet2", "NEWTRON_SERVICE_BINDING|Ethernet3",
	// Service tests: shared VRF artifacts (created by service apply for server-irb)
	"VRF|server-vpn",
	"VXLAN_TUNNEL_MAP|vtep1|map_10100_server-vpn",
	"BGP_GLOBALS_AF|server-vpn|l2vpn_evpn",
	"BGP_EVPN_VNI|server-vpn|10100",
	// BGP tests
	"ROUTE_REDISTRIBUTE|default|static|bgp|ipv4",
	"ROUTE_MAP|E2E_TEST_RM|10",
	"BGP_GLOBALS_AF_NETWORK|default|ipv4_unicast|10.99.0.0/24",
}

// ResetLabBaseline deletes stale CONFIG_DB entries from all SONiC nodes.
// This prevents vxlanmgrd/orchagent crashes caused by processing leftover
// VXLAN/VRF entries from a previous test run. Call from TestMain before m.Run().
func ResetLabBaseline() error {
	topoName := LabTopologyName()
	if topoName == "" {
		return nil // no lab running, tests will skip
	}

	generatedDir := filepath.Join(testlabDir(), ".generated")
	clabFile := filepath.Join(generatedDir, topoName+".clab.yml")
	profilesDir := filepath.Join(generatedDir, "specs", "profiles")

	out, err := exec.Command("containerlab", "inspect",
		"-t", clabFile, "--format", "json").Output()
	if err != nil {
		return fmt.Errorf("containerlab inspect: %w", err)
	}

	var result map[string][]struct {
		Name        string `json:"name"`
		IPv4Address string `json:"ipv4_address"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return fmt.Errorf("parse containerlab output: %w", err)
	}

	type nodeInfo struct {
		name, ip, user, pass string
	}
	var nodes []nodeInfo
	for _, containers := range result {
		for _, c := range containers {
			name := strings.TrimPrefix(c.Name, "clab-"+topoName+"-")
			ip := c.IPv4Address
			if idx := strings.Index(ip, "/"); idx > 0 {
				ip = ip[:idx]
			}
			data, err := os.ReadFile(filepath.Join(profilesDir, name+".json"))
			if err != nil {
				continue // not a SONiC node
			}
			var profile struct {
				SSHUser string `json:"ssh_user"`
				SSHPass string `json:"ssh_pass"`
			}
			if err := json.Unmarshal(data, &profile); err != nil || profile.SSHUser == "" {
				continue
			}
			nodes = append(nodes, nodeInfo{name, ip, profile.SSHUser, profile.SSHPass})
		}
	}
	if len(nodes) == 0 {
		return nil
	}

	// Build individual redis-cli DEL commands joined by &&.
	// Each key gets its own DEL call to avoid redis-cli argument parsing issues.
	var parts []string
	for _, key := range staleE2EKeys {
		parts = append(parts, fmt.Sprintf("redis-cli -n 4 DEL '%s'", key))
	}
	delCmd := strings.Join(parts, " && ")

	// Clean stale entries on all nodes in parallel.
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(node nodeInfo) {
			defer wg.Done()
			fmt.Fprintf(os.Stderr, "  cleaning stale entries on %s...\n", node.name)
			cmd := exec.Command("sshpass", "-p", node.pass,
				"ssh", "-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "LogLevel=ERROR",
				"-o", "ConnectTimeout=10",
				node.user+"@"+node.ip,
				delCmd)
			if out, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "  WARNING: cleanup on %s: %v\n%s\n", node.name, err, string(out))
			}
		}(n)
	}
	wg.Wait()

	// Allow orchagent/vxlanmgrd to process the deletions.
	time.Sleep(5 * time.Second)

	return nil
}

// infrastructureTables lists CONFIG_DB tables that define the fabric infrastructure.
// EnsureStartupConfig pushes entries from these tables to running nodes so that
// the runtime CONFIG_DB matches the generated startup config_db.json.
var infrastructureTables = map[string]bool{
	"LOOPBACK_INTERFACE": true,
	"INTERFACE":          true,
	"BGP_GLOBALS":        true,
	"BGP_GLOBALS_AF":     true,
	"BGP_NEIGHBOR":       true,
	"BGP_NEIGHBOR_AF":    true,
	"ROUTE_REDISTRIBUTE": true,
	"VXLAN_TUNNEL":       true,
	"VXLAN_EVPN_NVO":     true,
}

// EnsureStartupConfig reads each SONiC node's generated config_db.json and
// pushes infrastructure entries (BGP, INTERFACE, VTEP, etc.) to the running
// CONFIG_DB via SSH. This must run AFTER ResetLabBaseline (which deletes stale
// test keys) to ensure the underlay fabric and VTEP are operational.
//
// The function is idempotent — HMSET overwrites existing fields without
// affecting other entries. It replaces the narrower EnsureLeafVTEP function.
func EnsureStartupConfig() error {
	topoName := LabTopologyName()
	if topoName == "" {
		return nil
	}

	generatedDir := filepath.Join(testlabDir(), ".generated")
	clabFile := filepath.Join(generatedDir, topoName+".clab.yml")
	profilesDir := filepath.Join(generatedDir, "specs", "profiles")

	out, err := exec.Command("containerlab", "inspect",
		"-t", clabFile, "--format", "json").Output()
	if err != nil {
		return fmt.Errorf("containerlab inspect: %w", err)
	}

	var result map[string][]struct {
		Name        string `json:"name"`
		IPv4Address string `json:"ipv4_address"`
	}
	if err := json.Unmarshal(out, &result); err != nil {
		return fmt.Errorf("parse containerlab output: %w", err)
	}

	type nodeInfo struct {
		name, ip, user, pass string
	}
	var nodes []nodeInfo
	for _, containers := range result {
		for _, c := range containers {
			name := strings.TrimPrefix(c.Name, "clab-"+topoName+"-")
			ip := c.IPv4Address
			if idx := strings.Index(ip, "/"); idx > 0 {
				ip = ip[:idx]
			}
			data, err := os.ReadFile(filepath.Join(profilesDir, name+".json"))
			if err != nil {
				continue // not a SONiC node
			}
			var profile struct {
				SSHUser string `json:"ssh_user"`
				SSHPass string `json:"ssh_pass"`
			}
			if err := json.Unmarshal(data, &profile); err != nil || profile.SSHUser == "" {
				continue
			}
			nodes = append(nodes, nodeInfo{name, ip, profile.SSHUser, profile.SSHPass})
		}
	}
	if len(nodes) == 0 {
		return nil
	}

	// Push infrastructure config to each node in parallel.
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(node nodeInfo) {
			defer wg.Done()

			// Read generated config_db.json for this node.
			configPath := filepath.Join(generatedDir, node.name, "config_db.json")
			data, err := os.ReadFile(configPath)
			if err != nil {
				fmt.Fprintf(os.Stderr, "  WARNING: reading config for %s: %v\n", node.name, err)
				return
			}

			var configDB map[string]map[string]map[string]string
			if err := json.Unmarshal(data, &configDB); err != nil {
				fmt.Fprintf(os.Stderr, "  WARNING: parsing config for %s: %v\n", node.name, err)
				return
			}

			// Build redis-cli commands for infrastructure tables.
			var cmds []string
			for table, keys := range configDB {
				if !infrastructureTables[table] {
					continue
				}
				for key, fields := range keys {
					redisKey := table + "|" + key
					if len(fields) == 0 {
						// SONiC empty-hash convention: single NULL field.
						cmds = append(cmds, fmt.Sprintf("redis-cli -n 4 HSET '%s' NULL NULL", redisKey))
					} else {
						var args string
						for k, v := range fields {
							args += fmt.Sprintf(" '%s' '%s'", k, v)
						}
						cmds = append(cmds, fmt.Sprintf("redis-cli -n 4 HMSET '%s'%s", redisKey, args))
					}
				}
			}

			if len(cmds) == 0 {
				return
			}

			sshCmd := strings.Join(cmds, " && ")
			fmt.Fprintf(os.Stderr, "  ensuring startup config on %s (%d entries)...\n", node.name, len(cmds))
			cmd := exec.Command("sshpass", "-p", node.pass,
				"ssh", "-o", "StrictHostKeyChecking=no",
				"-o", "UserKnownHostsFile=/dev/null",
				"-o", "LogLevel=ERROR",
				"-o", "ConnectTimeout=10",
				node.user+"@"+node.ip,
				sshCmd)
			if out, err := cmd.CombinedOutput(); err != nil {
				fmt.Fprintf(os.Stderr, "  WARNING: config push on %s: %v\n%s\n", node.name, err, string(out))
			}
		}(n)
	}
	wg.Wait()

	// Allow SONiC daemons (bgpcfgd, intfmgrd, vxlanmgrd) to process.
	time.Sleep(5 * time.Second)

	return nil
}

// EnsureLeafVTEP is an alias for EnsureStartupConfig for backward compatibility.
// Deprecated: use EnsureStartupConfig directly.
func EnsureLeafVTEP() error {
	return EnsureStartupConfig()
}

// =============================================================================
// Server Container Helpers
// =============================================================================

// LabServerNode returns the LabNode for a server container by name.
// Server containers are not SONiC devices (no profile file) but are present in
// the containerlab topology. Fatals if the server is not found.
func LabServerNode(t *testing.T, name string) LabNode {
	t.Helper()

	nodes := LabNodes(t)
	for _, n := range nodes {
		if n.Name == name {
			return n
		}
	}
	t.Fatalf("server node %q not found in lab topology", name)
	return LabNode{}
}

// SkipIfNoServers skips the test if any of the named server containers are
// not present in the running lab topology.
func SkipIfNoServers(t *testing.T, names ...string) {
	t.Helper()

	nodes := LabNodes(t)
	nodeSet := make(map[string]bool, len(nodes))
	for _, n := range nodes {
		nodeSet[n.Name] = true
	}
	for _, name := range names {
		if !nodeSet[name] {
			t.Skipf("server container %q not found in lab topology (need topology with servers)", name)
		}
	}
}

// ServerExec runs a command on a server container via docker exec.
// The container name follows the containerlab convention: clab-<topoName>-<serverName>.
// Returns stdout and any error. Logs the command and output.
func ServerExec(t *testing.T, serverName string, args ...string) (string, error) {
	t.Helper()

	topoName := LabTopologyName()
	containerName := "clab-" + topoName + "-" + serverName

	dockerArgs := append([]string{"exec", containerName}, args...)
	t.Logf("docker %s", strings.Join(dockerArgs, " "))

	cmd := exec.Command("docker", dockerArgs...)
	out, err := cmd.CombinedOutput()
	outStr := strings.TrimSpace(string(out))
	if outStr != "" {
		t.Logf("  output: %s", outStr)
	}
	return outStr, err
}

// serverToolsInstalled tracks per-server tool installation to avoid repeated installs.
var serverToolsInstalled sync.Map // map[string]bool

// EnsureServerTools verifies that required network tools (ping, ip) are available
// on a server container. With nicolaka/netshoot, tools are pre-installed.
// Skips repeated checks for the same server across tests.
func EnsureServerTools(t *testing.T, serverName string) {
	t.Helper()

	if _, ok := serverToolsInstalled.Load(serverName); ok {
		return
	}

	t.Logf("verifying network tools on %s", serverName)
	_, err := ServerExec(t, serverName, "sh", "-c", "which ping && which ip")
	if err != nil {
		t.Fatalf("network tools not available on %s (expected nicolaka/netshoot image): %v", serverName, err)
	}
	serverToolsInstalled.Store(serverName, true)
}

// ServerConfigureInterface configures an IP address and optional default route
// on a server container's interface. Registers t.Cleanup to flush the interface.
func ServerConfigureInterface(t *testing.T, serverName, iface, ipCIDR, gateway string) {
	t.Helper()

	EnsureServerTools(t, serverName)

	cmds := fmt.Sprintf(
		"ip addr flush dev %s && ip addr add %s dev %s && ip link set %s up",
		iface, ipCIDR, iface, iface)

	if gateway != "" {
		cmds += fmt.Sprintf(" && ip route replace default via %s dev %s", gateway, iface)
	}

	_, err := ServerExec(t, serverName, "sh", "-c", cmds)
	if err != nil {
		t.Fatalf("configuring %s on %s: %v", iface, serverName, err)
	}

	t.Cleanup(func() {
		ServerCleanupInterface(t, serverName, iface)
	})
}

// ServerPing pings targetIP from a server container. Returns true if any
// packet was received. Logs full ping output regardless of result. On failure,
// also runs diagnostic commands (ip addr, ip route, arp -n).
func ServerPing(t *testing.T, serverName, targetIP string, count int) bool {
	t.Helper()

	EnsureServerTools(t, serverName)

	out, err := ServerExec(t, serverName,
		"ping", "-c", fmt.Sprintf("%d", count), "-W", "2", targetIP)

	if err != nil {
		t.Logf("ping from %s to %s failed: %v", serverName, targetIP, err)
		// Run diagnostics
		t.Log("  diagnostics:")
		if diag, _ := ServerExec(t, serverName, "ip", "addr", "show"); diag != "" {
			t.Logf("  ip addr:\n%s", diag)
		}
		if diag, _ := ServerExec(t, serverName, "ip", "route", "show"); diag != "" {
			t.Logf("  ip route:\n%s", diag)
		}
		if diag, _ := ServerExec(t, serverName, "sh", "-c", "arp -n 2>/dev/null || true"); diag != "" {
			t.Logf("  arp:\n%s", diag)
		}
		return false
	}

	_ = out // already logged by ServerExec
	return true
}

// ServerCleanupInterface flushes IP addresses from a server container's interface.
func ServerCleanupInterface(t *testing.T, serverName, iface string) {
	t.Helper()
	_, _ = ServerExec(t, serverName, "ip", "addr", "flush", "dev", iface)
}
