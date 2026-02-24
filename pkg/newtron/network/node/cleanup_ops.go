package node

import (
	"context"
	"strings"

	"github.com/newtron-network/newtron/pkg/util"
)

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
				cs.Deletes(n.deleteAclTableConfig(aclName))
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
				cs.Deletes(createVrfConfig(vrfName))
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
				cs.Deletes(deleteVniMapByKeyConfig(mapKey))
			}
		}
	}

	_ = util.WithDevice(n.name)
	return cs, summary, nil
}
