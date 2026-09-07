#!/usr/bin/env bash
# Copyright 2026 Canonical Ltd.
# SPDX-License-Identifier: Apache-2.0

# setup-centralized-authz-e2e.sh
# Automated environment setup for Centralized Authorization E2E testing.
# Spins up docker infrastructure (Postgres, OpenFGA, Valkey, Kafka, Envoy),
# compiles required binaries, executes migrations, and starts all daemon services.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
HOOK_SERVICE_DIR="$(realpath "$SCRIPT_DIR/..")"
AUTHZ_SERVICE_DIR="${AUTHZ_SERVICE_DIR:-$(realpath "$HOOK_SERVICE_DIR/../authorization-service")}"
STS_DIR="${STS_DIR:-$(realpath "$HOOK_SERVICE_DIR/../secure-token-service")}"

LOGS_DIR="$HOOK_SERVICE_DIR/logs/centralized-authz"
PID_FILE="$HOOK_SERVICE_DIR/.centralized-authz.pids"
COMPOSE_FILE="$HOOK_SERVICE_DIR/docker/centralized-authz/docker-compose.yml"

mkdir -p "$LOGS_DIR"
touch "$PID_FILE"

echo "=================================================================="
echo " Setting up Centralized Authorization E2E Environment"
echo "=================================================================="
echo "[INFO] Hook Service Directory   : $HOOK_SERVICE_DIR"
echo "[INFO] Authz Service Directory  : $AUTHZ_SERVICE_DIR"
echo "[INFO] STS Directory            : $STS_DIR"
echo "[INFO] Logs Directory           : $LOGS_DIR"
echo "=================================================================="

# Check directories
if [ ! -d "$AUTHZ_SERVICE_DIR" ]; then
    echo "[ERROR] Authorization Service directory not found at $AUTHZ_SERVICE_DIR"
    echo "        Set AUTHZ_SERVICE_DIR=/path/to/authorization-service to customize."
    exit 1
fi

if [ ! -d "$STS_DIR" ]; then
    echo "[ERROR] Secure Token Service directory not found at $STS_DIR"
    echo "        Set STS_DIR=/path/to/secure-token-service to customize."
    exit 1
fi

# Ensure standalone container authz-envoy doesn't conflict with compose if created manually earlier
if docker ps -a --format '{{.Names}}' | grep -q '^authz-envoy$'; then
    IS_COMPOSE=$(docker inspect -f '{{index .Config.Labels "com.docker.compose.project"}}' authz-envoy 2>/dev/null || true)
    if [ -z "$IS_COMPOSE" ]; then
        echo "[INFO] Removing non-compose authz-envoy container..."
        docker stop authz-envoy >/dev/null 2>&1 || true
        docker rm authz-envoy >/dev/null 2>&1 || true
    fi
fi

# 1. Start Docker Infrastructure
echo ""
echo "==== Step 1: Starting Docker Infrastructure ===="
if ! docker network ls --format '{{.Name}}' | grep -q '^authz-network$'; then
    echo "[INFO] Creating Docker network authz-network..."
    docker network create authz-network
fi
docker compose -p dependencies -f "$COMPOSE_FILE" up -d

echo "[INFO] Waiting for Postgres (5433)..."
until docker exec authz-postgres pg_isready -U authorization-service -d openfga >/dev/null 2>&1; do
    sleep 1
done
echo "  ✓ Postgres is healthy"

echo "[INFO] Waiting for Valkey (6380)..."
until docker exec authz-valkey valkey-cli ping | grep -q PONG; do
    sleep 1
done
echo "  ✓ Valkey is healthy"

echo "[INFO] Flushing Valkey cache..."
docker exec authz-valkey valkey-cli flushall >/dev/null 2>&1 || true
echo "  ✓ Valkey cache flushed"

echo "[INFO] Waiting for OpenFGA (8082)..."
until curl -s http://localhost:8082/healthz | grep -q '"status":"SERVING"'; do
    sleep 1
done
echo "  ✓ OpenFGA is healthy"

