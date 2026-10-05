package node

import (
	"context"
	"fmt"
	"sort"
	"strconv"
	"strings"

	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
	"github.com/aldrin-isaac/newtron/pkg/newtron/spec"
	"github.com/aldrin-isaac/newtron/pkg/util"
)

// isVLANMember reports whether intfName is a member of vlanID, tagged or
// untagged — whether its membership record exists.
func (n *Node) isVLANMember(intfName string, vlanID int) bool {
	return n.GetIntent(vlanMembershipKey(intfName, vlanID)) != nil
}

// vlanMemberPorts returns the members of a VLAN — every interface with a
// membership record in it, tagged or untagged, whether an operator or a bridged
// service joined it. An irb service's per-member policy is bound
// to exactly this set (§4): a member gets the policy iff a service is bound on
// the VLAN's IRB AND the port is a member. The set is derived from the intent DB,
// never recorded (§21), so it is correct whichever fact (membership or binding)
// arrived second. Sorted for a stable ACL_TABLE ports-list (deterministic §48
// in-place diffs).
func (n *Node) vlanMemberPorts(vlanID int) []string {
	want := strconv.Itoa(vlanID)
	set := map[string]bool{}
	for resource, intent := range n.IntentsByParam(sonic.FieldVLANID, want) {
		switch intent.Operation {
		case sonic.OpConfigureInterface, sonic.OpAddTrunkVLAN:
			if name := resourceInterfaceName(resource); name != "" {
				set[name] = true
			}
		}
	}
	ports := make([]string, 0, len(set))
	for p := range set {
		ports = append(ports, p)
	}
	sort.Strings(ports)
	return ports
}

// InterfaceExists checks if an interface exists.
// Accepts both short (Eth0) and full (Ethernet0) interface names.
// Existence is kind-specific: physical ports from the RegisterPort map,
// PortChannels and VLAN SVIs from intents. Classification and existence
// share one source (interfaceKindOf) so they cannot diverge — and
// ListInterfaces enumerates from the same sources, so whatever exists
// is also listed (§24).
func (n *Node) InterfaceExists(name string) bool {
	name = util.NormalizeInterfaceName(name)
	switch interfaceKindOf(name) {
	case KindEthernet:
		_, ok := n.interfaces[name]
		return ok
	case KindPortChannel:
		return n.GetIntent("portchannel|"+name) != nil
	case KindIRB:
		vlanID := strings.TrimPrefix(name, "Vlan")
		return n.GetIntent("vlan|"+vlanID) != nil
	default:
		return false
	}
}

// InterfaceConfig holds the combined configuration for ConfigureInterface.
// Routed mode (VRF+IP) and bridged mode (VLAN) are mutually exclusive.
type InterfaceConfig struct {
	VRF    string // VRF binding (routed mode)
	IP     string // IP address in CIDR notation (routed mode)
	VLAN   int    // VLAN ID (bridged mode)
	Tagged bool   // Tagged membership (bridged mode)
}

// vlanMembershipKey returns the intent resource key of an interface's membership
// in vlanID — untagged (configure-interface) or tagged (add-trunk-vlan). One key
// per VLAN, so a port cannot be both a tagged and an untagged member of the same
// VLAN. The record is a child of the interface's identity record and of
// vlan|<id>, so the VLAN cannot be deleted while it has members.
func vlanMembershipKey(intfName string, vlanID int) string {
	return fmt.Sprintf("interface|%s|vlan|%d", intfName, vlanID)
}

// routedKey returns the intent resource key of an interface's L3 association —
// its IP address and VRF binding (configure-interface, routed mode). A child of
// the interface's identity record, and of vrf|<name> when a VRF is bound.
func routedKey(intfName string) string {
	return "interface|" + intfName + "|routed"
}

// bindingKey returns the intent resource key for an interface's service
// binding — a sub-resource of the interface's identity record
// (interface|<name>), the single owner of this key so writer, readers, and
// teardown cannot diverge (§25). The identity record interface|<name> holds
// what the interface *is* (from configure-interface / configure-irb /
// interface-init); the binding holds the one service applied to it.
func bindingKey(intfName string) string {
	return "interface|" + intfName + "|service"
}

