// qos.go implements QoS policy → CONFIG_DB translation.
//
// A QoSPolicy is a self-contained queue definition from which newtron derives
// all CONFIG_DB tables: DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER,
// WRED_PROFILE, PORT_QOS_MAP, and QUEUE entries.
package network

import (
	"fmt"
	"strings"

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
func generateQoSDeviceEntries(policyName string, policy *spec.QoSPolicy) []CompositeEntry {
	var entries []CompositeEntry

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
	entries = append(entries, CompositeEntry{
		Table:  "DSCP_TO_TC_MAP",
		Key:    policyName,
		Fields: dscpFields,
	})

	// TC_TO_QUEUE_MAP: identity mapping (TC N → Queue N).
	tcFields := make(map[string]string, len(policy.Queues))
	for i := range policy.Queues {
		tcFields[fmt.Sprintf("%d", i)] = fmt.Sprintf("%d", i)
	}
	entries = append(entries, CompositeEntry{
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
		entries = append(entries, CompositeEntry{
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
		entries = append(entries, CompositeEntry{
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

// generateQoSInterfaceEntries produces per-interface CONFIG_DB entries for a QoS policy:
//   - 1 PORT_QOS_MAP entry (bracket-ref to maps)
//   - N QUEUE entries (one per queue, bracket-ref to SCHEDULER, optionally WRED_PROFILE)
func generateQoSInterfaceEntries(policyName string, policy *spec.QoSPolicy, interfaceName string) []CompositeEntry {
	var entries []CompositeEntry

	// PORT_QOS_MAP: bind maps to the port.
	entries = append(entries, CompositeEntry{
		Table: "PORT_QOS_MAP",
		Key:   interfaceName,
		Fields: map[string]string{
			"dscp_to_tc_map":  fmt.Sprintf("[DSCP_TO_TC_MAP|%s]", policyName),
			"tc_to_queue_map": fmt.Sprintf("[TC_TO_QUEUE_MAP|%s]", policyName),
		},
	})

	// QUEUE: one per queue, binding scheduler (and optionally WRED).
	wredKey := policyName + ".ecn"
	for i, q := range policy.Queues {
		queueKey := fmt.Sprintf("%s|%d", interfaceName, i)
		queueFields := map[string]string{
			"scheduler": fmt.Sprintf("[SCHEDULER|%s.%d]", policyName, i),
		}
		if q.ECN {
			queueFields["wred_profile"] = fmt.Sprintf("[WRED_PROFILE|%s]", wredKey)
		}
		entries = append(entries, CompositeEntry{
			Table:  "QUEUE",
			Key:    queueKey,
			Fields: queueFields,
		})
	}

	return entries
}

// resolveServiceQoSPolicy returns the QoS policy name and definition for a service.
// It checks QoSPolicy (new-style) first, then falls back to legacy QoSProfile.
// Returns ("", nil) if neither is set.
func resolveServiceQoSPolicy(n *Network, svc *spec.ServiceSpec) (string, *spec.QoSPolicy) {
	if svc.QoSPolicy != "" {
		if n.spec.QoSPolicies != nil {
			if policy, ok := n.spec.QoSPolicies[svc.QoSPolicy]; ok {
				return svc.QoSPolicy, policy
			}
		}
	}
	return "", nil
}
