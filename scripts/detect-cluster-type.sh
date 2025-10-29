#!/bin/bash

# Intelligent Cluster Type Detection Script
# Examines the environment and determines the best Kubernetes distribution to install

set -e

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m' # No Color

# Function to print colored output
print_status() {
    echo -e "${BLUE}[INFO]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[SUCCESS]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[WARNING]${NC} $1"
}

print_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

# Function to check if command exists
command_exists() {
    command -v "$@" > /dev/null 2>&1
}

# Function to get total system memory in MB
get_total_memory() {
    if command_exists free; then
        free -m | awk 'NR==2{print $2}'
    elif [ -f /proc/meminfo ]; then
        awk '/MemTotal/ {printf "%.0f", $2/1024}' /proc/meminfo
    elif [ "$(uname)" = "Darwin" ] && command_exists sysctl; then
        # macOS memory in bytes, convert to MB
        sysctl -n hw.memsize | awk '{printf "%.0f", $1/1024/1024}'
    else
        echo "0"
    fi
}

# Function to get available memory in MB
get_available_memory() {
    if command_exists free; then
        free -m | awk 'NR==2{printf "%.0f", $7}'
    elif [ -f /proc/meminfo ]; then
        awk '/MemAvailable/ {printf "%.0f", $2/1024}' /proc/meminfo
    elif [ "$(uname)" = "Darwin" ] && command_exists vm_stat; then
        # macOS available memory calculation (free + inactive + speculative)
        local vm_output=$(vm_stat)
        local pages_free=$(echo "$vm_output" | grep "Pages free" | awk '{print $3}' | sed 's/\.//')
        local pages_inactive=$(echo "$vm_output" | grep "Pages inactive" | awk '{print $3}' | sed 's/\.//')
        local pages_speculative=$(echo "$vm_output" | grep "Pages speculative" | awk '{print $3}' | sed 's/\.//')
        local page_size=$(echo "$vm_output" | head -1 | awk '{print $8}')
        local available_pages=$((pages_free + pages_inactive + pages_speculative))
        echo $(((available_pages * page_size) / 1024 / 1024))
    elif [ "$(uname)" = "Darwin" ] && command_exists sysctl; then
        # Fallback: assume 75% of total memory is available on macOS
        local total=$(sysctl -n hw.memsize | awk '{printf "%.0f", $1/1024/1024}')
        echo $((total * 75 / 100))
    else
        echo "0"
    fi
}

# Function to get CPU cores
get_cpu_cores() {
    if command_exists nproc; then
        nproc
    elif [ -f /proc/cpuinfo ]; then
        grep -c ^processor /proc/cpuinfo
    elif command_exists sysctl; then
        sysctl -n hw.ncpu 2>/dev/null || echo "1"
    else
        echo "1"
    fi
}

# Function to get available disk space in GB
get_disk_space() {
    if [ "$(uname)" = "Darwin" ]; then
        # macOS df output is different
        df -g / | awk 'NR==2{print $4}'
    else
        # Linux df output
        df / | awk 'NR==2{print int($4/1024/1024)}'
    fi
}

# Function to detect if running in Docker container
is_docker_container() {
    if [ -f /.dockerenv ]; then
        return 0
    fi
    
    if grep -q docker /proc/1/cgroup 2>/dev/null; then
        return 0
    fi
    
    return 1
}

# Function to detect if running in LXC container
is_lxc_container() {
    if [ -n "$container" ] && [ "$container" = "lxc" ]; then
        return 0
    fi
    
    if grep -q "container=lxc" /proc/1/environ 2>/dev/null; then
        return 0
    fi
    
    return 1
}

# Function to detect if running in WSL
is_wsl() {
    if grep -qi microsoft /proc/version 2>/dev/null; then
        return 0
    fi
    
    if [ -n "$WSL_DISTRO_NAME" ]; then
        return 0
    fi
    
    return 1
}

# Function to detect OS
detect_os() {
    if [ -f /etc/os-release ]; then
        . /etc/os-release
        echo "$ID"
    elif [ "$(uname)" = "Darwin" ]; then
        echo "macos"
    else
        echo "unknown"
    fi
}

