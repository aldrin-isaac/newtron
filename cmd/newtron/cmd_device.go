package main

import (
	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/newtron"
)

var deviceCmd = &cobra.Command{
	Use:   "device",
	Short: "Device-level operations",
	Long: `Device-level operations (setup, metadata).

The 'setup' command creates the device root intent and configures baseline
infrastructure (metadata, loopback, BGP). This is required before any
service operations — the intent DAG requires a 'device' root.

Examples:
  newtron leaf1 device setup -x
  newtron leaf1 device setup --hostname leaf1 --type LeafRouter -x
  newtron leaf1 device setup --vtep-source 10.0.0.1 -x`,
}

// setup flags
var (
	setupHostname string
	setupBGPASN   string
	setupType     string
	setupHWSKU    string
	setupVTEP     string
)

var deviceSetupCmd = &cobra.Command{
	Use:   "setup",
	Short: "Initialize device baseline (metadata, loopback, BGP)",
	Long: `Set up a device for newtron management by configuring baseline infrastructure.

This writes the 'device' root intent and configures:
  - DEVICE_METADATA (hostname, BGP ASN, type, HWSKU, routing config mode)
  - Loopback interface (from profile loopback_ip)
  - BGP globals (from profile underlay_asn)
  - VTEP (optional, if --vtep-source is provided or profile has VTEP config)

This is the required first operation after 'init' — the intent DAG requires
a 'device' root before any service operations (apply, vrf, evpn, etc.).

Values not provided via flags are derived from the device profile. The
profile already has hostname (device name), BGP ASN (underlay_asn), and
loopback IP — so running without flags uses sensible defaults.

Examples:
  newtron leaf1 device setup -x                    # all values from profile
  newtron leaf1 device setup --type LeafRouter -x  # override device type
  newtron leaf1 device setup --vtep-source 10.0.0.1 -x  # enable VTEP`,
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := requireDevice(); err != nil {
			return err
		}

		// Build SetupDeviceOpts from flags
		fields := make(map[string]string)
		if setupHostname != "" {
			fields["hostname"] = setupHostname
		} else {
			// Default hostname to device name
			fields["hostname"] = app.deviceName
		}
		if setupBGPASN != "" {
			fields["bgp_asn"] = setupBGPASN
		}
		if setupType != "" {
			fields["type"] = setupType
		}
		if setupHWSKU != "" {
			fields["hwsku"] = setupHWSKU
		}
		// Always set unified routing mode (required for frrcfgd)
		fields["docker_routing_config_mode"] = "unified"
		fields["frr_mgmt_framework_config"] = "true"

		sdOpts := newtron.SetupDeviceOpts{
			Fields:   fields,
			SourceIP: setupVTEP,
		}

		result, err := app.client.SetupDevice(app.deviceName, sdOpts, execOpts())
		if err != nil {
			return err
		}

		return displayWriteResult(result, nil)
	},
}

func init() {
	deviceSetupCmd.Flags().StringVar(&setupHostname, "hostname", "", "Device hostname (default: device name)")
	deviceSetupCmd.Flags().StringVar(&setupBGPASN, "bgp-asn", "", "BGP autonomous system number")
	deviceSetupCmd.Flags().StringVar(&setupType, "type", "", "Device type (e.g., LeafRouter, SpineRouter)")
	deviceSetupCmd.Flags().StringVar(&setupHWSKU, "hwsku", "", "Hardware SKU (e.g., Force10-S6000)")
	deviceSetupCmd.Flags().StringVar(&setupVTEP, "vtep-source", "", "VTEP source IP for VXLAN overlay")

	deviceCmd.AddCommand(deviceSetupCmd)
}
