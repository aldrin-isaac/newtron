package node

import (
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
)

// createLoopbackConfig returns CONFIG_DB entries for Loopback0 with an IP address.
// Produces base entry + IP sub-entry (intfmgrd requires the base entry first).
func createLoopbackConfig(loopbackIP string) []sonic.Entry {
	return []sonic.Entry{
		{Table: "LOOPBACK_INTERFACE", Key: "Loopback0", Fields: map[string]string{}},
		{Table: "LOOPBACK_INTERFACE", Key: fmt.Sprintf("Loopback0|%s/32", loopbackIP), Fields: map[string]string{}},
	}
}

// deleteLoopbackConfig returns delete entries for Loopback0 (children before parents).
func deleteLoopbackConfig(loopbackIP string) []sonic.Entry {
	var entries []sonic.Entry
	if loopbackIP != "" {
		entries = append(entries, sonic.Entry{Table: "LOOPBACK_INTERFACE", Key: fmt.Sprintf("Loopback0|%s/32", loopbackIP)})
	}
	entries = append(entries, sonic.Entry{Table: "LOOPBACK_INTERFACE", Key: "Loopback0"})
	return entries
}