# Function to check if Docker is available and working
check_docker() {
    if ! command_exists docker; then
        return 1
    fi
    
    if ! docker ps >/dev/null 2>&1; then
        return 1
    fi
    
    return 0
}

# Function to check if system can run k3s
can_run_k3s() {
    local total_mem=$(get_total_memory)
    local available_mem=$(get_available_memory)
    local cpu_cores=$(get_cpu_cores)
    local disk_space=$(get_disk_space)
    
    # k3s minimum requirements:
    # - 512MB RAM (we recommend 1GB)
    # - 1 CPU core
    # - 2GB disk space
    # - Not in Docker container
    # - Linux OS
    
    if [ "$(uname)" != "Linux" ]; then
        return 1
    fi
    
    if is_docker_container; then
        return 1
    fi
    
    if [ "$total_mem" -lt 512 ]; then
        return 1
    fi
    
    if [ "$cpu_cores" -lt 1 ]; then
        return 1
    fi
    
    if [ "$disk_space" -lt 2 ]; then
        return 1
    fi
    
    return 0
}

# Function to check if system can run minikube
can_run_minikube() {
    local total_mem=$(get_total_memory)
    local cpu_cores=$(get_cpu_cores)
    local disk_space=$(get_disk_space)
    
    # minikube minimum requirements:
    # - 2GB RAM
    # - 2 CPU cores
    # - 20GB disk space
    # - Docker or another driver
    
    if [ "$total_mem" -lt 2048 ]; then
        return 1
    fi
    
    if [ "$cpu_cores" -lt 2 ]; then
        return 1
    fi
    
    if [ "$disk_space" -lt 20 ]; then
        return 1
    fi
    
    # Check if we have a driver (Docker is most common)
    if ! check_docker; then
        # Could also check for other drivers (virtualbox, kvm, etc.)
        return 1
    fi
    
    return 0
}

# Function to check if system can run k3d
can_run_k3d() {
    local total_mem=$(get_total_memory)
    local cpu_cores=$(get_cpu_cores)
    local disk_space=$(get_disk_space)
    
    # k3d minimum requirements:
    # - 1GB RAM
    # - 1 CPU core
    # - 5GB disk space
    # - Docker
    
    if [ "$total_mem" -lt 1024 ]; then
        return 1
    fi
    
    if [ "$cpu_cores" -lt 1 ]; then
        return 1
    fi
    
    if [ "$disk_space" -lt 5 ]; then
        return 1
    fi
    
    if ! check_docker; then
        return 1
    fi
    
    return 0
}

# Function to check if system can run kind
can_run_kind() {
    local total_mem=$(get_total_memory)
    local cpu_cores=$(get_cpu_cores)
    local disk_space=$(get_disk_space)
    
    # kind minimum requirements:
    # - 2GB RAM (for single node)
    # - 2 CPU cores
    # - 10GB disk space
    # - Docker
    
    if [ "$total_mem" -lt 2048 ]; then
        return 1
    fi
    
    if [ "$cpu_cores" -lt 2 ]; then
        return 1
    fi
    
    if [ "$disk_space" -lt 10 ]; then
        return 1
    fi
    
    if ! check_docker; then
        return 1
    fi
    
    return 0
}

# Function to check if system can run Talos
can_run_talos() {
    local total_mem=$(get_total_memory)
    local cpu_cores=$(get_cpu_cores)
    local disk_space=$(get_disk_space)
    
    # Talos minimum requirements:
    # - 2GB RAM (recommend 4GB for control plane)
    # - 2 CPU cores
    # - 10GB disk space
    # - Linux (bare metal or VM)
    # - Not in Docker container
    # - x86_64 or aarch64 architecture
    
    if [ "$(uname)" != "Linux" ]; then
        return 1
    fi
    
    if is_docker_container; then
        return 1
    fi
    
    if [ "$total_mem" -lt 2048 ]; then
        return 1
    fi
    
    if [ "$cpu_cores" -lt 2 ]; then
        return 1
    fi
    
    if [ "$disk_space" -lt 10 ]; then
        return 1
    fi
    
    # Check architecture
    local arch=$(uname -m)
    if [ "$arch" != "x86_64" ] && [ "$arch" != "aarch64" ]; then
        return 1
    fi
    
    return 0
}

