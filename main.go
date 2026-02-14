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

	// --- NEW: Create a Pipe for Synchronization ---
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
		Cloneflags:   syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS,
		Unshareflags: syscall.CLONE_NEWNS,
	}

	if err := cmd.Start(); err != nil {
		panic(err)
	}

	fmt.Printf("DEBUG: Child Host PID is: %d\n", cmd.Process.Pid)

	// 1. SETUP CGROUPS (Child is currently paused waiting for us)
	cg(cmd.Process.Pid)

	// 2. SIGNAL CHILD TO RESUME
	// We write to the pipe. This unblocks the child.
	fmt.Println("Parent: Cgroup set. Unpausing child...")
	w.Write([]byte("OK"))
	w.Close()

	// 3. CLEANUP ON EXIT
	defer func() {
		fmt.Println("\nParent: Cleaning up cgroups...")
		removeCgroup()
	}()

	if err := cmd.Wait(); err != nil {
		fmt.Printf("Parent: Container exited with error: %v\n", err)
	}
}

func child() {
	// --- NEW: Wait for Parent Signal ---
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
	// -----------------------------------

	must(syscall.Sethostname([]byte("container")))

	// SETUP ROOT FS
	newRoot := "/tmp/my-container-root"
	must(syscall.Chroot(newRoot))
	must(syscall.Chdir("/"))

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

// Keep your existing cg(), removeCgroup(), and must() functions exactly as they were!
// Copy them from the previous step if you need to.
// (I am omitting them here to save space, but DO NOT DELETE THEM from your file)
func cg(pid int) {
	cgroups := "/sys/fs/cgroup"
	myGroup := filepath.Join(cgroups, "rocker")

	if err := os.Mkdir(myGroup, 0755); err != nil && !os.IsExist(err) {
		panic(err)
	}

	// LIMIT: 10 Processes
	if err := os.WriteFile(filepath.Join(myGroup, "pids.max"), []byte("10"), 0700); err != nil {
		fmt.Printf("Warning: Could not set pids.max: %v\n", err)
	}

	// Add process
	if err := os.WriteFile(filepath.Join(myGroup, "cgroup.procs"), []byte(strconv.Itoa(pid)), 0700); err != nil {
		panic(fmt.Sprintf("Failed to add process to cgroup: %v", err))
	}
}

func removeCgroup() {
	os.Remove("/sys/fs/cgroup/rocker")
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
