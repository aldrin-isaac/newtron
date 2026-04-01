package client

import (
	"fmt"
	"net/url"

	"github.com/newtron-network/newtron/pkg/newtron"
	"github.com/newtron-network/newtron/pkg/newtron/api"
)

// ============================================================================
// Network management
// ============================================================================

// ListNetworks returns all registered networks.
func (c *Client) ListNetworks() ([]api.NetworkInfo, error) {
	var result []api.NetworkInfo
	if err := c.doGet("/network", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// HasTopology returns whether the registered network has a topology.
func (c *Client) HasTopology() (bool, error) {
	var result []api.NetworkInfo
	if err := c.doGet("/network", &result); err != nil {
		return false, err
	}
	for _, n := range result {
		if n.ID == c.networkID {
			return n.HasTopology, nil
		}
	}
	return false, fmt.Errorf("network %q not found", c.networkID)
}

// TopologyDeviceNames returns the sorted device names from the topology.
func (c *Client) TopologyDeviceNames() ([]string, error) {
	var result []string
	if err := c.doGet(c.networkPath()+"/topology/node", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// IsHostDevice checks if a device is a virtual host (non-SONiC).
func (c *Client) IsHostDevice(name string) (bool, error) {
	var result newtron.HostProfile
	err := c.doGet(c.networkPath()+"/host/"+url.PathEscape(name), &result)
	if err != nil {
		if se, ok := err.(*ServerError); ok && se.StatusCode == 404 {
			return false, nil
		}
		return false, err
	}
	return true, nil
}

// GetHostProfile returns SSH connection params for a host device.
func (c *Client) GetHostProfile(name string) (*newtron.HostProfile, error) {
	var result newtron.HostProfile
	if err := c.doGet(c.networkPath()+"/host/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ============================================================================
// Profiles
// ============================================================================

// ListProfiles returns all device profile names.
func (c *Client) ListProfiles() ([]string, error) {
	var result []string
	if err := c.doGet(c.networkPath()+"/profile", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowProfile returns details of a named device profile.
func (c *Client) ShowProfile(name string) (*newtron.DeviceProfileDetail, error) {
	var result newtron.DeviceProfileDetail
	if err := c.doGet(c.networkPath()+"/profile/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateProfile creates a new device profile.
func (c *Client) CreateProfile(req newtron.CreateDeviceProfileRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/create-profile"+execQuery(opts), req, nil)
}

// DeleteProfile deletes a device profile.
func (c *Client) DeleteProfile(name string, opts newtron.ExecOpts) error {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.doPost(c.networkPath()+"/delete-profile"+execQuery(opts), body, nil)
}

// ============================================================================
// Zones
// ============================================================================

// ListZones returns all zone names.
func (c *Client) ListZones() ([]string, error) {
	var result []string
	if err := c.doGet(c.networkPath()+"/zone", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowZone returns details of a named zone.
func (c *Client) ShowZone(name string) (*newtron.ZoneDetail, error) {
	var result newtron.ZoneDetail
	if err := c.doGet(c.networkPath()+"/zone/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateZone creates a new zone.
func (c *Client) CreateZone(req newtron.CreateZoneRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/create-zone"+execQuery(opts), req, nil)
}

// DeleteZone deletes a zone.
func (c *Client) DeleteZone(name string, opts newtron.ExecOpts) error {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.doPost(c.networkPath()+"/delete-zone"+execQuery(opts), body, nil)
}

// ============================================================================
// Spec reads
// ============================================================================

// ListServices returns all service names.
func (c *Client) ListServices() ([]string, error) {
	var result []string
	if err := c.doGet(c.networkPath()+"/service", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowService returns details of a named service.
func (c *Client) ShowService(name string) (*newtron.ServiceDetail, error) {
	var result newtron.ServiceDetail
	if err := c.doGet(c.networkPath()+"/service/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListIPVPNs returns all IP-VPN specs keyed by name.
func (c *Client) ListIPVPNs() (map[string]*newtron.IPVPNDetail, error) {
	var result map[string]*newtron.IPVPNDetail
	if err := c.doGet(c.networkPath()+"/ipvpn", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowIPVPN returns details of a named IP-VPN.
func (c *Client) ShowIPVPN(name string) (*newtron.IPVPNDetail, error) {
	var result newtron.IPVPNDetail
	if err := c.doGet(c.networkPath()+"/ipvpn/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListMACVPNs returns all MAC-VPN specs keyed by name.
func (c *Client) ListMACVPNs() (map[string]*newtron.MACVPNDetail, error) {
	var result map[string]*newtron.MACVPNDetail
	if err := c.doGet(c.networkPath()+"/macvpn", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowMACVPN returns details of a named MAC-VPN.
func (c *Client) ShowMACVPN(name string) (*newtron.MACVPNDetail, error) {
	var result newtron.MACVPNDetail
	if err := c.doGet(c.networkPath()+"/macvpn/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListQoSPolicies returns all QoS policy names.
func (c *Client) ListQoSPolicies() ([]string, error) {
	var result []string
	if err := c.doGet(c.networkPath()+"/qos-policy", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowQoSPolicy returns details of a named QoS policy.
func (c *Client) ShowQoSPolicy(name string) (*newtron.QoSPolicyDetail, error) {
	var result newtron.QoSPolicyDetail
	if err := c.doGet(c.networkPath()+"/qos-policy/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListFilters returns all filter names.
func (c *Client) ListFilters() ([]string, error) {
	var result []string
	if err := c.doGet(c.networkPath()+"/filter", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowFilter returns details of a named filter.
func (c *Client) ShowFilter(name string) (*newtron.FilterDetail, error) {
	var result newtron.FilterDetail
	if err := c.doGet(c.networkPath()+"/filter/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListPlatforms returns all platform specs keyed by name.
func (c *Client) ListPlatforms() (map[string]*newtron.PlatformDetail, error) {
	var result map[string]*newtron.PlatformDetail
	if err := c.doGet(c.networkPath()+"/platform", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowPlatform returns details of a named platform.
func (c *Client) ShowPlatform(name string) (*newtron.PlatformDetail, error) {
	var result newtron.PlatformDetail
	if err := c.doGet(c.networkPath()+"/platform/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ListRoutePolicies returns all route policy names.
func (c *Client) ListRoutePolicies() ([]string, error) {
	var result []string
	if err := c.doGet(c.networkPath()+"/route-policy", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ListPrefixLists returns all prefix list names.
func (c *Client) ListPrefixLists() ([]string, error) {
	var result []string
	if err := c.doGet(c.networkPath()+"/prefix-list", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ShowPrefixList returns the detail for a prefix list.
func (c *Client) ShowPrefixList(name string) (*newtron.PrefixListDetail, error) {
	var result newtron.PrefixListDetail
	if err := c.doGet(c.networkPath()+"/prefix-list/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreatePrefixList creates a new prefix list.
func (c *Client) CreatePrefixList(req newtron.CreatePrefixListRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/create-prefix-list"+execQuery(opts), req, nil)
}

// DeletePrefixList deletes a prefix list.
func (c *Client) DeletePrefixList(name string, opts newtron.ExecOpts) error {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.doPost(c.networkPath()+"/delete-prefix-list"+execQuery(opts), body, nil)
}

// AddPrefixListEntry adds an entry to a prefix list.
func (c *Client) AddPrefixListEntry(req newtron.AddPrefixListEntryRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/add-prefix-list-entry"+execQuery(opts), req, nil)
}

// RemovePrefixListEntry removes an entry from a prefix list.
func (c *Client) RemovePrefixListEntry(prefixList, prefix string, opts newtron.ExecOpts) error {
	body := struct {
		PrefixList string `json:"prefix_list"`
		Prefix     string `json:"prefix"`
	}{PrefixList: prefixList, Prefix: prefix}
	return c.doPost(c.networkPath()+"/remove-prefix-list-entry"+execQuery(opts), body, nil)
}

// ShowRoutePolicy returns the detail for a route policy.
func (c *Client) ShowRoutePolicy(name string) (*newtron.RoutePolicyDetail, error) {
	var result newtron.RoutePolicyDetail
	if err := c.doGet(c.networkPath()+"/route-policy/"+url.PathEscape(name), &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// CreateRoutePolicy creates a new route policy.
func (c *Client) CreateRoutePolicy(req newtron.CreateRoutePolicyRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/create-route-policy"+execQuery(opts), req, nil)
}

// DeleteRoutePolicy deletes a route policy.
func (c *Client) DeleteRoutePolicy(name string, opts newtron.ExecOpts) error {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.doPost(c.networkPath()+"/delete-route-policy"+execQuery(opts), body, nil)
}

// AddRoutePolicyRule adds a rule to a route policy.
func (c *Client) AddRoutePolicyRule(req newtron.AddRoutePolicyRuleRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/add-route-policy-rule"+execQuery(opts), req, nil)
}

// RemoveRoutePolicyRule removes a rule from a route policy.
func (c *Client) RemoveRoutePolicyRule(policy string, seq int, opts newtron.ExecOpts) error {
	body := struct {
		Policy   string `json:"policy"`
		Sequence int    `json:"sequence"`
	}{Policy: policy, Sequence: seq}
	return c.doPost(c.networkPath()+"/remove-route-policy-rule"+execQuery(opts), body, nil)
}

// GetAllFeatures returns all feature names.
func (c *Client) GetAllFeatures() ([]string, error) {
	var result []string
	if err := c.doGet(c.networkPath()+"/feature", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// GetFeatureDependencies returns dependencies of a feature.
func (c *Client) GetFeatureDependencies(feature string) ([]string, error) {
	var result []string
	if err := c.doGet(c.networkPath()+"/feature/"+url.PathEscape(feature)+"/dependency", &result); err != nil {
		return nil, err
	}
	return result, nil
}

// PlatformSupportsFeature checks if a platform supports a feature.
func (c *Client) PlatformSupportsFeature(platform, feature string) (bool, error) {
	var result map[string]bool
	path := c.networkPath() + "/platform/" + url.PathEscape(platform) + "/supports/" + url.PathEscape(feature)
	if err := c.doGet(path, &result); err != nil {
		return false, err
	}
	return result["supported"], nil
}

// GetUnsupportedDueTo returns features unsupported because a base feature is missing.
func (c *Client) GetUnsupportedDueTo(feature string) ([]string, error) {
	var result []string
	path := c.networkPath() + "/feature/" + url.PathEscape(feature) + "/unsupported-due-to"
	if err := c.doGet(path, &result); err != nil {
		return nil, err
	}
	return result, nil
}

// ============================================================================
// Spec writes
// ============================================================================

// CreateService creates a new service spec.
func (c *Client) CreateService(req newtron.CreateServiceRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/create-service"+execQuery(opts), req, nil)
}

// DeleteService deletes a service spec.
func (c *Client) DeleteService(name string, opts newtron.ExecOpts) error {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.doPost(c.networkPath()+"/delete-service"+execQuery(opts), body, nil)
}

// CreateIPVPN creates a new IP-VPN spec.
func (c *Client) CreateIPVPN(req newtron.CreateIPVPNRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/create-ipvpn"+execQuery(opts), req, nil)
}

// DeleteIPVPN deletes an IP-VPN spec.
func (c *Client) DeleteIPVPN(name string, opts newtron.ExecOpts) error {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.doPost(c.networkPath()+"/delete-ipvpn"+execQuery(opts), body, nil)
}

// CreateMACVPN creates a new MAC-VPN spec.
func (c *Client) CreateMACVPN(req newtron.CreateMACVPNRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/create-macvpn"+execQuery(opts), req, nil)
}

// DeleteMACVPN deletes a MAC-VPN spec.
func (c *Client) DeleteMACVPN(name string, opts newtron.ExecOpts) error {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.doPost(c.networkPath()+"/delete-macvpn"+execQuery(opts), body, nil)
}

// CreateQoSPolicy creates a new QoS policy spec.
func (c *Client) CreateQoSPolicy(req newtron.CreateQoSPolicyRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/create-qos-policy"+execQuery(opts), req, nil)
}

// DeleteQoSPolicy deletes a QoS policy spec.
func (c *Client) DeleteQoSPolicy(name string, opts newtron.ExecOpts) error {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.doPost(c.networkPath()+"/delete-qos-policy"+execQuery(opts), body, nil)
}

// AddQoSQueue adds a queue to a QoS policy.
func (c *Client) AddQoSQueue(req newtron.AddQoSQueueRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/add-qos-queue"+execQuery(opts), req, nil)
}

// RemoveQoSQueue removes a queue from a QoS policy.
func (c *Client) RemoveQoSQueue(policy string, queueID int, opts newtron.ExecOpts) error {
	body := struct {
		Policy  string `json:"policy"`
		QueueID int    `json:"queue_id"`
	}{Policy: policy, QueueID: queueID}
	return c.doPost(c.networkPath()+"/remove-qos-queue"+execQuery(opts), body, nil)
}

// CreateFilter creates a new filter spec.
func (c *Client) CreateFilter(req newtron.CreateFilterRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/create-filter"+execQuery(opts), req, nil)
}

// DeleteFilter deletes a filter spec.
func (c *Client) DeleteFilter(name string, opts newtron.ExecOpts) error {
	body := struct {
		Name string `json:"name"`
	}{Name: name}
	return c.doPost(c.networkPath()+"/delete-filter"+execQuery(opts), body, nil)
}

// AddFilterRule adds a rule to a filter.
func (c *Client) AddFilterRule(req newtron.AddFilterRuleRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/add-filter-rule"+execQuery(opts), req, nil)
}

// RemoveFilterRule removes a rule from a filter.
func (c *Client) RemoveFilterRule(filter string, seq int, opts newtron.ExecOpts) error {
	body := struct {
		Filter   string `json:"filter"`
		Sequence int    `json:"sequence"`
	}{Filter: filter, Sequence: seq}
	return c.doPost(c.networkPath()+"/remove-filter-rule"+execQuery(opts), body, nil)
}

// InitDevice prepares a device for newtron management by enabling frrcfgd.
// Returns the status: "initialized" or "already_initialized".
// If force is true, proceeds even if the device has active BGP configuration.
func (c *Client) InitDevice(device string, force bool) (string, error) {
	path := c.nodePath(device) + "/init-device"
	if force {
		path += "?force=true"
	}
	var result map[string]string
	if err := c.doPost(path, nil, &result); err != nil {
		return "", err
	}
	return result["status"], nil
}

