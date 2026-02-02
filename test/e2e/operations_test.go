//go:build e2e

package e2e_test

import (
	"context"
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/network"
	"github.com/newtron-network/newtron/pkg/operations"
)

// leafNodeName returns the name of a leaf node from the topology.
func leafNodeName(t *testing.T) string {
	t.Helper()

	nodes := testutil.LabSonicNodes(t)
	for _, n := range nodes {
		if len(n.Name) >= 4 && n.Name[:4] == "leaf" {
			return n.Name
		}
	}
	if len(nodes) > 0 {
		return nodes[0].Name
	}
	t.Fatal("no lab nodes available")
	return ""
}

// spineNodeName returns the name of a spine node from the topology.
func spineNodeName(t *testing.T) string {
	t.Helper()

	nodes := testutil.LabSonicNodes(t)
	for _, n := range nodes {
		if len(n.Name) >= 5 && n.Name[:5] == "spine" {
			return n.Name
		}
	}
	t.Fatal("no spine node available")
	return ""
}

// findFreePhysicalInterface finds a physical interface that is not a LAG member,
// has no service bound, and has no IP addresses on the given device.
func findFreePhysicalInterface(t *testing.T, dev *network.Device) string {
	t.Helper()

	for _, name := range dev.ListInterfaces() {
		if dev.InterfaceHasService(name) {
			continue
		}
		intf, err := dev.GetInterface(name)
		if err != nil {
			continue
		}
		if intf.IsPhysical() && !intf.IsLAGMember() && len(intf.IPAddresses()) == 0 && intf.VRF() == "" {
			return name
		}
	}
	return ""
}

// findFreePhysicalInterfaces returns up to count free physical interfaces.
func findFreePhysicalInterfaces(t *testing.T, dev *network.Device, count int) []string {
	t.Helper()

	var result []string
	for _, name := range dev.ListInterfaces() {
		if dev.InterfaceHasService(name) {
			continue
		}
		intf, err := dev.GetInterface(name)
		if err != nil {
			continue
		}
		if intf.IsPhysical() && !intf.IsLAGMember() && len(intf.IPAddresses()) == 0 && intf.VRF() == "" {
			result = append(result, name)
			if len(result) >= count {
				return result
			}
		}
	}
	return result
}

// =============================================================================
// VLAN Operations
// =============================================================================

func TestE2E_CreateVLAN(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "VLAN", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)
	op := &operations.CreateVLANOp{
		ID:   500,
		Desc: "e2e-test-vlan",
	}
	if err := op.Validate(ctx, dev); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute: %v", err)
	}

	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		return d.DeleteVLAN(ctx, 500)
	})

	verifyDev := testutil.LabConnectedDevice(t, nodeName)

	if !verifyDev.VLANExists(500) {
		t.Fatal("VLAN 500 should exist after creation")
	}

	vlan, err := verifyDev.GetVLAN(500)
	if err != nil {
		t.Fatalf("getting VLAN 500: %v", err)
	}
	if vlan.ID != 500 {
		t.Errorf("VLAN ID = %d, want 500", vlan.ID)
	}
}

func TestE2E_DeleteVLAN(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "VLAN", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)
	createOp := &operations.CreateVLANOp{
		ID:   501,
		Desc: "e2e-delete-test",
	}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create: %v", err)
	}

	checkDev := testutil.LabConnectedDevice(t, nodeName)
	if !checkDev.VLANExists(501) {
		t.Fatal("VLAN 501 should exist after creation")
	}

	delDev := testutil.LabLockedDevice(t, nodeName)
	deleteOp := &operations.DeleteVLANOp{ID: 501}
	if err := deleteOp.Validate(ctx, delDev); err != nil {
		t.Fatalf("validate delete: %v", err)
	}
	if err := deleteOp.Execute(ctx, delDev); err != nil {
		t.Fatalf("execute delete: %v", err)
	}

	verifyDev := testutil.LabConnectedDevice(t, nodeName)
	if verifyDev.VLANExists(501) {
		t.Fatal("VLAN 501 should not exist after deletion")
	}
}