// resourceInterfaceName returns the interface name a resource key names —
// the first segment after "interface|", so identity records
// (interface|Ethernet0) and every sub-resource (interface|Ethernet0|service,
// interface|Ethernet0|acl|ingress, interface|Ethernet0|vlan|100) all
// resolve to the same interface. Scans that iterate intents and extract the
// bound port MUST route through this, or a sub-resource key leaks its suffix
// into a port list. Returns "" for non-interface keys.
func resourceInterfaceName(resource string) string {
	if !strings.HasPrefix(resource, "interface|") {
		return ""
	}
	rest := resource[len("interface|"):]
	return strings.SplitN(rest, "|", 2)[0]
}

// accessVLAN returns the VLAN this interface is an untagged member of, or 0. An
// interface has at most one.
func (i *Interface) accessVLAN() int {
	for _, intent := range i.node.IntentsByPrefix("interface|" + i.name + "|vlan|") {
		if intent.Operation == sonic.OpConfigureInterface {
			v, _ := strconv.Atoi(intent.Params[sonic.FieldVLANID])
			return v
		}
	}
	return 0
}

// routedService returns the name of the routed or evpn-routed service bound on
// this interface, or "". Such a service owns the interface's L3 config exactly as
// a routed association does, so the two are never on one interface: each would
// overwrite the other's base entry and address.
func (i *Interface) routedService() string {
	binding := i.node.GetIntent(bindingKey(i.name))
	if binding == nil {
		return ""
	}
	switch binding.Params[sonic.FieldServiceType] {
	case spec.ServiceTypeRouted, spec.ServiceTypeEVPNRouted:
		return binding.Params[sonic.FieldServiceName]
	}
	return ""
}

// hasVLANMembership reports whether this interface is a member of any VLAN,
// tagged or untagged.
func (i *Interface) hasVLANMembership() bool {
	return len(i.node.IntentsByPrefix("interface|"+i.name+"|vlan|")) > 0
}

// createInterfaceIntent writes the interface|INTF identity intent if absent
// (idempotent). It is the interface's intent-writer — the standalone equivalent of
// the writeIntent every CreateVLAN / CreateVRF / CreatePortChannel does inline,
// needed here because the interface is the one delivery target newtron does not
// create (a physical port pre-exists via RegisterPort, so there is no
// CreateInterface to own interface|INTF). The identity record means only "newtron
// manages this interface": every record on the interface — its properties, ACL and
// QoS bindings, BGP peer, service binding, and its VLAN or routed association —
// is a child of it. Its §15 reverse is destroyInterfaceIntent. (An IRB's identity
// is its configure-irb record instead: an SVI exists only once configured.)
func (i *Interface) createInterfaceIntent(cs *ChangeSet) error {
	resource := "interface|" + i.name
	if i.node.GetIntent(resource) != nil {
		return nil
	}
	parents := []string{"device"}
	if i.IsPortChannel() {
		parents = append(parents, "portchannel|"+i.name)
	}
	return i.node.writeIntent(cs, sonic.OpInterfaceInit, resource, map[string]string{}, parents)
}

// refuseMissing refuses an operation on a record this interface does not hold — a
// state the request needs and the device lacks, so a precondition failure (409),
// like a missing VLAN or ACL table.
func (i *Interface) refuseMissing(operation, record string) error {
	return util.NewPreconditionError(operation, i.name, record+" exists", fmt.Sprintf("%s has no %s", i.name, record))
}

// destroyInterfaceIntent is the §15 reverse of createInterfaceIntent: it removes
// the interface's identity record once nothing is left on the interface. Every
// operation that removes a record from an interface calls it, so whichever
// removal takes the last child also removes the identity. A no-op while any child
// remains, and for an identity that is not a bare interface-init record (an IRB's
// configure-irb record has its own reverse).
func (i *Interface) destroyInterfaceIntent(cs *ChangeSet) error {
	resource := "interface|" + i.name
	identity := i.node.GetIntent(resource)
	if identity == nil || identity.Operation != sonic.OpInterfaceInit || len(identity.Children) > 0 {
		return nil
	}
	return i.node.deleteIntent(cs, resource)
}