# Function to calculate suitability score for each cluster type
calculate_scores() {
    local total_mem=$(get_total_memory)
    local available_mem=$(get_available_memory)
    local cpu_cores=$(get_cpu_cores)
    local disk_space=$(get_disk_space)
    local os=$(detect_os)
    
    local k3s_score=0
    local minikube_score=0
    local k3d_score=0
    local kind_score=0
    local talos_score=0
    
    # k3s scoring
    if can_run_k3s; then
        k3s_score=50  # Base score
        
        # Prefer k3s on production VMs and bare metal
        if ! is_docker_container && ! is_lxc_container && ! is_wsl; then
            k3s_score=$((k3s_score + 30))
        fi
        
        # Bonus for good resources
        if [ "$total_mem" -ge 2048 ]; then
            k3s_score=$((k3s_score + 10))
        fi
        
        if [ "$cpu_cores" -ge 2 ]; then
            k3s_score=$((k3s_score + 5))
        fi
        
        # Bonus for cloud/production environments
        if [ "$total_mem" -ge 4096 ] && [ "$cpu_cores" -ge 2 ]; then
            k3s_score=$((k3s_score + 15))
        fi
        
        # Special handling for LXC containers
        if is_lxc_container; then
            k3s_score=$((k3s_score + 10))  # k3s works well with proper LXC config
        fi
    fi
    
    # minikube scoring
    if can_run_minikube; then
        minikube_score=40  # Base score
        
        # Prefer minikube on macOS
        if [ "$os" = "macos" ]; then
            minikube_score=$((minikube_score + 40))
        fi
        
        # Prefer for development environments
        if [ "$total_mem" -ge 4096 ] && [ "$total_mem" -lt 8192 ]; then
            minikube_score=$((minikube_score + 10))
        fi
        
        # Bonus if Docker is available
        if check_docker; then
            minikube_score=$((minikube_score + 10))
        fi
    fi
    
    # k3d scoring
    if can_run_k3d; then
        k3d_score=45  # Base score
        
        # Prefer k3d for Docker environments with limited resources
        if check_docker; then
            k3d_score=$((k3d_score + 15))
            
            # Extra points for resource-constrained environments
            if [ "$total_mem" -ge 1024 ] && [ "$total_mem" -lt 2048 ]; then
                k3d_score=$((k3d_score + 20))
            fi
        fi
        
        # Prefer k3d in Docker containers or WSL
        if is_docker_container || is_wsl; then
            k3d_score=$((k3d_score + 25))
        fi
        
        # Fast startup is valuable
        k3d_score=$((k3d_score + 10))
    fi
    
    # kind scoring
    if can_run_kind; then
        kind_score=35  # Base score
        
        # Prefer kind for CI/CD and multi-node testing
        if check_docker; then
            kind_score=$((kind_score + 10))
        fi
        
        # Bonus for good resources (kind is resource-heavy)
        if [ "$total_mem" -ge 4096 ] && [ "$cpu_cores" -ge 2 ]; then
            kind_score=$((kind_score + 15))
        fi
        
        # Prefer kind in CI environments
        if [ -n "$CI" ] || [ -n "$GITHUB_ACTIONS" ] || [ -n "$GITLAB_CI" ]; then
            kind_score=$((kind_score + 20))
        fi
    fi
    
    # Talos scoring
    if can_run_talos; then
        talos_score=55  # High base score for production use
        
        # Prefer Talos for production VMs and bare metal
        if ! is_docker_container && ! is_lxc_container && ! is_wsl; then
            talos_score=$((talos_score + 25))
        fi
        
        # Bonus for good resources (Talos benefits from more resources)
        if [ "$total_mem" -ge 4096 ]; then
            talos_score=$((talos_score + 15))
        fi
        
        if [ "$cpu_cores" -ge 4 ]; then
            talos_score=$((talos_score + 10))
        fi
        
        # Big bonus for production/cloud environments
        if [ "$total_mem" -ge 8192 ] && [ "$cpu_cores" -ge 4 ]; then
            talos_score=$((talos_score + 20))
        fi
        
        # Security-focused environments benefit from Talos
        # Check for indicators of production/security-conscious setup
        if [ -d /etc/kubernetes ] || [ -d /var/lib/kubelet ]; then
            talos_score=$((talos_score + 10))
        fi
    fi
    
    echo "k3s:$k3s_score,minikube:$minikube_score,k3d:$k3d_score,kind:$kind_score,talos:$talos_score"
}