func TestE2E_AddVLANMember(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "VLAN", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Create VLAN first
	dev := testutil.LabLockedDevice(t, nodeName)
	createOp := &operations.CreateVLANOp{ID: 502, Desc: "e2e-member-test"}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create VLAN: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create VLAN: %v", err)
	}

	// Find a free interface for membership
	memberDev := testutil.LabLockedDevice(t, nodeName)
	port := findFreePhysicalInterface(t, memberDev)
	if port == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface for VLAN member")
	}

	// Cleanup: remove member then VLAN (registered in creation order, runs in reverse)
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "VLAN_MEMBER|Vlan502|"+port)
		client.Del(c, "VLAN|Vlan502")
	})

	addOp := &operations.AddVLANMemberOp{
		VLANID: 502,
		Port:   port,
		Tagged: true,
	}
	if err := addOp.Validate(ctx, memberDev); err != nil {
		t.Fatalf("validate add member: %v", err)
	}
	if err := addOp.Execute(ctx, memberDev); err != nil {
		t.Fatalf("execute add member: %v", err)
	}

	// Verify
	testutil.AssertConfigDBEntry(t, nodeName, "VLAN_MEMBER", "Vlan502|"+port, map[string]string{
		"tagging_mode": "tagged",
	})
}

func TestE2E_RemoveVLANMember(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "VLAN", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Create VLAN
	dev := testutil.LabLockedDevice(t, nodeName)
	createOp := &operations.CreateVLANOp{ID: 503, Desc: "e2e-rm-member"}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create VLAN: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create VLAN: %v", err)
	}

	// Add member
	addDev := testutil.LabLockedDevice(t, nodeName)
	port := findFreePhysicalInterface(t, addDev)
	if port == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface")
	}

	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "VLAN_MEMBER|Vlan503|"+port)
		client.Del(c, "VLAN|Vlan503")
	})

	addOp := &operations.AddVLANMemberOp{VLANID: 503, Port: port, Tagged: false}
	if err := addOp.Validate(ctx, addDev); err != nil {
		t.Fatalf("validate add: %v", err)
	}
	if err := addOp.Execute(ctx, addDev); err != nil {
		t.Fatalf("execute add: %v", err)
	}

	// Verify member exists
	testutil.AssertConfigDBEntryExists(t, nodeName, "VLAN_MEMBER", "Vlan503|"+port)

	// Remove member
	rmDev := testutil.LabLockedDevice(t, nodeName)
	rmOp := &operations.RemoveVLANMemberOp{VLANID: 503, Port: port}
	if err := rmOp.Validate(ctx, rmDev); err != nil {
		t.Fatalf("validate remove: %v", err)
	}
	if err := rmOp.Execute(ctx, rmDev); err != nil {
		t.Fatalf("execute remove: %v", err)
	}

	// Verify member is gone
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "VLAN_MEMBER", "Vlan503|"+port)
}

func TestE2E_ConfigureSVI(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "VLAN", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Create VLAN
	dev := testutil.LabLockedDevice(t, nodeName)
	vlanOp := &operations.CreateVLANOp{ID: 504, Desc: "e2e-svi-test"}
	if err := vlanOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create VLAN: %v", err)
	}
	if err := vlanOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create VLAN: %v", err)
	}

	// Create VRF
	vrfDev := testutil.LabLockedDevice(t, nodeName)
	vrfOp := &operations.CreateVRFOp{VRFName: "Vrf_e2e_svi"}
	if err := vrfOp.Validate(ctx, vrfDev); err != nil {
		t.Fatalf("validate create VRF: %v", err)
	}
	if err := vrfOp.Execute(ctx, vrfDev); err != nil {
		t.Fatalf("execute create VRF: %v", err)
	}

	// Cleanup via Redis (reverse dependency order)
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "VLAN_INTERFACE|Vlan504|10.99.1.1/24")
		client.Del(c, "VLAN_INTERFACE|Vlan504")
		client.Del(c, "VRF|Vrf_e2e_svi")
		client.Del(c, "VLAN|Vlan504")
	})

	// Configure SVI
	sviDev := testutil.LabLockedDevice(t, nodeName)
	sviOp := &operations.ConfigureSVIOp{
		VLANID:    504,
		VRF:       "Vrf_e2e_svi",
		IPAddress: "10.99.1.1/24",
	}
	if err := sviOp.Validate(ctx, sviDev); err != nil {
		t.Fatalf("validate SVI: %v", err)
	}
	if err := sviOp.Execute(ctx, sviDev); err != nil {
		t.Fatalf("execute SVI: %v", err)
	}

	// Verify VLAN_INTERFACE entries
	testutil.AssertConfigDBEntry(t, nodeName, "VLAN_INTERFACE", "Vlan504", map[string]string{
		"vrf_name": "Vrf_e2e_svi",
	})
	testutil.AssertConfigDBEntryExists(t, nodeName, "VLAN_INTERFACE", "Vlan504|10.99.1.1/24")
}

// =============================================================================
// LAG Operations
// =============================================================================