// createVLANMembership makes this interface a member of vlanID, tagged or
// untagged, and is the one owner of a VLAN join (§25): the VLAN_MEMBER row, the
// membership record, the checks a join must pass, and the per-member policy a
// member receives (§4). ConfigureInterface (operator authoring) and the bridged /
// evpn-bridged composite (a service's L2 delivery point) both join through it. A
// join that repeats an existing membership writes nothing. Its §15 reverse is
// destroyVLANMembership. The caller renders.
func (i *Interface) createVLANMembership(cs *ChangeSet, vlanID int, tagged bool) error {
	n := i.node
	op := sonic.OpConfigureInterface
	if tagged {
		op = sonic.OpAddTrunkVLAN
	}
	refuse := func(precondition, detail string) error {
		return util.NewPreconditionError(sonic.OpConfigureInterface, i.name, precondition, detail)
	}
	key := vlanMembershipKey(i.name, vlanID)
	if existing := n.GetIntent(key); existing != nil {
		if existing.Operation != op {
			return refuse("membership matches the requested tagging",
				fmt.Sprintf("%s is already a member of VLAN %d with the other tagging; remove that membership first", i.name, vlanID))
		}
		return nil
	}
	if n.GetIntent(routedKey(i.name)) != nil {
		return refuse("interface is not routed",
			fmt.Sprintf("%s is routed; unconfigure it before joining VLAN %d", i.name, vlanID))
	}
	if svc := i.routedService(); svc != "" {
		return refuse("interface is not routed",
			fmt.Sprintf("%s carries routed service %s; remove it before joining VLAN %d", i.name, svc, vlanID))
	}
	if current := i.accessVLAN(); !tagged && current != 0 {
		return refuse("interface has at most one untagged VLAN",
			fmt.Sprintf("%s is already an untagged member of VLAN %d; unconfigure it before joining VLAN %d untagged", i.name, current, vlanID))
	}
	// Single-VLAN-member gate (§7): refuse a join that would make the port a trunk
	// while it carries an irb service's per-member filter/QoS — that policy cannot
	// be delivered correctly to a multi-VLAN member. Enforced when authored, not on
	// replay.
	if !n.reconstructing {
		if err := n.refuseUndeliverablePolicy(i.name, vlanID); err != nil {
			return err
		}
	}
	if err := i.createInterfaceIntent(cs); err != nil {
		return err
	}
	cs.Adds(createVlanMemberConfig(vlanID, i.name, tagged))
	if err := n.writeIntent(cs, op, key, map[string]string{
		sonic.FieldVLANID: strconv.Itoa(vlanID),
		sonic.FieldTagged: strconv.FormatBool(tagged),
	}, []string{"interface|" + i.name, "vlan|" + strconv.Itoa(vlanID)}); err != nil {
		return err
	}
	// Per-member policy (§4): an irb service bound on this VLAN reaches its new
	// member.
	n.rebindMemberACLs(cs, vlanID)
	n.bindMemberQoS(cs, vlanID)
	return nil
}

// destroyVLANMembership is the §15 reverse of createVLANMembership: it removes the
// VLAN_MEMBER row and the membership record, then withdraws the per-member policy
// the port received through this VLAN (§4). The membership record has no
// children, so whatever else the port carries never blocks it. The identity
// record is left to its own reverse, destroyInterfaceIntent.
func (i *Interface) destroyVLANMembership(cs *ChangeSet, vlanID int) error {
	n := i.node
	cs.Deletes(deleteVlanMemberConfig(vlanID, i.name))
	if err := n.deleteIntent(cs, vlanMembershipKey(i.name, vlanID)); err != nil {
		return err
	}
	n.rebindMemberACLs(cs, vlanID)
	n.unbindMemberQoS(cs, i.name, map[int]bool{vlanID: true})
	return nil
}

