#!/usr/bin/env bash
# Clean up test resources created during conformance testing.
#
# This script removes any VMs created by the test suite.
# VMs created by tests use VMIDs in the range 900000-999999.

set -euo pipefail

echo "Cleaning up Proxmox test resources..."

if [ -z "${PROXMOX_API_URL:-}" ] || [ -z "${PROXMOX_TOKEN_ID:-}" ] || [ -z "${PROXMOX_SECRET:-}" ] || [ -z "${PROXMOX_NODE:-}" ]; then
  echo "Proxmox credentials not set, skipping cleanup."
  exit 0
fi

CURL_OPTS=(-s -k)
AUTH_HEADER="Authorization: PVEAPIToken=${PROXMOX_TOKEN_ID}=${PROXMOX_SECRET}"
BASE_URL="${PROXMOX_API_URL}/api2/json"

# List VMs on the node
VMS=$(curl "${CURL_OPTS[@]}" -H "$AUTH_HEADER" "${BASE_URL}/nodes/${PROXMOX_NODE}/qemu" | python3 -c "
import sys, json
data = json.load(sys.stdin).get('data', [])
for vm in data:
    vmid = vm.get('vmid', 0)
    if 900000 <= vmid <= 999999:
        print(vmid)
" 2>/dev/null || true)

if [ -z "$VMS" ]; then
  echo "No test VMs found to clean up."
  exit 0
fi

for VMID in $VMS; do
  echo "Stopping and removing test VM $VMID..."
  # Stop VM (ignore errors if already stopped)
  curl "${CURL_OPTS[@]}" -X POST -H "$AUTH_HEADER" \
    "${BASE_URL}/nodes/${PROXMOX_NODE}/qemu/${VMID}/status/stop" 2>/dev/null || true
  sleep 2
  # Delete VM
  curl "${CURL_OPTS[@]}" -X DELETE -H "$AUTH_HEADER" \
    "${BASE_URL}/nodes/${PROXMOX_NODE}/qemu/${VMID}" 2>/dev/null || true
done

echo "Cleanup complete."