func TestE2E_CreateLAG(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "LAG", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)

	memberIface := findFreePhysicalInterface(t, dev)
	if memberIface == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface for LAG member")
	}

	op := &operations.CreateLAGOp{
		LAGName:  "PortChannel200",
		Members:  []string{memberIface},
		MTU:      9100,
		MinLinks: 1,
	}
	if err := op.Validate(ctx, dev); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute: %v", err)
	}

	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		return d.DeletePortChannel(ctx, "PortChannel200")
	})

	verifyDev := testutil.LabConnectedDevice(t, nodeName)

	if !verifyDev.PortChannelExists("PortChannel200") {
		t.Fatal("PortChannel200 should exist after creation")
	}

	pc, err := verifyDev.GetPortChannel("PortChannel200")
	if err != nil {
		t.Fatalf("getting PortChannel200: %v", err)
	}
	if pc.Name != "PortChannel200" {
		t.Errorf("PortChannel name = %q, want %q", pc.Name, "PortChannel200")
	}
}

func TestE2E_DeleteLAG(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "LAG", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Create LAG
	dev := testutil.LabLockedDevice(t, nodeName)
	port := findFreePhysicalInterface(t, dev)
	if port == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface")
	}

	createOp := &operations.CreateLAGOp{
		LAGName: "PortChannel201",
		Members: []string{port},
	}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create: %v", err)
	}

	// Verify it exists
	checkDev := testutil.LabConnectedDevice(t, nodeName)
	if !checkDev.PortChannelExists("PortChannel201") {
		t.Fatal("PortChannel201 should exist after creation")
	}

	// Delete LAG
	delDev := testutil.LabLockedDevice(t, nodeName)
	delOp := &operations.DeleteLAGOp{LAGName: "PortChannel201"}
	if err := delOp.Validate(ctx, delDev); err != nil {
		t.Fatalf("validate delete: %v", err)
	}
	if err := delOp.Execute(ctx, delDev); err != nil {
		t.Fatalf("execute delete: %v", err)
	}

	// Verify it's gone
	verifyDev := testutil.LabConnectedDevice(t, nodeName)
	if verifyDev.PortChannelExists("PortChannel201") {
		t.Fatal("PortChannel201 should not exist after deletion")
	}
}

func TestE2E_AddLAGMember(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "LAG", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Need 2 free interfaces
	dev := testutil.LabLockedDevice(t, nodeName)
	ports := findFreePhysicalInterfaces(t, dev, 2)
	if len(ports) < 2 {
		testutil.TrackComment(t, "need at least 2 free physical interfaces")
		t.Skip("need at least 2 free physical interfaces")
	}

	// Create LAG with first member
	createOp := &operations.CreateLAGOp{
		LAGName: "PortChannel202",
		Members: []string{ports[0]},
	}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create: %v", err)
	}

	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		return d.DeletePortChannel(ctx, "PortChannel202")
	})

	// Add second member
	addDev := testutil.LabLockedDevice(t, nodeName)
	addOp := &operations.AddLAGMemberOp{
		LAGName: "PortChannel202",
		Member:  ports[1],
	}
	if err := addOp.Validate(ctx, addDev); err != nil {
		t.Fatalf("validate add member: %v", err)
	}
	if err := addOp.Execute(ctx, addDev); err != nil {
		t.Fatalf("execute add member: %v", err)
	}

	// Verify second member exists
	testutil.AssertConfigDBEntryExists(t, nodeName, "PORTCHANNEL_MEMBER", "PortChannel202|"+ports[1])
}

func TestE2E_RemoveLAGMember(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "LAG", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Need 2 free interfaces
	dev := testutil.LabLockedDevice(t, nodeName)
	ports := findFreePhysicalInterfaces(t, dev, 2)
	if len(ports) < 2 {
		testutil.TrackComment(t, "need at least 2 free physical interfaces")
		t.Skip("need at least 2 free physical interfaces")
	}

	// Create LAG with both members
	createOp := &operations.CreateLAGOp{
		LAGName: "PortChannel203",
		Members: []string{ports[0], ports[1]},
	}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create: %v", err)
	}

	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		return d.DeletePortChannel(ctx, "PortChannel203")
	})

	// Verify both members exist
	testutil.AssertConfigDBEntryExists(t, nodeName, "PORTCHANNEL_MEMBER", "PortChannel203|"+ports[0])
	testutil.AssertConfigDBEntryExists(t, nodeName, "PORTCHANNEL_MEMBER", "PortChannel203|"+ports[1])

	// Remove second member
	rmDev := testutil.LabLockedDevice(t, nodeName)
	rmOp := &operations.RemoveLAGMemberOp{
		LAGName: "PortChannel203",
		Member:  ports[1],
	}
	if err := rmOp.Validate(ctx, rmDev); err != nil {
		t.Fatalf("validate remove: %v", err)
	}
	if err := rmOp.Execute(ctx, rmDev); err != nil {
		t.Fatalf("execute remove: %v", err)
	}

	// Verify: first member remains, second is gone
	testutil.AssertConfigDBEntryExists(t, nodeName, "PORTCHANNEL_MEMBER", "PortChannel203|"+ports[0])
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "PORTCHANNEL_MEMBER", "PortChannel203|"+ports[1])
}

