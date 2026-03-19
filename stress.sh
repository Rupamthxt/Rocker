#!/bin/sh

# Colors for readability
GREEN='\033[0;32m'
RED='\033[0;31m'
NC='\033[0m'

echo -e "${GREEN}=== Starting Container Load Test ===${NC}"

# 1. TEST: Memory Exhaustion
# We try to allocate more than the 100MB limit defined in your cg() function.
test_memory() {
    echo -e "\n${GREEN}[1/3] Testing Memory Limit (100MB)...${NC}"
    # This creates a large variable in memory using awk
    # 1024 * 1024 * 110 (approx 110MB)
    awk 'BEGIN { for(i=0;i<110;i++) a[i]=sprintf("%1048576s", "a") }'
    
    if [ $? -ne 0 ]; then
        echo -e "${RED}Process killed! Cgroup memory limit enforced.${NC}"
    else
        echo "Memory allocation finished without being killed."
    fi
}

# 2. TEST: Process Limit (Fork Bomb)
# Your code sets pids.max to 100.
test_pids() {
    echo -e "\n${GREEN}[2/3] Testing PID Limit (100 PIDs)...${NC}"
    echo "Spawning background sleep processes..."
    for i in $(seq 1 120); do
        sleep 100 & 
        if [ $? -ne 0 ]; then
            echo -e "${RED}Failed to fork at process $i. PID limit enforced!${NC}"
            break
        fi
    done
    # Cleanup
    kill $(jobs -p) 2>/dev/null
}

# 3. TEST: Network Connectivity
test_network() {
    echo -e "\n${GREEN}[3/3] Testing Network Bridge...${NC}"
    ping -c 3 192.168.100.1
    if [ $? -eq 0 ]; then
        echo -e "${GREEN}Network to host is UP.${NC}"
    else
        echo -e "${RED}Network to host is DOWN.${NC}"
    fi
}

# Run tests
test_network
test_memory
test_pids

echo -e "\n${GREEN}=== Load Test Complete ===${NC}"