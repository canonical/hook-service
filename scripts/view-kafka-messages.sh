#!/usr/bin/env bash
# Copyright 2026 Canonical Ltd.
# SPDX-License-Identifier: AGPL-3.0-only

# view-kafka-messages.sh
# Convenience wrapper around view-kafka-messages.go to visualize and inspect
# Protobuf messages published to Kafka in local/E2E environments.

set -euo pipefail

SCRIPT_DIR="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
HOOK_SERVICE_DIR="$(realpath "$SCRIPT_DIR/..")"

show_help() {
    cat <<EOF
Usage: ./scripts/view-kafka-messages.sh [OPTIONS]

Options:
  -topic <name>      Kafka topic to consume (default: "permissions.hook-service")
  -broker <address>  Kafka broker address (default: "localhost:9092" or \$KAFKA_BROKERS)
  -follow            Keep listening and tail new incoming messages
  -limit <n>         Stop after consuming N messages
  -json-only         Print only raw JSON objects (ideal for jq piping)
  -timeout <dur>     Poll timeout when not in follow mode (default: 3s)
  -tls               Enable TLS encryption (default: auto if \$KAFKA_TLS_ENABLED=true)
  -tls-ca <file>     Path to CA certificate file (default: \$KAFKA_TLS_CA_FILE)
  -h, --help         Show this help message

Examples:
  # Dump all existing messages on default topic:
  ./scripts/view-kafka-messages.sh

  # Follow/tail messages live during test runs:
  ./scripts/view-kafka-messages.sh -follow

  # Filter JSON payloads using jq:
  ./scripts/view-kafka-messages.sh -json-only | jq .
EOF
}

for arg in "$@"; do
    if [ "$arg" == "-h" ] || [ "$arg" == "--help" ]; then
        show_help
        exit 0
    fi
done

exec go run "$SCRIPT_DIR/view-kafka-messages.go" "$@"
