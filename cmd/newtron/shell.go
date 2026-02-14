package main

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/newtron-network/newtron/pkg/network"
)

// Shell provides an interactive REPL with persistent device connection.
type Shell struct {
	dev        *network.Device
	deviceName string
	currentIntf *network.Interface // nil = device scope
	intfName    string             // "" = device scope
	reader     *bufio.Reader
	dirty      bool // true if changes applied since last save
	commands   map[string]func(args []string)

	// Composite build mode
	composite    *network.CompositeBuilder // non-nil when in composite mode
	compositeFor string                    // "overwrite" or "merge"
}

// NewShell creates a new interactive shell for the given device.
func NewShell(dev *network.Device, deviceName string) *Shell {
	s := &Shell{
		dev:        dev,
		deviceName: deviceName,
		reader:     bufio.NewReader(os.Stdin),
	}
	s.commands = map[string]func(args []string){
		"show":           func([]string) { s.cmdShow() },
		"list":           s.cmdList,
		"interface":      s.cmdInterface,
		"exit":           func([]string) { s.cmdExit() },
		"apply-service":  s.cmdApplyService,
		"remove-service": func([]string) { s.cmdRemoveService() },
		"composite":      s.cmdComposite,
		"save":           func([]string) { s.cmdSave() },
		"help":           func([]string) { s.cmdHelp() },
		"?":              func([]string) { s.cmdHelp() },
	}
	return s
}

// Run starts the interactive shell loop.
func (s *Shell) Run() error {
	fmt.Printf("Connected to %s.\n", bold(s.deviceName))
	fmt.Println("Type 'help' for available commands.")

	for {
		prompt := s.prompt()
		fmt.Print(prompt)

		line, err := s.reader.ReadString('\n')
		if err != nil { // EOF
			return s.handleQuit()
		}
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}

		args := strings.Fields(line)
		cmd := args[0]

		switch cmd {
		case "quit", "disconnect", "q":
			return s.handleQuit()
		default:
			if fn, ok := s.commands[cmd]; ok {
				fn(args[1:])
			} else {
				fmt.Printf("Unknown command: %s (type 'help' for commands)\n", cmd)
			}
		}
	}
}

// prompt returns the current prompt string.
func (s *Shell) prompt() string {
	prefix := ""
	if s.composite != nil {
		prefix = "[composite] "
	}
	if s.intfName != "" {
		return fmt.Sprintf("%s%s:%s> ", prefix, s.deviceName, s.intfName)
	}
	return fmt.Sprintf("%s%s> ", prefix, s.deviceName)
}

// cmdShow displays details for the current context.
func (s *Shell) cmdShow() {
	if s.currentIntf != nil {
		s.showInterface()
	} else {
		s.showDevice()
	}
}

func (s *Shell) showDevice() {
	if err := showDevice(s.dev); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}

func (s *Shell) showInterface() {
	intf := s.currentIntf
	fmt.Printf("Interface: %s\n", bold(intf.Name()))
	fmt.Printf("Admin Status: %s\n", intf.AdminStatus())
	fmt.Printf("Oper Status: %s\n", intf.OperStatus())
	fmt.Printf("Speed: %s\n", intf.Speed())
	fmt.Printf("MTU: %d\n", intf.MTU())

	if addrs := intf.IPAddresses(); len(addrs) > 0 {
		fmt.Printf("IP Addresses: %s\n", strings.Join(addrs, ", "))
	}
	if vrf := intf.VRF(); vrf != "" {
		fmt.Printf("VRF: %s\n", vrf)
	}
	if svc := intf.ServiceName(); svc != "" {
		fmt.Printf("Service: %s\n", svc)
	}
}

// cmdList lists child objects.
func (s *Shell) cmdList(args []string) {
	if s.currentIntf != nil {
		fmt.Println("list is only available at device scope (use 'exit' first)")
		return
	}

	if len(args) == 0 {
		fmt.Println("Usage: list <interfaces|vlans|lags|vrfs>")
		return
	}

	switch args[0] {
	case "interfaces":
		interfaces := s.dev.ListInterfaces()
		for _, name := range interfaces {
			fmt.Printf("  %s\n", name)
		}
		if len(interfaces) == 0 {
			fmt.Println("  (none)")
		}
	case "vlans":
		vlans := s.dev.ListVLANs()
		for _, id := range vlans {
			fmt.Printf("  Vlan%d\n", id)
		}
		if len(vlans) == 0 {
			fmt.Println("  (none)")
		}
	case "lags", "portchannels":
		pcs := s.dev.ListPortChannels()
		for _, name := range pcs {
			fmt.Printf("  %s\n", name)
		}
		if len(pcs) == 0 {
			fmt.Println("  (none)")
		}
	case "vrfs":
		vrfs := s.dev.ListVRFs()
		for _, name := range vrfs {
			fmt.Printf("  %s\n", name)
		}
		if len(vrfs) == 0 {
			fmt.Println("  (none)")
		}
	default:
		fmt.Printf("Unknown type: %s (try: interfaces, vlans, lags, vrfs)\n", args[0])
	}
}

