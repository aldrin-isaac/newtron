package network

// These tests pin DESIGN_PRINCIPLES_NEWTRON §7 "a node's view is resolved per
// operation": a node sees every spec change — added, replaced, deleted, or
// overridden at any scope — at its next operation; a view holds still within
// one operation; a refused write changes nothing; and resolving a view races
// with no writer (run under go test -race, as CI does).

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/aldrin-isaac/newtron/pkg/newtron/network/node"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// loadViewTestNetwork is loadScopedTestNetwork plus one node, switch1, in zone
// amer — enough for a node to resolve its specs through all three scopes.
func loadViewTestNetwork(t *testing.T) *Network {
	t.Helper()
	n := loadScopedTestNetwork(t)
	nodesDir := filepath.Join(n.specDir, "nodes")
	if err := os.MkdirAll(nodesDir, 0o755); err != nil {
		t.Fatalf("mkdir nodes: %v", err)
	}
	if err := os.WriteFile(filepath.Join(nodesDir, "switch1.json"),
		[]byte(`{"zone":"amer","mgmt_ip":"192.0.2.1","loopback_ip":"10.0.0.1"}`), 0o644); err != nil {
		t.Fatalf("write node spec: %v", err)
	}
	return n
}

// startOperation runs what every operation runs first (NodeActor.execute):
// the per-operation rebuild, which re-resolves the node's specs.
func startOperation(t *testing.T, dev *node.Node) {
	t.Helper()
	if err := dev.RebuildProjection(context.Background()); err != nil {
		t.Fatalf("RebuildProjection: %v", err)
	}
}

func buildSwitch1(t *testing.T, n *Network) *node.Node {
	t.Helper()
	dev, err := n.GetAbstractNode("switch1")
	if err != nil {
		t.Fatalf("GetAbstractNode: %v", err)
	}
	return dev
}

func twoQueues() *spec.QoSPolicy {
	return &spec.QoSPolicy{Queues: []*spec.QoSQueue{
		{Name: "q0", Type: "dwrr", Weight: 50, DSCP: []int{0}},
		{Name: "q1", Type: "dwrr", Weight: 50, DSCP: []int{8}},
	}}
}

func fourQueues() *spec.QoSPolicy {
	return &spec.QoSPolicy{Queues: []*spec.QoSQueue{
		{Name: "q0", Type: "dwrr", Weight: 25, DSCP: []int{0}},
		{Name: "q1", Type: "dwrr", Weight: 25, DSCP: []int{8}},
		{Name: "q2", Type: "dwrr", Weight: 25, DSCP: []int{16}},
		{Name: "q3", Type: "dwrr", Weight: 25, DSCP: []int{24}},
	}}
}

// The #511 reproduction: a policy deleted and re-created under the same name.
func TestNodeSpecView_RecreatedSpec(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.CreateQoSPolicy("", "", "GOLD", twoQueues()); err != nil {
		t.Fatal(err)
	}
	dev := buildSwitch1(t, n)

	if err := n.DeleteQoSPolicy("", "", "GOLD"); err != nil {
		t.Fatal(err)
	}
	if err := n.CreateQoSPolicy("", "", "GOLD", fourQueues()); err != nil {
		t.Fatal(err)
	}

	startOperation(t, dev)
	p, err := dev.GetQoSPolicy("GOLD")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Queues) != 4 {
		t.Errorf("node resolved GOLD with %d queues, want the re-created policy's 4", len(p.Queues))
	}
}

func TestNodeSpecView_UpdatedSpec(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.CreateService("", "", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed", Description: "v1"}); err != nil {
		t.Fatal(err)
	}
	dev := buildSwitch1(t, n)

	if err := n.UpdateService("", "", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed", Description: "v2"}); err != nil {
		t.Fatal(err)
	}

	startOperation(t, dev)
	svc, err := dev.GetService("TRANSIT")
	if err != nil {
		t.Fatal(err)
	}
	if svc.Description != "v2" {
		t.Errorf("node resolved TRANSIT description %q, want the updated %q", svc.Description, "v2")
	}
}

func TestNodeSpecView_DeletedSpec(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.CreateQoSPolicy("", "", "GOLD", twoQueues()); err != nil {
		t.Fatal(err)
	}
	dev := buildSwitch1(t, n)

	if err := n.DeleteQoSPolicy("", "", "GOLD"); err != nil {
		t.Fatal(err)
	}

	startOperation(t, dev)
	if p, err := dev.GetQoSPolicy("GOLD"); err == nil {
		t.Errorf("node still resolves deleted GOLD (%d queues), want not found", len(p.Queues))
	}
}

func TestNodeSpecView_PrefixListEdit(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.CreatePrefixList("", "", "PL", []string{"10.0.0.0/8"}); err != nil {
		t.Fatal(err)
	}
	dev := buildSwitch1(t, n)

	if err := n.AddPrefixToPrefixList("", "", "PL", "192.168.0.0/16"); err != nil {
		t.Fatal(err)
	}

	startOperation(t, dev)
	pl, err := dev.GetPrefixList("PL")
	if err != nil {
		t.Fatal(err)
	}
	if len(pl) != 2 {
		t.Errorf("node resolved PL as %v, want both prefixes", pl)
	}
}