# Function to recommend cluster type
recommend_cluster_type() {
    local scores=$(calculate_scores)
    local k3s_score=$(echo "$scores" | cut -d',' -f1 | cut -d':' -f2)
    local minikube_score=$(echo "$scores" | cut -d',' -f2 | cut -d':' -f2)
    local k3d_score=$(echo "$scores" | cut -d',' -f3 | cut -d':' -f2)
    local kind_score=$(echo "$scores" | cut -d',' -f4 | cut -d':' -f2)
    local talos_score=$(echo "$scores" | cut -d',' -f5 | cut -d':' -f2)
    
    local max_score=0
    local recommended="k3s"  # Default fallback
    
    if [ "$k3s_score" -gt "$max_score" ]; then
        max_score=$k3s_score
        recommended="k3s"
    fi
    
    if [ "$minikube_score" -gt "$max_score" ]; then
        max_score=$minikube_score
        recommended="minikube"
    fi
    
    if [ "$k3d_score" -gt "$max_score" ]; then
        max_score=$k3d_score
        recommended="k3d"
    fi
    
    if [ "$kind_score" -gt "$max_score" ]; then
        max_score=$kind_score
        recommended="kind"
    fi
    
    if [ "$talos_score" -gt "$max_score" ]; then
        max_score=$talos_score
        recommended="talos"
    fi
    
    # If all scores are 0, we can't run any cluster type
    if [ "$max_score" -eq 0 ]; then
        echo "none"
    else
        echo "$recommended"
    fi
}