// cmdInterface enters interface context.
func (s *Shell) cmdInterface(args []string) {
	if s.currentIntf != nil {
		fmt.Println("Already in interface context. Use 'exit' first.")
		return
	}
	if len(args) == 0 {
		fmt.Println("Usage: interface <name>")
		return
	}

	name := args[0]
	intf, err := s.dev.GetInterface(name)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	s.currentIntf = intf
	s.intfName = intf.Name()
	fmt.Printf("Entered interface context: %s\n", s.intfName)
}

// cmdExit returns to device context from interface context.
func (s *Shell) cmdExit() {
	if s.currentIntf == nil {
		fmt.Println("Already at device scope. Use 'quit' to disconnect.")
		return
	}
	s.currentIntf = nil
	s.intfName = ""
}

// cmdApplyService applies a service to the current interface.
func (s *Shell) cmdApplyService(args []string) {
	if s.currentIntf == nil {
		fmt.Println("apply-service requires interface context (use 'interface <name>' first)")
		return
	}
	if len(args) == 0 {
		fmt.Println("Usage: apply-service <service> [--ip <address>]")
		return
	}

	serviceName := args[0]
	var ipAddress string

	// Parse --ip flag from args
	for i := 1; i < len(args); i++ {
		if args[i] == "--ip" && i+1 < len(args) {
			ipAddress = args[i+1]
			i++
		}
	}

	ctx := context.Background()

	if err := s.dev.Lock(); err != nil {
		fmt.Printf("Error locking device: %v\n", err)
		return
	}
	defer s.dev.Unlock()

	changeSet, err := s.currentIntf.ApplyService(ctx, serviceName, network.ApplyServiceOpts{
		IPAddress: ipAddress,
	})
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Print(changeSet.Preview())

	if !s.confirmExecute() {
		fmt.Println("Cancelled.")
		return
	}

	if err := changeSet.Apply(s.dev); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Println(green("Applied successfully."))
	s.dirty = true
}

// cmdRemoveService removes the service from the current interface.
func (s *Shell) cmdRemoveService() {
	if s.currentIntf == nil {
		fmt.Println("remove-service requires interface context (use 'interface <name>' first)")
		return
	}

	ctx := context.Background()

	if err := s.dev.Lock(); err != nil {
		fmt.Printf("Error locking device: %v\n", err)
		return
	}
	defer s.dev.Unlock()

	changeSet, err := s.currentIntf.RemoveService(ctx)
	if err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}

	fmt.Print(changeSet.Preview())

	if !s.confirmExecute() {
		fmt.Println("Cancelled.")
		return
	}

	if err := changeSet.Apply(s.dev); err != nil {
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Println(green("Applied successfully."))
	s.dirty = true
}

// cmdSave saves the config to disk.
func (s *Shell) cmdSave() {
	ctx := context.Background()
	fmt.Print("Saving configuration... ")
	if err := s.dev.SaveConfig(ctx); err != nil {
		fmt.Println(red("FAILED"))
		fmt.Printf("Error: %v\n", err)
		return
	}
	fmt.Println(green("saved."))
	s.dirty = false
}

// handleQuit handles the quit/disconnect command.
func (s *Shell) handleQuit() error {
	if s.dirty {
		fmt.Print("Unsaved changes. Save before disconnecting? [Y/n]: ")
		confirm, _ := s.reader.ReadString('\n')
		confirm = strings.TrimSpace(strings.ToLower(confirm))
		if confirm != "n" && confirm != "no" {
			s.cmdSave()
		}
	}
	fmt.Println("Disconnecting...")
	return nil
}

// confirmExecute prompts the user to confirm execution.
func (s *Shell) confirmExecute() bool {
	fmt.Print("Execute? [y/N]: ")
	confirm, _ := s.reader.ReadString('\n')
	confirm = strings.TrimSpace(strings.ToLower(confirm))
	return confirm == "y" || confirm == "yes"
}

