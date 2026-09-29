#!/usr/bin/env bash
# Copyright 2026 Canonical Ltd.
# SPDX-License-Identifier: Apache-2.0

# teardown-centralized-authz-e2e.sh
# Gracefully stops all background daemon services and tears down Docker containers.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
HOOK_SERVICE_DIR="$(realpath "$SCRIPT_DIR/..")"
PID_FILE="$HOOK_SERVICE_DIR/.centralized-authz.pids"
COMPOSE_FILE="$HOOK_SERVICE_DIR/docker/centralized-authz/docker-compose.yml"

WIPE_VOLUMES=true
for arg in "$@"; do
    if [ "$arg" == "--keep-volumes" ] || [ "$arg" == "--no-wipe" ]; then
        WIPE_VOLUMES=false
    fi
done

echo "=================================================================="
echo " Tearing Down Centralized Authorization E2E Environment"
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

# Fallback check on standard service ports
if ! command -v lsof >/dev/null 2>&1 && ! command -v fuser >/dev/null 2>&1 && ! command -v ss >/dev/null 2>&1; then
    echo "[WARN] Neither lsof, fuser, nor ss found; skipping port-based orphan cleanup"
else
    for port in 8888 8080 9090 9091 9100 9101 9102 8000 9095; do
        PIDS=$(find_port_pids "$port")
        if [ -n "$PIDS" ]; then
            echo "[INFO] Cleaning up lingering process on port :$port (PID: $PIDS)..."
            kill $PIDS 2>/dev/null || true
            sleep 0.5
            kill -9 $PIDS 2>/dev/null || true
        fi
    done
fi

# 2. Stop Docker Compose infrastructure
echo ""
echo "==== Step 2: Stopping Docker Compose Services ===="
if [ "$WIPE_VOLUMES" = true ]; then
    echo "[INFO] Stopping containers and removing persistent volumes (-v)..."
    docker compose -p dependencies -f "$COMPOSE_FILE" down -v --remove-orphans || true
else
    echo "[INFO] Stopping containers (preserving database volumes)..."
    docker compose -p dependencies -f "$COMPOSE_FILE" down --remove-orphans || true
fi

# Clean up standalone container if left behind
if docker ps -a --format '{{.Names}}' | grep -q '^authz-envoy$'; then
    docker stop authz-envoy >/dev/null 2>&1 || true
    docker rm authz-envoy >/dev/null 2>&1 || true
fi

echo ""
echo "=================================================================="
echo " Teardown Completed Successfully!"
echo "=================================================================="