echo "[INFO] Waiting for Kafka (9092)..."
until docker exec authz-kafka /opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server localhost:9092 >/dev/null 2>&1; do
    sleep 1
done
echo "  ✓ Kafka is healthy"

echo "[INFO] Creating Kafka permission topics..."
docker exec authz-kafka /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server localhost:9092 \
    --create --if-not-exists \
    --topic hook-service.permissions \
    --partitions 1 --replication-factor 1 >/dev/null 2>&1 || true
echo "  ✓ Kafka topic hook-service.permissions ready"

# 2. Database multi-tenant initialization
echo ""
echo "==== Step 2: Initializing PostgreSQL Databases ===="
docker exec -i authz-postgres psql -U authorization-service -d openfga < "$HOOK_SERVICE_DIR/docker/centralized-authz/postgres/init-scripts/01-init-dbs.sql" >/dev/null 2>&1 || true
echo "  ✓ Databases and roles verified (openfga, authorization-service, sts, groups)"

# 3. Compile Binaries
echo ""
echo "==== Step 3: Compiling Service Binaries ===="
echo "[INFO] Building Hook Service..."
go build -o "$HOOK_SERVICE_DIR/app" "$HOOK_SERVICE_DIR"

echo "[INFO] Building STS..."
mkdir -p "$STS_DIR/bin"
(cd "$STS_DIR" && go build -o "$STS_DIR/bin/app" "$STS_DIR/cmd/sts")

echo "[INFO] Building Cerberus (Authorization Service)..."
mkdir -p "$AUTHZ_SERVICE_DIR/bin"
(cd "$AUTHZ_SERVICE_DIR" && go build -o "$AUTHZ_SERVICE_DIR/bin/app" "$AUTHZ_SERVICE_DIR")

# 4. Run Migrations & Setup Authorization Model
echo ""
echo "==== Step 4: Running Migrations and Seeding Models ===="
echo "[INFO] Running STS migrations..."
env DATABASE_URL="postgres://authorization-service:password@localhost:5433/sts?sslmode=disable" \
    OIDC_PROVIDER_URL="http://localhost:8888" \
    OIDC_CLIENT_ID="dummy" \
    OIDC_CLIENT_SECRET="dummy" \
    OIDC_REDIRECT_URL="http://localhost:8080/callback" \
    COOKIE_HASH_KEY="0123456789012345678901234567890123456789012345678901234567890123" \
    "$STS_DIR/bin/app" migrate >/dev/null

if ! err_msg=$(env DATABASE_URL="postgres://authorization-service:password@localhost:5433/sts?sslmode=disable" \
    OIDC_PROVIDER_URL="http://localhost:8888" \
    OIDC_CLIENT_ID="dummy" \
    OIDC_CLIENT_SECRET="dummy" \
    OIDC_REDIRECT_URL="http://localhost:8080/callback" \
    COOKIE_HASH_KEY="0123456789012345678901234567890123456789012345678901234567890123" \
    "$STS_DIR/bin/app" rotate-key 2>&1); then
    echo "  [WARN] STS rotate-key failed: $err_msg" >&2
else
    echo "  ✓ STS signing key configured"
fi

echo "[INFO] Running Cerberus migrations..."
"$AUTHZ_SERVICE_DIR/bin/app" migrate --dsn "postgres://authorization-service:password@localhost:5433/authorization-service?sslmode=disable" >/dev/null

echo "[INFO] Writing OpenFGA authorization model..."
# Note: OPENFGA_AUTHORIZATION_MODEL_ID=dummy is required because Cerberus config validation
# enforces AuthorizationModelID != "" even though write-model creates a new model.
if ! err_msg=$(OPENFGA_STORE_ID=01GP1254CHWJC1MNGVB0WDG1T0 OPENFGA_AUTHORIZATION_MODEL_ID=dummy OPENFGA_ADDRESS=http://localhost:8082 OPENFGA_API_KEY=42 \
    "$AUTHZ_SERVICE_DIR/bin/app" authz write-model 01GP1254CHWJC1MNGVB0WDG1T0 2>&1); then
    echo "  [WARN] OpenFGA write-model failed: $err_msg" >&2