// =============================================================================
// Interface Operations
// =============================================================================

func TestE2E_ConfigureInterface(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Interface", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)
	targetIface := findFreePhysicalInterface(t, dev)
	if targetIface == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface available")
	}

	op := &operations.ConfigureInterfaceOp{
		Interface: targetIface,
		Desc:      "e2e-test-description",
		MTU:       9000,
	}
	if err := op.Validate(ctx, dev); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute: %v", err)
	}

	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		restoreIntf, err := d.GetInterface(targetIface)
		if err != nil {
			return nil, err
		}
		return restoreIntf.Configure(ctx, network.InterfaceConfig{MTU: 9100})
	})

	// Verify CONFIG_DB has the correct values. MTU is verified via CONFIG_DB
	// because the virtual switch ASIC simulator may not apply MTU changes to
	// the kernel, so STATE_DB (operational state) can lag behind.
	testutil.AssertConfigDBEntry(t, nodeName, "PORT", targetIface, map[string]string{
		"mtu":         "9000",
		"description": "e2e-test-description",
	})
}

func TestE2E_SetInterfaceVRF(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Interface", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Create VRF first
	dev := testutil.LabLockedDevice(t, nodeName)
	vrfOp := &operations.CreateVRFOp{VRFName: "Vrf_e2e_iface"}
	if err := vrfOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create VRF: %v", err)
	}
	if err := vrfOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create VRF: %v", err)
	}

	// Find free interface
	ifaceDev := testutil.LabLockedDevice(t, nodeName)
	port := findFreePhysicalInterface(t, ifaceDev)
	if port == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface")
	}

	// Cleanup via Redis
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "INTERFACE|"+port+"|10.99.3.1/30")
		client.Del(c, "INTERFACE|"+port)
		client.Del(c, "VRF|Vrf_e2e_iface")
	})

	setOp := &operations.SetInterfaceVRFOp{
		Interface: port,
		VRF:       "Vrf_e2e_iface",
		IPAddress: "10.99.3.1/30",
	}
	if err := setOp.Validate(ctx, ifaceDev); err != nil {
		t.Fatalf("validate set VRF: %v", err)
	}
	if err := setOp.Execute(ctx, ifaceDev); err != nil {
		t.Fatalf("execute set VRF: %v", err)
	}

	// Verify
	testutil.AssertConfigDBEntry(t, nodeName, "INTERFACE", port, map[string]string{
		"vrf_name": "Vrf_e2e_iface",
	})
	testutil.AssertConfigDBEntryExists(t, nodeName, "INTERFACE", port+"|10.99.3.1/30")
}

func TestE2E_SetInterfaceIP(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Interface", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)
	port := findFreePhysicalInterface(t, dev)
	if port == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface")
	}

	// Cleanup via Redis
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "INTERFACE|"+port+"|10.99.2.1/30")
	})

	op := &operations.SetInterfaceIPOp{
		Interface: port,
		IPAddress: "10.99.2.1/30",
	}
	if err := op.Validate(ctx, dev); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Verify
	testutil.AssertConfigDBEntryExists(t, nodeName, "INTERFACE", port+"|10.99.2.1/30")
}

func TestE2E_BindACL(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "ACL", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Create ACL table first
	dev := testutil.LabLockedDevice(t, nodeName)
	createOp := &operations.CreateACLTableOp{
		TableName: "E2E_BIND_ACL",
		Type:      "L3",
		Stage:     "ingress",
		Desc:      "e2e bind test",
	}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create ACL: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create ACL: %v", err)
	}

	// Find free interface
	bindDev := testutil.LabLockedDevice(t, nodeName)
	port := findFreePhysicalInterface(t, bindDev)
	if port == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface")
	}

	// Cleanup
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "ACL_TABLE|E2E_BIND_ACL")
	})

	bindOp := &operations.BindACLOp{
		Interface: port,
		ACLName:   "E2E_BIND_ACL",
		Direction: "ingress",
	}
	if err := bindOp.Validate(ctx, bindDev); err != nil {
		t.Fatalf("validate bind: %v", err)
	}
	if err := bindOp.Execute(ctx, bindDev); err != nil {
		t.Fatalf("execute bind: %v", err)
	}

	// Verify ACL_TABLE has ports field updated
	testutil.AssertConfigDBEntry(t, nodeName, "ACL_TABLE", "E2E_BIND_ACL", map[string]string{
		"ports": port,
		"stage": "ingress",
	})
}

