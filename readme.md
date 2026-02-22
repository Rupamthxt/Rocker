# Rocker: A Mini-Docker Container Runtime in Go

Rocker is an educational container runtime built entirely from scratch in Go. It does not use `libcontainer`, `containerd`, or any high-level containerization libraries. Instead, it interfaces directly with the Linux Kernel via raw system calls to demonstrate exactly how tools like Docker and `runc` work under the hood.

## 🚀 Features

* **Namespace Isolation (`CLONE_NEWUTS`, `CLONE_NEWPID`, `CLONE_NEWNS`)**: 
    * **UTS**: The container has its own isolated hostname.
    * **PID**: Processes inside the container think they are PID 1. They cannot see or interact with host processes.
    * **Mount**: Mount points created inside the container do not leak to the host.
* **Cryptographically Secure Root Filesystem (`pivot_root`)**: 
    * Swaps the host OS mount for an Alpine Linux root filesystem.
    * Uses `pivot_root` instead of the insecure `chroot` to completely detach and remove the host filesystem from the container's memory space, preventing escape attacks.
* **Resource Limits (Cgroups v2)**: 
    * Creates dynamic Control Groups for the container.
    * Implements a strict PID limit (`pids.max = 10`) to protect the host machine from malicious fork bombs.
* **Process Synchronization (IPC Pipes)**: 
    * Uses anonymous in-memory pipes to synchronize the parent and child processes. The child is forced to block (sleep) until the parent finishes building the cgroup walls around it, eliminating race conditions.
* **Automatic Cleanup**: 
    * Gracefully unmounts the pseudo-filesystems (`/proc`) and deletes the cgroup directories when the container exits.

## 📋 Prerequisites

* **OS**: Linux (or WSL2 on Windows).
* **Go**: Go 1.18+ installed.
* **Permissions**: `root` privileges are required to execute Linux namespace system calls.

## 🛠️ Getting Started

### 1. Prepare the Root Filesystem
Rocker needs a minimal Linux filesystem to act as the container's world. We use Alpine Linux.

```bash
# Create a directory for the root filesystem
mkdir -p rootfs

# Download the Alpine minimal rootfs tarball
wget -nc [https://dl-cdn.alpinelinux.org/alpine/v3.18/releases/x86_64/alpine-minirootfs-3.18.4-x86_64.tar.gz](https://dl-cdn.alpinelinux.org/alpine/v3.18/releases/x86_64/alpine-minirootfs-3.18.4-x86_64.tar.gz)

# Extract the filesystem
tar -xzf alpine-minirootfs-3.18.4-x86_64.tar.gz -C rootfs/
```

### 2. Build the Runtime
```bash
go build -o rocker main.go
```

### 3. Run a container
Launch an interactive shell inside your new isolated environment:
```bash
sudo ./rocker run /bin/sh
```

## 🧪 Testing the Defenses
Once inside the Rocker container, try the following commands to verify the isolation:
### 1. Test Process Isolation
```bash
/ ps
```
You will only see the processes running inside the container (PID 1 and your shell), not the host's processes.
### 2. Test Filesystem Isolation
```bash
/ mount
```
You will only see the root mount (/) and /proc. The host's hard drives and system mounts are invisible.
### 3. Test Cgroup Limits (The Fork Bomb)
```bash
/  for i in $(seq 1 30); do sleep 60 & done
```
The container will successfully spawn a few processes and then explicitly fail with /bin/sh: can't fork: Resource temporarily unavailable, proving the Cgroup PID limit successfully protected the host OS.

## 🧠 Architecture / How it Works
Rocker uses the Re-execution pattern. Because the Go runtime is multithreaded, it does not play nicely with the clone system call.

### 1. The user runs ./rocker run <cmd>.
### 2. The parent process prepares the namespace flags and re-executes itself (/proc/self/exe) as a child process.
### 3. The parent creates an IPC pipe, sets up the Cgroup v2 limits for the child's PID, and then sends a wake-up signal through the pipe.
### 4. The child wakes up, executes pivot_root to trap itself in the rootfs, mounts /proc, and finally execs the user's requested command (e.g., /bin/sh).