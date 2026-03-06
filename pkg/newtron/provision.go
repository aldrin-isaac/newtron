package newtron

import (
	"context"
	"fmt"

	"github.com/newtron-network/newtron/pkg/newtron/network"
)

// GenerateDeviceComposite generates the composite CONFIG_DB for a device without applying it.
// Useful for dry-run inspection and serialization. Requires a topology to be loaded.
func (net *Network) GenerateDeviceComposite(device string) (*CompositeInfo, error) {
	if !net.HasTopology() {
		return nil, &ValidationError{Message: "no topology loaded"}
	}
	tp, err := network.NewTopologyProvisioner(net.internal)
	if err != nil {
		return nil, err
	}
	cc, err := tp.GenerateDeviceComposite(device)
	if err != nil {
		return nil, err
	}
	return wrapComposite(cc), nil
}

// ProvisionDevices provisions one or more devices from the topology.
// If req.Devices is empty, all topology devices are provisioned.
// In dry-run mode (opts.Execute == false), composites are generated but not delivered.
func (net *Network) ProvisionDevices(ctx context.Context, req ProvisionRequest, opts ExecOpts) (*ProvisionResult, error) {
	if !net.HasTopology() {
		return nil, &ValidationError{Message: "no topology loaded — provision requires a topology"}
	}
	tp, err := network.NewTopologyProvisioner(net.internal)
	if err != nil {
		return nil, err
	}
	deviceNames := req.Devices
	if len(deviceNames) == 0 {
		deviceNames = net.internal.GetTopology().DeviceNames()
	}
	result := &ProvisionResult{}
	for _, name := range deviceNames {
		dr := ProvisionDeviceResult{Device: name}
		if !opts.Execute {
			// Dry-run: just generate the composite for summary
			ci, err := net.GenerateDeviceComposite(name)
			if err != nil {
				dr.Err = fmt.Errorf("generating composite: %w", err)
			} else {
				dr.Applied = ci.EntryCount
			}
			result.Results = append(result.Results, dr)
			continue
		}
		deliveryResult, err := tp.ProvisionDevice(ctx, name)
		if err != nil {
			dr.Err = fmt.Errorf("provisioning: %w", err)
			result.Results = append(result.Results, dr)
			continue
		}
		dr.Applied = deliveryResult.Applied
		result.Results = append(result.Results, dr)
	}
	return result, nil
}
