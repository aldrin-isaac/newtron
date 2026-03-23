package client

import (
	"fmt"
	"net/url"
	"time"

	"github.com/newtron-network/newtron/pkg/newtron"
	"github.com/newtron-network/newtron/pkg/newtron/api"
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

// ListInterfaces returns all interface summaries.
func (c *Client) ListInterfaces(device string) ([]newtron.InterfaceSummary, error) {
	var result []newtron.InterfaceSummary
	if err := c.doGet(c.nodePath(device)+"/interface", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowInterface returns details of a single interface.
func (c *Client) ShowInterface(device, name string) (*newtron.InterfaceDetail, error) {
	var result newtron.InterfaceDetail
	if err := c.doGet(c.nodePath(device)+"/interface/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListVLANs returns VLAN status entries.
func (c *Client) ListVLANs(device string) ([]newtron.VLANStatusEntry, error) {
	var result []newtron.VLANStatusEntry
	if err := c.doGet(c.nodePath(device)+"/vlan", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowVLAN returns details of a single VLAN.
func (c *Client) ShowVLAN(device string, id int) (*newtron.VLANStatusEntry, error) {
	var result newtron.VLANStatusEntry
	if err := c.doGet(fmt.Sprintf("%s/vlan/%d", c.nodePath(device), id), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListVRFs returns VRF status entries.
func (c *Client) ListVRFs(device string) ([]newtron.VRFStatusEntry, error) {
	var result []newtron.VRFStatusEntry
	if err := c.doGet(c.nodePath(device)+"/vrf", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowVRF returns details of a single VRF.
func (c *Client) ShowVRF(device, name string) (*newtron.VRFDetail, error) {
	var result newtron.VRFDetail
	if err := c.doGet(c.nodePath(device)+"/vrf/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListACLs returns ACL table summaries.
func (c *Client) ListACLs(device string) ([]newtron.ACLTableSummary, error) {
	var result []newtron.ACLTableSummary
	if err := c.doGet(c.nodePath(device)+"/acl", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowACL returns details of a single ACL.
func (c *Client) ShowACL(device, name string) (*newtron.ACLTableDetail, error) {
	var result newtron.ACLTableDetail
	if err := c.doGet(c.nodePath(device)+"/acl/"+url.PathEscape(name), &result); err != nil {
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
	if err := c.doGet(c.nodePath(device)+"/lag", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowLAGDetail returns details of a single LAG.
func (c *Client) ShowLAGDetail(device, name string) (*newtron.LAGStatusEntry, error) {
	var result newtron.LAGStatusEntry
	if err := c.doGet(c.nodePath(device)+"/lag/"+url.PathEscape(name), &result); err != nil {
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
	path := fmt.Sprintf("%s/route/%s/%s", c.nodePath(device), url.PathEscape(vrf), prefix)
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// GetRouteASIC looks up a route in ASIC_DB.
func (c *Client) GetRouteASIC(device, prefix string) (*newtron.RouteEntry, error) {
	var result newtron.RouteEntry
	path := fmt.Sprintf("%s/route-asic/%s", c.nodePath(device), prefix)
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

// QueryStateDB reads a STATE_DB hash entry.
func (c *Client) QueryStateDB(device, table, key string) (map[string]string, error) {
	var result map[string]string
	path := fmt.Sprintf("%s/statedb/%s/%s", c.nodePath(device), url.PathEscape(table), url.PathEscape(key))
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

// ConfigureBGP configures BGP globals on a device.
func (c *Client) ConfigureBGP(device string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "configure-bgp", nil, opts)
}

// RemoveBGPGlobals removes BGP globals from a device.
func (c *Client) RemoveBGPGlobals(device string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "remove-bgp", nil, opts)
}

// AddOverlayPeer adds a loopback (overlay) BGP neighbor.
func (c *Client) AddOverlayPeer(device string, config newtron.BGPNeighborConfig, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "add-overlay-peer", config, opts)
}

// RemoveOverlayPeer removes an overlay BGP neighbor by IP.
func (c *Client) RemoveOverlayPeer(device, ip string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		IP string `json:"ip"`
	}{IP: ip}
	return c.nodeWrite(device, "remove-overlay-peer", body, opts)
}

// SetupVTEP configures the EVPN overlay on a device.
func (c *Client) SetupVTEP(device, sourceIP string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.SetupVTEPRequest{SourceIP: sourceIP}
	return c.nodeWrite(device, "setup-vtep", body, opts)
}

// TeardownVTEP removes the EVPN overlay.
func (c *Client) TeardownVTEP(device string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "teardown-vtep", nil, opts)
}

// MapL2VNI maps a VLAN to an L2VNI for EVPN.
func (c *Client) MapL2VNI(device string, vlanID, vni int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.MapL2VNIRequest{VlanID: vlanID, VNI: vni}
	return c.nodeWrite(device, "map-l2vni", body, opts)
}

// UnmapL2VNI removes the L2VNI mapping for a VLAN.
func (c *Client) UnmapL2VNI(device string, vlanID int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.UnmapL2VNIRequest{VlanID: vlanID}
	return c.nodeWrite(device, "unmap-l2vni", body, opts)
}

// ConfigureRouteReflector configures a device as a BGP route reflector.
func (c *Client) ConfigureRouteReflector(device string, rrOpts newtron.RouteReflectorOpts, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "configure-route-reflector", rrOpts, opts)
}

// ConfigureLoopback configures the loopback interface.
func (c *Client) ConfigureLoopback(device string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "configure-loopback", nil, opts)
}

// RemoveLoopback removes the loopback interface.
func (c *Client) RemoveLoopback(device string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "remove-loopback", nil, opts)
}

// CreateVLAN creates a VLAN.
func (c *Client) CreateVLAN(device string, id int, description string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.VLANCreateRequest{ID: id, Description: description}
	return c.nodeWrite(device, "create-vlan", body, opts)
}

// DeleteVLAN deletes a VLAN.
func (c *Client) DeleteVLAN(device string, id int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		ID int `json:"id"`
	}{ID: id}
	return c.nodeWrite(device, "delete-vlan", body, opts)
}

// AddVLANMember adds an interface to a VLAN.
func (c *Client) AddVLANMember(device string, id int, iface string, tagged bool, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.VLANMemberRequest{ID: id, Interface: iface, Tagged: tagged}
	return c.nodeWrite(device, "add-vlan-member", body, opts)
}

// RemoveVLANMember removes an interface from a VLAN.
func (c *Client) RemoveVLANMember(device string, id int, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		ID        int    `json:"id"`
		Interface string `json:"interface"`
	}{ID: id, Interface: iface}
	return c.nodeWrite(device, "remove-vlan-member", body, opts)
}

// ConfigureSVI configures an SVI.
func (c *Client) ConfigureSVI(device string, config newtron.SVIConfigureRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "configure-svi", config, opts)
}

// RemoveSVI removes an SVI.
func (c *Client) RemoveSVI(device string, vlanID int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.RemoveSVIRequest{VlanID: vlanID}
	return c.nodeWrite(device, "remove-svi", body, opts)
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

// AddVRFInterface adds an interface to a VRF.
func (c *Client) AddVRFInterface(device, vrf, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.VRFInterfaceRequest{VRF: vrf, Interface: iface}
	return c.nodeWrite(device, "add-vrf-interface", body, opts)
}

// RemoveVRFInterface removes an interface from a VRF.
func (c *Client) RemoveVRFInterface(device, vrf, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.VRFInterfaceRequest{VRF: vrf, Interface: iface}
	return c.nodeWrite(device, "remove-vrf-interface", body, opts)
}

// BindIPVPN binds an IP-VPN to a VRF.
func (c *Client) BindIPVPN(device, vrf, ipvpn string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.BindIPVPNRequest{VRF: vrf, IPVPN: ipvpn}
	return c.nodeWrite(device, "bind-ipvpn", body, opts)
}

// UnbindIPVPN unbinds an IP-VPN from a VRF.
func (c *Client) UnbindIPVPN(device, vrf string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		VRF string `json:"vrf"`
	}{VRF: vrf}
	return c.nodeWrite(device, "unbind-ipvpn", body, opts)
}

// AddStaticRoute adds a static route to a VRF.
func (c *Client) AddStaticRoute(device, vrf, prefix, nexthop string, metric int, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.StaticRouteRequest{VRF: vrf, Prefix: prefix, NextHop: nexthop, Metric: metric}
	return c.nodeWrite(device, "add-static-route", body, opts)
}

// RemoveStaticRoute removes a static route from a VRF.
func (c *Client) RemoveStaticRoute(device, vrf, prefix string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		VRF    string `json:"vrf"`
		Prefix string `json:"prefix"`
	}{VRF: vrf, Prefix: prefix}
	return c.nodeWrite(device, "remove-static-route", body, opts)
}

// CreateACLTable creates an ACL table.
func (c *Client) CreateACLTable(device string, config newtron.ACLCreateRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	return c.nodeWrite(device, "create-acl-table", config, opts)
}

// DeleteACLTable deletes an ACL table.
func (c *Client) DeleteACLTable(device, name string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.nodeWrite(device, "delete-acl-table", body, opts)
}

// AddACLRule adds a rule to an ACL.
func (c *Client) AddACLRule(device, acl string, config newtron.ACLRuleAddRequest, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	// The handler expects acl in the body alongside the rule fields.
	body := struct {
		ACL      string `json:"acl"`
		RuleName string `json:"rule_name"`
		Priority int    `json:"priority"`
		Action   string `json:"action"`
		SrcIP    string `json:"src_ip"`
		DstIP    string `json:"dst_ip"`
		Protocol string `json:"protocol"`
		SrcPort  string `json:"src_port"`
		DstPort  string `json:"dst_port"`
	}{
		ACL:      acl,
		RuleName: config.RuleName,
		Priority: config.Priority,
		Action:   config.Action,
		SrcIP:    config.SrcIP,
		DstIP:    config.DstIP,
		Protocol: config.Protocol,
		SrcPort:  config.SrcPort,
		DstPort:  config.DstPort,
	}
	return c.nodeWrite(device, "add-acl-rule", body, opts)
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

// SetDeviceMetadata updates DEVICE_METADATA fields.
func (c *Client) SetDeviceMetadata(device string, fields map[string]string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.SetDeviceMetadataRequest{Fields: fields}
	return c.nodeWrite(device, "set-device-metadata", body, opts)
}

// ApplyQoS applies a QoS policy to an interface (node-level).
func (c *Client) ApplyQoS(device, iface, policy string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.NodeApplyQoSRequest{Interface: iface, Policy: policy}
	return c.nodeWrite(device, "apply-qos", body, opts)
}

// RemoveQoS removes QoS from an interface (node-level).
func (c *Client) RemoveQoS(device, iface string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.NodeRemoveQoSRequest{Interface: iface}
	return c.nodeWrite(device, "remove-qos", body, opts)
}

// Cleanup removes orphaned config.
func (c *Client) Cleanup(device, cleanupType string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	body := api.CleanupRequest{Type: cleanupType}
	return c.nodeWrite(device, "cleanup", body, opts)
}

// VerifyCommitted re-verifies all committed changes.
func (c *Client) VerifyCommitted(device string) (*newtron.VerificationResult, error) {
	var result newtron.VerificationResult
	if err := c.doPost(c.nodePath(device)+"/verify-committed", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
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

// Refresh reloads the ConfigDB cache from Redis.
func (c *Client) Refresh(device string) error {
	return c.doPost(c.nodePath(device)+"/refresh", nil, nil)
}

// RefreshWithRetry polls Refresh until successful.
func (c *Client) RefreshWithRetry(device string, timeout time.Duration) error {
	path := fmt.Sprintf("%s/refresh?timeout=%s", c.nodePath(device), timeout)
	return c.doPost(path, nil, nil)
}

// ApplyFRRDefaults applies FRR defaults on the device.
func (c *Client) ApplyFRRDefaults(device string) error {
	return c.doPost(c.nodePath(device)+"/apply-frr-defaults", nil, nil)
}

// RestartService restarts a SONiC Docker service.
func (c *Client) RestartService(device, service string) error {
	body := api.RestartDaemonRequest{Daemon: service}
	return c.doPost(c.nodePath(device)+"/restart-daemon", body, nil)
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

// Execute runs a batch of operations on a device.
func (c *Client) Execute(device string, req api.ExecuteRequest) (*newtron.WriteResult, error) {
	var result newtron.WriteResult
	if err := c.doPost(c.nodePath(device)+"/execute", req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================================================================
// Intent methods
// ============================================================================

// ListIntents returns all intents on a device (connects read-only, no lock).
func (c *Client) ListIntents(device string) ([]newtron.Intent, error) {
	var result []newtron.Intent
	if err := c.doGet(c.nodePath(device)+"/intents", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ============================================================================
// Zombie operation methods (crash recovery)
// ============================================================================

// ReadZombie reads the zombie operation record from CONFIG_DB (no lock required).
func (c *Client) ReadZombie(device string) (*newtron.OperationIntent, error) {
	var result newtron.OperationIntent
	if err := c.doGet(c.nodePath(device)+"/zombie", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RollbackZombie reverses a zombie operation's changes.
func (c *Client) RollbackZombie(device string, opts newtron.ExecOpts) (*newtron.WriteResult, error) {
	var result newtron.WriteResult
	if err := c.doPost(c.nodePath(device)+"/rollback-zombie"+execQuery(opts), nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ClearZombie clears the zombie operation record without rollback.
func (c *Client) ClearZombie(device string) error {
	return c.doPost(c.nodePath(device)+"/clear-zombie", nil, nil)
}

// ============================================================================
// Device settings
// ============================================================================

// ReadSettings reads newtron operational settings from a device.
func (c *Client) ReadSettings(device string) (*newtron.DeviceSettings, error) {
	var result newtron.DeviceSettings
	if err := c.doGet(c.nodePath(device)+"/settings", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// WriteSettings writes newtron operational settings to a device.
func (c *Client) WriteSettings(device string, s *newtron.DeviceSettings) error {
	return c.doPut(c.nodePath(device)+"/settings", s, nil)
}

// ============================================================================
// History operations
// ============================================================================

// ReadHistory returns the rolling history for a device.
func (c *Client) ReadHistory(device string) (*newtron.HistoryResult, error) {
	var result newtron.HistoryResult
	if err := c.doGet(c.nodePath(device)+"/history", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// RollbackHistory reverses the most recent history entry.
func (c *Client) RollbackHistory(device string, opts newtron.ExecOpts) (*newtron.HistoryRollbackResult, error) {
	var result newtron.HistoryRollbackResult
	if err := c.doPost(c.nodePath(device)+"/rollback-history"+execQuery(opts), nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================================================================
// Drift detection
// ============================================================================

// DetectDrift compares expected vs actual CONFIG_DB for a device.
func (c *Client) DetectDrift(device string) (*newtron.DriftReport, error) {
	var result newtron.DriftReport
	if err := c.doGet(c.nodePath(device)+"/drift", &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// NetworkDrift runs drift detection across all topology devices.
func (c *Client) NetworkDrift() (*newtron.NetworkDriftSummary, error) {
	var result newtron.NetworkDriftSummary
	if err := c.doGet(c.networkPath()+"/drift", &result); err != nil {
		return nil, err
	}
	return &result, nil
}
