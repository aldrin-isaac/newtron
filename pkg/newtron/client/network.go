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
	return c.doPost(c.networkPath()+"/service"+execQuery(opts), req, nil)
}

// DeleteService deletes a service spec.
func (c *Client) DeleteService(name string, opts newtron.ExecOpts) error {
	return c.doDelete(c.networkPath()+"/service/"+url.PathEscape(name)+execQuery(opts), nil)
}

// CreateIPVPN creates a new IP-VPN spec.
func (c *Client) CreateIPVPN(req newtron.CreateIPVPNRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/ipvpn"+execQuery(opts), req, nil)
}

// DeleteIPVPN deletes an IP-VPN spec.
func (c *Client) DeleteIPVPN(name string, opts newtron.ExecOpts) error {
	return c.doDelete(c.networkPath()+"/ipvpn/"+url.PathEscape(name)+execQuery(opts), nil)
}

// CreateMACVPN creates a new MAC-VPN spec.
func (c *Client) CreateMACVPN(req newtron.CreateMACVPNRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/macvpn"+execQuery(opts), req, nil)
}

// DeleteMACVPN deletes a MAC-VPN spec.
func (c *Client) DeleteMACVPN(name string, opts newtron.ExecOpts) error {
	return c.doDelete(c.networkPath()+"/macvpn/"+url.PathEscape(name)+execQuery(opts), nil)
}

// CreateQoSPolicy creates a new QoS policy spec.
func (c *Client) CreateQoSPolicy(req newtron.CreateQoSPolicyRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/qos-policy"+execQuery(opts), req, nil)
}

// DeleteQoSPolicy deletes a QoS policy spec.
func (c *Client) DeleteQoSPolicy(name string, opts newtron.ExecOpts) error {
	return c.doDelete(c.networkPath()+"/qos-policy/"+url.PathEscape(name)+execQuery(opts), nil)
}

// AddQoSQueue adds a queue to a QoS policy.
func (c *Client) AddQoSQueue(req newtron.AddQoSQueueRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/qos-policy/"+url.PathEscape(req.Policy)+"/queue"+execQuery(opts), req, nil)
}

// RemoveQoSQueue removes a queue from a QoS policy.
func (c *Client) RemoveQoSQueue(policy string, queueID int, opts newtron.ExecOpts) error {
	return c.doDelete(fmt.Sprintf("%s/qos-policy/%s/queue/%d%s",
		c.networkPath(), url.PathEscape(policy), queueID, execQuery(opts)), nil)
}

// CreateFilter creates a new filter spec.
func (c *Client) CreateFilter(req newtron.CreateFilterRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/filter"+execQuery(opts), req, nil)
}

// DeleteFilter deletes a filter spec.
func (c *Client) DeleteFilter(name string, opts newtron.ExecOpts) error {
	return c.doDelete(c.networkPath()+"/filter/"+url.PathEscape(name)+execQuery(opts), nil)
}

// AddFilterRule adds a rule to a filter.
func (c *Client) AddFilterRule(req newtron.AddFilterRuleRequest, opts newtron.ExecOpts) error {
	return c.doPost(c.networkPath()+"/filter/"+url.PathEscape(req.Filter)+"/rule"+execQuery(opts), req, nil)
}

// RemoveFilterRule removes a rule from a filter.
func (c *Client) RemoveFilterRule(filter string, seq int, opts newtron.ExecOpts) error {
	return c.doDelete(fmt.Sprintf("%s/filter/%s/rule/%d%s",
		c.networkPath(), url.PathEscape(filter), seq, execQuery(opts)), nil)
}

// ============================================================================
// Network provision
// ============================================================================

// GenerateComposite generates a composite CONFIG_DB for a device.
// Returns a handle that can be passed to DeliverComposite or VerifyComposite.
func (c *Client) GenerateComposite(device string) (*api.CompositeHandleResponse, error) {
	var result api.CompositeHandleResponse
	if err := c.doPost(c.nodePath(device)+"/composite/generate", nil, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// VerifyComposite verifies a previously generated composite against CONFIG_DB.
func (c *Client) VerifyComposite(device, handle string) (*newtron.VerificationResult, error) {
	var result newtron.VerificationResult
	body := api.CompositeHandleRequest{Handle: handle}
	if err := c.doPost(c.nodePath(device)+"/composite/verify", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// DeliverComposite delivers a composite to a device.
func (c *Client) DeliverComposite(device, handle, mode string) (*newtron.DeliveryResult, error) {
	var result newtron.DeliveryResult
	body := api.CompositeHandleRequest{Handle: handle, Mode: mode}
	if err := c.doPost(c.nodePath(device)+"/composite/deliver", body, &result); err != nil {
		return nil, err
	}
	return &result, nil
}

// ProvisionDevices provisions devices from the topology.
func (c *Client) ProvisionDevices(req newtron.ProvisionRequest, opts newtron.ExecOpts) (*newtron.ProvisionResult, error) {
	var result newtron.ProvisionResult
	if err := c.doPost(c.networkPath()+"/provision"+execQuery(opts), req, &result); err != nil {
		return nil, err
	}
	return &result, nil
}
