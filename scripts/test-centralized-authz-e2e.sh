#!/usr/bin/env bash
# Copyright 2026 Canonical Ltd.
# SPDX-License-Identifier: AGPL-3.0-only

set -euo pipefail

# Configuration
SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
HOOK_SERVICE_DIR="$(realpath "$SCRIPT_DIR/..")"
ENVOY_URL="${ENVOY_URL:-http://localhost:10000}"
FGA_URL="${OPENFGA_URL:-http://localhost:8082}"
STORE_ID="${OPENFGA_STORE_ID:-01GP1254CHWJC1MNGVB0WDG1T0}"
STS_DIR="${STS_DIR:-$(realpath "$HOOK_SERVICE_DIR/../secure-token-service")}"

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

assert_status() {
  local method="$1"
  local path="$2"
  local cookie="$3"
  local body="$4"
  local expected="$5"
  local desc="$6"
  local extra_header="${7:-}"

  local cmd=(curl -s -o /dev/null -w "%{http_code}" -X "$method" "${ENVOY_URL}${path}")
  if [ -n "$cookie" ]; then
    cmd+=(-H "Cookie: session_id=$cookie")
  fi
  if [ -n "$extra_header" ]; then
    cmd+=(-H "$extra_header")
  fi
  if [ -n "$body" ]; then
    cmd+=(-H "Content-Type: application/json" -d "$body")
  fi

  local actual
  actual=$("${cmd[@]}")

  if [ "$actual" = "$expected" ]; then
    log_pass "$desc [HTTP $actual]"
  else
    log_fail "$desc" "$expected" "$actual"
  fi
}

echo "=================================================================="
echo " Hook Service Admin API - Centralized Authz Permission Test Suite "
echo "=================================================================="
log_info "Target Gateway (Envoy) : $ENVOY_URL"
log_info "OpenFGA Server         : $FGA_URL"
log_info "OpenFGA Store ID       : $STORE_ID"

log_step "Step 0: Minting Session Cookies via Secure Token Service"

mint_cookie() {
  local user="$1"
  local cookie
  cookie=$(CACHE_ADDR="localhost:6380" \
    COOKIE_HASH_KEY="0123456789012345678901234567890123456789012345678901234567890123" \
    OIDC_PROVIDER_URL="http://localhost:8888" \
    OIDC_CLIENT_ID="dummy" \
    OIDC_CLIENT_SECRET="dummy" \
    OIDC_REDIRECT_URL="http://localhost:8080/callback" \
    "$STS_DIR/bin/app" create-cookie --user-id "$user" 2>/dev/null | grep -E "^Cookie:" | awk '{print $2}')
  if [ -z "$cookie" ]; then
    echo "[ERROR] Failed to mint session cookie for $user. Ensure STS binary exists at $STS_DIR/bin/app and Valkey is running on port 6380." >&2
    exit 1
  fi
  echo "$cookie"
}

COOKIE_ADMIN=$(mint_cookie "domain-admin@canonical.com")
COOKIE_ALICE=$(mint_cookie "alice@canonical.com")
COOKIE_BOB=$(mint_cookie "bob@canonical.com")
COOKIE_CHARLIE=$(mint_cookie "charlie@canonical.com")
COOKIE_MALLORY=$(mint_cookie "mallory@canonical.com")

log_info "Minted cookies for: domain-admin, alice, bob, charlie, mallory"

# Seed Domain Admin in OpenFGA if missing
curl -s -X POST "$FGA_URL/stores/$STORE_ID/write" \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer 42" \
  -d '{
    "writes": {
      "tuple_keys": [
        {
          "user": "user:domain-admin@canonical.com",
          "relation": "assignee",
          "object": "role:admin",
          "condition": {
            "name": "tenant_match",
            "context": {"tenant": "hook-service"}
          }
        }
      ]
    }
  }' > /dev/null

