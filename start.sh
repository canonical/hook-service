#!/bin/bash

# set -x
set -e

cleanup () {
  docker compose -f ./docker-compose.dev.yml down > /dev/null
  docker stop oidc_client > /dev/null
  exit
}

trap "cleanup" INT EXIT

# Start build in background
make build &
BUILD_PID=$!

SCRIPT_DIR=$( cd -- "$( dirname -- "${BASH_SOURCE[0]}" )" &> /dev/null && pwd )

# Start dependencies
echo "Starting docker compose services..."
docker compose -f ./docker-compose.dev.yml up --wait --force-recreate --build --remove-orphans -d 2>&1 | grep -E "(Creating|Starting|Waiting|Error|error)" || true
echo "Docker compose services started successfully"

# Start client app
HYDRA_CONTAINER_ID=$(docker ps -aqf "name=hook-service-hydra-1")
HYDRA_IMAGE=ghcr.io/canonical/hydra:2.2.0-canonical

CLIENT_RESULT=$(docker exec "$HYDRA_CONTAINER_ID" \
  hydra create client \
    --endpoint http://127.0.0.1:4445 \
    --name "OIDC App" \
    --grant-type authorization_code,refresh_token,urn:ietf:params:oauth:grant-type:device_code \
    --response-type code \
    --format json \
    --scope openid,profile,offline_access,email \
    --redirect-uri http://127.0.0.1:4446/callback)

CLIENT_ID=$(echo "$CLIENT_RESULT" | cut -d '"' -f4)
CLIENT_SECRET=$(echo "$CLIENT_RESULT" | cut -d '"' -f12)

# Create a client credentials client for JWT authentication
AUTH_CLIENT_RESULT=$(docker exec "$HYDRA_CONTAINER_ID" \
  hydra create client \
    --endpoint http://127.0.0.1:4445 \
    --name "Hook Service Auth Client" \
    --grant-type client_credentials \
    --format json \
    --scope hook-service:admin)

AUTH_CLIENT_ID=$(echo "$AUTH_CLIENT_RESULT" | cut -d '"' -f4)
AUTH_CLIENT_SECRET=$(echo "$AUTH_CLIENT_RESULT" | cut -d '"' -f12)

docker stop oidc_client > /dev/null 2>&1  || true
docker rm oidc_client > /dev/null 2>&1  || true
docker run --network="host" -d --name=oidc_client --rm $HYDRA_IMAGE \
  exec hydra perform authorization-code \
  --endpoint http://localhost:4444 \
  --client-id $CLIENT_ID \
  --client-secret $CLIENT_SECRET \
  --scope openid,profile,email,offline_access \
  --no-open --no-shutdown --format json

echo "Waiting for build to complete..."
wait $BUILD_PID
echo "Build completed."

export PORT="8000"
export TRACING_ENABLED="false"
export LOG_LEVEL="debug"
export API_TOKEN="secret_api_key"
export SALESFORCE_ENABLED="true"
export OPENFGA_API_SCHEME="http"
export OPENFGA_API_HOST="127.0.0.1:8080"
export OPENFGA_API_TOKEN="42"
export OPENFGA_STORE_ID=$(fga store create --name hook-service | yq .store.id)
export OPENFGA_AUTHORIZATION_MODEL_ID=$(./app create-fga-model --fga-api-url http://127.0.0.1:8080 --fga-api-token $OPENFGA_API_TOKEN --fga-store-id $OPENFGA_STORE_ID --format json | yq .model_id)
export SALESFORCE_ENABLED="false"
export AUTHORIZATION_ENABLED="true"
export DSN="postgres://groups:groups@127.0.0.1:5432/groups"

# JWT Authentication configuration (enabled by default with Hydra)
export AUTHENTICATION_ENABLED="true"
export AUTHENTICATION_ISSUER="http://localhost:4444"
export AUTHENTICATION_ALLOWED_SUBJECTS="$AUTH_CLIENT_ID"
export AUTHENTICATION_REQUIRED_SCOPE="hook-service:admin"

echo "Running database migrations..."
./app migrate --dsn $DSN up

echo
echo "==============================================="
echo "Client ID: $CLIENT_ID"
echo "Auth Client ID: $AUTH_CLIENT_ID"
echo "Auth Client Secret: $AUTH_CLIENT_SECRET"
echo "Store ID: $OPENFGA_STORE_ID"
echo "Model ID: $OPENFGA_AUTHORIZATION_MODEL_ID"
echo "==============================================="
echo "To get a JWT token, run:"
echo "curl -X POST http://localhost:4444/oauth2/token \\"
echo "  -u \"$AUTH_CLIENT_ID:$AUTH_CLIENT_SECRET\" \\"
echo "  -d 'grant_type=client_credentials&scope=hook-service:admin'"
echo "==============================================="
echo

./app serve