// createRoutedAssociation routes this interface and is the one owner of that write
// (§25): the routed record (IP, VRF) and the L3 rows it renders — the VRF binding,
// or the routing base entry, and the address. Repeating it with a new IP moves the
// address; a different VRF is a different association and is refused, as is
// routing a VLAN member. Its §15 reverse is destroyRoutedAssociation. The caller
// renders.
func (i *Interface) createRoutedAssociation(cs *ChangeSet, vrf, ip string) error {
	n := i.node
	refuse := func(precondition, detail string) error {
		return util.NewPreconditionError(sonic.OpConfigureInterface, i.name, precondition, detail)
	}
	if i.hasVLANMembership() {
		return refuse("interface is not a VLAN member",
			fmt.Sprintf("%s is a VLAN member; unconfigure it before routing it", i.name))
	}
	if svc := i.routedService(); svc != "" {
		return refuse("interface carries no routed service",
			fmt.Sprintf("%s carries routed service %s, which owns its L3 config; remove it before routing the interface", i.name, svc))
	}
	key := routedKey(i.name)
	if existing := n.GetIntent(key); existing != nil {
		if oldVRF := existing.Params[sonic.FieldVRF]; oldVRF != vrf {
			return refuse("routed interface keeps its VRF",
				fmt.Sprintf("%s is routed in VRF %q; unconfigure it before routing it in VRF %q", i.name, oldVRF, vrf))
		}
		// The address is the sub-entry's key: a new one must not leave the old
		// row behind (#228).
		if oldIP := existing.Params[sonic.FieldIntfIP]; oldIP != "" && oldIP != ip {
			cs.Deletes(deleteInterfaceIPConfig(i.name, oldIP))
		}
	}
	if err := i.createInterfaceIntent(cs); err != nil {
		return err
	}
	params := map[string]string{}
	parents := []string{"interface|" + i.name}
	if vrf != "" {
		params[sonic.FieldVRF] = vrf
		parents = append(parents, "vrf|"+vrf)
	}
	if ip != "" {
		params[sonic.FieldIntfIP] = ip
	}
	if err := n.writeIntent(cs, sonic.OpConfigureInterface, key, params, parents); err != nil {
		return err
	}
	// The VRF binding is the base entry the address needs; without a VRF the
	// address needs the plain routing base entry.
	switch {
	case vrf != "":
		cs.Adds(bindVrfConfig(i.name, vrf))
	case ip != "":
		cs.Adds(enableIpRoutingConfig(i.name))
	}
	if ip != "" {
		cs.Adds(assignIpAddressConfig(i.name, ip))
	}
	return nil
}

// destroyRoutedAssociation is the §15 reverse of createRoutedAssociation: it
// removes the address, then the base entry the VRF binding or routing created,
// then the routed record. Nothing else on the interface writes L3 config — a
// routed service is refused alongside it — so the base entry is this record's
// alone.
func (i *Interface) destroyRoutedAssociation(cs *ChangeSet) error {
	key := routedKey(i.name)
	routed := i.node.GetIntent(key)
	if routed == nil {
		return nil
	}
	ip := routed.Params[sonic.FieldIntfIP]
	if ip != "" {
		cs.Deletes(deleteInterfaceIPConfig(i.name, ip))
	}
	if ip != "" || routed.Params[sonic.FieldVRF] != "" {
		cs.Deletes(deleteInterfaceBaseConfig(i.name))
	}
	return i.node.deleteIntent(cs, key)
}

// ConfigureInterface gives an interface its association: bridged (a VLAN
// membership, tagged or untagged) or routed (a VRF and/or an IP address), never
// both. This is the intent-producing method that topology steps should use.
func (i *Interface) ConfigureInterface(ctx context.Context, cfg InterfaceConfig) (*ChangeSet, error) {
	n := i.node
	routed := cfg.VRF != "" || cfg.IP != ""
	switch {
	case cfg.VLAN > 0 && routed:
		return nil, util.NewValidationError("cannot mix routed (VRF/IP) and bridged (VLAN) config")
	case cfg.VLAN <= 0 && !routed:
		return nil, util.NewValidationError("configure-interface needs a VLAN, or a VRF and/or an IP address")
	case cfg.IP != "" && !util.IsValidIPv4CIDR(cfg.IP):
		return nil, util.NewValidationError(fmt.Sprintf("invalid IP address: %s", cfg.IP))
	}

	// Capability gate, content-derived: bridged config needs VLAN membership,
	// routed config needs an L3 identity the interface-op path authors
	// (configure-interface declares nil registry Needs; this is its in-method
	// half — see contentDerivedOps). On an IRB the routed case refuses with the
	// configure-irb redirect.
	pc := n.precondition(sonic.OpConfigureInterface, i.name).RequireInterfaceNotPortChannelMember(i.name)
	if cfg.VLAN > 0 {
		pc.RequireInterfaceCapabilities(i.name, CapabilityVLANMembership).RequireVLANExists(cfg.VLAN)
	} else {
		pc.RequireInterfaceCapabilities(i.name, CapabilityRouting)
		if cfg.VRF != "" {
			pc.RequireVRFExists(cfg.VRF)
		}
	}
	if err := pc.Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpConfigureInterface)
	cs.ReverseOp = "interface.unconfigure-interface"
	cs.OperationParams = map[string]string{"interface": i.name}
	if cfg.VLAN > 0 {
		if err := i.createVLANMembership(cs, cfg.VLAN, cfg.Tagged); err != nil {
			return nil, err
		}
		if cfg.Tagged {
			cs.ReverseOp = "interface." + sonic.OpRemoveTrunkVLAN
			cs.OperationParams["vlan_id"] = strconv.Itoa(cfg.VLAN)
		}
	} else if err := i.createRoutedAssociation(cs, cfg.VRF, cfg.IP); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Configured interface %s (vrf=%s, ip=%s, vlan=%d, tagged=%t)", i.name, cfg.VRF, cfg.IP, cfg.VLAN, cfg.Tagged)
	return cs, nil
}

