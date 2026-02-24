package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Loopback Configuration
// ============================================================================

// ConfigureLoopback creates the Loopback0 interface with the device's loopback IP.
// Reads the IP from the resolved profile — no vars indirection needed.
func (n *Node) ConfigureLoopback(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("configure-loopback", "loopback").Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.configure-loopback")

	loopbackIP := ""
	if n.resolved != nil {
		loopbackIP = n.resolved.LoopbackIP
	}
	if loopbackIP == "" {
		return nil, fmt.Errorf("no loopback IP configured for device %s", n.name)
	}

	// Base entry required for intfmgrd to bind the IP (Update = idempotent create-or-update)
	cs.Update("LOOPBACK_INTERFACE", "Loopback0", map[string]string{})
	cs.Add("LOOPBACK_INTERFACE", fmt.Sprintf("Loopback0|%s/32", loopbackIP), map[string]string{})

	n.applyShadow(cs)
	util.WithDevice(n.name).Infof("Configured Loopback0 with IP %s/32", loopbackIP)
	return cs, nil
}

// RemoveLoopback removes all Loopback0 entries from CONFIG_DB.
// Reverses ConfigureLoopback: deletes base entry and all IP sub-entries.
func (n *Node) RemoveLoopback(ctx context.Context) (*ChangeSet, error) {
	if err := n.precondition("remove-loopback", "loopback").Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.name, "device.remove-loopback")

	configDB := n.ConfigDB()
	if configDB == nil {
		return cs, nil
	}

	// Delete all LOOPBACK_INTERFACE entries for Loopback0 (IP sub-entries first, then base)
	for key := range configDB.LoopbackInterface {
		if key == "Loopback0" || strings.HasPrefix(key, "Loopback0|") {
			cs.Delete("LOOPBACK_INTERFACE", key)
		}
	}

	util.WithDevice(n.name).Infof("Removed Loopback0 configuration")
	return cs, nil
}