# Function to display system information
show_system_info() {
    local total_mem=$(get_total_memory)
    local available_mem=$(get_available_memory)
    local cpu_cores=$(get_cpu_cores)
    local disk_space=$(get_disk_space)
    local os=$(detect_os)
    
    echo ""
    echo -e "${BLUE}╔════════════════════════════════════════════════════╗${NC}"
    echo -e "${BLUE}║       System Environment Detection                ║${NC}"
    echo -e "${BLUE}╚════════════════════════════════════════════════════╝${NC}"
    echo ""
    echo -e "${BLUE}System Resources:${NC}"
    echo "  • Operating System: $os ($(uname -s))"
    echo "  • Total Memory: ${total_mem}MB"
    echo "  • Available Memory: ${available_mem}MB"
    echo "  • CPU Cores: $cpu_cores"
    echo "  • Available Disk Space: ${disk_space}GB"
    echo ""
    
    echo -e "${BLUE}Environment:${NC}"
    if is_docker_container; then
        echo "  • Running in Docker container: Yes"
    else
        echo "  • Running in Docker container: No"
    fi
    
    if is_lxc_container; then
        echo "  • Running in LXC container: Yes"
    else
        echo "  • Running in LXC container: No"
    fi
    
    if is_wsl; then
        echo "  • Running in WSL: Yes"
    else
        echo "  • Running in WSL: No"
    fi
    
    if check_docker; then
        echo "  • Docker available: Yes"
    else
        echo "  • Docker available: No"
    fi
    echo ""
    
    echo -e "${BLUE}Cluster Type Compatibility:${NC}"
    
    if can_run_k3s; then
        echo -e "  • k3s: ${GREEN}✓ Compatible${NC}"
    else
        echo -e "  • k3s: ${RED}✗ Not compatible${NC}"
    fi
    
    if can_run_minikube; then
        echo -e "  • minikube: ${GREEN}✓ Compatible${NC}"
    else
        echo -e "  • minikube: ${RED}✗ Not compatible${NC}"
    fi
    
    if can_run_k3d; then
        echo -e "  • k3d: ${GREEN}✓ Compatible${NC}"
    else
        echo -e "  • k3d: ${RED}✗ Not compatible${NC}"
    fi
    
    if can_run_kind; then
        echo -e "  • kind: ${GREEN}✓ Compatible${NC}"
    else
        echo -e "  • kind: ${RED}✗ Not compatible${NC}"
    fi
    
    if can_run_talos; then
        echo -e "  • talos: ${GREEN}✓ Compatible${NC}"
    else
        echo -e "  • talos: ${RED}✗ Not compatible${NC}"
    fi
    echo ""
    
    # Show scores
    local scores=$(calculate_scores)
    echo -e "${BLUE}Suitability Scores (higher is better):${NC}"
    echo "  • k3s: $(echo "$scores" | cut -d',' -f1 | cut -d':' -f2)"
    echo "  • minikube: $(echo "$scores" | cut -d',' -f2 | cut -d':' -f2)"
    echo "  • k3d: $(echo "$scores" | cut -d',' -f3 | cut -d':' -f2)"
    echo "  • kind: $(echo "$scores" | cut -d',' -f4 | cut -d':' -f2)"
    echo "  • talos: $(echo "$scores" | cut -d',' -f5 | cut -d':' -f2)"
    echo ""
    
    # Show recommendation
    local recommended=$(recommend_cluster_type)
    if [ "$recommended" = "none" ]; then
        echo -e "${RED}╔════════════════════════════════════════════════════╗${NC}"
        echo -e "${RED}║  No compatible cluster type found!                 ║${NC}"
        echo -e "${RED}╚════════════════════════════════════════════════════╝${NC}"
        echo ""
        print_error "System does not meet minimum requirements for any cluster type"
        echo ""
        echo "Minimum requirements:"
        echo "  • k3s: 512MB RAM, 1 CPU, 2GB disk, Linux"
        echo "  • minikube: 2GB RAM, 2 CPUs, 20GB disk, Docker"
        echo "  • k3d: 1GB RAM, 1 CPU, 5GB disk, Docker"
        echo "  • kind: 2GB RAM, 2 CPUs, 10GB disk, Docker"
        echo "  • talos: 2GB RAM, 2 CPUs, 10GB disk, Linux (bare metal/VM)"
        return 1
    else
        echo -e "${GREEN}╔════════════════════════════════════════════════════╗${NC}"
        echo -e "${GREEN}║  Recommended: ${recommended}${NC}"
        printf "${GREEN}╚════════════════════════════════════════════════════╝${NC}\n"
        echo ""
        
        # Show reason for recommendation
        case "$recommended" in
            "k3s")
                print_success "k3s is recommended for production workloads and bare metal/VM environments"
                ;;
            "minikube")
                print_success "minikube is recommended for local development and testing"
                ;;
            "k3d")
                print_success "k3d is recommended for fast, lightweight development with Docker"
                ;;
            "kind")
                print_success "kind is recommended for CI/CD and multi-node testing scenarios"
                ;;
            "talos")
                print_success "Talos is recommended for secure, immutable production Kubernetes deployments"
                print_warning "Note: Talos requires OS-level installation (ISO boot or cloud image)"
                print_warning "For existing Linux systems, use k3s instead"
                ;;
        esac
    fi
    
    echo ""
}

# Main execution
main() {
    case "${1:-}" in
        "info"|"--info"|"-i")
            show_system_info
            ;;
        "recommend"|"--recommend"|"-r"|"")
            # Just output the recommendation
            recommend_cluster_type
            ;;
        "scores"|"--scores")
            calculate_scores
            ;;
        "help"|"--help"|"-h")
            echo "Usage: $0 [COMMAND]"
            echo ""
            echo "Commands:"
            echo "  recommend    Output recommended cluster type (default)"
            echo "  info         Show detailed system information and recommendation"
            echo "  scores       Show suitability scores for each cluster type"
            echo "  help         Show this help message"
            echo ""
            echo "Examples:"
            echo "  $0                  # Get recommendation"
            echo "  $0 info             # Show detailed system info"
            echo "  $0 recommend        # Get recommendation"
            echo "  CLUSTER_TYPE=\$($0)  # Use in script"
            ;;
        *)
            print_error "Unknown command: $1"
            echo "Use --help for usage information"
            exit 1
            ;;
    esac
}

main "$@"
