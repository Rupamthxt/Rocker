package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"syscall"
)

// usage: sudo ./go-cont run <cmd> <args>
func main() {
	if len(os.Args) < 2 {
		panic("not enough arguments")
	}

	switch os.Args[1] {
	case "run":
		run()
	case "child":
		child()
	default:
		panic("help")
	}
}

// run is the Parent process.
// It sets up the namespaces and invokes the "child" command.
func run() {
	fmt.Printf("Running %v as %d\n", os.Args[2:], os.Getpid())

	// We re-execute this binary with the "child" argument.
	// This "child" process will be the one running inside the namespaces.
	args := append([]string{"child"}, os.Args[2:]...)
	cmd := exec.Command("/proc/self/exe", args...)

	// Connect standard I/O so we can interact with the container
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	// THE MAGIC: Here we define the Namespaces we want to create.
	// CLONE_NEWUTS: New Hostname Namespace
	// CLONE_NEWPID: New PID Namespace
	// CLONE_NEWNS:  New Mount Isolation
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
	}

	if err := cmd.Start(); err != nil {
		fmt.Printf("Error starting the run command - %s\n", err)
		os.Exit(1)
	}

	cg(cmd.Process.Pid)

	if err := cmd.Wait(); err != nil {
		fmt.Printf("Error waiting for command - %s\n", err)
		os.Exit(1)
	}
}

func cg(pid int) {
	// 1. Determine the correct Cgroup Path
	// Check if the "pids" controller exists (Cgroup V1)
	cgroups := "/sys/fs/cgroup"
	pidsPath := filepath.Join(cgroups, "pids")

	containerCgroup := ""

	if _, err := os.Stat(pidsPath); err == nil {
		// V1: We must create our cgroup INSIDE the "pids" folder
		fmt.Println("DEBUG: Detected Cgroup V1")
		containerCgroup = filepath.Join(pidsPath, "go-cont")
	} else {
		// V2: We create it directly in the root
		fmt.Println("DEBUG: Detected Cgroup V2")
		containerCgroup = filepath.Join(cgroups, "go-cont")
	}

	// 2. Create the Directory
	// If it fails, we panic (unless it just exists)
	if err := os.Mkdir(containerCgroup, 0755); err != nil && !os.IsExist(err) {
		panic(fmt.Errorf("failed to create cgroup: %v", err))
	}

	// 3. Set the Limit (Max 20 Processes)
	// pids.max is the standard file for both V1 and V2
	must(os.WriteFile(filepath.Join(containerCgroup, "pids.max"), []byte("20"), 0700))

	// 4. Move Process to Cgroup
	// cgroup.procs works for both V1 and V2 to add a process
	must(os.WriteFile(filepath.Join(containerCgroup, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0700))
}

// child is the process running INSIDE the namespaces.
func child() {
	fmt.Printf("Running %v as %d\n", os.Args[2:], os.Getpid())

	// Set the hostname inside the container
	syscall.Sethostname([]byte("container"))

	// CHROOT
	// We hardcode the path for simplicity
	// In a real implementation, this is dynamic
	newRoot := "/tmp/container-root"

	must(syscall.Chroot(newRoot))
	must(syscall.Chdir("/"))

	// Mount the proc filesystem
	// source="proc", target="proc", fstype="proc"
	must(syscall.Mount("proc", "proc", "proc", 0, ""))

	// Execute the user's command (e.g., /bin/sh)
	// We use logic to ensure the command executes properly.
	cmd := exec.Command(os.Args[2], os.Args[3:]...)
	cmd.Stdin = os.Stdin
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr

	if err := cmd.Run(); err != nil {
		fmt.Printf("Error running the child command - %s\n", err)
		os.Exit(1)
	}
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
