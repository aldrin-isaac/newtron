package node

import (
	"context"
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

// ============================================================================
// Baseline Configuration
// ============================================================================

// ApplyBaseline applies a baseline configlet to the device.
func (n *Node) ApplyBaseline(ctx context.Context, configletName string, vars []string) (*ChangeSet, error) {
	if err := n.precondition("apply-baseline", configletName).Result(); err != nil {
		return nil, err
	}

	// Parse vars into a map
	varMap := make(map[string]string)
	for _, v := range vars {
		parts := strings.SplitN(v, "=", 2)
		if len(parts) == 2 {
			varMap[parts[0]] = parts[1]
		}
	}

	// Add default variables from resolved config
	if n.resolved != nil {
		if _, ok := varMap["loopback_ip"]; !ok {
			varMap["loopback_ip"] = n.resolved.LoopbackIP
		}
		if _, ok := varMap["device_name"]; !ok {
			varMap["device_name"] = n.name
		}
	}

	cs := NewChangeSet(n.name, "device.apply-baseline")

	// Load configlet based on name (simplified - in production would load from file)
	switch configletName {
	case "sonic-baseline":
		// Basic SONiC baseline
		e := updateDeviceMetadata(map[string]string{"hostname": varMap["device_name"]})
		cs.Update(e.Table, e.Key, e.Fields)
		if loopbackIP, ok := varMap["loopback_ip"]; ok && loopbackIP != "" {
			// Base entry required for intfmgrd to bind the IP (Update = idempotent create-or-update)
			cs.Update("LOOPBACK_INTERFACE", "Loopback0", map[string]string{})
			cs.Add("LOOPBACK_INTERFACE", fmt.Sprintf("Loopback0|%s/32", loopbackIP), map[string]string{})
		}

	case "sonic-evpn":
		// EVPN baseline - create VTEP (delegates to evpn_ops.go VTEP)
		if loopbackIP, ok := varMap["loopback_ip"]; ok && loopbackIP != "" {
			cs.Adds(CreateVTEP(loopbackIP))
		}

	default:
		return nil, fmt.Errorf("unknown configlet: %s", configletName)
	}

	n.trackOffline(cs)
	util.WithDevice(n.name).Infof("Applied baseline configlet '%s'", configletName)
	return cs, nil
}

// ============================================================================
// Cleanup (Orphaned Resource Removal)
// ============================================================================

// CleanupSummary provides details about orphaned resources found.
type CleanupSummary struct {
	OrphanedACLs        []string
	OrphanedVRFs        []string
	OrphanedVNIMappings []string
}

// Cleanup identifies and removes orphaned configurations.
// Returns a changeset to remove them and a summary of what was found.
func (n *Node) Cleanup(ctx context.Context, cleanupType string) (*ChangeSet, *CleanupSummary, error) {
	if err := n.precondition("cleanup", cleanupType).Result(); err != nil {
		return nil, nil, err
	}

	cs := NewChangeSet(n.name, "device.cleanup")
	summary := &CleanupSummary{}

	configDB := n.ConfigDB()
	if configDB == nil {
		return cs, summary, nil
	}

	// Find orphaned ACLs (no interfaces bound)
	if cleanupType == "" || cleanupType == "acl" {
		for aclName, acl := range configDB.ACLTable {
			if acl.Ports == "" {
				summary.OrphanedACLs = append(summary.OrphanedACLs, aclName)
				cs.Deletes(deleteAclTable(configDB, aclName))
			}
		}
	}

	// Find orphaned VRFs (no interfaces bound)
	if cleanupType == "" || cleanupType == "vrf" {
		for vrfName := range configDB.VRF {
			if vrfName == "default" {
				continue
			}
			hasUsers := false
			for intfName, intf := range configDB.Interface {
				if strings.Contains(intfName, "|") {
					continue
				}
				if intf.VRFName == vrfName {
					hasUsers = true
					break
				}
			}
			if !hasUsers {
				summary.OrphanedVRFs = append(summary.OrphanedVRFs, vrfName)
				cs.Deletes(createVrf(vrfName))
			}
		}
	}

	// Find orphaned VNI mappings (VRF or VLAN doesn't exist)
	if cleanupType == "" || cleanupType == "vni" {
		for mapKey, mapping := range configDB.VXLANTunnelMap {
			orphaned := false
			if mapping.VRF != "" {
				if _, ok := configDB.VRF[mapping.VRF]; !ok {
					orphaned = true
				}
			}
			if mapping.VLAN != "" {
				if _, ok := configDB.VLAN[mapping.VLAN]; !ok {
					orphaned = true
				}
			}
			if orphaned {
				summary.OrphanedVNIMappings = append(summary.OrphanedVNIMappings, mapKey)
				cs.Deletes(deleteVniMapByKey(mapKey))
			}
		}
	}

	return cs, summary, nil
}