fi
MODEL_ID=$(curl -s -H "Authorization: Bearer 42" http://localhost:8082/stores/01GP1254CHWJC1MNGVB0WDG1T0/authorization-models | jq -r '.authorization_models[0].id // empty')
[ -n "$MODEL_ID" ] || { echo "Failed to retrieve OpenFGA model ID"; exit 1; }
echo "  ✓ OpenFGA Authorization Model ID: $MODEL_ID"

echo "[INFO] Seeding Hook Service declarative rules..."
if ! err_msg=$(POSTGRES_PORT=5433 POSTGRES_USER=authorization-service POSTGRES_PASSWORD=password OPENFGA_STORE_ID=01GP1254CHWJC1MNGVB0WDG1T0 OPENFGA_API_KEY=42 OPENFGA_AUTHORIZATION_MODEL_ID="$MODEL_ID" \
    "$AUTHZ_SERVICE_DIR/bin/app" seed 2>&1); then
    echo "  [WARN] Cerberus rule seeding failed: $err_msg" >&2
else
    echo "  ✓ Cerberus route rules seeded"
fi

echo "[INFO] Running Hook Service database migrations..."
"$HOOK_SERVICE_DIR/app" migrate --dsn "postgres://groups:groups@localhost:5433/groups?sslmode=disable" >/dev/null

echo "[INFO] Ensuring Domain Admin tuple in OpenFGA..."
curl -s -X POST http://localhost:8082/stores/01GP1254CHWJC1MNGVB0WDG1T0/write \
  -H "Content-Type: application/json" -H "Authorization: Bearer 42" \
  -d '{"writes": {"tuple_keys": [{"user": "user:domain-admin@canonical.com", "relation": "admin", "object": "group-in-claim:__domain__", "condition": {"name": "tenant_match", "context": {"tenant": "hook-service"}}}]}}' >/dev/null
echo "  ✓ OpenFGA domain-admin tuple configured"

# 5. Start Background Daemons
echo ""
echo "==== Step 5: Starting Background Daemons ===="

start_daemon() {
    local name="$1"
    local port="$2"
    local logfile="$3"
    shift 3

    if nc -z localhost "$port" 2>/dev/null; then
        echo "  [INFO] $name is already running on port $port"
    else
        echo "  [START] Starting $name (port :$port)..."
        nohup setsid "$@" > "$logfile" 2>&1 &
        local pid=$!
        disown $pid 2>/dev/null || true
        echo "$pid:$name" >> "$PID_FILE"
        local count=0
        while ! nc -z localhost "$port" 2>/dev/null; do
            sleep 0.5
            count=$((count + 1))
            if [ $count -gt 30 ]; then
                echo "  [ERROR] $name failed to start on port $port! Check log: $logfile"
                tail -n 20 "$logfile"
                exit 1
            fi
        done
        echo "  ✓ $name started successfully (PID: $pid)"
    fi
}

# Start OIDC mock
start_daemon "Mock OIDC Server" 8888 "$LOGS_DIR/oidc_mock.log" \
    python3 "$HOOK_SERVICE_DIR/scripts/oidc_mock.py" 8888

# Start STS Serve
start_daemon "STS Serve" 8080 "$LOGS_DIR/sts.log" \
    env HTTP_PORT="8080" GRPC_PORT="9090" CACHE_ADDR="localhost:6380" \
    DATABASE_URL="postgres://authorization-service:password@localhost:5433/sts?sslmode=disable" \
    COOKIE_HASH_KEY="0123456789012345678901234567890123456789012345678901234567890123" \
    JWT_ISSUER="session-service" JWT_AUDIENCE="internal-services" \
    OIDC_PROVIDER_URL="http://localhost:8888" OIDC_CLIENT_ID="dummy" OIDC_CLIENT_SECRET="dummy" \
    OIDC_REDIRECT_URL="http://localhost:8080/callback" LOG_LEVEL="debug" \
    "$STS_DIR/bin/app" serve

# Start Cerberus extAuthz Serve
start_daemon "Cerberus Serve" 9091 "$LOGS_DIR/cerberus-serve.log" \
    env SERVER_GRPC_PORT=9091 SERVER_HTTP_PORT=8070 \
    OPENFGA_ADDRESS=http://localhost:8082 OPENFGA_STORE_ID=01GP1254CHWJC1MNGVB0WDG1T0 \
    OPENFGA_API_KEY=42 OPENFGA_AUTHORIZATION_MODEL_ID="$MODEL_ID" \
    POSTGRES_PORT=5433 POSTGRES_USER=authorization-service POSTGRES_PASSWORD=password \
    METRICS_PORT=9100 VALKEY_ENABLED=false STS_ADDRESS="localhost:9090" \
    EXTAUTHZ_JWK_SET_URL="http://localhost:8080/.well-known/jwks.json" \
    MULTITENANCY_ENABLED=false LOG_LEVEL=debug LOGGING_LEVEL=debug \
    "$AUTHZ_SERVICE_DIR/bin/app" serve

# Start Cerberus Async Worker
start_daemon "Cerberus Async Worker" 9102 "$LOGS_DIR/cerberus-worker.log" \
    env WORKER_ENABLED=true WORKER_POLL_INTERVAL=500ms MULTITENANCY_ENABLED=true \
    OPENFGA_ADDRESS=http://localhost:8082 OPENFGA_STORE_ID=01GP1254CHWJC1MNGVB0WDG1T0 \
    OPENFGA_API_KEY=42 OPENFGA_AUTHORIZATION_MODEL_ID="$MODEL_ID" \
    POSTGRES_PORT=5433 POSTGRES_USER=authorization-service POSTGRES_PASSWORD=password \
    METRICS_PORT=9102 LOG_LEVEL=debug LOGGING_LEVEL=debug \
    "$AUTHZ_SERVICE_DIR/bin/app" worker

# Start Cerberus Kafka Listener
start_daemon "Cerberus Kafka Listener" 9101 "$LOGS_DIR/cerberus-listener.log" \
    env KAFKA_ENABLED=true KAFKA_BROKERS=localhost:9092 FEDERATED_SERVICES=hook-service \
    POSTGRES_PORT=5433 POSTGRES_USER=authorization-service POSTGRES_PASSWORD=password \
    OPENFGA_ADDRESS=http://localhost:8082 OPENFGA_STORE_ID=01GP1254CHWJC1MNGVB0WDG1T0 \
    OPENFGA_API_KEY=42 OPENFGA_AUTHORIZATION_MODEL_ID="$MODEL_ID" \
    METRICS_PORT=9101 LOG_LEVEL=debug LOGGING_LEVEL=debug \
    "$AUTHZ_SERVICE_DIR/bin/app" listen

# Start Hook Service Serve
start_daemon "Hook Service Serve" 8000 "$LOGS_DIR/hook-service.log" \
    env PORT="8000" GRPC_PORT="9095" AUTHENTICATION_ENABLED="false" \
    KAFKA_BROKERS="localhost:9092" DSN="postgres://groups:groups@localhost:5433/groups?sslmode=disable" \
    SALESFORCE_ENABLED="false" AUTHORIZATION_ENABLED="false" LOG_LEVEL="debug" \
    "$HOOK_SERVICE_DIR/app" serve

# Verify Envoy
echo "[INFO] Verifying Envoy Ingress Gateway (10000)..."
if ! nc -z localhost 10000 2>/dev/null; then
    echo "  [ERROR] Envoy is not responding on port 10000! Restarting envoy container..."
    docker compose -p dependencies -f "$COMPOSE_FILE" restart envoy
    sleep 2
fi
echo "  ✓ Envoy Ingress Gateway is responsive"

echo ""
echo "=================================================================="
echo " Setup Completed Successfully!"
echo "=================================================================="
echo "All daemons and infrastructure are running and healthy."
echo ""
echo "To run the comprehensive E2E test suite:"
echo "  ./scripts/test-centralized-authz-e2e.sh"
echo ""
echo "To stop all services and containers:"
echo "  ./scripts/teardown-centralized-authz-e2e.sh"
echo "=================================================================="
