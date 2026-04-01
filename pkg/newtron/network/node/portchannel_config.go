package node

import (
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/device/sonic"
)

// ============================================================================
// PortChannel Config Functions (pure, no Node state)
// ============================================================================

// createPortChannelConfig returns a CONFIG_DB entry for creating a PortChannel.
func createPortChannelConfig(name string, fields map[string]string) []sonic.Entry {
	return []sonic.Entry{{Table: "PORTCHANNEL", Key: name, Fields: fields}}
}

// deletePortChannelConfig returns a delete entry for a PORTCHANNEL.
// Under the DAG, members are removed as children before the PortChannel can be deleted.
func deletePortChannelConfig(name string) []sonic.Entry {
	return []sonic.Entry{{Table: "PORTCHANNEL", Key: name}}
}

// createPortChannelMemberConfig returns a CONFIG_DB entry for adding a member to a PortChannel.
func createPortChannelMemberConfig(pcName, member string) []sonic.Entry {
	return []sonic.Entry{{Table: "PORTCHANNEL_MEMBER", Key: fmt.Sprintf("%s|%s", pcName, member), Fields: map[string]string{}}}
}

// deletePortChannelMemberConfig returns a delete entry for removing a member from a PortChannel.
func deletePortChannelMemberConfig(pcName, member string) []sonic.Entry {
	return []sonic.Entry{{Table: "PORTCHANNEL_MEMBER", Key: fmt.Sprintf("%s|%s", pcName, member)}}
}
