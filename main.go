package main

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"rocker/helpers"
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

	helpers.ApplySeccomp()

	cmd.SysProcAttr = &syscall.SysProcAttr{
		Cloneflags:   syscall.CLONE_NEWUTS | syscall.CLONE_NEWPID | syscall.CLONE_NEWNS | syscall.CLONE_NEWNET,
		Unshareflags: syscall.CLONE_NEWNS,
	}

	if err := cmd.Start(); err != nil {
		panic(err)
	}

	fmt.Printf("DEBUG: Child Host PID is: %d\n", cmd.Process.Pid)

	// SETUP CGROUPS (Child is currently paused waiting for parents signal)
	helpers.Cg(cmd.Process.Pid)
	helpers.SetupNetwork(cmd.Process.Pid)
	helpers.SetupPortForwarding()

	// SIGNAL CHILD TO RESUME
	// We write to the pipe. This unblocks the child.
	fmt.Println("Parent: Cgroup set. Unpausing child...")
	w.Write([]byte("OK"))
	w.Close()

	defer func() {
		fmt.Println("\nParent: Cleaning up cgroups and network...")
		exec.Command("ip", "link", "delete", "veth0").Run()
		helpers.RemoveCgroup()
		helpers.RemovePortForwarding()
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

func must(err error) {
	if err != nil {
		panic(err)
	}
}