// =============================================================================
// ACL Operations
// =============================================================================

func TestE2E_CreateACLTable(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "ACL", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)
	createOp := &operations.CreateACLTableOp{
		TableName: "E2E_TEST_ACL",
		Type:      "L3",
		Stage:     "ingress",
		Desc:      "e2e test ACL",
	}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create table: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create table: %v", err)
	}

	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		return d.DeleteACLTable(ctx, "E2E_TEST_ACL")
	})

	verifyDev := testutil.LabConnectedDevice(t, nodeName)

	if !verifyDev.ACLTableExists("E2E_TEST_ACL") {
		t.Fatal("ACL table E2E_TEST_ACL should exist after creation")
	}

	aclInfo, err := verifyDev.GetACLTable("E2E_TEST_ACL")
	if err != nil {
		t.Fatalf("getting ACL table: %v", err)
	}
	if aclInfo.Type != "L3" {
		t.Errorf("ACL type = %q, want %q", aclInfo.Type, "L3")
	}
}

func TestE2E_AddACLRule(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "ACL", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Create ACL table
	dev := testutil.LabLockedDevice(t, nodeName)
	createOp := &operations.CreateACLTableOp{
		TableName: "E2E_RULE_ACL",
		Type:      "L3",
		Stage:     "ingress",
		Desc:      "e2e rule test",
	}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create: %v", err)
	}

	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "ACL_RULE|E2E_RULE_ACL|RULE_200")
		client.Del(c, "ACL_TABLE|E2E_RULE_ACL")
	})

	// Add rule with match conditions
	ruleDev := testutil.LabLockedDevice(t, nodeName)
	ruleOp := &operations.AddACLRuleOp{
		TableName: "E2E_RULE_ACL",
		RuleName:  "RULE_200",
		Priority:  200,
		Action:    "DROP",
		SrcIP:     "192.168.0.0/16",
	}
	if err := ruleOp.Validate(ctx, ruleDev); err != nil {
		t.Fatalf("validate rule: %v", err)
	}
	if err := ruleOp.Execute(ctx, ruleDev); err != nil {
		t.Fatalf("execute rule: %v", err)
	}

	// Verify rule fields
	verifyDev := testutil.LabConnectedDevice(t, nodeName)
	cdb := verifyDev.ConfigDB()
	rule, ok := cdb.ACLRule["E2E_RULE_ACL|RULE_200"]
	if !ok {
		t.Fatal("ACL rule E2E_RULE_ACL|RULE_200 not found")
	}
	if rule.Priority != "200" {
		t.Errorf("priority = %q, want %q", rule.Priority, "200")
	}
	if rule.PacketAction != "DROP" {
		t.Errorf("action = %q, want %q", rule.PacketAction, "DROP")
	}
	if rule.SrcIP != "192.168.0.0/16" {
		t.Errorf("src_ip = %q, want %q", rule.SrcIP, "192.168.0.0/16")
	}
}

func TestE2E_DeleteACLRule(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "ACL", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Create ACL table + rule
	dev := testutil.LabLockedDevice(t, nodeName)
	createOp := &operations.CreateACLTableOp{
		TableName: "E2E_DELRULE_ACL",
		Type:      "L3",
		Stage:     "ingress",
	}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create: %v", err)
	}

	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "ACL_RULE|E2E_DELRULE_ACL|RULE_300")
		client.Del(c, "ACL_TABLE|E2E_DELRULE_ACL")
	})

	ruleDev := testutil.LabLockedDevice(t, nodeName)
	addOp := &operations.AddACLRuleOp{
		TableName: "E2E_DELRULE_ACL",
		RuleName:  "RULE_300",
		Priority:  300,
		Action:    "FORWARD",
	}
	if err := addOp.Validate(ctx, ruleDev); err != nil {
		t.Fatalf("validate add rule: %v", err)
	}
	if err := addOp.Execute(ctx, ruleDev); err != nil {
		t.Fatalf("execute add rule: %v", err)
	}

	// Verify rule exists
	testutil.AssertConfigDBEntryExists(t, nodeName, "ACL_RULE", "E2E_DELRULE_ACL|RULE_300")

	// Delete rule
	delDev := testutil.LabLockedDevice(t, nodeName)
	delOp := &operations.DeleteACLRuleOp{
		TableName: "E2E_DELRULE_ACL",
		RuleName:  "RULE_300",
	}
	if err := delOp.Validate(ctx, delDev); err != nil {
		t.Fatalf("validate delete rule: %v", err)
	}
	if err := delOp.Execute(ctx, delDev); err != nil {
		t.Fatalf("execute delete rule: %v", err)
	}

	// Verify rule is gone but table remains
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "ACL_RULE", "E2E_DELRULE_ACL|RULE_300")
	testutil.AssertConfigDBEntryExists(t, nodeName, "ACL_TABLE", "E2E_DELRULE_ACL")
}

