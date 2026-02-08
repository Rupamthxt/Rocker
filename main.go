package main

import (
	"fmt"
	"os"
	"os/exec"
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
	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags: syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID,
	}

	if err := cmd.Run(); err != nil {
		fmt.Printf("Error running the run command - %s\n", err)
		os.Exit(1)
	}
}

// child is the process running INSIDE the namespaces.
func child() {
	fmt.Printf("Running %v as %d\n", os.Args[2:], os.Getpid())

	// 1. Set the hostname inside the container
	syscall.Sethostname([]byte("container"))

	// 2. Execute the user's command (e.g., /bin/sh)
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
