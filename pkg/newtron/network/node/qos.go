// qos.go implements QoS policy → CONFIG_DB translation.
//
// A QoSPolicy is a self-contained queue definition from which newtron derives
// all CONFIG_DB tables: DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER,
// WRED_PROFILE, PORT_QOS_MAP, and QUEUE entries.
package node

import (
	"fmt"
	"strings"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// Default WRED thresholds for ECN profiles.
const (
	defaultWREDMinThreshold  = "1048576" // 1 MB
	defaultWREDMaxThreshold  = "2097152" // 2 MB
	defaultWREDDropProbility = "5"       // 5%
)

// generateQoSDeviceEntries produces device-wide CONFIG_DB entries for a QoS policy:
//   - 1 DSCP_TO_TC_MAP entry (all 64 DSCP values, unmapped → "0")
//   - 1 TC_TO_QUEUE_MAP entry (identity mapping)
//   - N SCHEDULER entries (one per queue)
//   - 0 or 1 WRED_PROFILE entry (if any queue has ECN)
func GenerateDeviceQoSConfig(policyName string, policy *spec.QoSPolicy) []sonic.Entry {
	var entries []sonic.Entry

	// DSCP_TO_TC_MAP: map all 64 DSCP values to their traffic class.
	// Unmapped DSCPs default to TC 0.
	dscpFields := make(map[string]string, 64)
	for i := 0; i < 64; i++ {
		dscpFields[fmt.Sprintf("%d", i)] = "0" // default
	}
	for queueIdx, q := range policy.Queues {
		for _, dscp := range q.DSCP {
			dscpFields[fmt.Sprintf("%d", dscp)] = fmt.Sprintf("%d", queueIdx)
		}
	}
	entries = append(entries, sonic.Entry{
		Table:  "DSCP_TO_TC_MAP",
		Key:    policyName,
		Fields: dscpFields,
	})

	// TC_TO_QUEUE_MAP: identity mapping (TC N → Queue N).
	tcFields := make(map[string]string, len(policy.Queues))
	for i := range policy.Queues {
		tcFields[fmt.Sprintf("%d", i)] = fmt.Sprintf("%d", i)
	}
	entries = append(entries, sonic.Entry{
		Table:  "TC_TO_QUEUE_MAP",
		Key:    policyName,
		Fields: tcFields,
	})

	// SCHEDULER: one per queue.
	for i, q := range policy.Queues {
		schedKey := fmt.Sprintf("%s.%d", policyName, i)
		schedFields := map[string]string{
			"type": strings.ToUpper(q.Type),
		}
		if q.Type == "dwrr" && q.Weight > 0 {
			schedFields["weight"] = fmt.Sprintf("%d", q.Weight)
		}
		entries = append(entries, sonic.Entry{
			Table:  "SCHEDULER",
			Key:    schedKey,
			Fields: schedFields,
		})
	}

	// WRED_PROFILE: created if any queue has ECN enabled.
	hasECN := false
	for _, q := range policy.Queues {
		if q.ECN {
			hasECN = true
			break
		}
	}
	if hasECN {
		entries = append(entries, sonic.Entry{
			Table: "WRED_PROFILE",
			Key:   policyName + ".ecn",
			Fields: map[string]string{
				"ecn":                   "ecn_all",
				"green_min_threshold":   defaultWREDMinThreshold,
				"green_max_threshold":   defaultWREDMaxThreshold,
				"green_drop_probability": defaultWREDDropProbility,
			},
		})
	}

	return entries
}

// bindQos produces per-interface CONFIG_DB entries for a QoS policy:
//   - 1 PORT_QOS_MAP entry (bracket-ref to maps)
//   - N QUEUE entries (one per queue, bracket-ref to SCHEDULER, optionally WRED_PROFILE)
func (i *Interface) bindQos(policyName string, policy *spec.QoSPolicy) []sonic.Entry {
	var entries []sonic.Entry

	// PORT_QOS_MAP: bind maps to the port.
	entries = append(entries, sonic.Entry{
		Table: "PORT_QOS_MAP",
		Key:   i.name,
		Fields: map[string]string{
			"dscp_to_tc_map":  fmt.Sprintf("[DSCP_TO_TC_MAP|%s]", policyName),
			"tc_to_queue_map": fmt.Sprintf("[TC_TO_QUEUE_MAP|%s]", policyName),
		},
	})

	// QUEUE: one per queue, binding scheduler (and optionally WRED).
	wredKey := policyName + ".ecn"
	for idx, q := range policy.Queues {
		queueKey := fmt.Sprintf("%s|%d", i.name, idx)
		queueFields := map[string]string{
			"scheduler": fmt.Sprintf("[SCHEDULER|%s.%d]", policyName, idx),
		}
		if q.ECN {
			queueFields["wred_profile"] = fmt.Sprintf("[WRED_PROFILE|%s]", wredKey)
		}
		entries = append(entries, sonic.Entry{
			Table:  "QUEUE",
			Key:    queueKey,
			Fields: queueFields,
		})
	}

	return entries
}

// bindQosProfile returns a PORT_QOS_MAP entry for a QoS profile.
// Profiles reference pre-existing maps by name (not bracket-ref like policies).
// Returns nil if the profile has no map fields set.
func (i *Interface) bindQosProfile(profile *spec.QoSProfile) []sonic.Entry {
	fields := map[string]string{}
	if profile.DSCPToTCMap != "" {
		fields["dscp_to_tc_map"] = profile.DSCPToTCMap
	}
	if profile.TCToQueueMap != "" {
		fields["tc_to_queue_map"] = profile.TCToQueueMap
	}
	if len(fields) == 0 {
		return nil
	}
	return []sonic.Entry{{Table: "PORT_QOS_MAP", Key: i.name, Fields: fields}}
}

// deleteDeviceQoSConfig returns delete entries for the device-wide QoS tables
// created by GenerateDeviceQoSConfig: DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP,
// SCHEDULER (prefix scan), and WRED_PROFILE (prefix scan).
func (n *Node) deleteDeviceQoSConfig(policyName string) []sonic.Entry {
	var entries []sonic.Entry
	// DSCP_TO_TC_MAP and TC_TO_QUEUE_MAP: exact key match
	entries = append(entries, sonic.Entry{Table: "DSCP_TO_TC_MAP", Key: policyName})
	entries = append(entries, sonic.Entry{Table: "TC_TO_QUEUE_MAP", Key: policyName})
	// SCHEDULER and WRED_PROFILE: scan for policyName.* prefix
	if n.configDB != nil {
		prefix := policyName + "."
		for key := range n.configDB.Scheduler {
			if strings.HasPrefix(key, prefix) {
				entries = append(entries, sonic.Entry{Table: "SCHEDULER", Key: key})
			}
		}
		for key := range n.configDB.WREDProfile {
			if strings.HasPrefix(key, prefix) {
				entries = append(entries, sonic.Entry{Table: "WRED_PROFILE", Key: key})
			}
		}
	}
	return entries
}

// isQoSPolicyReferenced checks if any PORT_QOS_MAP entry (excluding the given
// interface) references the policy via bracket ref [DSCP_TO_TC_MAP|{policyName}].
func (n *Node) isQoSPolicyReferenced(policyName, excludeInterface string) bool {
	if n.configDB == nil {
		return false
	}
	ref := fmt.Sprintf("[DSCP_TO_TC_MAP|%s]", policyName)
	for intfName, entry := range n.configDB.PortQoSMap {
		if intfName != excludeInterface && entry.DSCPToTCMap == ref {
			return true
		}
	}
	return false
}

// parsePolicyName extracts the policy name from a PORT_QOS_MAP bracket-ref
// like "[DSCP_TO_TC_MAP|myPolicy]" → "myPolicy". Returns "" if not a bracket-ref.
func parsePolicyName(bracketRef string) string {
	const prefix = "[DSCP_TO_TC_MAP|"
	if !strings.HasPrefix(bracketRef, prefix) || !strings.HasSuffix(bracketRef, "]") {
		return ""
	}
	return bracketRef[len(prefix) : len(bracketRef)-1]
}

// GetServiceQoSPolicy returns the QoS policy name and definition for a service.
// It checks QoSPolicy (new-style) first, then falls back to legacy QoSProfile.
// Returns ("", nil) if neither is set.
func GetServiceQoSPolicy(sp SpecProvider, svc *spec.ServiceSpec) (string, *spec.QoSPolicy) {
	if svc.QoSPolicy != "" {
		if policy, err := sp.GetQoSPolicy(svc.QoSPolicy); err == nil {
			return svc.QoSPolicy, policy
		}
	}
	return "", nil
}