// RemoveTrunkVLAN removes one tagged VLAN membership from this interface — the
// §15 reverse of ConfigureInterface(tagged=true). Every other record on the
// interface is untouched; the identity record goes only if this was the last.
func (i *Interface) RemoveTrunkVLAN(ctx context.Context, vlanID int) (*ChangeSet, error) {
	n := i.node
	if err := n.precondition(sonic.OpRemoveTrunkVLAN, i.name).Result(); err != nil {
		return nil, err
	}
	if vlanID <= 0 {
		return nil, util.NewValidationError("vlan_id must be positive")
	}
	if member := n.GetIntent(vlanMembershipKey(i.name, vlanID)); member == nil || member.Operation != sonic.OpAddTrunkVLAN {
		return nil, i.refuseMissing(sonic.OpRemoveTrunkVLAN, fmt.Sprintf("tagged membership in VLAN %d", vlanID))
	}
	cs := NewChangeSet(n.Name(), "interface."+sonic.OpRemoveTrunkVLAN)
	cs.OperationParams = map[string]string{"interface": i.name, "vlan_id": strconv.Itoa(vlanID)}
	if err := i.destroyVLANMembership(cs, vlanID); err != nil {
		return nil, err
	}
	if err := i.destroyInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Removed trunk VLAN %d from %s", vlanID, i.name)
	return cs, nil
}

// UnconfigureInterface returns the interface to unmanaged. It removes every record
// on the interface through that record's own reverse, leaves first as the intent
// DAG orders them — so a BGP peer goes before the routed association that supplies
// its address — and then the identity record. An IRB keeps its identity, the
// configure-irb record, which unconfigure-irb removes. Refused while a service is
// bound (remove-service owns that teardown). Parameterless: the intent records are
// self-sufficient for teardown.
func (i *Interface) UnconfigureInterface(ctx context.Context) (*ChangeSet, error) {
	n := i.node
	identityKey := "interface|" + i.name
	if err := n.precondition("unconfigure-interface", i.name).
		Check(n.GetIntent(bindingKey(i.name)) == nil, "no service is bound",
			fmt.Sprintf("%s has a service bound; remove-service tears it down", i.name)).
		Result(); err != nil {
		return nil, err
	}
	if n.GetIntent(identityKey) == nil {
		return nil, i.refuseMissing("unconfigure-interface", "configuration")
	}

	cs := NewChangeSet(n.Name(), "interface.unconfigure-interface")
	for {
		resource := i.nextLeafRecord()
		if resource == "" {
			break
		}
		record := n.GetIntent(resource)
		var sub *ChangeSet
		var err error
		switch record.Operation {
		case sonic.OpAddBGPPeer:
			sub, err = i.RemoveBGPPeer(ctx)
		case sonic.OpBindQoS:
			sub, err = i.UnbindQoS(ctx)
		case sonic.OpBindACL:
			sub, err = i.UnbindACL(ctx, record.Params[sonic.FieldACLName])
		case sonic.OpSetProperty:
			sub, err = i.ClearProperty(ctx, record.Params[sonic.FieldProperty])
		case sonic.OpAddTrunkVLAN, sonic.OpConfigureInterface:
			// An association has no operation of its own to remove it; its owner's
			// reverse does.
			if resource == routedKey(i.name) {
				err = i.destroyRoutedAssociation(cs)
			} else {
				vlanID, _ := strconv.Atoi(record.Params[sonic.FieldVLANID])
				err = i.destroyVLANMembership(cs, vlanID)
			}
		default:
			err = fmt.Errorf("unconfigure-interface: no reverse for %s (%s)", resource, record.Operation)
		}
		if err != nil {
			return nil, fmt.Errorf("unconfigure %s: %w", resource, err)
		}
		if sub != nil {
			cs.Merge(sub)
		}
	}
	if identity := n.GetIntent(identityKey); identity != nil && len(identity.Children) > 0 {
		return nil, fmt.Errorf("unconfigure %s: no record in %v can be removed first", i.name, identity.Children)
	}
	if err := i.destroyInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Unconfigured interface %s", i.name)
	return cs, nil
}