log_step "Phase 1: Edge Perimeter & Authentication Defense"
assert_status "GET" "/api/v0/authz/groups" "" "" "401" "TC-AUTH-01: Missing session cookie rejected at edge"
assert_status "GET" "/api/v0/authz/groups" "tampered_corrupted_cookie_xyz" "" "403" "TC-AUTH-02: Corrupted session cookie rejected by STS"
assert_status "GET" "/api/v0/authz/groups/00000000-0000-0000-0000-000000000000" "$COOKIE_MALLORY" "" "403" "TC-AUTH-03: Non-existent resource blocked by OpenFGA PDP"
assert_status "POST" "/api/v0/authz/groups" "$COOKIE_MALLORY" '{"name":"spoofed-group"}' "403" "TC-AUTH-04: Custom header spoofing (X-User-Id) rejected - evaluated as Mallory" "X-User-Id: domain-admin@canonical.com"
assert_status "GET" "/api/v0/authz/groups" "" "" "401" "TC-AUTH-05: Unauthenticated request with X-User-Id rejected at edge" "X-User-Id: domain-admin@canonical.com"
assert_status "GET" "/api/v0/authz/groups" "" "" "401" "TC-AUTH-06: Direct forged Bearer token without session rejected at edge" "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.e30.t-IDcSemACt8x4iTMCda8Yhe3iZaWbvV5XKSTbuAn0M"
assert_status "POST" "/api/v0/authz/groups" "$COOKIE_MALLORY" '{"name":"spoofed-bearer"}' "403" "TC-AUTH-07: Injected Bearer token cannot elevate privileges" "Authorization: Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.fake-admin.signature"

log_step "Phase 2: Coarse-Grained RBAC & Endpoint Enforcement"
assert_status "GET" "/api/v0/authz/groups" "$COOKIE_ADMIN" "" "200" "TC-RBAC-01: Domain Admin lists all groups"
assert_status "GET" "/api/v0/authz/groups" "$COOKIE_ALICE" "" "403" "TC-RBAC-02: Alice denied from listing all groups"
assert_status "GET" "/api/v0/authz/groups" "$COOKIE_BOB" "" "403" "TC-RBAC-03: Bob denied from listing all groups"
assert_status "GET" "/api/v0/authz/groups" "$COOKIE_MALLORY" "" "403" "TC-RBAC-04: Mallory denied from listing all groups"

assert_status "POST" "/api/v0/authz/groups" "$COOKIE_ALICE" '{"name":"illegal-alice"}' "403" "TC-RBAC-05: Alice denied from creating groups"
assert_status "POST" "/api/v0/authz/groups" "$COOKIE_MALLORY" '{"name":"illegal-mallory"}' "403" "TC-RBAC-06: Mallory denied from creating groups"

log_step "Phase 3: Administrative Group Lifecycle & CRUD Operations"
# Create Group Alpha as Admin
ALPHA_RESP=$(curl -s -X POST "${ENVOY_URL}/api/v0/authz/groups" \
  -H "Content-Type: application/json" \
  -H "Cookie: session_id=$COOKIE_ADMIN" \
  -d '{"name":"test-group-alpha", "description":"Group Alpha for E2E"}')
ALPHA_ID=$(echo "$ALPHA_RESP" | jq -r '.data[0].id // empty')
[ -n "$ALPHA_ID" ] || { echo "Failed to create Group Alpha"; exit 1; }
log_info "Created Group Alpha (ID: $ALPHA_ID)"

# Create Group Beta as Admin
BETA_RESP=$(curl -s -X POST "${ENVOY_URL}/api/v0/authz/groups" \
  -H "Content-Type: application/json" \
  -H "Cookie: session_id=$COOKIE_ADMIN" \
  -d '{"name":"test-group-beta", "description":"Group Beta for E2E"}')
BETA_ID=$(echo "$BETA_RESP" | jq -r '.data[0].id // empty')
[ -n "$BETA_ID" ] || { echo "Failed to create Group Beta"; exit 1; }
log_info "Created Group Beta (ID: $BETA_ID)"

assert_status "GET" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_ADMIN" "" "200" "TC-GRP-01: Admin reads Group Alpha"
assert_status "GET" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_ALICE" "" "403" "TC-GRP-02: Alice denied read of Group Alpha"
assert_status "GET" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_BOB" "" "403" "TC-GRP-03: Bob denied read of Group Alpha"
assert_status "GET" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_MALLORY" "" "403" "TC-GRP-04: Mallory denied read of Group Alpha"

assert_status "GET" "/api/v0/authz/groups/$BETA_ID" "$COOKIE_ADMIN" "" "200" "TC-GRP-05: Admin reads Group Beta"
assert_status "GET" "/api/v0/authz/groups/$BETA_ID" "$COOKIE_CHARLIE" "" "403" "TC-GRP-06: Charlie denied read of Group Beta"

assert_status "PUT" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_ADMIN" '{"description":"Updated Alpha","type":"local"}' "200" "TC-GRP-07: Admin updates Group Alpha"
assert_status "PUT" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_ALICE" '{"description":"Updated by Alice","type":"local"}' "403" "TC-GRP-08: Alice denied update of Group Alpha"
assert_status "PUT" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_BOB" '{"description":"Updated by Bob","type":"local"}' "403" "TC-GRP-09: Bob denied update of Group Alpha"
assert_status "PUT" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_MALLORY" '{"description":"Updated by Mallory","type":"local"}' "403" "TC-GRP-10: Mallory denied update of Group Alpha"

