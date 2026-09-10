#!/usr/bin/env bash
# Copyright 2026 Canonical Ltd.
# SPDX-License-Identifier: Apache-2.0

# run-centralized-authz-e2e.sh
# Complete end-to-end orchestration runner:
# 1. Sets up the environment via setup-centralized-authz-e2e.sh
# 2. Runs the test suite via test-centralized-authz-e2e.sh
# 3. Cleans up via teardown-centralized-authz-e2e.sh (unless --keep-running is provided)

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
KEEP_RUNNING=false

for arg in "$@"; do
    if [ "$arg" == "--keep-running" ] || [ "$arg" == "--no-teardown" ]; then
        KEEP_RUNNING=true
    fi
done

cleanup() {
    local exit_code=$?
    if [ "$KEEP_RUNNING" = false ]; then
        echo ""
        echo "[INFO] Automatically tearing down services..."
        "$SCRIPT_DIR/teardown-centralized-authz-e2e.sh" || true
    else
        echo ""
        echo "[INFO] --keep-running specified. Services left running."
        echo "       Run ./scripts/teardown-centralized-authz-e2e.sh when finished."
    fi
    exit $exit_code
}

trap cleanup EXIT INT TERM

# Run Setup
"$SCRIPT_DIR/setup-centralized-authz-e2e.sh"

# Run Tests
echo ""
echo "=================================================================="
echo " Executing Centralized Authz E2E Tests"
echo "=================================================================="
"$SCRIPT_DIR/test-centralized-authz-e2e.sh"