// nextLeafRecord returns a record on this interface that nothing depends on — a
// child of the identity record with no children of its own — or "" when the
// identity has no children left. The choice among several leaves is by key, so
// the teardown order is deterministic.
func (i *Interface) nextLeafRecord() string {
	identity := i.node.GetIntent("interface|" + i.name)
	if identity == nil {
		return ""
	}
	children := append([]string(nil), identity.Children...)
	sort.Strings(children)
	for _, child := range children {
		if record := i.node.GetIntent(child); record != nil && len(record.Children) == 0 {
			return child
		}
	}
	return ""
}

// BindACL binds an ACL to this interface.
// ACLs are shared - adds this interface to the ACL's binding list.
func (i *Interface) BindACL(ctx context.Context, aclName, direction string) (*ChangeSet, error) {
	n := i.node

	if direction != "ingress" && direction != "egress" {
		return nil, util.NewValidationError("direction must be 'ingress' or 'egress'")
	}
	if err := n.precondition(sonic.OpBindACL, i.name).
		RequireACLTableExists(aclName).Result(); err != nil {
		return nil, err
	}

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpBindACL)
	if err := i.createInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := i.node.writeIntent(cs, sonic.OpBindACL, "interface|"+i.name+"|acl|"+direction,
		map[string]string{sonic.FieldACLName: aclName, sonic.FieldDirection: direction},
		[]string{"interface|" + i.name, aclKey(aclName)}); err != nil {
		return nil, err
	}
	cs.ReverseOp = "interface.unbind-acl"
	cs.OperationParams = map[string]string{"interface": i.name, "acl_name": aclName}

	// ACLs are shared — collect port list from intents (this interface's
	// binding intent was written above, so aclPortsFromIntents includes it)
	currentPorts := n.aclPortsFromIntents(aclName, direction)

	e := bindAclConfig(aclName, currentPorts, direction)
	cs.Update(e.Table, e.Key, e.Fields)
	if err := n.render(cs); err != nil {
		return nil, err
	}

	util.WithDevice(n.Name()).Infof("Bound ACL %s to interface %s (%s)", aclName, i.name, direction)
	return cs, nil
}

// UnbindACL removes this interface from an ACL table's binding list.
func (i *Interface) UnbindACL(ctx context.Context, aclName string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("unbind-acl", i.name).
		RequireACLTableExists(aclName).
		Result(); err != nil {
		return nil, err
	}

	// Find the intent record for this ACL binding. Try both directions since
	// the caller passes aclName but not direction.
	var direction string
	for _, dir := range []string{"ingress", "egress"} {
		if intent := n.GetIntent("interface|" + i.name + "|acl|" + dir); intent != nil {
			if intent.Params[sonic.FieldACLName] == aclName {
				direction = dir
				break
			}
		}
	}
	if direction == "" {
		return nil, i.refuseMissing("unbind-acl", "binding of ACL "+aclName)
	}

	cs := NewChangeSet(n.Name(), "interface.unbind-acl")

	// Collect remaining ports from intents (this interface's intent hasn't been
	// deleted yet, so explicitly exclude it)
	allPorts := n.aclPortsFromIntents(aclName, direction)
	remainingPorts := util.RemoveFromCSV(allPorts, i.name)
	e := updateAclPorts(aclName, remainingPorts)
	cs.Update(e.Table, e.Key, e.Fields)

	if err := i.node.deleteIntent(cs, "interface|"+i.name+"|acl|"+direction); err != nil {
		return nil, err
	}
	if err := i.destroyInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}
	util.WithDevice(n.Name()).Infof("Unbound ACL %s from interface %s", aclName, i.name)
	return cs, nil
}

// ============================================================================
// Generic Property Setting
// ============================================================================