func TestE2E_DeleteACLTable(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "ACL", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Create ACL table
	dev := testutil.LabLockedDevice(t, nodeName)
	createOp := &operations.CreateACLTableOp{
		TableName: "E2E_DELTABLE_ACL",
		Type:      "L3",
		Stage:     "egress",
	}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create: %v", err)
	}

	// Verify it exists
	checkDev := testutil.LabConnectedDevice(t, nodeName)
	if !checkDev.ACLTableExists("E2E_DELTABLE_ACL") {
		t.Fatal("ACL table should exist after creation")
	}

	// Delete
	delDev := testutil.LabLockedDevice(t, nodeName)
	delOp := &operations.DeleteACLTableOp{TableName: "E2E_DELTABLE_ACL"}
	if err := delOp.Validate(ctx, delDev); err != nil {
		t.Fatalf("validate delete: %v", err)
	}
	if err := delOp.Execute(ctx, delDev); err != nil {
		t.Fatalf("execute delete: %v", err)
	}

	// Verify it's gone
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "ACL_TABLE", "E2E_DELTABLE_ACL")
}

// =============================================================================
// EVPN Operations
// =============================================================================

func TestE2E_CreateVRF(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "EVPN", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)

	l3vni := 0
	if dev.VTEPExists() && dev.BGPConfigured() {
		l3vni = 99999
	}

	op := &operations.CreateVRFOp{
		VRFName: "Vrf_e2e_test",
		L3VNI:   l3vni,
	}
	if err := op.Validate(ctx, dev); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute: %v", err)
	}

	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		if l3vni > 0 {
			client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_99999_Vrf_e2e_test")
		}
		client.Del(c, "VRF|Vrf_e2e_test")
	})

	verifyDev := testutil.LabConnectedDevice(t, nodeName)

	if !verifyDev.VRFExists("Vrf_e2e_test") {
		t.Fatal("VRF Vrf_e2e_test should exist after creation")
	}

	vrfInfo, err := verifyDev.GetVRF("Vrf_e2e_test")
	if err != nil {
		t.Fatalf("getting VRF: %v", err)
	}
	if l3vni > 0 && vrfInfo.L3VNI != l3vni {
		t.Errorf("VRF L3VNI = %d, want %d", vrfInfo.L3VNI, l3vni)
	}
}

func TestE2E_DeleteVRF(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "EVPN", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	// Create VRF (without L3VNI for simpler cleanup)
	dev := testutil.LabLockedDevice(t, nodeName)
	createOp := &operations.CreateVRFOp{VRFName: "Vrf_e2e_delete"}
	if err := createOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create: %v", err)
	}
	if err := createOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create: %v", err)
	}

	// Verify it exists
	checkDev := testutil.LabConnectedDevice(t, nodeName)
	if !checkDev.VRFExists("Vrf_e2e_delete") {
		t.Fatal("VRF should exist after creation")
	}

	// Delete VRF
	delDev := testutil.LabLockedDevice(t, nodeName)
	delOp := &operations.DeleteVRFOp{VRFName: "Vrf_e2e_delete"}
	if err := delOp.Validate(ctx, delDev); err != nil {
		t.Fatalf("validate delete: %v", err)
	}
	if err := delOp.Execute(ctx, delDev); err != nil {
		t.Fatalf("execute delete: %v", err)
	}

	// Verify it's gone
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "VRF", "Vrf_e2e_delete")
}

