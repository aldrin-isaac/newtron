// Package newtest implements an E2E test orchestrator for newtron and SONiC.
// It parses YAML scenario files, deploys VM topologies via newtlab, provisions
// devices via newtron, and runs multi-step verification sequences.
package newtest

import (
	"fmt"
	"sort"
	"time"
)

// Scenario is a parsed test scenario from a YAML file.
type Scenario struct {
	Name             string   `yaml:"name"`
	Description      string   `yaml:"description"`
	Topology         string   `yaml:"topology"`
	Platform         string   `yaml:"platform"`
	Requires         []string `yaml:"requires,omitempty"`
	RequiresFeatures []string `yaml:"requires_features,omitempty"` // Platform features required (e.g., ["acl", "macvpn"])
	Repeat           int      `yaml:"repeat,omitempty"`
	Steps            []Step   `yaml:"steps"`
}

// Step is a single action within a scenario.
// Fields are action-specific — the parser validates that only relevant
// fields are set for each action type.
type Step struct {
	Name    string         `yaml:"name"`
	Action  StepAction     `yaml:"action"`
	Devices deviceSelector `yaml:"devices,omitempty"`

	// wait
	Duration time.Duration `yaml:"duration,omitempty"`

	// verify-config-db, verify-state-db
	Table string `yaml:"table,omitempty"`
	Key   string `yaml:"key,omitempty"`

	// verify-route
	Prefix string `yaml:"prefix,omitempty"`
	VRF    string `yaml:"vrf,omitempty"`

	// apply-service, remove-service
	Interface string         `yaml:"interface,omitempty"`
	Service   string         `yaml:"service,omitempty"`
	Params    map[string]any `yaml:"params,omitempty"`

	// apply-baseline
	Configlet string            `yaml:"configlet,omitempty"`
	Vars      map[string]string `yaml:"vars,omitempty"`

	// ssh-command
	Command string `yaml:"command,omitempty"`

	// verify-ping
	Target string `yaml:"target,omitempty"`
	Count  int    `yaml:"count,omitempty"`

	// create-vlan, delete-vlan, add-vlan-member, remove-vlan-member
	VLANID  int    `yaml:"vlan_id,omitempty"`
	Tagging string `yaml:"tagging,omitempty"` // "tagged" or "untagged"

	// All verify-* actions, ssh-command
	Expect *ExpectBlock `yaml:"expect,omitempty"`
}

// StepAction identifies the type of step to execute.
type StepAction string

