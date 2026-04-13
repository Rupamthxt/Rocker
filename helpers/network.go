package helpers

import (
	"fmt"
	"os/exec"
	"strconv"
)

func SetupNetwork(pid int) {
	fmt.Println("Parent: Configuring veth pair.....")
	// Create veth pair (veth0 and veth1)
	must(exec.Command("ip", "link", "add", "veth0", "type", "veth", "peer", "name", "veth1").Run())
	// Assign IP to the host side (veth0) and bring it up
	must(exec.Command("ip", "addr", "add", "192.168.100.1/24", "dev", "veth0").Run())
	must(exec.Command("ip", "link", "set", "veth0", "up").Run())
	// Move veth1 into child's network namespace
	must(exec.Command("ip", "link", "set", "veth1", "netns", strconv.Itoa(pid)).Run())
}

func SetupPortForwarding() {
	fmt.Println("Parent: Setting up Port Forwarding (Host:8080 -> Container:8080)...")

	// Inbound traffic
	must(exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "tcp", "--dport", "8080", "-j", "DNAT", "--to-destination", "192.168.100.2:8080").Run())

	// Outbound traffic
	must(exec.Command("iptables", "-t", "nat", "-A", "OUTPUT", "-p", "tcp", "--dport", "8080", "-j", "DNAT", "--to-destination", "192.168.100.2:8080").Run())
}

func RemovePortForwarding() {
	// Cleanup
	exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-p", "tcp", "--dport", "8080", "-j", "DNAT", "--to-destination", "192.168.100.2:8080").Run()
	exec.Command("iptables", "-t", "nat", "-D", "OUTPUT", "-p", "tcp", "--dport", "8080", "-j", "DNAT", "--to-destination", "192.168.100.2:8080").Run()
}
