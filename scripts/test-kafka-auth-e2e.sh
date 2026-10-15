#!/usr/bin/env bash
# Copyright 2026 Canonical Ltd.
# SPDX-License-Identifier: AGPL-3.0-only

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
HOOK_SERVICE_DIR="$(realpath "$SCRIPT_DIR/..")"
CERTS_DIR="$HOOK_SERVICE_DIR/docker/kafka-auth-e2e/certs"
CA_FILE="$CERTS_DIR/ca.crt"

HYDRA_PUBLIC="http://localhost:4444"
HYDRA_ADMIN="http://localhost:4445"
KAFKA_SASL_SSL="localhost:9096"
KAFKA_PLAINTEXT="localhost:9092"
HOOK_SERVICE_URL="http://localhost:8000"

# Colors
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
NC='\033[0m' # No Color

PASSED_COUNT=0
FAILED_COUNT=0
TOTAL_COUNT=0

log_info() { echo -e "${CYAN}[INFO]${NC} $1"; }
log_step() { echo -e "\n${BLUE}==== $1 ====${NC}"; }
log_pass() { echo -e "  ${GREEN}✓ PASS:${NC} $1"; PASSED_COUNT=$((PASSED_COUNT + 1)); TOTAL_COUNT=$((TOTAL_COUNT + 1)); }
log_fail() { echo -e "  ${RED}✗ FAIL:${NC} $1 (Expected $2, got $3)"; FAILED_COUNT=$((FAILED_COUNT + 1)); TOTAL_COUNT=$((TOTAL_COUNT + 1)); }

echo "=================================================================="
echo " Kafka Authentication (SASL_SSL + OAUTHBEARER) E2E Test Suite     "
echo "=================================================================="
log_info "Kafka SASL_SSL Broker   : $KAFKA_SASL_SSL"
log_info "Ory Hydra Endpoint      : $HYDRA_PUBLIC"
log_info "Broker CA Certificate   : $CA_FILE"
log_info "Hook Service Platform   : $HOOK_SERVICE_URL"

# Step 1: Hydra OAuth2 Token Issuance Tests
log_step "Step 1: Ory Hydra OAuth2 Token Endpoint Verification"

# TC-1.1: Valid client credentials for hook-service
HTTP_CODE=$(curl -s -o /tmp/token-hook.json -w "%{http_code}" -X POST "$HYDRA_PUBLIC/oauth2/token" \
    -u "hook-service:hook-service-secret" \
    -d "grant_type=client_credentials&audience=kafka")

if [ "$HTTP_CODE" = "200" ] && grep -q '"access_token"' /tmp/token-hook.json; then
    log_pass "Hydra Token Issuance: hook-service (aud=kafka) [HTTP 200]"
else
    log_fail "Hydra Token Issuance: hook-service" "HTTP 200 with access_token" "HTTP $HTTP_CODE"
fi

# TC-1.2: Valid client credentials for unauthorized-client
HTTP_CODE=$(curl -s -o /tmp/token-unauth.json -w "%{http_code}" -X POST "$HYDRA_PUBLIC/oauth2/token" \
    -u "unauthorized-client:unauthorized-secret" \
    -d "grant_type=client_credentials&audience=kafka")

if [ "$HTTP_CODE" = "200" ] && grep -q '"access_token"' /tmp/token-unauth.json; then
    log_pass "Hydra Token Issuance: unauthorized-client (aud=kafka) [HTTP 200]"
else
    log_fail "Hydra Token Issuance: unauthorized-client" "HTTP 200 with access_token" "HTTP $HTTP_CODE"
fi

# TC-1.3: Valid client credentials with mismatched audience
HTTP_CODE=$(curl -s -o /tmp/token-wrong-aud.json -w "%{http_code}" -X POST "$HYDRA_PUBLIC/oauth2/token" \
    -u "wrong-audience-client:wrong-audience-secret" \
    -d "grant_type=client_credentials&audience=some-other-service")

if [ "$HTTP_CODE" = "200" ] && grep -q '"access_token"' /tmp/token-wrong-aud.json; then
    log_pass "Hydra Token Issuance: wrong-audience-client (aud=some-other-service) [HTTP 200]"
else
    log_fail "Hydra Token Issuance: wrong-audience-client" "HTTP 200 with access_token" "HTTP $HTTP_CODE"
fi

# TC-1.4: Invalid secret rejection
HTTP_CODE=$(curl -s -o /dev/null -w "%{http_code}" -X POST "$HYDRA_PUBLIC/oauth2/token" \
    -u "hook-service:bad-secret" \
    -d "grant_type=client_credentials&audience=kafka")

if [ "$HTTP_CODE" = "401" ]; then
    log_pass "Hydra Rejection: Invalid client secret [HTTP 401]"
else
    log_fail "Hydra Rejection: Invalid client secret" "HTTP 401" "HTTP $HTTP_CODE"
fi

# Step 2: Kafka ACL Audit
log_step "Step 2: Charmed Kafka ACL & Security State Audit"

ACL_OUTPUT=$(docker exec kafka-auth-e2e-broker /opt/kafka/bin/kafka-acls.sh \
    --bootstrap-server localhost:9092 \
    --list --topic permissions.hook-service 2>&1)

if echo "$ACL_OUTPUT" | grep -q 'User:hook-service' && \
   echo "$ACL_OUTPUT" | grep -qi 'operation=WRITE' && \
   echo "$ACL_OUTPUT" | grep -qi 'operation=DESCRIBE'; then
    log_pass "Kafka ACL Check: User:hook-service granted Write+Describe on topic permissions.hook-service"