const (
	ActionProvision          StepAction = "provision"
	ActionWait               StepAction = "wait"
	ActionVerifyProvisioning StepAction = "verify-provisioning"
	ActionVerifyConfigDB     StepAction = "verify-config-db"
	ActionVerifyStateDB      StepAction = "verify-state-db"
	ActionVerifyBGP          StepAction = "verify-bgp"
	ActionVerifyHealth       StepAction = "verify-health"
	ActionVerifyRoute        StepAction = "verify-route"
	ActionVerifyPing         StepAction = "verify-ping"
	ActionApplyService       StepAction = "apply-service"
	ActionRemoveService      StepAction = "remove-service"
	ActionApplyBaseline      StepAction = "apply-baseline"
	ActionSSHCommand         StepAction = "ssh-command"
	ActionRestartService     StepAction = "restart-service"
	ActionApplyFRRDefaults   StepAction = "apply-frr-defaults"
	ActionSetInterface       StepAction = "set-interface"
	ActionCreateVLAN         StepAction = "create-vlan"
	ActionDeleteVLAN         StepAction = "delete-vlan"
	ActionAddVLANMember      StepAction = "add-vlan-member"
	ActionCreateVRF          StepAction = "create-vrf"
	ActionDeleteVRF          StepAction = "delete-vrf"
	ActionSetupEVPN          StepAction = "setup-evpn"
	ActionAddVRFInterface    StepAction = "add-vrf-interface"
	ActionRemoveVRFInterface StepAction = "remove-vrf-interface"
	ActionBindIPVPN          StepAction = "bind-ipvpn"
	ActionUnbindIPVPN        StepAction = "unbind-ipvpn"
	ActionBindMACVPN         StepAction = "bind-macvpn"
	ActionUnbindMACVPN       StepAction = "unbind-macvpn"
	ActionAddStaticRoute     StepAction = "add-static-route"
	ActionRemoveStaticRoute  StepAction = "remove-static-route"
	ActionRemoveVLANMember   StepAction = "remove-vlan-member"
	ActionApplyQoS           StepAction = "apply-qos"
	ActionRemoveQoS          StepAction = "remove-qos"
	ActionConfigureSVI       StepAction = "configure-svi"
	ActionBGPAddNeighbor     StepAction = "bgp-add-neighbor"
	ActionBGPRemoveNeighbor  StepAction = "bgp-remove-neighbor"
	ActionRefreshService          StepAction = "refresh-service"
	ActionCleanup                 StepAction = "cleanup"
	ActionCreatePortChannel       StepAction = "create-portchannel"
	ActionDeletePortChannel       StepAction = "delete-portchannel"
	ActionAddPortChannelMember    StepAction = "add-portchannel-member"
	ActionRemovePortChannelMember StepAction = "remove-portchannel-member"

	// Host test actions
	ActionHostExec StepAction = "host-exec"

	// ACL management actions
	ActionCreateACLTable StepAction = "create-acl-table"
	ActionAddACLRule     StepAction = "add-acl-rule"
	ActionDeleteACLRule  StepAction = "delete-acl-rule"
	ActionDeleteACLTable StepAction = "delete-acl-table"
	ActionBindACL        StepAction = "bind-acl"
	ActionUnbindACL      StepAction = "unbind-acl"
)

// validActions is the set of all recognized step actions, derived from the
// executors map in steps.go at init time. This avoids manual synchronization
// between the two maps.
var validActions map[StepAction]bool

func init() {
	validActions = make(map[StepAction]bool, len(executors))
	for action := range executors {
		validActions[action] = true
	}
}

// deviceSelector handles the two YAML forms for the "devices" field:
//
//	devices: all           → All: true
//	devices: [leaf1, leaf2] → Devices: ["leaf1", "leaf2"]
type deviceSelector struct {
	All     bool
	Devices []string
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (ds *deviceSelector) UnmarshalYAML(unmarshal func(any) error) error {
	var s string
	if err := unmarshal(&s); err == nil {
		if s == "all" {
			ds.All = true
			return nil
		}
		return fmt.Errorf("invalid device selector string: %q (expected \"all\")", s)
	}
	return unmarshal(&ds.Devices)
}

// Resolve returns the list of device names to target.
// If All is true, returns allDevices sorted for deterministic ordering.
func (ds *deviceSelector) Resolve(allDevices []string) []string {
	if ds.All {
		sorted := make([]string, len(allDevices))
		copy(sorted, allDevices)
		sort.Strings(sorted)
		return sorted
	}
	return ds.Devices
}

// ExpectBlock is a union of all action-specific expectation fields.
type ExpectBlock struct {
	// verify-config-db
	MinEntries *int              `yaml:"min_entries,omitempty"`
	Exists     *bool             `yaml:"exists,omitempty"`
	Fields     map[string]string `yaml:"fields,omitempty"`

	// Polling
	Timeout      time.Duration `yaml:"timeout,omitempty"`
	PollInterval time.Duration `yaml:"poll_interval,omitempty"`

	// verify-bgp
	State string `yaml:"state,omitempty"`

	// verify-health
	Overall string `yaml:"overall,omitempty"`

	// verify-route
	Protocol  string `yaml:"protocol,omitempty"`
	NextHopIP string `yaml:"nexthop_ip,omitempty"`
	Source    string `yaml:"source,omitempty"`

	// verify-ping
	SuccessRate *float64 `yaml:"success_rate,omitempty"`

	// ssh-command
	Contains string `yaml:"contains,omitempty"`
}