// A zone override authored after the node was built must win over the network
// base the node resolved at build time.
func TestNodeSpecView_ZoneOverride(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.CreateService("", "", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed", Description: "network"}); err != nil {
		t.Fatal(err)
	}
	dev := buildSwitch1(t, n)

	if err := n.CreateService(spec.ScopeZone, "amer", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed", Description: "zone"}); err != nil {
		t.Fatal(err)
	}

	startOperation(t, dev)
	svc, err := dev.GetService("TRANSIT")
	if err != nil {
		t.Fatal(err)
	}
	if svc.Description != "zone" {
		t.Errorf("node resolved TRANSIT from %q, want the zone override", svc.Description)
	}
}

func TestNodeSpecView_NodeOverride(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.CreateService("", "", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed", Description: "network"}); err != nil {
		t.Fatal(err)
	}
	dev := buildSwitch1(t, n)

	if err := n.CreateService(spec.ScopeNode, "switch1", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed", Description: "node"}); err != nil {
		t.Fatal(err)
	}

	startOperation(t, dev)
	svc, err := dev.GetService("TRANSIT")
	if err != nil {
		t.Fatal(err)
	}
	if svc.Description != "node" {
		t.Errorf("node resolved TRANSIT from %q, want the node override", svc.Description)
	}
}

// Updating the network base does not unseat a node override of the same name.
func TestNodeSpecView_OverrideOutranksUpdatedBase(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.CreateService("", "", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed", Description: "network"}); err != nil {
		t.Fatal(err)
	}
	if err := n.CreateService(spec.ScopeNode, "switch1", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed", Description: "node"}); err != nil {
		t.Fatal(err)
	}
	dev := buildSwitch1(t, n)

	if err := n.UpdateService("", "", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed", Description: "network-updated"}); err != nil {
		t.Fatal(err)
	}

	startOperation(t, dev)
	svc, err := dev.GetService("TRANSIT")
	if err != nil {
		t.Fatal(err)
	}
	if svc.Description != "node" {
		t.Errorf("node resolved TRANSIT from %q after the base was updated, want the node override", svc.Description)
	}
}

// The node's resolved values (here the SSH login) come from the same view.
func TestNodeSpecView_SSHLogin(t *testing.T) {
	n := loadViewTestNetwork(t)
	dev := buildSwitch1(t, n)

	if err := n.SetSSHCredentials("", "", "operator", "pw"); err != nil {
		t.Fatal(err)
	}

	startOperation(t, dev)
	if got := dev.Resolved().SSHUser; got != "operator" {
		t.Errorf("node resolved ssh_user %q, want %q", got, "operator")
	}
}

// One operation sees one version of the specs: an edit made while it runs does
// not reach the policy it already resolved.
func TestNodeSpecView_StableWithinOperation(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.CreateQoSPolicy("", "", "GOLD", twoQueues()); err != nil {
		t.Fatal(err)
	}
	dev := buildSwitch1(t, n)
	startOperation(t, dev)
	p, err := dev.GetQoSPolicy("GOLD")
	if err != nil {
		t.Fatal(err)
	}

	if err := n.AddQoSQueueToPolicy("", "", "GOLD", 2, &spec.QoSQueue{Name: "q2", Type: "dwrr", Weight: 10, DSCP: []int{16}}); err != nil {
		t.Fatal(err)
	}

	if len(p.Queues) != 2 {
		t.Errorf("policy resolved by the running operation changed under it: %d queues, want 2", len(p.Queues))
	}
}

// A refused write leaves the specs as they were — in memory as on disk.
func TestNodeSpecView_RefusedWriteChangesNothing(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.CreateQoSPolicy("", "", "GOLD", twoQueues()); err != nil {
		t.Fatal(err)
	}
	// Queue name q0 is already taken, so the policy's own validation refuses it.
	err := n.AddQoSQueueToPolicy("", "", "GOLD", 2, &spec.QoSQueue{Name: "q0", Type: "dwrr", Weight: 10, DSCP: []int{16}})
	if err == nil {
		t.Fatal("duplicate queue name accepted, want refusal")
	}

	p, err := n.GetQoSPolicy("GOLD")
	if err != nil {
		t.Fatal(err)
	}
	if len(p.Queues) != 2 {
		t.Errorf("refused write changed the in-memory policy: %d queues, want 2", len(p.Queues))
	}
}

// Building a node's view while a spec write runs must not race (go test -race).
func TestNodeSpecView_ConcurrentWrite(t *testing.T) {
	n := loadViewTestNetwork(t)
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = n.CreateService("", "", fmt.Sprintf("SVC%d", i), &spec.ServiceSpec{ServiceType: "routed"})
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			dev, err := n.GetAbstractNode("switch1")
			if err != nil {
				t.Error(err)
				return
			}
			_ = dev.RebuildProjection(context.Background())
		}
	}()
	wg.Wait()
}

