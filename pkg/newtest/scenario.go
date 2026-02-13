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
	Name        string   `yaml:"name"`
	Description string   `yaml:"description"`
	Topology    string   `yaml:"topology"`
	Platform    string   `yaml:"platform"`
	Requires    []string `yaml:"requires,omitempty"`
	Repeat      int      `yaml:"repeat,omitempty"`
	Steps       []Step   `yaml:"steps"`
}

// Step is a single action within a scenario.
// Fields are action-specific — the parser validates that only relevant
// fields are set for each action type.
type Step struct {
	Name    string         `yaml:"name"`
	Action  StepAction     `yaml:"action"`
	Devices DeviceSelector `yaml:"devices,omitempty"`

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
	ActionRefreshService     StepAction = "refresh-service"
	ActionCleanup            StepAction = "cleanup"
)

// validActions is the set of all recognized step actions.
var validActions = map[StepAction]bool{
	ActionProvision:          true,
	ActionWait:               true,
	ActionVerifyProvisioning: true,
	ActionVerifyConfigDB:     true,
	ActionVerifyStateDB:      true,
	ActionVerifyBGP:          true,
	ActionVerifyHealth:       true,
	ActionVerifyRoute:        true,
	ActionVerifyPing:         true,
	ActionApplyService:       true,
	ActionRemoveService:      true,
	ActionApplyBaseline:      true,
	ActionSSHCommand:         true,
	ActionRestartService:     true,
	ActionApplyFRRDefaults:   true,
	ActionSetInterface:       true,
	ActionCreateVLAN:         true,
	ActionDeleteVLAN:         true,
	ActionAddVLANMember:      true,
	ActionCreateVRF:          true,
	ActionDeleteVRF:          true,
	ActionSetupEVPN:          true,
	ActionAddVRFInterface:    true,
	ActionRemoveVRFInterface: true,
	ActionBindIPVPN:          true,
	ActionUnbindIPVPN:        true,
	ActionBindMACVPN:         true,
	ActionUnbindMACVPN:       true,
	ActionAddStaticRoute:     true,
	ActionRemoveStaticRoute:  true,
	ActionRemoveVLANMember:   true,
	ActionApplyQoS:           true,
	ActionRemoveQoS:          true,
	ActionConfigureSVI:       true,
	ActionBGPAddNeighbor:     true,
	ActionBGPRemoveNeighbor:  true,
	ActionRefreshService:     true,
	ActionCleanup:            true,
}

// DeviceSelector handles the two YAML forms for the "devices" field:
//
//	devices: all           → All: true
//	devices: [leaf1, leaf2] → Devices: ["leaf1", "leaf2"]
type DeviceSelector struct {
	All     bool
	Devices []string
}

// UnmarshalYAML implements yaml.Unmarshaler.
func (ds *DeviceSelector) UnmarshalYAML(unmarshal func(any) error) error {
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
func (ds *DeviceSelector) Resolve(allDevices []string) []string {
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
