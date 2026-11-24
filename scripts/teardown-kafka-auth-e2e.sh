#!/usr/bin/env bash
# Copyright 2026 Canonical Ltd.
# SPDX-License-Identifier: Apache-2.0

# teardown-kafka-auth-e2e.sh
# Gracefully stops Hook Service background daemon and tears down Kafka Auth E2E Docker infrastructure.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
HOOK_SERVICE_DIR="$(realpath "$SCRIPT_DIR/..")"
E2E_DIR="$HOOK_SERVICE_DIR/docker/kafka-auth-e2e"
PID_FILE="$HOOK_SERVICE_DIR/.kafka-auth-e2e.pids"
COMPOSE_FILE="$E2E_DIR/docker-compose.yml"

WIPE_VOLUMES=true
for arg in "$@"; do
    if [ "$arg" == "--keep-volumes" ] || [ "$arg" == "--no-wipe" ]; then
        WIPE_VOLUMES=false
    fi
done

echo "=================================================================="
echo " Tearing Down Kafka Authentication E2E Environment"
echo "=================================================================="

# 1. Stop background processes tracked in PID file
if [ -f "$PID_FILE" ]; then
    echo "==== Step 1: Stopping Background Daemons ===="
    while IFS=: read -r pid name || [ -n "$pid" ]; do
        [ -z "$pid" ] && continue
        if kill -0 "$pid" 2>/dev/null; then
            echo "[INFO] Stopping $name (PID: $pid)..."
            kill "$pid" 2>/dev/null || true
            sleep 0.5
            if kill -0 "$pid" 2>/dev/null; then
                kill -9 "$pid" 2>/dev/null || true
            fi
            echo "  ✓ $name stopped"
        else
            echo "[INFO] $name (PID: $pid) is already stopped"
        fi
    done < "$PID_FILE"
    rm -f "$PID_FILE"
else
    echo "[INFO] No PID file found at $PID_FILE"
fi

# Fallback check on port 8000
find_port_pids() {
    local port="$1"
    if command -v lsof >/dev/null 2>&1; then
        lsof -t -i ":$port" 2>/dev/null || true
    elif command -v fuser >/dev/null 2>&1; then
        fuser "$port/tcp" 2>/dev/null || true
    elif command -v ss >/dev/null 2>&1; then
        ss -lptn "sport = :$port" 2>/dev/null | grep -oP 'pid=\K[0-9]+' || true
    fi
}

for port in 8000; do
    PIDS=$(find_port_pids "$port")
    if [ -n "$PIDS" ]; then
        echo "[INFO] Cleaning up lingering process on port :$port (PID: $PIDS)..."
        kill $PIDS 2>/dev/null || true
        sleep 0.5
        kill -9 $PIDS 2>/dev/null || true
    fi
done

# 2. Stop Docker Compose infrastructure
echo ""
echo "==== Step 2: Stopping Docker Compose Services ===="
if [ -f "$COMPOSE_FILE" ]; then
    if [ "$WIPE_VOLUMES" = true ]; then
        echo "[INFO] Stopping containers and removing persistent volumes (-v)..."
        docker compose -p kafka-auth-e2e -f "$COMPOSE_FILE" down -v --remove-orphans || true
    else
        echo "[INFO] Stopping containers (preserving database volumes)..."
        docker compose -p kafka-auth-e2e -f "$COMPOSE_FILE" down --remove-orphans || true
    fi
fi

echo ""
echo "=================================================================="
echo " Teardown Completed Successfully!"
echo "=================================================================="
