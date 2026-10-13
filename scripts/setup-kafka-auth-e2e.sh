#!/usr/bin/env bash
# Copyright 2026 Canonical Ltd.
# SPDX-License-Identifier: Apache-2.0

# setup-kafka-auth-e2e.sh
# Automated environment setup for Kafka Authentication (SASL_SSL + OAUTHBEARER) E2E testing.
# Generates PKI/keystores, spins up Docker infrastructure (Postgres, Hydra, Kafka),
# registers Hydra OAuth2 clients, provisions Kafka ACLs, applies migrations,
# and starts Hook Service in Platform Mode.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
HOOK_SERVICE_DIR="$(realpath "$SCRIPT_DIR/..")"
E2E_DIR="$HOOK_SERVICE_DIR/docker/kafka-auth-e2e"
CERTS_DIR="$E2E_DIR/certs"
LOGS_DIR="$HOOK_SERVICE_DIR/logs/kafka-auth"
PID_FILE="$HOOK_SERVICE_DIR/.kafka-auth-e2e.pids"
COMPOSE_FILE="$E2E_DIR/docker-compose.yml"

mkdir -p "$CERTS_DIR" "$LOGS_DIR"
touch "$PID_FILE"

echo "=================================================================="
echo " Setting up Kafka Authentication (SASL_SSL + OAUTHBEARER) E2E Env"
echo "=================================================================="
echo "[INFO] Hook Service Directory : $HOOK_SERVICE_DIR"
echo "[INFO] E2E Docker Directory   : $E2E_DIR"
echo "[INFO] Certificates Directory : $CERTS_DIR"
echo "[INFO] Logs Directory         : $LOGS_DIR"
echo "=================================================================="

# Stop any lingering background processes from previous test runs
if [ -s "$PID_FILE" ]; then
    echo "[INFO] Stopping previous background processes..."
    while IFS=: read -r pid name || [ -n "$pid" ]; do
        [ -z "$pid" ] && continue
        if kill -0 "$pid" 2>/dev/null; then
            kill "$pid" 2>/dev/null || true
            sleep 0.5
            kill -9 "$pid" 2>/dev/null || true
        fi
    done < "$PID_FILE"
    truncate -s 0 "$PID_FILE"
fi

# 1. Generate TLS Certificates and PKCS12 Keystores
echo ""
echo "==== Step 1: Generating PKI & PKCS12 Keystores ===="
if [ ! -f "$CERTS_DIR/server.keystore.p12" ] || [ ! -f "$CERTS_DIR/ca.crt" ]; then
    echo "[INFO] Generating CA private key and certificate..."
    openssl req -new -x509 -keyout "$CERTS_DIR/ca.key" -out "$CERTS_DIR/ca.crt" -days 365 -nodes \
        -subj "/CN=Kafka-E2E-CA/O=Canonical" >/dev/null 2>&1

    echo "[INFO] Generating Kafka broker certificate and CSR..."
    openssl req -new -newkey rsa:2048 -nodes -keyout "$CERTS_DIR/server.key" -out "$CERTS_DIR/server.csr" \
        -subj "/CN=localhost/O=Canonical" >/dev/null 2>&1

    cat <<EOF > "$CERTS_DIR/extfile.cnf"
subjectAltName = DNS:localhost,DNS:kafka,DNS:kafka-auth-e2e-broker,IP:127.0.0.1
extendedKeyUsage = serverAuth,clientAuth
EOF

    openssl x509 -req -in "$CERTS_DIR/server.csr" -CA "$CERTS_DIR/ca.crt" -CAkey "$CERTS_DIR/ca.key" \
        -CAcreateserial -out "$CERTS_DIR/server.crt" -days 365 -extfile "$CERTS_DIR/extfile.cnf" >/dev/null 2>&1

    echo "[INFO] Exporting PKCS12 server keystore..."
    openssl pkcs12 -export -in "$CERTS_DIR/server.crt" -inkey "$CERTS_DIR/server.key" \
        -out "$CERTS_DIR/server.keystore.p12" -name localhost -passout pass:changeit >/dev/null 2>&1

    echo "[INFO] Exporting PKCS12 server truststore..."
    rm -f "$CERTS_DIR/server.truststore.p12"
    keytool -importcert -noprompt -alias ca -file "$CERTS_DIR/ca.crt" \
        -keystore "$CERTS_DIR/server.truststore.p12" -storepass changeit -storetype PKCS12 >/dev/null 2>&1

    chmod -R 777 "$CERTS_DIR"
    echo "  ✓ Generated CA, Broker Certificates, and PKCS12 Keystores"
else
    echo "  ✓ Existing PKI & Keystores detected in $CERTS_DIR"
fi

# 2. Start Docker Compose Stack
echo ""
echo "==== Step 2: Starting Docker Infrastructure ===="
docker compose -p kafka-auth-e2e -f "$COMPOSE_FILE" up -d

echo "[INFO] Waiting for PostgreSQL (5434)..."
for i in $(seq 1 30); do
    if docker exec kafka-auth-e2e-postgres pg_isready -U groups -d groups >/dev/null 2>&1; then
        echo "  ✓ PostgreSQL is healthy"
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "[ERROR] Timed out waiting for PostgreSQL container"
        docker logs kafka-auth-e2e-postgres --tail 50
        exit 1
    fi
    sleep 1
done

echo "[INFO] Waiting for Ory Hydra (4444)..."
for i in $(seq 1 30); do
    if curl -s http://localhost:4444/health/ready | grep -q 'ok'; then
        echo "  ✓ Ory Hydra is healthy"
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "[ERROR] Timed out waiting for Ory Hydra container"
        docker logs kafka-auth-e2e-hydra --tail 50
        exit 1
    fi
    sleep 1
