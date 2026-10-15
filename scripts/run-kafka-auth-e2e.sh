#!/usr/bin/env bash
# Copyright 2026 Canonical Ltd.
# SPDX-License-Identifier: Apache-2.0

# run-kafka-auth-e2e.sh
# Complete end-to-end orchestration runner for Kafka Authentication (SASL_SSL + OAUTHBEARER):
# 1. Sets up the environment via setup-kafka-auth-e2e.sh
# 2. Runs the test suite via test-kafka-auth-e2e.sh
# 3. Cleans up via teardown-kafka-auth-e2e.sh (unless --keep-running is provided)

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
KEEP_RUNNING=false
ENABLE_UI=false

for arg in "$@"; do
    if [ "$arg" == "--keep-running" ] || [ "$arg" == "--no-teardown" ]; then
        KEEP_RUNNING=true
    fi
    if [ "$arg" == "--ui" ] || [ "$arg" == "--with-ui" ]; then
        ENABLE_UI=true
    fi
done

cleanup() {
    local exit_code=$?
    if [ "$KEEP_RUNNING" = false ]; then
        echo ""
        echo "[INFO] Automatically tearing down services..."
        "$SCRIPT_DIR/teardown-kafka-auth-e2e.sh" || true
    else
        echo ""
        echo "[INFO] --keep-running specified. Services left running."
        if [ "$ENABLE_UI" = true ]; then
            echo "       Kafbat UI: http://localhost:8088"
        fi
        echo "       Inspect messages with CLI: ./scripts/view-kafka-messages.sh"
        echo "       Run ./scripts/teardown-kafka-auth-e2e.sh when finished."
    fi
    exit $exit_code
}

trap cleanup EXIT INT TERM

# Run Setup
SETUP_ARGS=()
if [ "$ENABLE_UI" = true ]; then
    SETUP_ARGS+=("--ui")
fi
"$SCRIPT_DIR/setup-kafka-auth-e2e.sh" "${SETUP_ARGS[@]}"

# Run Tests
echo ""
echo "=================================================================="
echo " Executing Kafka Authentication E2E Tests"
echo "=================================================================="
"$SCRIPT_DIR/test-kafka-auth-e2e.sh"