// cmdComposite handles the composite build mode commands.
func (s *Shell) cmdComposite(args []string) {
	if len(args) == 0 {
		fmt.Println("Usage: composite <begin|show|commit|discard>")
		return
	}

	switch args[0] {
	case "begin":
		if s.composite != nil {
			fmt.Println("Already in composite mode. Use 'composite commit' or 'composite discard' first.")
			return
		}
		mode := network.CompositeMerge
		if len(args) > 1 && args[1] == "overwrite" {
			mode = network.CompositeOverwrite
		}
		s.composite = network.NewCompositeBuilder(s.deviceName, mode)
		s.compositeFor = string(mode)
		fmt.Printf("Composite mode (%s). Changes will be batched.\n", mode)
		fmt.Println("Use 'composite commit' to apply, 'composite discard' to cancel.")

	case "show":
		if s.composite == nil {
			fmt.Println("Not in composite mode. Use 'composite begin' first.")
			return
		}
		config := s.composite.Build()
		fmt.Printf("Composite entries: %d\n", config.EntryCount())
		for table, keys := range config.Tables {
			fmt.Printf("  %s: %d keys\n", table, len(keys))
		}

	case "commit":
		if s.composite == nil {
			fmt.Println("Not in composite mode. Use 'composite begin' first.")
			return
		}
		config := s.composite.Build()
		if config.EntryCount() == 0 {
			fmt.Println("Composite is empty, nothing to commit.")
			return
		}
		fmt.Printf("Delivering %d entries (%s mode)...\n", config.EntryCount(), s.compositeFor)
		if !s.confirmExecute() {
			fmt.Println("Cancelled.")
			return
		}
		if err := s.dev.Lock(); err != nil {
			fmt.Printf("Error locking device: %v\n", err)
			return
		}
		result, err := s.dev.DeliverComposite(config, network.CompositeMode(s.compositeFor))
		s.dev.Unlock()
		if err != nil {
			fmt.Printf("Error: %v\n", err)
			return
		}
		fmt.Printf("Applied %d entries.\n", result.Applied)
		s.composite = nil
		s.compositeFor = ""
		s.dirty = true

	case "discard":
		if s.composite == nil {
			fmt.Println("Not in composite mode.")
			return
		}
		s.composite = nil
		s.compositeFor = ""
		fmt.Println("Composite discarded.")

	default:
		fmt.Println("Usage: composite <begin|show|commit|discard>")
	}
}

// cmdHelp displays available commands.
func (s *Shell) cmdHelp() {
	if s.currentIntf != nil {
		fmt.Println("Interface commands:")
		fmt.Println("  show               Show interface details")
		fmt.Println("  apply-service <svc> [--ip <ip>]  Apply service to interface")
		fmt.Println("  remove-service     Remove service from interface")
		fmt.Println("  save               Save config to disk")
		fmt.Println("  exit               Return to device context")
		fmt.Println("  quit               Disconnect from device")
		fmt.Println("  help               Show this help")
	} else {
		fmt.Println("Device commands:")
		fmt.Println("  show               Show device details")
		fmt.Println("  list <type>        List objects (interfaces, vlans, lags, vrfs)")
		fmt.Println("  interface <name>   Enter interface context")
		fmt.Println("  composite <cmd>    Composite build mode (begin, show, commit, discard)")
		fmt.Println("  save               Save config to disk")
		fmt.Println("  quit               Disconnect from device")
		fmt.Println("  help               Show this help")
	}
}

// shellCmd is the cobra command for the interactive shell.
var shellCmd = &cobra.Command{
	Use:    "shell",
	Short:  "Interactive shell with persistent device connection",
	Hidden: true,
	Long: `Start an interactive shell with a persistent connection to a SONiC device.

The shell provides a REPL with:
  - Persistent device connection (connected on entry, disconnected on quit)
  - Interface context switching (interface <name> / exit)
  - Inline execution with confirmation prompts
  - Dirty tracking and save-on-disconnect prompts
  - Explicit save command (runs config save via SSH)

Examples:
  newtron -d leaf1-ny shell
  newtron -d leaf1-ny -S /path/to/specs shell`,
	Aliases: []string{"sh"},
	RunE: func(cmd *cobra.Command, args []string) error {
		if app.deviceName == "" {
			return fmt.Errorf("device required: use -d <device> flag")
		}
		ctx := context.Background()
		dev, err := app.net.ConnectDevice(ctx, app.deviceName)
		if err != nil {
			return err
		}
		defer dev.Disconnect()

		sh := NewShell(dev, app.deviceName)
		return sh.Run()
	},
}
