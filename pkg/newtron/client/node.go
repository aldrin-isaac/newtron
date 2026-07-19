package client

import (
	"fmt"
	"net/url"

	"github.com/aldrin-isaac/newtron/pkg/newtron"
	"github.com/aldrin-isaac/newtron/pkg/newtron/api"
	"github.com/aldrin-isaac/newtron/pkg/newtron/device/sonic"
)

// ============================================================================
// Node read operations
// ============================================================================

// DeviceInfo returns device information.
func (c *Client) DeviceInfo(device string) (*newtron.DeviceInfo, error) {
	var result newtron.DeviceInfo
	if err := c.doGet(c.nodePath(device)+"/info", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListInterfaces returns the node's platform-supported interface inventory —
// every interface the platform declares, with topology wiring and port config.
func (c *Client) ListInterfaces(device string) ([]newtron.InterfaceInventoryEntry, error) {
	var result []newtron.InterfaceInventoryEntry
	if err := c.doGet(c.nodePath(device)+"/interfaces", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowInterface returns details of a single interface.
func (c *Client) ShowInterface(device, name string) (*newtron.InterfaceDetail, error) {
	var result newtron.InterfaceDetail
	if err := c.doGet(c.nodePath(device)+"/interfaces/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// InterfaceStatus returns the interface's composed live operational picture
// (link state, counters, rates, resolved neighbors, LLDP far end, optics).
func (c *Client) InterfaceStatus(device, iface string) (*newtron.InterfaceStatus, error) {
	var result newtron.InterfaceStatus
	if err := c.doGet(c.interfacePath(device, iface)+"/status", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ShowServiceBinding returns the service binding on an interface.
func (c *Client) ShowServiceBinding(device, iface string) (*newtron.ServiceBindingDetail, error) {
	var result newtron.ServiceBindingDetail
	if err := c.doGet(c.interfacePath(device, iface)+"/binding", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListVLANs returns VLAN status entries.
func (c *Client) ListVLANs(device string) ([]newtron.VLANStatusEntry, error) {
	var result []newtron.VLANStatusEntry
	if err := c.doGet(c.nodePath(device)+"/vlans", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowVLAN returns details of a single VLAN.
func (c *Client) ShowVLAN(device string, id int) (*newtron.VLANStatusEntry, error) {
	var result newtron.VLANStatusEntry
	if err := c.doGet(fmt.Sprintf("%s/vlans/%d", c.nodePath(device), id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListVRFs returns VRF status entries.
func (c *Client) ListVRFs(device string) ([]newtron.VRFStatusEntry, error) {
	var result []newtron.VRFStatusEntry
	if err := c.doGet(c.nodePath(device)+"/vrfs", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowVRF returns details of a single VRF.
func (c *Client) ShowVRF(device, name string) (*newtron.VRFDetail, error) {
	var result newtron.VRFDetail
	if err := c.doGet(c.nodePath(device)+"/vrfs/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListACLs returns ACL table summaries.
func (c *Client) ListACLs(device string) ([]newtron.ACLTableSummary, error) {
	var result []newtron.ACLTableSummary
	if err := c.doGet(c.nodePath(device)+"/acls", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowACL returns details of a single ACL.
func (c *Client) ShowACL(device, name string) (*newtron.ACLTableDetail, error) {
	var result newtron.ACLTableDetail
	if err := c.doGet(c.nodePath(device)+"/acls/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// BGPStatus returns BGP status.
func (c *Client) BGPStatus(device string) (*newtron.BGPStatusResult, error) {
	var result newtron.BGPStatusResult
	if err := c.doGet(c.nodePath(device)+"/bgp/status", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// EVPNStatus returns EVPN status.
func (c *Client) EVPNStatus(device string) (*newtron.EVPNStatusResult, error) {
	var result newtron.EVPNStatusResult
	if err := c.doGet(c.nodePath(device)+"/evpn/status", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// IntentTree returns the intent DAG as a tree structure.
func (c *Client) IntentTree(device, kind, resource string, ancestors bool) ([]newtron.IntentTreeNode, error) {
	path := c.nodePath(device) + "/intent/tree"
	params := url.Values{}
	if kind != "" {
		params.Set("kind", kind)
	}
	if resource != "" {
		params.Set("resource", resource)
	}
	if ancestors {
		params.Set("ancestors", "true")
	}
	if len(params) > 0 {
		path += "?" + params.Encode()
	}
	var result []newtron.IntentTreeNode
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// HealthCheck returns a health report for a device.
func (c *Client) HealthCheck(device string) (*newtron.HealthReport, error) {
	var result newtron.HealthReport
	if err := c.doGet(c.nodePath(device)+"/health", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListLAGs returns LAG status entries.
func (c *Client) ListLAGs(device string) ([]newtron.LAGStatusEntry, error) {
	var result []newtron.LAGStatusEntry
	if err := c.doGet(c.nodePath(device)+"/lags", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowLAGDetail returns details of a single LAG.
func (c *Client) ShowLAGDetail(device, name string) (*newtron.LAGStatusEntry, error) {
	var result newtron.LAGStatusEntry
	if err := c.doGet(c.nodePath(device)+"/lags/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CheckBGPSessions returns BGP session health check results.
func (c *Client) CheckBGPSessions(device string) ([]newtron.HealthCheckResult, error) {
	var result []newtron.HealthCheckResult
	if err := c.doGet(c.nodePath(device)+"/bgp/check", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetRoute looks up a route in APP_DB.
func (c *Client) GetRoute(device, vrf, prefix string) (*newtron.RouteEntry, error) {
	var result newtron.RouteEntry
	path := fmt.Sprintf("%s/routes/%s/%s", c.nodePath(device), url.PathEscape(vrf), prefix)
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetRouteASIC looks up a route in ASIC_DB.
func (c *Client) GetRouteASIC(device, prefix string) (*newtron.RouteEntry, error) {
	var result newtron.RouteEntry
	path := fmt.Sprintf("%s/routes-asic/%s", c.nodePath(device), prefix)
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================================================================
// DB query operations
// ============================================================================

// QueryConfigDB reads a CONFIG_DB hash entry.
func (c *Client) QueryConfigDB(device, table, key string) (map[string]string, error) {
	var result map[string]string
	path := fmt.Sprintf("%s/configdb/%s/%s", c.nodePath(device), url.PathEscape(table), url.PathEscape(key))
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ConfigDBTableKeys lists keys in a CONFIG_DB table.
func (c *Client) ConfigDBTableKeys(device, table string) ([]string, error) {
	var result []string
	path := fmt.Sprintf("%s/configdb/%s", c.nodePath(device), url.PathEscape(table))
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ConfigDBEntryExists checks if a CONFIG_DB entry exists.
func (c *Client) ConfigDBEntryExists(device, table, key string) (bool, error) {
	var result map[string]bool
	path := fmt.Sprintf("%s/configdb/%s/%s/exists", c.nodePath(device), url.PathEscape(table), url.PathEscape(key))
	if err := c.doGet(path, &result); err != nil {
		return false, err
	}
	return result["exists"], nil
}

// OperDBSnapshot reads an entire operational DB (STATE_DB, APPL_DB,
// COUNTERS_DB, ASIC_DB) as table → key → fields.
func (c *Client) OperDBSnapshot(device, dbName string) (map[string]map[string]map[string]string, error) {
	var result map[string]map[string]map[string]string
	path := fmt.Sprintf("%s/db/%s", c.nodePath(device), url.PathEscape(dbName))
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// OperDBTable reads one table of an operational DB as key → fields. A
// flat-hash table (e.g. COUNTERS_DB's COUNTERS_PORT_NAME_MAP) comes back as
// a single "" entry.
func (c *Client) OperDBTable(device, dbName, table string) (map[string]map[string]string, error) {
	var result map[string]map[string]string
	path := fmt.Sprintf("%s/db/%s/%s", c.nodePath(device), url.PathEscape(dbName), url.PathEscape(table))
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// OperDBEntry reads one entry of an operational DB. The key may embed the
// DB's separator (APPL_DB "NEIGH_TABLE:Ethernet4:10.255.255.4" → key
// "Ethernet4:10.255.255.4") — the server routes it as a path wildcard.
func (c *Client) OperDBEntry(device, dbName, table, key string) (map[string]string, error) {
	var result map[string]string
	path := fmt.Sprintf("%s/db/%s/%s/%s", c.nodePath(device), url.PathEscape(dbName), url.PathEscape(table), url.PathEscape(key))
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ============================================================================
// Node write operations
// ============================================================================

// nodeWriteResult decodes a WriteResult from a POST response.
func (c *Client) nodeWrite(device, endpoint string, body any, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	var result newtron.WriteResult
	if err := c.doPost(c.nodePath(device)+"/"+endpoint+execQuery(opts), body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// AddBGPEVPNPeer adds an EVPN BGP neighbor using loopback as update-source.
func (c *Client) AddBGPEVPNPeer(device string, config newtron.BGPNeighborConfig, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "add-bgp-evpn-peer", config, opts)
}

// UpdateBGPEVPNPeer atomically mutates an existing EVPN BGP peer's
// fields. The composite key (default + neighbor_ip) is the row's
// identity (§47); a re-IP is remove + add via separate verbs. #227.
func (c *Client) UpdateBGPEVPNPeer(device, neighborIP string, config newtron.BGPNeighborConfig, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	config.NeighborIP = neighborIP
	return c.nodeWrite(device, "update-bgp-evpn-peer", config, opts)
}

// RemoveBGPEVPNPeer removes an EVPN BGP neighbor by IP.
func (c *Client) RemoveBGPEVPNPeer(device, ip string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		NeighborIP string `json:"neighbor_ip"`
	}{NeighborIP: ip}
	return c.nodeWrite(device, "remove-bgp-evpn-peer", body, opts)
}

// SetupDevice performs consolidated device initialization.
func (c *Client) SetupDevice(device string, sdOpts newtron.SetupDeviceOpts, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "setup-device", sdOpts, opts)
}

// NodeBindMACVPN maps a VLAN to an L2VNI for EVPN at the device level.
func (c *Client) NodeBindMACVPN(device string, vlanID int, macvpnName string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.NodeBindMACVPNRequest{VlanID: vlanID, MACVPN: macvpnName}
	return c.nodeWrite(device, "bind-macvpn", body, opts)
}

// NodeUnbindMACVPN removes the MAC-VPN binding for a VLAN at the device level.
func (c *Client) NodeUnbindMACVPN(device string, vlanID int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.NodeUnbindMACVPNRequest{VlanID: vlanID}
	return c.nodeWrite(device, "unbind-macvpn", body, opts)
}

// CreateVLAN creates a VLAN.
func (c *Client) CreateVLAN(device string, config api.VLANCreateRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "create-vlan", config, opts)
}

// DeleteVLAN deletes a VLAN.
func (c *Client) DeleteVLAN(device string, id int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		ID int `json:"id"`
	}{ID: id}
	return c.nodeWrite(device, "delete-vlan", body, opts)
}

// ConfigureIRB configures an IRB (Integrated Routing and Bridging) interface.
func (c *Client) ConfigureIRB(device string, config newtron.IRBConfigureRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "configure-irb", config, opts)
}

// UpdateIRB mutates an existing IRB's identity in place (§48).
func (c *Client) UpdateIRB(device string, config newtron.IRBConfigureRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "update-irb", config, opts)
}

// UnconfigureIRB removes an IRB interface.
func (c *Client) UnconfigureIRB(device string, vlanID int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.UnconfigureIRBRequest{VlanID: vlanID}
	return c.nodeWrite(device, "unconfigure-irb", body, opts)
}

// CreateVRF creates a VRF.
func (c *Client) CreateVRF(device, name string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.VRFCreateRequest{Name: name}
	return c.nodeWrite(device, "create-vrf", body, opts)
}

// DeleteVRF deletes a VRF.
func (c *Client) DeleteVRF(device, name string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.nodeWrite(device, "delete-vrf", body, opts)
}

// BindIPVPN binds an IP-VPN. ipvpn is the IP-VPN spec name; the on-device
// VRF name is derived from it as "Vrf_"+name (sonic-vrf.yang).
func (c *Client) BindIPVPN(device, vrf, ipvpn string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.BindIPVPNRequest{VRF: vrf, IPVPN: ipvpn}
	return c.nodeWrite(device, "bind-ipvpn", body, opts)
}

// UnbindIPVPN removes an IP-VPN's enrollment from a VRF. vrf is the VRF that
// joined the VPN; ipvpn is the IP-VPN spec name.
func (c *Client) UnbindIPVPN(device, vrf, ipvpn string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.BindIPVPNRequest{VRF: vrf, IPVPN: ipvpn}
	return c.nodeWrite(device, "unbind-ipvpn", body, opts)
}

// AddStaticRoute adds a static route to a VRF.
func (c *Client) AddStaticRoute(device, vrf, prefix, nexthop string, metric int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.StaticRouteRequest{VRF: vrf, Prefix: prefix, NextHop: nexthop, Metric: metric}
	return c.nodeWrite(device, "add-static-route", body, opts)
}

// UpdateStaticRoute atomically mutates fields on a static route under
// the per-device intent lock. Per §47 the key (vrf, prefix) is
// immutable. Closes the forwarding black hole remove + add exposes
// today (#227).
func (c *Client) UpdateStaticRoute(device, vrf, prefix, nexthop string, metric int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.StaticRouteUpdateRequest{VRF: vrf, Prefix: prefix, NextHop: nexthop, Metric: metric}
	return c.nodeWrite(device, "update-static-route", body, opts)
}

// RemoveStaticRoute removes a static route from a VRF.
func (c *Client) RemoveStaticRoute(device, vrf, prefix string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		VRF    string `json:"vrf"`
		Prefix string `json:"prefix"`
	}{VRF: vrf, Prefix: prefix}
	return c.nodeWrite(device, "remove-static-route", body, opts)
}

// CreateACL creates an ACL table.
func (c *Client) CreateACL(device string, config newtron.ACLCreateRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "create-acl", config, opts)
}

// DeleteACL deletes an ACL table.
func (c *Client) DeleteACL(device, name string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.nodeWrite(device, "delete-acl", body, opts)
}

// AddACLRule adds a rule to an ACL. The acl parameter takes precedence
// over any ACL value already set on config — one authority for the target
// table per call.
func (c *Client) AddACLRule(device, acl string, config newtron.ACLRuleAddRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	config.ACL = acl
	return c.nodeWrite(device, "add-acl-rule", config, opts)
}

// UpdateACLRule atomically mutates fields on an ACL rule under the
// per-device intent lock. Per §47 the key (acl_table, rule_name) is
// immutable. Closes the packet-leak window remove + add exposes
// today (#227).
func (c *Client) UpdateACLRule(device, acl string, config newtron.ACLRuleUpdateRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	config.ACL = acl
	return c.nodeWrite(device, "update-acl-rule", config, opts)
}

// RemoveACLRule removes a rule from an ACL.
func (c *Client) RemoveACLRule(device, acl, rule string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		ACL  string `json:"acl"`
		Rule string `json:"rule"`
	}{ACL: acl, Rule: rule}
	return c.nodeWrite(device, "remove-acl-rule", body, opts)
}

// CreatePortChannel creates a port channel.
func (c *Client) CreatePortChannel(device string, config newtron.PortChannelCreateRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "create-portchannel", config, opts)
}

// DeletePortChannel deletes a port channel.
func (c *Client) DeletePortChannel(device, name string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.nodeWrite(device, "delete-portchannel", body, opts)
}

// AddPortChannelMember adds a member to a port channel.
func (c *Client) AddPortChannelMember(device, pc, member string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		PortChannel string `json:"portchannel"`
		Interface   string `json:"interface"`
	}{PortChannel: pc, Interface: member}
	return c.nodeWrite(device, "add-portchannel-member", body, opts)
}

// RemovePortChannelMember removes a member from a port channel.
func (c *Client) RemovePortChannelMember(device, pc, member string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		PortChannel string `json:"portchannel"`
		Interface   string `json:"interface"`
	}{PortChannel: pc, Interface: member}
	return c.nodeWrite(device, "remove-portchannel-member", body, opts)
}

// ============================================================================
// Device lifecycle operations (no ChangeSet)
// ============================================================================

// ConfigReload runs config reload on the device.
func (c *Client) ConfigReload(device string) error {
	return c.doPost(c.nodePath(device)+"/reload-config", nil, nil)
}

// SaveConfig saves the running config to config_db.json.
func (c *Client) SaveConfig(device string) error {
	return c.doPost(c.nodePath(device)+"/save-config", nil, nil)
}

// RestartService restarts a SONiC Docker service.
func (c *Client) RestartService(device, service string) error {
	body := api.RestartDaemonRequest{Daemon: service}
	return c.doPost(c.nodePath(device)+"/restart-daemon", body, nil)
}

// RefreshBGP forces the device's FRR to re-advertise all BGP routes via a soft
// clear — the operational post-provision convergence nudge newtlab invokes so
// device interaction stays inside newtron (§27). No request body.
func (c *Client) RefreshBGP(device string) error {
	return c.doPost(c.nodePath(device)+"/refresh-bgp", nil, nil)
}

// SSHCommand runs a command via SSH on the device.
func (c *Client) SSHCommand(device, command string) (string, error) {
	body := api.SSHCommandRequest{Command: command}
	var result api.SSHCommandResponse
	if err := c.doPost(c.nodePath(device)+"/ssh-command", body, &result); err != nil {
		return "", err
	}
	return result.Output, nil
}

// ============================================================================
// Intent methods
// ============================================================================

// ============================================================================
// Intent operations
// ============================================================================

// IntentProjection returns the per-Node projection (`sonic.RawConfigDB`) —
// the canonical expected state derived from intent replay. The client's
// Mode field selects the source (default intent / topology / loopback) and
// is appended to the URL automatically by withMode.
func (c *Client) IntentProjection(device string) (sonic.RawConfigDB, error) {
	var result sonic.RawConfigDB
	if err := c.doGet(c.nodePath(device)+"/intent/projection", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// IntentSnapshot returns the device's NEWTRON_INTENT records in canonical form
// (every record, DAG links normalized) — the substrate for before/after
// "back where we started?" comparisons.
func (c *Client) IntentSnapshot(device string) (map[string]map[string]string, error) {
	var result map[string]map[string]string
	if err := c.doGet(c.nodePath(device)+"/intent/snapshot", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ConfigDBSnapshot returns the device's actual CONFIG_DB as a single
// internally-consistent snapshot. ownedOnly=true returns only newtron-owned
// tables; ownedOnly=false returns the device's entire CONFIG_DB. The param
// is always sent explicitly — the client's behavior must not silently track
// a server-side default.
func (c *Client) ConfigDBSnapshot(device string, ownedOnly bool) (sonic.RawConfigDB, error) {
	path := c.nodePath(device) + fmt.Sprintf("/configdb?owned_only=%t", ownedOnly)
	var result sonic.RawConfigDB
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// IntentDrift compares the node projection (expected state) against actual
// CONFIG_DB. Mode selects the source of expected state: "" or "intent" uses
// device NEWTRON_INTENT records; "topology" uses topology.json steps.
func (c *Client) IntentDrift(device, mode string) ([]newtron.DriftEntry, error) {
	path := c.nodePath(device) + "/intent/drift"
	if mode == "topology" {
		path += "?mode=topology"
	}
	var result []newtron.DriftEntry
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// IntentSave persists the device's current intent DB back to topology.json.
func (c *Client) IntentSave(device, mode string) (*newtron.TopologySnapshot, error) {
	path := c.nodePath(device) + "/intent/save"
	if mode == "topology" {
		path += "?mode=topology"
	}
	var result newtron.TopologySnapshot
	if err := c.doPost(path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// IntentReload rebuilds the node from topology.json (topology mode only).
func (c *Client) IntentReload(device string) (*newtron.TopologySnapshot, error) {
	path := c.nodePath(device) + "/intent/reload?mode=topology"
	var result newtron.TopologySnapshot
	if err := c.doPost(path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// IntentClear creates an empty node with ports only (topology mode only).
func (c *Client) IntentClear(device string) (*newtron.TopologySnapshot, error) {
	path := c.nodePath(device) + "/intent/clear?mode=topology"
	var result newtron.TopologySnapshot
	if err := c.doPost(path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// Reconcile delivers the projection to the device, eliminating drift.
// mode selects the intent source: "topology" or "" (actuated).
// reconcileMode selects the delivery mechanism: "full", "delta", or "" (default).
func (c *Client) Reconcile(device, mode, reconcileMode string, opts newtron.ExecOpts) (*newtron.ReconcileResult, error) {
	q := url.Values{}
	if !opts.Execute {
		q.Set("dry_run", "true")
	}
	if opts.NoSave {
		q.Set("no_save", "true")
	}
	if mode == "topology" {
		q.Set("mode", "topology")
	}
	if reconcileMode != "" {
		q.Set("reconcile", reconcileMode)
	}
	path := c.nodePath(device) + "/intent/reconcile"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var result newtron.ReconcileResult
	if err := c.doPost(path, nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