else
    log_fail "Kafka ACL Check: User:hook-service" "Write+Describe permission listed" "$ACL_OUTPUT"
fi

if ! echo "$ACL_OUTPUT" | grep -q 'User:unauthorized-client'; then
    log_pass "Kafka ACL Check: User:unauthorized-client has NO ACL grants (Zero-Trust Enforced)"
else
    log_fail "Kafka ACL Check: Zero-Trust" "No grants for unauthorized-client" "Found grants in ACL list"
fi

# Step 3: Go End-to-End Security Verification Matrix
log_step "Step 3: Executing Go E2E Kafka Authentication Test Suite"

TEST_OUTPUT=$(env KAFKA_AUTH_E2E="1" \
    KAFKA_BROKERS="$KAFKA_SASL_SSL" \
    KAFKA_TLS_CA_FILE="$CA_FILE" \
    KAFKA_OAUTH_TOKEN_URL="$HYDRA_PUBLIC/oauth2/token" \
    go test -count=1 -v "$HOOK_SERVICE_DIR/tests/e2e" -run TestE2E_KafkaAuth 2>&1) || true

report_go_test() {
    local test_name="$1"
    local desc="$2"
    if echo "$TEST_OUTPUT" | grep -F -- "--- PASS: ${test_name} " >/dev/null || echo "$TEST_OUTPUT" | grep -F -- "--- PASS: ${test_name}(" >/dev/null; then
        log_pass "$desc"
    else
        log_fail "$desc" "PASS" "FAIL (Output logged below)"
        echo "$TEST_OUTPUT" | grep -A 10 -B 2 -F -- "--- FAIL: ${test_name}" || true
    fi
}

report_go_test "TestE2E_KafkaAuth_Positive_Publish" \
    "E2E Go: SASL_SSL + OAUTHBEARER Publish with hook-service credentials"

report_go_test "TestE2E_KafkaAuth_Batch_Envelope" \
    "E2E Go: Batch operations published within unified envelope"

report_go_test "TestE2E_KafkaAuth_TokenCaching_And_Reuse" \
    "E2E Go: In-memory token caching and connection reuse"

report_go_test "TestE2E_KafkaAuth_Negative_Untrusted_TLS_CA" \
    "E2E Go Negative: Untrusted CA certificate rejected during TLS handshake"

report_go_test "TestE2E_KafkaAuth_Negative_Invalid_OAuth_Client_Secret" \
    "E2E Go Negative: Invalid OAuth client secret rejected with HTTP 401"

report_go_test "TestE2E_KafkaAuth_Negative_Wrong_Audience" \
    "E2E Go Negative: Wrong audience claim rejected during SASL OAUTHBEARER handshake"

report_go_test "TestE2E_KafkaAuth_Negative_Unauthorized_Topic_ACL" \
    "E2E Go Negative: Missing topic ACL rejected by Kafka StandardAuthorizer [Error 29]"

# Step 4: Verification of Data Persistence on Kafka Broker
log_step "Step 4: Kafka Broker Partition & Message Verification"

OFFSET_OUTPUT=$(docker exec kafka-auth-e2e-broker /opt/kafka/bin/kafka-get-offsets.sh \
    --bootstrap-server localhost:9092 \
    --topic permissions.hook-service 2>&1)

OFFSET=$(echo "$OFFSET_OUTPUT" | awk -F: '{print $3}' | tr -d ' \r\n')
if [ -n "$OFFSET" ] && [ "$OFFSET" -gt 0 ]; then
    log_pass "Kafka Storage Verification: Topic permissions.hook-service contains $OFFSET committed messages"
else
    log_fail "Kafka Storage Verification: Committed messages" ">0" "$OFFSET_OUTPUT"
fi

# Step 5: Hook Service Platform Mode Status & Health
log_step "Step 5: Hook Service Platform Mode Daemon Verification"

STATUS_RESP=$(curl -s "$HOOK_SERVICE_URL/api/v0/status")
if echo "$STATUS_RESP" | grep -q '"status":"ok"' && echo "$STATUS_RESP" | grep -q '"mode":"platform"'; then
    log_pass "Hook Service Status: Reporting mode=platform and status=ok [HTTP 200]"
else
    log_fail "Hook Service Status" "status: ok, mode: platform" "$STATUS_RESP"
fi

METRICS_CODE=$(curl -s -o /dev/null -w "%{http_code}" "$HOOK_SERVICE_URL/api/v0/metrics")
if [ "$METRICS_CODE" = "200" ]; then
    log_pass "Hook Service Observability: Prometheus /api/v0/metrics endpoint active [HTTP 200]"
else
    log_fail "Hook Service Observability: Prometheus /api/v0/metrics" "HTTP 200" "HTTP $METRICS_CODE"
fi

# Summary
echo ""
echo "=================================================================="
echo " Test Suite Execution Summary"
echo "=================================================================="
echo -e "  Total Tests Executed : ${BLUE}$TOTAL_COUNT${NC}"
echo -e "  Passed               : ${GREEN}$PASSED_COUNT${NC}"
if [ "$FAILED_COUNT" -gt 0 ]; then
    echo -e "  Failed               : ${RED}$FAILED_COUNT${NC}"
    echo "=================================================================="
    exit 1
else
    echo -e "  Failed               : 0"
    echo "=================================================================="
    echo -e "  ${GREEN}All tests passed successfully!${NC}"
    echo "=================================================================="
fi
