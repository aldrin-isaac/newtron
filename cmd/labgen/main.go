// Command labgen generates containerlab topology files and SONiC startup
// configurations from a newtron topology YAML definition.
//
// Usage:
//
//	labgen -topology <file> -output <dir>
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/newtron-network/newtron/pkg/labgen"
)

func main() {
	topoFile := flag.String("topology", "", "Path to topology YAML file (required)")
	outputDir := flag.String("output", "", "Output directory for generated artifacts (required)")
	flag.Parse()

	if *topoFile == "" || *outputDir == "" {
		fmt.Fprintf(os.Stderr, "Usage: labgen -topology <file> -output <dir>\n")
		flag.PrintDefaults()
		os.Exit(1)
	}

	topo, err := labgen.LoadTopology(*topoFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Error: %v\n", err)
		os.Exit(1)
	}

	if err := os.MkdirAll(*outputDir, 0755); err != nil {
		fmt.Fprintf(os.Stderr, "Error creating output directory: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("Generating lab artifacts for topology %q\n", topo.Name)
	fmt.Printf("  Topology: %s\n", *topoFile)
	fmt.Printf("  Output:   %s\n", *outputDir)
	fmt.Println()

	// Generate minimal startup configs (DEVICE_METADATA + PORT only)
	fmt.Println("Generating minimal startup configs...")
	if err := labgen.GenerateMinimalStartupConfigs(topo, *outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating startup configs: %v\n", err)
		os.Exit(1)
	}
	for name, node := range topo.Nodes {
		if node.Role != "server" {
			fmt.Printf("  %s/config_db.json\n", name)
		}
	}

	// Generate containerlab topology
	fmt.Println("Generating containerlab topology...")
	if err := labgen.GenerateClabTopology(topo, *outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating containerlab topology: %v\n", err)
		os.Exit(1)
	}
	fmt.Printf("  %s.clab.yml\n", topo.Name)

	// Generate newtron specs (network, site, platforms, profiles)
	fmt.Println("Generating newtron specs...")
	if err := labgen.GenerateLabSpecs(topo, *outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating specs: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  specs/network.json")
	fmt.Println("  specs/site.json")
	fmt.Println("  specs/platforms.json")
	for name, node := range topo.Nodes {
		if node.Role != "server" {
			fmt.Printf("  specs/profiles/%s.json\n", name)
		}
	}

	// Generate topology.json for newtron provisioning
	fmt.Println("Generating topology spec...")
	if err := labgen.GenerateTopologySpec(topo, *outputDir); err != nil {
		fmt.Fprintf(os.Stderr, "Error generating topology spec: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("  specs/topology.json")

	fmt.Printf("\nDone. Deploy with: cd %s && sudo containerlab deploy -t %s.clab.yml\n", *outputDir, topo.Name)
}