done

echo "[INFO] Waiting for Kafka Broker (9092)..."
for i in $(seq 1 30); do
    if docker exec kafka-auth-e2e-broker /opt/kafka/bin/kafka-broker-api-versions.sh --bootstrap-server localhost:9092 >/dev/null 2>&1; then
        echo "  ✓ Kafka Broker is healthy"
        break
    fi
    if [ "$i" -eq 30 ]; then
        echo "[ERROR] Timed out waiting for Kafka Broker container"
        docker logs kafka-auth-e2e-broker --tail 50
        exit 1
    fi
    sleep 1
done

# 3. Register Ory Hydra OAuth2 Clients
echo ""
echo "==== Step 3: Registering Hydra OAuth2 Clients ===="

register_client() {
    local client_id="$1"
    local client_secret="$2"
    local audience="$3"

    # Delete if exists to make script idempotent
    curl -s -X DELETE "http://localhost:4445/admin/clients/$client_id" >/dev/null 2>&1 || true

    curl -s -X POST http://localhost:4445/admin/clients \
        -H "Content-Type: application/json" \
        -d "{
            \"client_id\": \"$client_id\",
            \"client_secret\": \"$client_secret\",
            \"grant_types\": [\"client_credentials\"],
            \"response_types\": [\"token\"],
            \"token_endpoint_auth_method\": \"client_secret_basic\",
            \"audience\": [\"$audience\"]
        }" >/dev/null
    echo "  ✓ Registered OAuth2 client: $client_id (Audience: $audience)"
}

register_client "hook-service" "hook-service-secret" "kafka"
register_client "unauthorized-client" "unauthorized-secret" "kafka"
register_client "wrong-audience-client" "wrong-audience-secret" "some-other-service"

# 4. Provision Kafka Topics & Access Control Lists (ACLs)
echo ""
echo "==== Step 4: Provisioning Kafka Topics & ACLs ===="
docker exec kafka-auth-e2e-broker /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server localhost:9092 \
    --create --if-not-exists \
    --topic permissions.hook-service \
    --partitions 1 --replication-factor 1 >/dev/null 2>&1
echo "  ✓ Kafka topic permissions.hook-service provisioned"

echo "[INFO] Applying Charmed Kafka ACL for User:hook-service..."
docker exec kafka-auth-e2e-broker /opt/kafka/bin/kafka-acls.sh \
    --bootstrap-server localhost:9092 \
    --add \
    --allow-principal User:hook-service \
    --operation Write --operation Describe \
    --topic permissions.hook-service >/dev/null 2>&1
echo "  ✓ ACL granted: User:hook-service -> [Write, Describe] on topic permissions.hook-service"

# 5. Database Multi-Tenant Migrations
echo ""
echo "==== Step 5: Applying Hook Service Database Migrations ===="
go run "$HOOK_SERVICE_DIR" migrate --dsn "postgresql://groups:groups@localhost:5434/groups?sslmode=disable" up >/dev/null
echo "  ✓ Database schema up-to-date"

# 6. Compile Hook Service Binary
echo ""
echo "==== Step 6: Compiling Hook Service Binary ===="
go build -o "$HOOK_SERVICE_DIR/app" "$HOOK_SERVICE_DIR"
echo "  ✓ Compiled Hook Service binary -> $HOOK_SERVICE_DIR/app"

# 7. Start Hook Service in Platform Mode (Background Daemon)
echo ""
echo "==== Step 7: Starting Hook Service in Platform Mode ===="
nohup setsid env PORT="8000" \
    DSN="postgresql://groups:groups@localhost:5434/groups?sslmode=disable" \
    AUTHENTICATION_ENABLED="false" \
    KAFKA_BROKERS="localhost:9096" \
    KAFKA_AUTH_TYPE="oauthbearer" \
    KAFKA_OAUTH_TOKEN_URL="http://localhost:4444/oauth2/token" \
    KAFKA_OAUTH_CLIENT_ID="hook-service" \
    KAFKA_OAUTH_CLIENT_SECRET="hook-service-secret" \
    KAFKA_OAUTH_AUDIENCE="kafka" \
    KAFKA_TLS_ENABLED="true" \
    KAFKA_TLS_CA_FILE="$CERTS_DIR/ca.crt" \
    FEDERATED_SERVICE_NAME="hook-service" \
    LOG_LEVEL="debug" \
    API_TOKEN="e2e-test-api-token" \
    "$HOOK_SERVICE_DIR/app" serve > "$LOGS_DIR/hook-service.log" 2>&1 &

HOOK_PID=$!
disown "$HOOK_PID" 2>/dev/null || true
echo "$HOOK_PID:hook-service" >> "$PID_FILE"
echo "[INFO] Hook Service process started (PID: $HOOK_PID)"

echo "[INFO] Waiting for Hook Service /api/v0/status endpoint..."
for _ in $(seq 1 30); do
    if curl -s http://localhost:8000/api/v0/status | grep -q '"status":"ok"'; then
        break
    fi
    sleep 0.5
done

if ! curl -s http://localhost:8000/api/v0/status | grep -q '"mode":"platform"'; then
    echo "[ERROR] Hook Service failed to report mode: platform"
    tail -n 30 "$LOGS_DIR/hook-service.log"
    exit 1
fi

echo "  ✓ Hook Service is healthy in Platform Mode (PID: $HOOK_PID)"
echo ""
echo "=================================================================="
echo " Kafka Authentication E2E Environment Setup Complete!"
echo "=================================================================="