func TestE2E_CreateVTEP(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "EVPN", "spine")

	// Use a spine node which has no VTEP configured
	nodeName := spineNodeName(t)
	ctx := testutil.LabContext(t)

	// Verify spine is reachable before proceeding
	if _, err := testutil.TryLabConnectedDevice(t, nodeName); err != nil {
		testutil.TrackComment(t, "spine unreachable")
		t.Skipf("skipping: spine %s unreachable: %v", nodeName, err)
	}
	dev := testutil.LabLockedDevice(t, nodeName)

	// Spine should not have VTEP
	if dev.VTEPExists() {
		testutil.TrackComment(t, "spine already has VTEP configured")
		t.Skip("spine already has VTEP configured")
	}

	// Get spine's loopback IP for VTEP source
	cdb := dev.ConfigDB()
	srcIP := ""
	for key := range cdb.LoopbackInterface {
		if len(key) > 10 && key[:10] == "Loopback0|" {
			// Extract IP from "Loopback0|x.x.x.x/32"
			parts := key[10:]
			if idx := len(parts) - 3; idx > 0 && parts[idx:] == "/32" {
				srcIP = parts[:idx]
			}
		}
	}
	if srcIP == "" {
		testutil.TrackComment(t, "could not determine spine loopback IP")
		t.Skip("could not determine spine loopback IP")
	}

	// Cleanup via Redis
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "VXLAN_EVPN_NVO|nvo1")
		client.Del(c, "VXLAN_TUNNEL|e2e_vtep")
	})

	op := &operations.CreateVTEPOp{
		VTEPName: "e2e_vtep",
		SourceIP: srcIP,
	}
	if err := op.Validate(ctx, dev); err != nil {
		t.Fatalf("validate: %v", err)
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Verify
	testutil.AssertConfigDBEntry(t, nodeName, "VXLAN_TUNNEL", "e2e_vtep", map[string]string{
		"src_ip": srcIP,
	})
	testutil.AssertConfigDBEntry(t, nodeName, "VXLAN_EVPN_NVO", "nvo1", map[string]string{
		"source_vtep": "e2e_vtep",
	})
}

func TestE2E_MapL2VNI(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "EVPN", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)

	// VTEP and BGP must be configured for EVPN
	if !dev.VTEPExists() || !dev.BGPConfigured() {
		testutil.TrackComment(t, "VTEP or BGP not configured")
		t.Skip("VTEP or BGP not configured on leaf")
	}

	// Create VLAN first
	vlanOp := &operations.CreateVLANOp{ID: 505, Desc: "e2e-l2vni"}
	if err := vlanOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create VLAN: %v", err)
	}
	if err := vlanOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create VLAN: %v", err)
	}

	// Cleanup via Redis
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "SUPPRESS_VLAN_NEIGH|Vlan505")
		client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10505_Vlan505")
		client.Del(c, "VLAN|Vlan505")
	})

	// Map L2VNI
	mapDev := testutil.LabLockedDevice(t, nodeName)
	mapOp := &operations.MapL2VNIOp{
		VLANID:         505,
		VNI:            10505,
		ARPSuppression: true,
	}
	if err := mapOp.Validate(ctx, mapDev); err != nil {
		t.Fatalf("validate map: %v", err)
	}
	if err := mapOp.Execute(ctx, mapDev); err != nil {
		t.Fatalf("execute map: %v", err)
	}

	// Verify VXLAN_TUNNEL_MAP
	testutil.AssertConfigDBEntry(t, nodeName, "VXLAN_TUNNEL_MAP", "vtep1|map_10505_Vlan505", map[string]string{
		"vlan": "Vlan505",
		"vni":  "10505",
	})
	// Verify ARP suppression
	testutil.AssertConfigDBEntry(t, nodeName, "SUPPRESS_VLAN_NEIGH", "Vlan505", map[string]string{
		"suppress": "on",
	})
}

func TestE2E_UnmapL2VNI(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "EVPN", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)

	if !dev.VTEPExists() || !dev.BGPConfigured() {
		testutil.TrackComment(t, "VTEP or BGP not configured")
		t.Skip("VTEP or BGP not configured on leaf")
	}

	// Create VLAN + map L2VNI
	vlanOp := &operations.CreateVLANOp{ID: 506, Desc: "e2e-unmap"}
	if err := vlanOp.Validate(ctx, dev); err != nil {
		t.Fatalf("validate create VLAN: %v", err)
	}
	if err := vlanOp.Execute(ctx, dev); err != nil {
		t.Fatalf("execute create VLAN: %v", err)
	}

	// Cleanup via Redis
	t.Cleanup(func() {
		client := testutil.LabRedisClient(t, nodeName, 4)
		c := context.Background()
		client.Del(c, "SUPPRESS_VLAN_NEIGH|Vlan506")
		client.Del(c, "VXLAN_TUNNEL_MAP|vtep1|map_10506_Vlan506")
		client.Del(c, "VLAN|Vlan506")
	})

	mapDev := testutil.LabLockedDevice(t, nodeName)
	mapOp := &operations.MapL2VNIOp{
		VLANID:         506,
		VNI:            10506,
		ARPSuppression: true,
	}
	if err := mapOp.Validate(ctx, mapDev); err != nil {
		t.Fatalf("validate map: %v", err)
	}
	if err := mapOp.Execute(ctx, mapDev); err != nil {
		t.Fatalf("execute map: %v", err)
	}

	// Verify mapping exists
	testutil.AssertConfigDBEntryExists(t, nodeName, "VXLAN_TUNNEL_MAP", "vtep1|map_10506_Vlan506")

	// Unmap L2VNI
	unmapDev := testutil.LabLockedDevice(t, nodeName)
	unmapOp := &operations.UnmapL2VNIOp{VLANID: 506}
	if err := unmapOp.Validate(ctx, unmapDev); err != nil {
		t.Fatalf("validate unmap: %v", err)
	}
	if err := unmapOp.Execute(ctx, unmapDev); err != nil {
		t.Fatalf("execute unmap: %v", err)
	}

	// Verify mapping is gone
	testutil.AssertConfigDBEntryAbsent(t, nodeName, "VXLAN_TUNNEL_MAP", "vtep1|map_10506_Vlan506")
}

