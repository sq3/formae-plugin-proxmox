#!/usr/bin/env bash
# Setup Proxmox VE credentials for conformance testing.
#
# Required environment variables:
#   PROXMOX_API_URL    - Proxmox API URL (e.g., https://pve.example.com:8006)
#   PROXMOX_TOKEN_ID   - API token ID (format: user@realm!tokenname)
#   PROXMOX_SECRET     - API token secret (UUID)
#   PROXMOX_NODE       - Default node name
#
# Optional:
#   PROXMOX_INSECURE_SKIP_VERIFY - Skip TLS verification (default: false)

set -euo pipefail

echo "Checking Proxmox credentials..."

required_vars=(PROXMOX_API_URL PROXMOX_TOKEN_ID PROXMOX_SECRET PROXMOX_NODE)
missing=()

for var in "${required_vars[@]}"; do
  if [ -z "${!var:-}" ]; then
    missing+=("$var")
  fi
done

if [ ${#missing[@]} -gt 0 ]; then
  echo "ERROR: Missing required environment variables:"
  for var in "${missing[@]}"; do
    echo "  - $var"
  done
  exit 1
fi

echo "Proxmox credentials configured:"
echo "  API URL: ${PROXMOX_API_URL}"
echo "  Token:   ${PROXMOX_TOKEN_ID}"
echo "  Node:    ${PROXMOX_NODE}"
echo "  TLS:     ${PROXMOX_INSECURE_SKIP_VERIFY:-verify}"
