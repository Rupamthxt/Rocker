package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

func main() {
	if len(os.Args) < 2 {
		panic("usage: sudo ./rocker run <cmd> <args>")
	}

	switch os.Args[1] {
	case "run":
		run()
	case "child":
		child()
	default:
		panic("unknown command")
	}
}

func run() {
	fmt.Printf("Parent: Starting container setup %v \n", os.Args[2:])

	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}

	cmd := exec.Command("/proc/self/exe", append([]string{"child"}, os.Args[2:]...)...)

	cmd.ExtraFiles = []*os.File{r}

	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET,
		Unshareflags: syscall.CLONE_NEWNS,
	}

	if err := cmd.Start(); err != nil {
		panic(err)
	}

	fmt.Printf("DEBUG: Child Host PID is: %d\n", cmd.Process.Pid)

	// SETUP CGROUPS (Child is currently paused waiting for parents signal)
	cg(cmd.Process.Pid)
	setupNetwork(cmd.Process.Pid)
	setupPortForwarding()

	// SIGNAL CHILD TO RESUME
	// We write to the pipe. This unblocks the child.
	fmt.Println("Parent: Cgroup set. Unpausing child...")
	w.Write([]byte("OK"))
	w.Close()

	defer func() {
		fmt.Println("\nParent: Cleaning up cgroups and network...")
		exec.Command("ip", "link", "delete", "veth0").Run()
		removeCgroup()
		removePortForwarding()
	}()

	if err := cmd.Wait(); err != nil {
		fmt.Printf("Parent: Container exited with error: %v\n", err)
	}
}

func child() {

	pipe := os.NewFile(3, "pipe")

	fmt.Println("Child: Waiting for Cgroup setup...")

	_, err := io.ReadAll(pipe)
	if err != nil {
		panic(err)
	}
	pipe.Close()

	fmt.Println("Child: Resuming execution!")

	must(syscall.Sethostname([]byte("container")))

	must(exec.Command("ip", "link", "set", "lo", "up").Run())

	must(exec.Command("ip", "addr", "add", "192.168.100.2/24", "dev", "veth1").Run())
	must(exec.Command("ip", "link", "set", "veth1", "up").Run())

	must(exec.Command("ip", "route", "add", "default", "via", "192.168.100.1").Run())

	newRoot := "rootfs"

	if _, err := os.Stat(newRoot); os.IsNotExist(err) {
		fmt.Printf("\n[FATAL] The folder %s is missing!\n", newRoot)
		fmt.Println("Please run: mkdir -p rootfs && tar -xzf alpine-minirootfs-3.18.4-x86_64.tar.gz -C rootfs/")
		os.Exit(1)
	}

	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""))

	must(syscall.Mount(newRoot, newRoot, "bind", syscall.MS_BIND|syscall.MS_REC, ""))

	hostVol, err := exec.Command("pwd").Output()
	if err != nil {
		panic(err)
	}

	host := filepath.Join(string(hostVol[:len(hostVol)-1]), "data")

	containerVol := filepath.Join(newRoot, "app", "data")

	must(os.MkdirAll(host, 0777))
	must(os.MkdirAll(containerVol, 0777))

	must(syscall.Mount(host, containerVol, "bind", syscall.MS_BIND|syscall.MS_REC, ""))

	putOld := filepath.Join(newRoot, ".put_old")
	if err := os.MkdirAll(putOld, 0700); err != nil {
		panic(err)
	}

	// Swaps the mounts. newRoot becomes "/" and the host "/" moves to ".put_old"
	must(syscall.PivotRoot(newRoot, putOld))
	// Change working directory to new root
	must(syscall.Chdir("/"))

	putOldInsideContainer := "/.put_old"
	must(syscall.Unmount(putOldInsideContainer, syscall.MNT_DETACH))
	must(os.Remove(putOldInsideContainer))

	// MOUNT PROC
	if err := os.MkdirAll("proc", 0755); err != nil {
		panic(err)
	}
	must(syscall.Mount("proc", "proc", "proc", 0, ""))
	defer syscall.Unmount("proc", 0)

	// RUN USER COMMAND
	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		os.Exit(1)
	}
}

func cg(pid int) {
	cgroups := "/sys/fs/cgroup"
	myGroup := filepath.Join(cgroups, "rocker")

	if err := os.Mkdir(myGroup, 0755); err != nil && !os.IsExist(err) {
		panic(err)
	}

	// LIMIT: 10 Processes
	if err := os.WriteFile(filepath.Join(myGroup, "pids.max"), []byte("20"), 0700); err != nil {
		fmt.Printf("Warning: Could not set pids.max: %v\n", err)
	}

	if err := os.WriteFile(filepath.Join(myGroup, "memory.max"), []byte("100M"), 0700); err != nil {
		fmt.Printf("Warning: Could not set memory.max: %v\n", err)
	}

	// Add process
	if err := os.WriteFile(filepath.Join(myGroup, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0700); err != nil {
		panic(fmt.Sprintf("Failed to add process to cgroup: %v", err))
	}
}

func setupNetwork(pid int) {
	fmt.Println("Parent: Configuring veth pair.....")
	// Create veth pair (veth0 and veth1)
	must(exec.Command("ip", "link", "add", "veth0", "type", "veth", "peer", "name", "veth1").Run())
	// Assign IP to the host side (veth0) and bring it up
	must(exec.Command("ip", "addr", "add", "192.168.100.1/24", "dev", "veth0").Run())
	must(exec.Command("ip", "link", "set", "veth0", "up").Run())
	// Move veth1 into child's network namespace
	must(exec.Command("ip", "link", "set", "veth1", "netns", strconv.Itoa(pid)).Run())
}

func setupPortForwarding() {
	fmt.Println("Parent: Setting up Port Forwarding (Host:8080 -> Container:8080)...")

	// Inbound traffic
	must(exec.Command("iptables", "-t", "nat", "-A", "PREROUTING", "-p", "tcp", "--dport", "8080", "-j", "DNAT", "--to-destination", "192.168.100.2:8080").Run())

	// Outbound traffic
	must(exec.Command("iptables", "-t", "nat", "-A", "OUTPUT", "-p", "tcp", "--dport", "8080", "-j", "DNAT", "--to-destination", "192.168.100.2:8080").Run())
}

func removePortForwarding() {
	// Cleanup
	exec.Command("iptables", "-t", "nat", "-D", "PREROUTING", "-p", "tcp", "--dport", "8080", "-j", "DNAT", "--to-destination", "192.168.100.2:8080").Run()
	exec.Command("iptables", "-t", "nat", "-D", "OUTPUT", "-p", "tcp", "--dport", "8080", "-j", "DNAT", "--to-destination", "192.168.100.2:8080").Run()
}

func removeCgroup() {
	os.Remove("/sys/fs/cgroup/rocker")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
