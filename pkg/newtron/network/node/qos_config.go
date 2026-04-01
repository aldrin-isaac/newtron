// qos_config.go implements QoS policy → CONFIG_DB translation.
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
	// Policy names are already normalized (uppercase, underscores) by the spec loader.
	for i, q := range policy.Queues {
		schedKey := fmt.Sprintf("%s_Q%d", policyName, i)
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
			Key:   policyName + "_ECN",
			Fields: map[string]string{
				"ecn":                    "ecn_all",
				"green_min_threshold":    defaultWREDMinThreshold,
				"green_max_threshold":    defaultWREDMaxThreshold,
				"green_drop_probability": defaultWREDDropProbility,
			},
		})
	}

	return entries
}

// bindQosConfig produces per-interface CONFIG_DB entries for a QoS policy:
//   - 1 PORT_QOS_MAP entry (bracket-ref to maps)
//   - N QUEUE entries (one per queue, bracket-ref to SCHEDULER, optionally WRED_PROFILE)
func bindQosConfig(intfName string, policyName string, policy *spec.QoSPolicy) []sonic.Entry {
	var entries []sonic.Entry

	// PORT_QOS_MAP: bind maps to the port.
	entries = append(entries, sonic.Entry{
		Table: "PORT_QOS_MAP",
		Key:   intfName,
		Fields: map[string]string{
			"dscp_to_tc_map":  fmt.Sprintf("[DSCP_TO_TC_MAP|%s]", policyName),
			"tc_to_queue_map": fmt.Sprintf("[TC_TO_QUEUE_MAP|%s]", policyName),
		},
	})

	// QUEUE: one per queue, binding scheduler (and optionally WRED).
	wredKey := policyName + "_ECN"
	for idx, q := range policy.Queues {
		queueKey := fmt.Sprintf("%s|%d", intfName, idx)
		queueFields := map[string]string{
			"scheduler": fmt.Sprintf("[SCHEDULER|%s_Q%d]", policyName, idx),
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

// unbindQosConfig returns delete entries for QoS on an interface: QUEUE entries and PORT_QOS_MAP.
// queueCount is the number of queues from the resolved QoS policy spec.
func unbindQosConfig(intfName string, queueCount int) []sonic.Entry {
	var entries []sonic.Entry
	for idx := 0; idx < queueCount; idx++ {
		entries = append(entries, sonic.Entry{Table: "QUEUE", Key: fmt.Sprintf("%s|%d", intfName, idx)})
	}
	entries = append(entries, sonic.Entry{Table: "PORT_QOS_MAP", Key: intfName})
	return entries
}

// deleteDeviceQoSConfig returns delete entries for the device-wide QoS tables
// created by GenerateDeviceQoSConfig: DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP,
// SCHEDULER, and WRED_PROFILE. Deterministic from policy spec.
func deleteDeviceQoSConfig(policyName string, policy *spec.QoSPolicy) []sonic.Entry {
	var entries []sonic.Entry
	entries = append(entries, sonic.Entry{Table: "DSCP_TO_TC_MAP", Key: policyName})
	entries = append(entries, sonic.Entry{Table: "TC_TO_QUEUE_MAP", Key: policyName})

	if policy != nil {
		hasECN := false
		for idx, q := range policy.Queues {
			entries = append(entries, sonic.Entry{Table: "SCHEDULER", Key: fmt.Sprintf("%s_Q%d", policyName, idx)})
			if q.ECN {
				hasECN = true
			}
		}
		if hasECN {
			entries = append(entries, sonic.Entry{Table: "WRED_PROFILE", Key: policyName + "_ECN"})
		}
	}
	return entries
}