log_step "Phase 4: Administrative Membership Management"
assert_status "GET" "/api/v0/authz/groups/$ALPHA_ID/users" "$COOKIE_ADMIN" "" "200" "TC-MEM-01: Admin lists users in Alpha"
assert_status "GET" "/api/v0/authz/groups/$ALPHA_ID/users" "$COOKIE_ALICE" "" "403" "TC-MEM-02: Alice denied user list in Alpha"
assert_status "GET" "/api/v0/authz/groups/$ALPHA_ID/users" "$COOKIE_BOB" "" "403" "TC-MEM-03: Bob denied user list in Alpha"
assert_status "GET" "/api/v0/authz/groups/$ALPHA_ID/users" "$COOKIE_MALLORY" "" "403" "TC-MEM-04: Mallory denied user list in Alpha"

assert_status "POST" "/api/v0/authz/groups/$ALPHA_ID/users" "$COOKIE_ALICE" '["dave@canonical.com"]' "403" "TC-MEM-05: Alice blocked from adding user"
assert_status "POST" "/api/v0/authz/groups/$ALPHA_ID/users" "$COOKIE_BOB" '["dave@canonical.com"]' "403" "TC-MEM-06: Bob blocked from adding user"
assert_status "POST" "/api/v0/authz/groups/$ALPHA_ID/users" "$COOKIE_MALLORY" '["dave@canonical.com"]' "403" "TC-MEM-07: Mallory blocked from adding user"
assert_status "POST" "/api/v0/authz/groups/$ALPHA_ID/users" "$COOKIE_ADMIN" '["bob@canonical.com","dave@canonical.com"]' "200" "TC-MEM-08: Admin adds Bob and Dave to Alpha"

# Idempotent member addition
assert_status "POST" "/api/v0/authz/groups/$ALPHA_ID/users" "$COOKIE_ADMIN" '["bob@canonical.com"]' "200" "TC-MEM-09: Idempotent member addition (duplicate member returns 200)"

assert_status "DELETE" "/api/v0/authz/groups/$ALPHA_ID/users/dave@canonical.com" "$COOKIE_ALICE" "" "403" "TC-MEM-10: Alice blocked from removing Dave"
assert_status "DELETE" "/api/v0/authz/groups/$ALPHA_ID/users/dave@canonical.com" "$COOKIE_BOB" "" "403" "TC-MEM-11: Bob blocked from removing Dave"
assert_status "DELETE" "/api/v0/authz/groups/$ALPHA_ID/users/dave@canonical.com" "$COOKIE_MALLORY" "" "403" "TC-MEM-12: Mallory blocked from removing Dave"
assert_status "DELETE" "/api/v0/authz/groups/$ALPHA_ID/users/dave@canonical.com" "$COOKIE_ADMIN" "" "200" "TC-MEM-13: Admin removes Dave from Alpha"

# Idempotent member deletion
assert_status "DELETE" "/api/v0/authz/groups/$ALPHA_ID/users/nonexistent-user@canonical.com" "$COOKIE_ADMIN" "" "200" "TC-MEM-14: Idempotent member deletion (non-existent user returns 200)"

log_step "Phase 5: Multi-Tenant ABAC Isolation Check"
TENANT_MATCH_CHECK=$(curl -s -X POST "$FGA_URL/stores/$STORE_ID/check" \
  -H "Content-Type: application/json" -H "Authorization: Bearer 42" \
  -d "{\"tuple_key\": {\"user\": \"user:domain-admin@canonical.com\", \"relation\": \"assignee\", \"object\": \"role:admin\"}, \"context\": {\"tenant_enabled\": true, \"user_tenant\": \"hook-service\"}}" \
  | jq -r .allowed)

if [ "$TENANT_MATCH_CHECK" = "true" ]; then
  log_pass "TC-TENANT-01: OpenFGA check succeeds with matching tenant context [hook-service]"
else
  log_fail "TC-TENANT-01: OpenFGA check with matching tenant" "true" "$TENANT_MATCH_CHECK"
fi