// SetProperty sets a property on this interface.
// Supported properties: mtu, speed, admin-status, description
func (i *Interface) SetProperty(ctx context.Context, property, value string) (*ChangeSet, error) {
	n := i.node

	// Per-property granularity within CapabilityPortProperties: speed and
	// description exist only on the physical PORT row.
	_, known := propertyApplicability[property]
	if err := n.precondition(sonic.OpSetProperty, i.name).
		RequireInterfaceNotPortChannelMember(i.name).
		Check(!known || propertyAppliesTo(property, i.Kind()), "property applies to the interface kind",
			fmt.Sprintf("property %q does not apply to a %s", property, i.Kind())).
		Result(); err != nil {
		return nil, err
	}

	// The value is validated and rendered by spec.PortConfig — the one owner of
	// port-property values, shared with the topology's port config — so a speed
	// is written in the Mbps form SONiC parses, never as authored (RCA-050).
	fields, err := portPropertyFields(i.name, property, value)
	if err != nil {
		return nil, err
	}

	intentParams := map[string]string{sonic.FieldProperty: property, sonic.FieldValue: value}
	if property == "speed" {
		// clear-property writes this back; recorded here so the reverse never
		// reads the platform spec (§20).
		defaultSpeed, err := n.platformDefaultSpeed(i.name)
		if err != nil {
			return nil, err
		}
		intentParams[sonic.FieldDefaultSpeed] = defaultSpeed
	}

	cs := NewChangeSet(n.Name(), "interface."+sonic.OpSetProperty)
	if err := i.createInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := i.node.writeIntent(cs, sonic.OpSetProperty, "interface|"+i.name+"|"+property,
		intentParams, []string{"interface|" + i.name}); err != nil {
		return nil, err
	}

	cs.Updates(setPropertyConfig(propertyTable(i.name), i.name, fields))
	if err := n.render(cs); err != nil {
		return nil, err
	}

	util.WithDevice(n.Name()).Infof("Set %s=%s on interface %s", property, value, i.name)
	return cs, nil
}

// ClearProperty removes a property override from this interface, reverting
// the field to its default. Deletes the property intent so it no longer
// blocks parent intent deletion.
func (i *Interface) ClearProperty(ctx context.Context, property string) (*ChangeSet, error) {
	n := i.node

	if err := n.precondition("clear-property", i.name).Result(); err != nil {
		return nil, err
	}

	intentKey := "interface|" + i.name + "|" + property
	intent := n.GetIntent(intentKey)
	if intent == nil {
		return nil, i.refuseMissing(sonic.OpClearProperty, property+" override")
	}

	defaultSpeed := intent.Params[sonic.FieldDefaultSpeed]
	if property == "speed" && defaultSpeed == "" {
		return nil, fmt.Errorf("speed intent on %s records no default speed to revert to", i.name)
	}
	cs := NewChangeSet(n.Name(), "interface."+sonic.OpClearProperty)
	cs.Updates(clearPropertyConfig(propertyTable(i.name), i.name, property, defaultSpeed))

	if err := n.deleteIntent(cs, intentKey); err != nil {
		return nil, err
	}
	if err := i.destroyInterfaceIntent(cs); err != nil {
		return nil, err
	}
	if err := n.render(cs); err != nil {
		return nil, err
	}

	util.WithDevice(n.Name()).Infof("Cleared %s on interface %s", property, i.name)
	return cs, nil
}

// platformDefaultSpeed returns the speed a port with no speed override runs at —
// the platform's default_speed, which an unset port inherits — in the Mbps form
// SONiC parses, rendered by spec.PortConfig like every other port value. Without
// one a speed override could not be reverted, so the override is refused as a
// precondition (409; replay skips such an intent rather than aborting).
func (n *Node) platformDefaultSpeed(intfName string) (string, error) {
	refuse := func(detail string) error {
		return util.NewPreconditionError(sonic.OpSetProperty, intfName,
			"platform declares a valid default_speed", detail)
	}
	platform, err := n.GetPlatform(n.resolved.Platform)
	if err != nil {
		return "", refuse(err.Error())
	}
	pc := spec.PortConfig{Speed: platform.DefaultSpeed}
	if platform.DefaultSpeed == "" {
		return "", refuse(fmt.Sprintf("platform %s declares no default_speed, so a speed override could not be reverted", n.resolved.Platform))
	}
	if err := pc.ValidateConstraints("default_speed of platform " + n.resolved.Platform); err != nil {
		return "", refuse(err.Error())
	}
	return pc.Fields()["speed"], nil
}
