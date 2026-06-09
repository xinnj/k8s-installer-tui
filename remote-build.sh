#!/bin/bash

# Test script for k8s-installer-tui
# This script:
# 1. Rsync project to remote build machine
# 2. Build Go binary
# 3. Package all necessary files for deployment

set -e  # Exit on error

# Load configuration from env file
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
source "${SCRIPT_DIR}/.env"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo_info() {
    echo -e "${GREEN}[INFO]${NC} $1"
}

echo_warn() {
    echo -e "${YELLOW}[WARN]${NC} $1"
}

echo_error() {
    echo -e "${RED}[ERROR]${NC} $1"
}

SSH_OPTS="-o StrictHostKeyChecking=no -p ${REMOTE_PORT}"
# Step 1: Rsync project to remote server
step1_rsync() {
    echo_info "Step 1: Syncing project to remote build machine..."

    echo_info "Creating remote directory..."
    ssh ${SSH_OPTS} \
        "${REMOTE_USER}@${REMOTE_HOST}" \
        "mkdir -p ${REMOTE_PATH}"

    echo_info "Syncing files to remote (this may take a moment)..."
    rsync -avz --delete \
        --exclude='.git' \
        --exclude='.DS_Store' \
        --exclude='kubespray-2.28.0' \
        --exclude='kubespray-runtime' \
        -e "ssh -o StrictHostKeyChecking=no -p ${REMOTE_PORT}" \
        ./ \
        "${REMOTE_USER}@${REMOTE_HOST}:${REMOTE_PATH}/"

    echo_info "Project synced successfully!"
}

# Step 2: Build Go binary
step2_build() {
    echo_info "Step 2: Building Go binary..."

    ssh ${SSH_OPTS} \
        "${REMOTE_USER}@${REMOTE_HOST}" \
        "cd ${REMOTE_PATH} && CGO_ENABLED=0 go build -o k8s-installer-tui ."

    echo_info "Go build completed successfully!"
}

# Step 3: Package all necessary files for deployment
step3_package() {
    echo_info "Step 3: Packaging files for deployment..."

    ssh ${SSH_OPTS} \
        "${REMOTE_USER}@${REMOTE_HOST}" \
        "cd ${REMOTE_PATH} &&
         PKG_DIR=\"${REMOTE_PATH}/pkg\"
         rm -rf \"\${PKG_DIR}\"
         mkdir -p \"\${PKG_DIR}\" &&
         chmod +x k8s-installer-tui &&
         cp -a ansible-playbooks ansible-roles images config.yaml k8s-installer-tui kubespray-*.tar.gz LICENSE README.md patches inventory_builder \"\${PKG_DIR}/\" &&
         echo 'Packaged files:' &&
         ls -la \"\${PKG_DIR}/\""

    echo_info "Package assembled successfully!"
}

# Main execution
main() {
    echo_info "=========================================="
    echo_info "Starting test run for k8s-installer-tui"
    echo_info "=========================================="
    echo ""

    # Execute steps
    step1_rsync
    echo ""

    step2_build
    echo ""

    step3_package
    echo ""

    echo_info "=========================================="
    echo_info "All steps completed successfully!"
    echo_info "=========================================="
}

# Run main function
main "$@"