TENANT_MISMATCH_CHECK=$(curl -s -X POST "$FGA_URL/stores/$STORE_ID/check" \
  -H "Content-Type: application/json" -H "Authorization: Bearer 42" \
  -d "{\"tuple_key\": {\"user\": \"user:domain-admin@canonical.com\", \"relation\": \"assignee\", \"object\": \"role:admin\"}, \"context\": {\"tenant_enabled\": true, \"user_tenant\": \"alien-tenant\"}}" \
  | jq -r .allowed)

if [ "$TENANT_MISMATCH_CHECK" = "false" ]; then
  log_pass "TC-TENANT-02: OpenFGA check denied with mismatched tenant context [alien-tenant]"
else
  log_fail "TC-TENANT-02: OpenFGA check with mismatched tenant" "false" "$TENANT_MISMATCH_CHECK"
fi

log_step "Phase 6: Session Eviction & Revocation Defense"
EPHEMERAL_USER="ephemeral-test@canonical.com"
EPHEMERAL_OUTPUT=$(CACHE_ADDR="localhost:6380" \
  COOKIE_HASH_KEY="0123456789012345678901234567890123456789012345678901234567890123" \
  OIDC_PROVIDER_URL="http://localhost:8888" \
  OIDC_CLIENT_ID="dummy" \
  OIDC_CLIENT_SECRET="dummy" \
  OIDC_REDIRECT_URL="http://localhost:8080/callback" \
  "$STS_DIR/bin/app" create-cookie --user-id "$EPHEMERAL_USER" 2>/dev/null)
EPHEMERAL_COOKIE=$(echo "$EPHEMERAL_OUTPUT" | grep -E "^Cookie:" | awk '{print $2}')
EPHEMERAL_SESS=$(echo "$EPHEMERAL_OUTPUT" | grep -E "^Session ID:" | awk '{print $3}')

# Evict session from Valkey cache
docker exec authz-valkey valkey-cli del "session:$EPHEMERAL_SESS" > /dev/null

assert_status "GET" "/api/v0/authz/groups" "$EPHEMERAL_COOKIE" "" "403" "TC-SESS-01: Evicted session rejected by edge auth PDP"

log_step "Phase 7: Global User Group Queries"
assert_status "GET" "/api/v0/authz/users/alice@canonical.com/groups" "$COOKIE_ADMIN" "" "200" "TC-USR-01: Admin queries user groups"
assert_status "GET" "/api/v0/authz/users/alice@canonical.com/groups" "$COOKIE_ALICE" "" "403" "TC-USR-02: Alice denied from querying user groups"
assert_status "GET" "/api/v0/authz/users/alice@canonical.com/groups" "$COOKIE_MALLORY" "" "403" "TC-USR-03: Mallory denied from querying user groups"

log_step "Phase 8: Group Deletion Security & Cleanup"
assert_status "DELETE" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_ALICE" "" "403" "TC-DEL-01: Alice denied group deletion"
assert_status "DELETE" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_BOB" "" "403" "TC-DEL-02: Bob denied group deletion"
assert_status "DELETE" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_CHARLIE" "" "403" "TC-DEL-03: Charlie denied group deletion"
assert_status "DELETE" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_MALLORY" "" "403" "TC-DEL-04: Mallory denied group deletion"

# Clean up Alpha and Beta as Domain Admin
assert_status "DELETE" "/api/v0/authz/groups/$ALPHA_ID" "$COOKIE_ADMIN" "" "200" "TC-DEL-05: Domain Admin deletes Group Alpha"
assert_status "DELETE" "/api/v0/authz/groups/$BETA_ID" "$COOKIE_ADMIN" "" "200" "TC-DEL-06: Domain Admin deletes Group Beta"

# Clean up domain admin tuple in OpenFGA
curl -s -X POST "$FGA_URL/stores/$STORE_ID/write" \
  -H "Content-Type: application/json" -H "Authorization: Bearer 42" \
  -d '{
    "deletes": {
      "tuple_keys": [
        {"user": "user:domain-admin@canonical.com", "relation": "assignee", "object": "role:admin"}
      ]
    }
  }' > /dev/null


echo ""
echo "=================================================================="
echo "                     E2E EXECUTION SUMMARY                        "
echo "=================================================================="
echo -e "Total Tests   : ${TOTAL_COUNT}"
echo -e "Passed Tests  : ${GREEN}${PASSED_COUNT}${NC}"
echo -e "Failed Tests  : ${RED}${FAILED_COUNT}${NC}"
echo "=================================================================="

if [ "$FAILED_COUNT" -gt 0 ]; then
  echo -e "${RED}SUITE FAILED${NC}"
  exit 1
else
  echo -e "${GREEN}ALL SUITE TESTS PASSED!${NC}"
fi
