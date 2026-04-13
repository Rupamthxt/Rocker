package helpers

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
)

func Cg(pid int) {
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

func RemoveCgroup() {
	os.Remove("/sys/fs/cgroup/rocker")
}
