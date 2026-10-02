// qos_config.go implements QoS policy → CONFIG_DB translation.
//
// A QoSPolicy is a self-contained queue definition from which newtron derives
// all CONFIG_DB tables: DSCP_TO_TC_MAP, TC_TO_QUEUE_MAP, SCHEDULER,
// WRED_PROFILE, PORT_QOS_MAP, and QUEUE entries.
package node

import (
	"fmt"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
)

// defaultSchedulerName is the shared scheduler every queue the policy does not
// describe points at, so that queue → treatment is total over the platform's
// queues rather than over the policy's.
//
// Named by newtron's own convention (§36) rather than adopting SONiC's
// "scheduler.0". That was the first intent, on the §37 reading that a community
// object should be used rather than a parallel one built beside it — but reading
// the platform template on a device shows there is no single "scheduler.0" to
// adopt: three branches of qos_config.j2 define it with weights 1, 40 and 14
// depending on platform and role. The name is stable, its content is not, so
// claiming it would mean asserting one of several community meanings. §37 is
// satisfied anyway: this uses the community's table, fields and role, exactly as
// the per-policy <POLICY>_Q<n> schedulers already do. It introduces no new
// mechanism, only one more object in an existing one.
//
// DWRR weight 1 is deliberate. Nothing classifies into these queues, so the
// treatment is inert — but if traffic ever did arrive, a minimum-share DWRR queue
// degrades gracefully where a strict-priority one would starve everything else.
// It matches the most conservative of the template's own definitions.
const (
	defaultSchedulerName   = "DEFAULT"
	defaultSchedulerType   = "DWRR"
	defaultSchedulerWeight = "1"
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

	// TC_TO_QUEUE_MAP: identity over every class the platform has, not just the
	// ones this policy gives meaning to. Sizing it to the policy leaves classes
	// with no entry, and a class with no entry has no defined queue — the map is
	// a function and a function should be total over its domain. SONiC's own
	// template writes the full identity map regardless of policy for the same
	// reason.
	tcFields := make(map[string]string, sonic.MaxQueuesPerPort)
	for i := 0; i < sonic.MaxQueuesPerPort; i++ {
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

	// The shared default scheduler, so the queues below carry a reference. Emitted
	// with every policy and identical each time, which is the §24 create-on-first-
	// reference shape: whichever bind runs first brings it into being.
	entries = append(entries, sonic.Entry{
		Table: "SCHEDULER",
		Key:   defaultSchedulerName,
		Fields: map[string]string{
			"type":   defaultSchedulerType,
			"weight": defaultSchedulerWeight,
		},
	})

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

	// QUEUE: a row for every queue the platform has, not just the ones the policy
	// describes. The policy's queues carry its schedulers; the rest carry the
	// shared default, so queue → treatment is total and the forward writes the
	// same range the reverse clears (unbindQosConfig). Sizing this to the policy
	// is what made the two disagree, and what made a policy's queue count a value
	// teardown had to recover.
	wredKey := policyName + "_ECN"
	for idx := 0; idx < sonic.MaxQueuesPerPort; idx++ {
		queueFields := map[string]string{
			"scheduler": fmt.Sprintf("[SCHEDULER|%s]", defaultSchedulerName),
		}
		if idx < len(policy.Queues) {
			queueFields["scheduler"] = fmt.Sprintf("[SCHEDULER|%s_Q%d]", policyName, idx)
			if policy.Queues[idx].ECN {
				queueFields["wred_profile"] = fmt.Sprintf("[WRED_PROFILE|%s]", wredKey)
			}
		}
		entries = append(entries, sonic.Entry{
			Table:  "QUEUE",
			Key:    fmt.Sprintf("%s|%d", intfName, idx),
			Fields: queueFields,
		})
	}

	return entries
}

// unbindQosConfig returns delete entries clearing a port's QoS: every QUEUE index
// the port can hold, then PORT_QOS_MAP.
//
// It takes no policy, by design. The extent of what a bind delivered is not
// knowable at teardown — reconstruction replays intents through *current* specs,
// so the spec, the projection, and the intent record all report what the policy
// says now rather than what it said when it was bound. A reverse therefore clears
// the namespace it owns instead of recomputing that extent (§15): QUEUE under a
// port belongs entirely to that port's binding (qos_ops.go is the table's sole
// owner, §27, and an interface holds one binding or none, §6), and every QUEUE
// write is validated against sonic.MaxQueuesPerPort, so no key outside the swept
// range has ever been delivered. Deletes for indices the port never held are
// no-ops on the wire and verify as absent. Mirrors DeleteBGPNeighborConfig, which
// clears all three address families whether or not a peer was configured with them.
func unbindQosConfig(intfName string) []sonic.Entry {
	var entries []sonic.Entry
	for idx := 0; idx < sonic.MaxQueuesPerPort; idx++ {
		entries = append(entries, sonic.Entry{Table: "QUEUE", Key: fmt.Sprintf("%s|%d", intfName, idx)})
	}
	entries = append(entries, sonic.Entry{Table: "PORT_QOS_MAP", Key: intfName})
	return entries
}

// deleteDeviceQoSConfig returns delete entries clearing the device-wide tables a
// policy owns — the reverse of GenerateDeviceQoSConfig. Called only once the
// policy has no remaining consumer (§24).
//
// Like unbindQosConfig it takes no policy spec, for the same reason and with the
// same namespace argument: the SCHEDULER keys a policy owns are exactly
// <policy>_Q0..MaxQueuesPerPort-1, and that range cannot collide with another
// policy's — a policy named GOLD_Q0 generates GOLD_Q0_Q0 and up, outside GOLD's.
// WRED_PROFILE is one derivable key, so it is deleted unconditionally rather than
// gated on whether any queue carried ECN.
func deleteDeviceQoSConfig(policyName string) []sonic.Entry {
	entries := []sonic.Entry{
		{Table: "DSCP_TO_TC_MAP", Key: policyName},
		{Table: "TC_TO_QUEUE_MAP", Key: policyName},
	}
	for idx := 0; idx < sonic.MaxQueuesPerPort; idx++ {
		entries = append(entries, sonic.Entry{Table: "SCHEDULER", Key: fmt.Sprintf("%s_Q%d", policyName, idx)})
	}
	return append(entries, sonic.Entry{Table: "WRED_PROFILE", Key: policyName + "_ECN"})
}