// =============================================================================
// Service Operations
// =============================================================================

func TestE2E_ApplyService(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Service", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)

	// customer-l3 uses ipvpn with L3 VNI which requires VTEP
	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; customer-l3 requires EVPN")
	}

	targetIface := findFreePhysicalInterface(t, dev)
	if targetIface == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface for service binding")
	}

	op := operations.NewApplyServiceOp(targetIface, "customer-l3", "10.99.99.1/30")
	if err := op.Validate(ctx, dev); err != nil {
		t.Logf("service validate failed (expected if service not in lab specs): %v", err)
		testutil.TrackComment(t, "service not available in lab specs")
		t.Skip("customer-l3 service not available in lab specs")
	}
	if err := op.Execute(ctx, dev); err != nil {
		t.Fatalf("execute: %v", err)
	}

	// Cleanup: remove service via fresh connection
	testutil.LabCleanupChanges(t, nodeName, func(ctx context.Context, d *network.Device) (*network.ChangeSet, error) {
		cleanIntf, err := d.GetInterface(targetIface)
		if err != nil {
			return nil, err
		}
		return cleanIntf.RemoveService(ctx)
	})

	// Verify via fresh connection
	verifyDev := testutil.LabConnectedDevice(t, nodeName)
	verifyIntf, err := verifyDev.GetInterface(targetIface)
	if err != nil {
		t.Fatalf("getting interface for verification: %v", err)
	}

	if !verifyIntf.HasService() {
		t.Fatal("interface should have a service bound")
	}
	if verifyIntf.ServiceName() != "customer-l3" {
		t.Errorf("service name = %q, want %q", verifyIntf.ServiceName(), "customer-l3")
	}
}

func TestE2E_RemoveService(t *testing.T) {
	testutil.SkipIfNoLab(t)
	testutil.Track(t, "Service", "leaf")

	nodeName := leafNodeName(t)
	ctx := testutil.LabContext(t)

	dev := testutil.LabLockedDevice(t, nodeName)

	// customer-l3 uses ipvpn with L3 VNI which requires VTEP
	if !dev.VTEPExists() {
		testutil.TrackComment(t, "VTEP not configured")
		t.Skip("VTEP not configured on leaf; customer-l3 requires EVPN")
	}

	targetIface := findFreePhysicalInterface(t, dev)
	if targetIface == "" {
		testutil.TrackComment(t, "no free physical interface")
		t.Skip("no free physical interface for service binding")
	}

	applyOp := operations.NewApplyServiceOp(targetIface, "customer-l3", "10.99.98.1/30")
	if err := applyOp.Validate(ctx, dev); err != nil {
		testutil.TrackComment(t, "service not available in lab specs")
		t.Skip("customer-l3 service not available in lab specs")
	}
	if err := applyOp.Execute(ctx, dev); err != nil {
		t.Fatalf("applying service: %v", err)
	}

	// Remove service via operations on a fresh locked device
	rmDev := testutil.LabLockedDevice(t, nodeName)
	removeOp := operations.NewRemoveServiceOp(targetIface)
	if err := removeOp.Validate(ctx, rmDev); err != nil {
		t.Fatalf("validate remove: %v", err)
	}
	if err := removeOp.Execute(ctx, rmDev); err != nil {
		t.Fatalf("execute remove: %v", err)
	}

	// Verify service is gone via fresh connection
	verifyDev := testutil.LabConnectedDevice(t, nodeName)
	verifyIntf, err := verifyDev.GetInterface(targetIface)
	if err != nil {
		t.Fatalf("getting interface for verification: %v", err)
	}

	if verifyIntf.HasService() {
		t.Fatalf("interface %s should have no service after removal, but has %q",
			targetIface, verifyIntf.ServiceName())
	}
}
