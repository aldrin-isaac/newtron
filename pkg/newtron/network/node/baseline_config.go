package node

import (
	"fmt"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
)

// loopbackIPKey returns the CONFIG_DB key for a Loopback0 IP sub-entry.
// Format: Loopback0|{ip}/32. One owner so the create and delete paths can
// never drift apart (§15 forward/reverse symmetry).
func loopbackIPKey(loopbackIP string) string {
	return fmt.Sprintf("Loopback0|%s/32", loopbackIP)
}

// createLoopbackConfig returns CONFIG_DB entries for Loopback0 with an IP address.
// Produces base entry + IP sub-entry (intfmgrd requires the base entry first).
func createLoopbackConfig(loopbackIP string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "LOOPBACK_INTERFACE", Key: "Loopback0", Fields: map[string]string{}},
		{Table: "LOOPBACK_INTERFACE", Key: loopbackIPKey(loopbackIP), Fields: map[string]string{}},
	}
}

// deleteLoopbackConfig returns delete entries for Loopback0 (children before parents).
func deleteLoopbackConfig(loopbackIP string) []sonic.Entry {
	var entries []sonic.Entry
	if loopbackIP != "" {
		entries = append(entries, sonic.Entry{Table: "LOOPBACK_INTERFACE", Key: loopbackIPKey(loopbackIP)})
	}
	entries = append(entries, sonic.Entry{Table: "LOOPBACK_INTERFACE", Key: "Loopback0"})
	return entries
}
