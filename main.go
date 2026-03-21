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

	// --- Create a Pipe for Synchronization ---
	// r = read end (passed to child), w = write end (kept by parent)
	r, w, err := os.Pipe()
	if err != nil {
		panic(err)
	}

	cmd := exec.Command("/proc/self/exe", append([]string{"child"}, os.Args[2:]...)...)

	// Pass the Read-End of the pipe to the child as an "ExtraFile"
	// It will show up as File Descriptor 3 (0=stdin, 1=stdout, 2=stderr, 3=pipe)
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

	// Setup Host Networking
	setupNetwork(cmd.Process.Pid)

	// SIGNAL CHILD TO RESUME
	// We write to the pipe. This unblocks the child.
	fmt.Println("Parent: Cgroup set. Unpausing child...")
	w.Write([]byte("OK"))
	w.Close()

	// 3. CLEANUP ON EXIT
	defer func() {
		fmt.Println("\nParent: Cleaning up cgroups and network...")
		exec.Command("ip", "link", "delete", "veth0").Run()
		removeCgroup()
	}()

	if err := cmd.Wait(); err != nil {
		fmt.Printf("Parent: Container exited with error: %v\n", err)
	}
}

func child() {
	// Wait for Parent Signal ---
	// File Descriptor 3 is the pipe we passed in cmd.ExtraFiles
	pipe := os.NewFile(3, "pipe")

	fmt.Println("Child: Waiting for Cgroup setup...")

	// This READ will block until the parent writes something.
	// This ensures we don't start the shell until we are jailed.
	_, err := io.ReadAll(pipe)
	if err != nil {
		panic(err)
	}
	pipe.Close()

	fmt.Println("Child: Resuming execution!")

	must(syscall.Sethostname([]byte("container")))

	// Bring up the loopback interface
	must(exec.Command("ip", "link", "set", "lo", "up").Run())
	// Assign an IP to child end of the veth cable
	must(exec.Command("ip", "addr", "add", "192.168.100.2/24", "dev", "veth1").Run())
	must(exec.Command("ip", "link", "set", "veth1", "up").Run())
	// Route all outside traffic through the host's IP
	must(exec.Command("ip", "route", "add", "default", "via", "192.168.100.1").Run())
	// SETUP ROOT FS
	newRoot := "rootfs"

	if _, err := os.Stat(newRoot); os.IsNotExist(err) {
		fmt.Printf("\n[FATAL] The folder %s is missing!\n", newRoot)
		fmt.Println("Please run: mkdir -p rootfs && tar -xzf alpine-minirootfs-3.18.4-x86_64.tar.gz -C rootfs/")
		os.Exit(1)
	}

	// Make all mounts in this new namespace PRIVATE.
	// This stops our mounts from leaking to the host and makes pivot_root happy.
	// source="", target="/", fstype="", flags=MS_PRIVATE|MS_REC
	must(syscall.Mount("", "/", "", syscall.MS_PRIVATE|syscall.MS_REC, ""))

	// Mount newRoot to itself so that it becomes a distinct mount point.
	must(syscall.Mount(newRoot, newRoot, "bind", syscall.MS_BIND|syscall.MS_REC, ""))

	// 1. Define the Host path (where data actually lives on your machine)
	hostVol := "/home/rupam/Projects/Rocker/vectradb_data"

	// 2. Define the Container path
	// CRITICAL: We must mount this into the `mergedDir` BEFORE we pivot_root!
	containerVol := filepath.Join(newRoot, "app", "data")

	// 3. Ensure both directories physically exist before mounting
	// We use 0777 here just to avoid any immediate permission fighting between
	// the host user and the container's root user.
	must(os.MkdirAll(hostVol, 0777))
	must(os.MkdirAll(containerVol, 0777))

	// 4. The Magic Bind Mount
	// This tells the kernel: "When the container writes to /app/data, actually write to hostVol"
	must(syscall.Mount(hostVol, containerVol, "bind", syscall.MS_BIND|syscall.MS_REC, ""))

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

func removeCgroup() {
	os.Remove("/sys/fs/cgroup/rocker")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
