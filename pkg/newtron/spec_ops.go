package newtron

import (
	"fmt"
	"sort"

	"github.com/newtron-network/newtron/pkg/newtron/auth"
	"github.com/newtron-network/newtron/pkg/newtron/spec"
)

// ============================================================================
// Services
// ============================================================================

// ListServices returns all service definition names.
func (net *Network) ListServices() []string {
	return net.internal.ListServices()
}

// ShowService returns the service spec for a given name, converted to ServiceDetail.
func (net *Network) ShowService(name string) (*ServiceDetail, error) {
	s, err := net.internal.GetService(name)
	if err != nil {
		return nil, err
	}
	return convertServiceDetail(name, s), nil
}

// CreateService creates a new service definition.
func (net *Network) CreateService(req CreateServiceRequest, opts ExecOpts) error {
	if req.Type == "" {
		return &ValidationError{Field: "type", Message: "required"}
	}
	if _, err := net.internal.GetService(req.Name); err == nil {
		return fmt.Errorf("service '%s' already exists", req.Name)
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	svc := &spec.ServiceSpec{
		Description:   req.Description,
		ServiceType:   req.Type,
		IPVPN:         req.IPVPN,
		MACVPN:        req.MACVPN,
		VRFType:       req.VRFType,
		QoSPolicy:     req.QoSPolicy,
		IngressFilter: req.IngressFilter,
		EgressFilter:  req.EgressFilter,
	}
	return net.internal.SaveService(req.Name, svc)
}

// DeleteService removes a service definition.
func (net *Network) DeleteService(name string, opts ExecOpts) error {
	if _, err := net.internal.GetService(name); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteService(name)
}

// ============================================================================
// IP-VPNs
// ============================================================================

// ListIPVPNs returns all IP-VPN definitions, converted to IPVPNDetail.
func (net *Network) ListIPVPNs() map[string]*IPVPNDetail {
	raw := net.internal.Spec().IPVPNs
	result := make(map[string]*IPVPNDetail, len(raw))
	for name, s := range raw {
		result[name] = convertIPVPNDetail(name, s)
	}
	return result
}

// ShowIPVPN returns the IP-VPN spec for a given name, converted to IPVPNDetail.
func (net *Network) ShowIPVPN(name string) (*IPVPNDetail, error) {
	s, err := net.internal.GetIPVPN(name)
	if err != nil {
		return nil, err
	}
	return convertIPVPNDetail(name, s), nil
}

// CreateIPVPN creates a new IP-VPN definition.
func (net *Network) CreateIPVPN(req CreateIPVPNRequest, opts ExecOpts) error {
	if req.L3VNI <= 0 {
		return &ValidationError{Field: "l3vni", Message: "required"}
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	ipvpn := &spec.IPVPNSpec{
		Description:  req.Description,
		L3VNI:        req.L3VNI,
		VRF:          req.VRF,
		RouteTargets: req.RouteTargets,
	}
	return net.internal.SaveIPVPN(req.Name, ipvpn)
}

// DeleteIPVPN removes an IP-VPN definition.
func (net *Network) DeleteIPVPN(name string, opts ExecOpts) error {
	if _, err := net.internal.GetIPVPN(name); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteIPVPN(name)
}

// ============================================================================
// MAC-VPNs
// ============================================================================

// ListMACVPNs returns all MAC-VPN definitions, converted to MACVPNDetail.
func (net *Network) ListMACVPNs() map[string]*MACVPNDetail {
	raw := net.internal.Spec().MACVPNs
	result := make(map[string]*MACVPNDetail, len(raw))
	for name, s := range raw {
		result[name] = convertMACVPNDetail(name, s)
	}
	return result
}

// ShowMACVPN returns the MAC-VPN spec for a given name, converted to MACVPNDetail.
func (net *Network) ShowMACVPN(name string) (*MACVPNDetail, error) {
	s, err := net.internal.GetMACVPN(name)
	if err != nil {
		return nil, err
	}
	return convertMACVPNDetail(name, s), nil
}

// CreateMACVPN creates a new MAC-VPN definition.
func (net *Network) CreateMACVPN(req CreateMACVPNRequest, opts ExecOpts) error {
	if req.VNI <= 0 {
		return &ValidationError{Field: "vni", Message: "required"}
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	macvpn := &spec.MACVPNSpec{
		Description:    req.Description,
		VNI:            req.VNI,
		VlanID:         req.VlanID,
		AnycastIP:      req.AnycastIP,
		AnycastMAC:     req.AnycastMAC,
		RouteTargets:   req.RouteTargets,
		ARPSuppression: req.ARPSuppression,
	}
	return net.internal.SaveMACVPN(req.Name, macvpn)
}

// DeleteMACVPN removes a MAC-VPN definition.
func (net *Network) DeleteMACVPN(name string, opts ExecOpts) error {
	if _, err := net.internal.GetMACVPN(name); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteMACVPN(name)
}

// ============================================================================
// QoS Policies
// ============================================================================

// ListQoSPolicies returns all QoS policy names.
func (net *Network) ListQoSPolicies() []string {
	return net.internal.ListQoSPolicies()
}

// ShowQoSPolicy returns the QoS policy for a given name, converted to QoSPolicyDetail.
func (net *Network) ShowQoSPolicy(name string) (*QoSPolicyDetail, error) {
	p, err := net.internal.GetQoSPolicy(name)
	if err != nil {
		return nil, err
	}
	return convertQoSPolicyDetail(name, p), nil
}

// CreateQoSPolicy creates a new QoS policy.
func (net *Network) CreateQoSPolicy(req CreateQoSPolicyRequest, opts ExecOpts) error {
	if _, err := net.internal.GetQoSPolicy(req.Name); err == nil {
		return fmt.Errorf("QoS policy '%s' already exists", req.Name)
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermQoSCreate, auth.NewContext().WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	policy := &spec.QoSPolicy{
		Description: req.Description,
		Queues:      []*spec.QoSQueue{},
	}
	return net.internal.SaveQoSPolicy(req.Name, policy)
}

// DeleteQoSPolicy removes a QoS policy.
func (net *Network) DeleteQoSPolicy(name string, opts ExecOpts) error {
	if _, err := net.internal.GetQoSPolicy(name); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermQoSDelete, auth.NewContext().WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteQoSPolicy(name)
}

// AddQoSQueue adds a queue to a QoS policy.
func (net *Network) AddQoSQueue(req AddQoSQueueRequest, opts ExecOpts) error {
	if req.QueueID < 0 || req.QueueID > 7 {
		return &ValidationError{Field: "queue_id", Message: "must be 0-7"}
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(req.Policy)); err != nil {
			return err
		}
	}
	policy, err := net.internal.GetQoSPolicy(req.Policy)
	if err != nil {
		return err
	}
	for len(policy.Queues) <= req.QueueID {
		policy.Queues = append(policy.Queues, nil)
	}
	if policy.Queues[req.QueueID] != nil {
		return fmt.Errorf("queue %d already exists in policy '%s'", req.QueueID, req.Policy)
	}
	if !opts.Execute {
		return nil
	}
	queue := &spec.QoSQueue{
		Name:   req.Name,
		Type:   req.Type,
		Weight: req.Weight,
		DSCP:   req.DSCP,
		ECN:    req.ECN,
	}
	policy.Queues[req.QueueID] = queue
	return net.internal.SaveQoSPolicy(req.Policy, policy)
}

// RemoveQoSQueue removes a queue from a QoS policy.
func (net *Network) RemoveQoSQueue(policy string, queueID int, opts ExecOpts) error {
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(policy)); err != nil {
			return err
		}
	}
	p, err := net.internal.GetQoSPolicy(policy)
	if err != nil {
		return err
	}
	if queueID < 0 || queueID >= len(p.Queues) || p.Queues[queueID] == nil {
		return fmt.Errorf("queue %d not found in policy '%s'", queueID, policy)
	}
	if !opts.Execute {
		return nil
	}
	p.Queues[queueID] = nil
	for len(p.Queues) > 0 && p.Queues[len(p.Queues)-1] == nil {
		p.Queues = p.Queues[:len(p.Queues)-1]
	}
	return net.internal.SaveQoSPolicy(policy, p)
}

// ============================================================================
// Filters
// ============================================================================

// ListFilters returns all filter template names.
func (net *Network) ListFilters() []string {
	return net.internal.ListFilters()
}

// ShowFilter returns the filter spec for a given name, converted to FilterDetail.
func (net *Network) ShowFilter(name string) (*FilterDetail, error) {
	f, err := net.internal.GetFilter(name)
	if err != nil {
		return nil, err
	}
	return convertFilterDetail(name, f), nil
}

// CreateFilter creates a new filter template.
func (net *Network) CreateFilter(req CreateFilterRequest, opts ExecOpts) error {
	if req.Type == "" {
		return &ValidationError{Field: "type", Message: "required (ipv4, ipv6)"}
	}
	if _, err := net.internal.GetFilter(req.Name); err == nil {
		return fmt.Errorf("filter '%s' already exists", req.Name)
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermFilterCreate, auth.NewContext().WithResource(req.Name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	fs := &spec.FilterSpec{
		Description: req.Description,
		Type:        req.Type,
		Rules:       []*spec.FilterRule{},
	}
	return net.internal.SaveFilter(req.Name, fs)
}

// DeleteFilter removes a filter template.
func (net *Network) DeleteFilter(name string, opts ExecOpts) error {
	if _, err := net.internal.GetFilter(name); err != nil {
		return err
	}
	if opts.Execute {
		if err := net.checkPermission(auth.PermFilterDelete, auth.NewContext().WithResource(name)); err != nil {
			return err
		}
	}
	if !opts.Execute {
		return nil
	}
	return net.internal.DeleteFilter(name)
}

// AddFilterRule adds a rule to a filter template.
func (net *Network) AddFilterRule(req AddFilterRuleRequest, opts ExecOpts) error {
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(req.Filter)); err != nil {
			return err
		}
	}
	fs, err := net.internal.GetFilter(req.Filter)
	if err != nil {
		return err
	}
	for _, r := range fs.Rules {
		if r.Sequence == req.Sequence {
			return fmt.Errorf("rule with priority %d already exists in filter '%s'", req.Sequence, req.Filter)
		}
	}
	if !opts.Execute {
		return nil
	}
	rule := &spec.FilterRule{
		Sequence:      req.Sequence,
		Action:        req.Action,
		SrcIP:         req.SrcIP,
		DstIP:         req.DstIP,
		SrcPrefixList: req.SrcPrefixList,
		DstPrefixList: req.DstPrefixList,
		Protocol:      req.Protocol,
		SrcPort:       req.SrcPort,
		DstPort:       req.DstPort,
		DSCP:          req.DSCP,
		CoS:           req.CoS,
		Log:           req.Log,
	}
	fs.Rules = append(fs.Rules, rule)
	sort.Slice(fs.Rules, func(i, j int) bool {
		return fs.Rules[i].Sequence < fs.Rules[j].Sequence
	})
	return net.internal.SaveFilter(req.Filter, fs)
}

// RemoveFilterRule removes a rule from a filter template by sequence number.
func (net *Network) RemoveFilterRule(filter string, seq int, opts ExecOpts) error {
	if opts.Execute {
		if err := net.checkPermission(auth.PermSpecAuthor, auth.NewContext().WithResource(filter)); err != nil {
			return err
		}
	}
	fs, err := net.internal.GetFilter(filter)
	if err != nil {
		return err
	}
	found := false
	newRules := make([]*spec.FilterRule, 0, len(fs.Rules))
	for _, r := range fs.Rules {
		if r.Sequence == seq {
			found = true
			continue
		}
		newRules = append(newRules, r)
	}
	if !found {
		return fmt.Errorf("rule with priority %d not found in filter '%s'", seq, filter)
	}
	if !opts.Execute {
		return nil
	}
	fs.Rules = newRules
	return net.internal.SaveFilter(filter, fs)
}

// ============================================================================
// Route Policies + Prefix Lists
// ============================================================================

// ListRoutePolicies returns all route policy names.
func (net *Network) ListRoutePolicies() []string {
	m := net.internal.Spec().RoutePolicies
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}

// ListPrefixLists returns all prefix list names.
func (net *Network) ListPrefixLists() []string {
	m := net.internal.Spec().PrefixLists
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	return names
}

// ============================================================================
// Conversion helpers
// ============================================================================

func convertServiceDetail(name string, s *spec.ServiceSpec) *ServiceDetail {
	return &ServiceDetail{
		Name:          name,
		Description:   s.Description,
		ServiceType:   s.ServiceType,
		IPVPN:         s.IPVPN,
		MACVPN:        s.MACVPN,
		VRFType:       s.VRFType,
		QoSPolicy:     s.QoSPolicy,
		IngressFilter: s.IngressFilter,
		EgressFilter:  s.EgressFilter,
	}
}

func convertIPVPNDetail(name string, s *spec.IPVPNSpec) *IPVPNDetail {
	return &IPVPNDetail{
		Name:         name,
		Description:  s.Description,
		VRF:          s.VRF,
		L3VNI:        s.L3VNI,
		RouteTargets: s.RouteTargets,
	}
}

func convertMACVPNDetail(name string, s *spec.MACVPNSpec) *MACVPNDetail {
	return &MACVPNDetail{
		Name:           name,
		Description:    s.Description,
		VNI:            s.VNI,
		VlanID:         s.VlanID,
		AnycastIP:      s.AnycastIP,
		AnycastMAC:     s.AnycastMAC,
		RouteTargets:   s.RouteTargets,
		ARPSuppression: s.ARPSuppression,
	}
}

func convertQoSPolicyDetail(name string, p *spec.QoSPolicy) *QoSPolicyDetail {
	detail := &QoSPolicyDetail{Name: name, Description: p.Description}
	for i, q := range p.Queues {
		if q == nil {
			continue
		}
		detail.Queues = append(detail.Queues, QoSQueueEntry{
			QueueID: i,
			Name:    q.Name,
			Type:    q.Type,
			Weight:  q.Weight,
			DSCP:    q.DSCP,
			ECN:     q.ECN,
		})
	}
	return detail
}

func convertFilterDetail(name string, f *spec.FilterSpec) *FilterDetail {
	detail := &FilterDetail{Name: name, Description: f.Description, Type: f.Type}
	for _, r := range f.Rules {
		detail.Rules = append(detail.Rules, FilterRuleEntry{
			Sequence:      r.Sequence,
			Action:        r.Action,
			SrcIP:         r.SrcIP,
			DstIP:         r.DstIP,
			SrcPrefixList: r.SrcPrefixList,
			DstPrefixList: r.DstPrefixList,
			Protocol:      r.Protocol,
			SrcPort:       r.SrcPort,
			DstPort:       r.DstPort,
			DSCP:          r.DSCP,
			CoS:           r.CoS,
			Log:           r.Log,
		})
	}
	return detail
}
