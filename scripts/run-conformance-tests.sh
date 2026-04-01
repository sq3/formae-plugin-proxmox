#!/usr/bin/env bash
# Run conformance tests against a specified formae version.
#
# Usage: ./scripts/run-conformance-tests.sh [VERSION]
#
# Environment variables:
#   FORMAE_TEST_FILTER  - Filter tests by name pattern
#   FORMAE_TEST_TYPE    - Test type: crud or discovery
#   FORMAE_TEST_TIMEOUT - Timeout in minutes (default: 5)

set -euo pipefail

VERSION="${1:-latest}"
TEST_FILTER="${FORMAE_TEST_FILTER:-}"
TEST_TYPE="${FORMAE_TEST_TYPE:-crud}"
TEST_TIMEOUT="${FORMAE_TEST_TIMEOUT:-5}"

echo "Running ${TEST_TYPE} conformance tests (formae version: ${VERSION})..."

# Build test arguments
TEST_ARGS="-v -tags=conformance -timeout=${TEST_TIMEOUT}m"

if [ -n "$TEST_FILTER" ]; then
  TEST_ARGS="${TEST_ARGS} -run ${TEST_FILTER}"
fi

# Run the tests
go test ${TEST_ARGS} .