// Two nodes for the same device resolving at once must not race: resolving the
// node's secrets writes nothing shared (go test -race).
func TestNodeSpecView_ConcurrentResolve(t *testing.T) {
	n := loadViewTestNetwork(t)
	var wg sync.WaitGroup
	for g := 0; g < 2; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < 50; i++ {
				if _, err := n.GetAbstractNode("switch1"); err != nil {
					t.Error(err)
					return
				}
			}
		}()
	}
	wg.Wait()
}

// The node device setup connects with must resolve the login as it stands now,
// not as it stood the first time setup ran.
func TestNodeSpecView_SetupNodeSSHLogin(t *testing.T) {
	n := loadViewTestNetwork(t)
	if _, err := n.GetNode("switch1"); err != nil {
		t.Fatal(err)
	}
	if err := n.SetSSHCredentials("", "", "operator", "pw"); err != nil {
		t.Fatal(err)
	}
	dev, err := n.GetNode("switch1")
	if err != nil {
		t.Fatal(err)
	}
	if got := dev.Resolved().SSHUser; got != "operator" {
		t.Errorf("setup node resolved ssh_user %q, want %q", got, "operator")
	}
}

// A node's ${secret:} reference resolves to the secret's current value, so a
// rotated secret reaches the next operation.
func TestNodeSpecView_RotatedSecret(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.SetSecret("SW1_PASS", "old"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(n.specDir, "nodes", "switch1.json"),
		[]byte(`{"zone":"amer","mgmt_ip":"192.0.2.1","loopback_ip":"10.0.0.1","ssh_user":"admin","ssh_pass":"${secret:SW1_PASS}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	dev := buildSwitch1(t, n)

	if err := n.SetSecret("SW1_PASS", "new"); err != nil {
		t.Fatal(err)
	}

	startOperation(t, dev)
	if got := dev.Resolved().SSHPass; got != "new" {
		t.Errorf("node resolved ssh_pass %q, want the rotated %q", got, "new")
	}
}

// A node's EVPN neighbors are derived from the topology, so resolving them while
// the topology is edited must not race (go test -race).
func TestNodeSpecView_ConcurrentTopologyEdit(t *testing.T) {
	n := loadViewTestNetwork(t)
	nodes := filepath.Join(n.specDir, "nodes")
	if err := os.WriteFile(filepath.Join(nodes, "switch1.json"),
		[]byte(`{"zone":"amer","mgmt_ip":"192.0.2.1","loopback_ip":"10.0.0.1","evpn":{"peers":["switch2"]}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(nodes, "switch2.json"),
		[]byte(`{"zone":"amer","mgmt_ip":"192.0.2.2","loopback_ip":"10.0.0.2"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	wg.Add(2)
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			_ = n.AddTopologyDevice("switch2", &spec.TopologyNode{})
			_ = n.DeleteTopologyDevice("switch2", true)
		}
	}()
	go func() {
		defer wg.Done()
		for i := 0; i < 50; i++ {
			if _, err := n.GetAbstractNode("switch1"); err != nil {
				t.Error(err)
				return
			}
		}
	}()
	wg.Wait()
}

// Resolving a node never writes its secrets into the authored spec: the raw
// node-scope read still returns the ${secret:} reference afterwards.
func TestNodeSpecView_ResolutionLeavesAuthoredSpec(t *testing.T) {
	n := loadViewTestNetwork(t)
	if err := n.SetSecret("SW1_PASS", "plain"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(n.specDir, "nodes", "switch1.json"),
		[]byte(`{"zone":"amer","mgmt_ip":"192.0.2.1","loopback_ip":"10.0.0.1","ssh_user":"admin","ssh_pass":"${secret:SW1_PASS}"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	buildSwitch1(t, n)

	got, err := n.GetSSHCredentialsAt(spec.ScopeNode, "switch1")
	if err != nil {
		t.Fatal(err)
	}
	if got.SSHPass != "${secret:SW1_PASS}" {
		t.Errorf("authored node ssh_pass reads back as %q after resolution, want the reference", got.SSHPass)
	}
}

// Specs added after the node was built — looked up by name, and by VNI — are
// visible to its next operation.
func TestNodeSpecView_AddedSpec(t *testing.T) {
	n := loadViewTestNetwork(t)
	dev := buildSwitch1(t, n)

	if err := n.CreateService("", "", "TRANSIT", &spec.ServiceSpec{ServiceType: "routed"}); err != nil {
		t.Fatal(err)
	}
	if err := n.CreateMACVPN("", "", "BLUE", &spec.MACVPNSpec{VNI: 2000, VlanID: 200}); err != nil {
		t.Fatal(err)
	}

	startOperation(t, dev)
	if _, err := dev.GetService("TRANSIT"); err != nil {
		t.Errorf("service added after the node was built: %v", err)
	}
	if name, def := dev.FindMACVPNByVNI(2000); name != "BLUE" || def == nil {
		t.Errorf("FindMACVPNByVNI(2000) = %q, want the MAC-VPN added after the node was built", name)
	}
}
