//go:build integration

package device_test

import (
	"testing"

	"github.com/newtron-network/newtron/internal/testutil"
	"github.com/newtron-network/newtron/pkg/device"
)

func TestConfigDBClientConnect(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	addr := testutil.RedisAddr()
	client := device.NewConfigDBClient(addr)
	defer client.Close()

	if err := client.Connect(); err != nil {
		t.Fatalf("ConfigDBClient.Connect failed: %v", err)
	}
}

func TestConfigDBGetAll(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	addr := testutil.RedisAddr()
	client := device.NewConfigDBClient(addr)
	defer client.Close()

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	db, err := client.GetAll()
	if err != nil {
		t.Fatalf("GetAll failed: %v", err)
	}

	if len(db.Port) != 8 {
		t.Errorf("Port count = %d, want 8", len(db.Port))
	}
	if len(db.VLAN) != 2 {
		t.Errorf("VLAN count = %d, want 2", len(db.VLAN))
	}
	if len(db.VRF) != 1 {
		t.Errorf("VRF count = %d, want 1", len(db.VRF))
	}
	if len(db.BGPNeighbor) != 2 {
		t.Errorf("BGPNeighbor count = %d, want 2", len(db.BGPNeighbor))
	}
}

func TestConfigDBGet(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	addr := testutil.RedisAddr()
	client := device.NewConfigDBClient(addr)
	defer client.Close()

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	vals, err := client.Get("PORT", "Ethernet0")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if vals["admin_status"] != "up" {
		t.Errorf("admin_status = %q, want %q", vals["admin_status"], "up")
	}
	if vals["mtu"] != "9100" {
		t.Errorf("mtu = %q, want %q", vals["mtu"], "9100")
	}
	if vals["speed"] != "40000" {
		t.Errorf("speed = %q, want %q", vals["speed"], "40000")
	}
}

func TestConfigDBSet(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	addr := testutil.RedisAddr()
	client := device.NewConfigDBClient(addr)
	defer client.Close()

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Write a new entry
	fields := map[string]string{
		"vlanid":       "300",
		"description":  "TestVLAN",
		"admin_status": "up",
	}
	if err := client.Set("VLAN", "Vlan300", fields); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Read it back
	vals, err := client.Get("VLAN", "Vlan300")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if vals["vlanid"] != "300" {
		t.Errorf("vlanid = %q, want %q", vals["vlanid"], "300")
	}
	if vals["description"] != "TestVLAN" {
		t.Errorf("description = %q, want %q", vals["description"], "TestVLAN")
	}
	if vals["admin_status"] != "up" {
		t.Errorf("admin_status = %q, want %q", vals["admin_status"], "up")
	}
}

func TestConfigDBDelete(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	addr := testutil.RedisAddr()
	client := device.NewConfigDBClient(addr)
	defer client.Close()

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Create an entry to delete
	fields := map[string]string{"vlanid": "999", "admin_status": "up"}
	if err := client.Set("VLAN", "Vlan999", fields); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Verify it exists
	exists, err := client.Exists("VLAN", "Vlan999")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Fatal("expected entry to exist before delete")
	}

	// Delete it
	if err := client.Delete("VLAN", "Vlan999"); err != nil {
		t.Fatalf("Delete failed: %v", err)
	}

	// Verify it is gone
	exists, err = client.Exists("VLAN", "Vlan999")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("expected entry to not exist after delete")
	}
}

func TestConfigDBDeleteField(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	addr := testutil.RedisAddr()
	client := device.NewConfigDBClient(addr)
	defer client.Close()

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Create an entry with multiple fields
	fields := map[string]string{
		"vlanid":       "500",
		"description":  "TempVLAN",
		"admin_status": "up",
	}
	if err := client.Set("VLAN", "Vlan500", fields); err != nil {
		t.Fatalf("Set failed: %v", err)
	}

	// Delete the description field
	if err := client.DeleteField("VLAN", "Vlan500", "description"); err != nil {
		t.Fatalf("DeleteField failed: %v", err)
	}

	// Read back and verify description is gone
	vals, err := client.Get("VLAN", "Vlan500")
	if err != nil {
		t.Fatalf("Get failed: %v", err)
	}

	if _, ok := vals["description"]; ok {
		t.Error("description field should have been deleted")
	}
	if vals["vlanid"] != "500" {
		t.Errorf("vlanid = %q, want %q (should still exist)", vals["vlanid"], "500")
	}
	if vals["admin_status"] != "up" {
		t.Errorf("admin_status = %q, want %q (should still exist)", vals["admin_status"], "up")
	}
}

func TestConfigDBExists(t *testing.T) {
	testutil.SkipIfNoRedis(t)
	testutil.SetupBothDBs(t)

	addr := testutil.RedisAddr()
	client := device.NewConfigDBClient(addr)
	defer client.Close()

	if err := client.Connect(); err != nil {
		t.Fatalf("Connect failed: %v", err)
	}

	// Existing key
	exists, err := client.Exists("PORT", "Ethernet0")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if !exists {
		t.Error("expected PORT|Ethernet0 to exist")
	}

	// Non-existing key
	exists, err = client.Exists("PORT", "Ethernet99")
	if err != nil {
		t.Fatalf("Exists failed: %v", err)
	}
	if exists {
		t.Error("expected PORT|Ethernet99 to not exist")
	}
}

func TestApplyChangesAdd(t *testing.T) {
	d := testutil.LockedDevice(t)

	changes := []device.ConfigChange{
		{
			Type:  device.ChangeTypeAdd,
			Table: "VLAN",
			Key:   "Vlan400",
			Fields: map[string]string{
				"vlanid":       "400",
				"description":  "NewVLAN",
				"admin_status": "up",
			},
		},
	}

	if err := d.ApplyChanges(changes); err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	// Verify the VLAN was added to ConfigDB (auto-reloaded by ApplyChanges)
	vlan, ok := d.ConfigDB.VLAN["Vlan400"]
	if !ok {
		t.Fatal("Vlan400 not found after ApplyChanges add")
	}
	if vlan.VLANID != "400" {
		t.Errorf("Vlan400 vlanid = %q, want %q", vlan.VLANID, "400")
	}
	if vlan.Description != "NewVLAN" {
		t.Errorf("Vlan400 description = %q, want %q", vlan.Description, "NewVLAN")
	}
}

func TestApplyChangesModify(t *testing.T) {
	d := testutil.LockedDevice(t)

	changes := []device.ConfigChange{
		{
			Type:  device.ChangeTypeModify,
			Table: "VLAN",
			Key:   "Vlan100",
			Fields: map[string]string{
				"description": "ModifiedServers",
			},
		},
	}

	if err := d.ApplyChanges(changes); err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	vlan, ok := d.ConfigDB.VLAN["Vlan100"]
	if !ok {
		t.Fatal("Vlan100 not found after ApplyChanges modify")
	}
	if vlan.Description != "ModifiedServers" {
		t.Errorf("Vlan100 description = %q, want %q", vlan.Description, "ModifiedServers")
	}
	// Original fields should still be present
	if vlan.VLANID != "100" {
		t.Errorf("Vlan100 vlanid = %q, want %q (should be preserved)", vlan.VLANID, "100")
	}
}

func TestApplyChangesDelete(t *testing.T) {
	d := testutil.LockedDevice(t)

	// First verify Vlan200 exists
	if _, ok := d.ConfigDB.VLAN["Vlan200"]; !ok {
		t.Fatal("Vlan200 should exist before delete")
	}

	changes := []device.ConfigChange{
		{
			Type:  device.ChangeTypeDelete,
			Table: "VLAN",
			Key:   "Vlan200",
		},
	}

	if err := d.ApplyChanges(changes); err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	if _, ok := d.ConfigDB.VLAN["Vlan200"]; ok {
		t.Error("Vlan200 should not exist after ApplyChanges delete")
	}
}

func TestReloadAfterChanges(t *testing.T) {
	d := testutil.ConnectedDevice(t)

	// Verify initial state
	if len(d.ConfigDB.VLAN) != 2 {
		t.Fatalf("expected 2 VLANs initially, got %d", len(d.ConfigDB.VLAN))
	}

	// Write a new VLAN directly via Redis (bypassing Device)
	addr := testutil.RedisAddr()
	testutil.WriteSingleEntry(t, addr, 4, "VLAN", "Vlan600", map[string]string{
		"vlanid":       "600",
		"description":  "ExternalVLAN",
		"admin_status": "up",
	})

	// Device should not see it yet
	if _, ok := d.ConfigDB.VLAN["Vlan600"]; ok {
		t.Fatal("Vlan600 should not be visible before Reload")
	}

	// Reload
	ctx := testutil.Context(t)
	if err := d.Reload(ctx); err != nil {
		t.Fatalf("Reload failed: %v", err)
	}

	// Now it should be visible
	vlan, ok := d.ConfigDB.VLAN["Vlan600"]
	if !ok {
		t.Fatal("Vlan600 not found after Reload")
	}
	if vlan.VLANID != "600" {
		t.Errorf("Vlan600 vlanid = %q, want %q", vlan.VLANID, "600")
	}
}

func TestApplyChangesMultiple(t *testing.T) {
	d := testutil.LockedDevice(t)

	changes := []device.ConfigChange{
		{
			Type:  device.ChangeTypeAdd,
			Table: "VLAN",
			Key:   "Vlan700",
			Fields: map[string]string{
				"vlanid":       "700",
				"description":  "BatchVLAN1",
				"admin_status": "up",
			},
		},
		{
			Type:  device.ChangeTypeAdd,
			Table: "VLAN",
			Key:   "Vlan800",
			Fields: map[string]string{
				"vlanid":       "800",
				"description":  "BatchVLAN2",
				"admin_status": "up",
			},
		},
		{
			Type:  device.ChangeTypeModify,
			Table: "VLAN",
			Key:   "Vlan100",
			Fields: map[string]string{
				"description": "BatchModified",
			},
		},
	}

	if err := d.ApplyChanges(changes); err != nil {
		t.Fatalf("ApplyChanges failed: %v", err)
	}

	// Verify Vlan700 was added
	if _, ok := d.ConfigDB.VLAN["Vlan700"]; !ok {
		t.Error("Vlan700 not found after batch apply")
	}

	// Verify Vlan800 was added
	if _, ok := d.ConfigDB.VLAN["Vlan800"]; !ok {
		t.Error("Vlan800 not found after batch apply")
	}

	// Verify Vlan100 was modified
	vlan100, ok := d.ConfigDB.VLAN["Vlan100"]
	if !ok {
		t.Fatal("Vlan100 not found after batch apply")
	}
	if vlan100.Description != "BatchModified" {
		t.Errorf("Vlan100 description = %q, want %q", vlan100.Description, "BatchModified")
	}
}
