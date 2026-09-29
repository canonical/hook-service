// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"flag"
	"fmt"
	"net"
	"strings"
	"time"
)

// EnvSpec is the basic environment configuration setup needed for the app to start
type EnvSpec struct {
	OtelGRPCEndpoint string `envconfig:"otel_grpc_endpoint"`
	OtelHTTPEndpoint string `envconfig:"otel_http_endpoint"`
	TracingEnabled   bool   `envconfig:"tracing_enabled" default:"true"`

	LogLevel string `envconfig:"log_level" default:"error"`
	Debug    bool   `envconfig:"debug" default:"false"`

	Port int `envconfig:"port" default:"8080"`

	GRPCPort                 int    `envconfig:"grpc_port" default:"9090"`
	GRPCMaxConcurrentStreams uint32 `envconfig:"grpc_max_concurrent_streams" default:"100"`

	ApiToken string `envconfig:"api_token" default:""`

	OpenfgaApiScheme string `envconfig:"openfga_api_scheme" default:""`
	OpenfgaApiHost   string `envconfig:"openfga_api_host"`
	OpenfgaApiToken  string `envconfig:"openfga_api_token"`
	OpenfgaStoreId   string `envconfig:"openfga_store_id"`
	OpenfgaModelId   string `envconfig:"openfga_authorization_model_id" default:""`

	SalesforceDomain         string `envconfig:"salesforce_domain"`
	SalesforceConsumerKey    string `envconfig:"salesforce_consumer_key"`
	SalesforceConsumerSecret string `envconfig:"salesforce_consumer_secret"`

	AuthorizationEnabled bool `envconfig:"authorization_enabled" default:"false"`
	OpenFGAWorkersTotal  int  `envconfig:"openfga_workers_total" default:"150"`

	AuthenticationEnabled         bool   `envconfig:"authentication_enabled" default:"true"`
	AuthenticationIssuer          string `envconfig:"authentication_issuer"`
	AuthenticationJwksURL         string `envconfig:"authentication_jwks_url"`
	AuthenticationAllowedSubjects string `envconfig:"authentication_allowed_subjects"`
	AuthenticationRequiredScope   string `envconfig:"authentication_required_scope"`

	DSN string `envconfig:"DSN" required:"true"`

	DBMaxConns        int32         `envconfig:"db_max_conns" default:"25"`
	DBMinConns        int32         `envconfig:"db_min_conns" default:"2"`
	DBMaxConnLifetime time.Duration `envconfig:"db_max_conn_lifetime" default:"1h"`
	DBMaxConnIdleTime time.Duration `envconfig:"db_max_conn_idle_time" default:"30m"`

	TenantServiceGRPCAddress string        `envconfig:"tenant_service_grpc_address" default:""`
	TenantServiceGRPCTimeout time.Duration `envconfig:"tenant_service_grpc_timeout" default:"5s"`
	TenantServiceTLSEnabled  bool          `envconfig:"tenant_service_tls_enabled" default:"false"`

	ReplicaDSN                string        `envconfig:"replica_dsn" default:""`
	ReplicaDBMaxConns         int32         `envconfig:"replica_db_max_conns" default:"25"`
	ReplicaDBMinConns         int32         `envconfig:"replica_db_min_conns" default:"2"`
	ReplicaDBMaxConnLifetime  time.Duration `envconfig:"replica_db_max_conn_lifetime" default:"1h"`
	ReplicaDBMaxConnIdleTime  time.Duration `envconfig:"replica_db_max_conn_idle_time" default:"30m"`
	MaxReplicaLagMs           int64         `envconfig:"max_replica_lag_ms" default:"1000"`
	ReplicaPoolSizeMultiplier float64       `envconfig:"replica_pool_size_multiplier" default:"1.0"`
	StreamTimeout             time.Duration `envconfig:"stream_timeout" default:"30s"`
	KafkaBrokers              []string      `envconfig:"kafka_brokers" default:""`

	FederatedServiceName string `envconfig:"federated_service_name" default:"hook-service"`

	HookMaxConcurrent int `envconfig:"hook_max_concurrent" default:"150"`
}

// DefaultFederatedServiceName is the federated service name used when none is configured.
const DefaultFederatedServiceName = "hook-service"

// MaxTopicNameLength is the maximum length Kafka permits for a topic name.
const MaxTopicNameLength = 249

// DeploymentMode names the behaviour selected at startup. It is derived solely from
// whether message broker addresses are configured.
type DeploymentMode string

// Deployment modes reported to operators.
const (
	ModePlatform   DeploymentMode = "platform"
	ModeStandalone DeploymentMode = "standalone"
)

// PermissionsTopic returns the topic a federated service publishes permission updates to.
func PermissionsTopic(federatedServiceName string) string {
	return federatedServiceName + ".permissions"
}

// ValidateFederatedServiceName checks that the federated service name is non-empty and
// yields a usable topic name.
func ValidateFederatedServiceName(name string) error {
	if strings.TrimSpace(name) == "" {
		return fmt.Errorf("invalid federated service name %q: must not be empty", name)
	}
	if strings.ContainsFunc(name, isNotTopicNameRune) {
		return fmt.Errorf("invalid federated service name %q: must contain only letters, digits, dots, underscores, or hyphens", name)
	}
	if topic := PermissionsTopic(name); len(topic) > MaxTopicNameLength {
		return fmt.Errorf("invalid federated service name %q: derived topic is %d characters, exceeding the %d character limit", name, len(topic), MaxTopicNameLength)
	}
	return nil
}

// isNotTopicNameRune reports whether r is disallowed in a topic name.
func isNotTopicNameRune(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return false
	case r == '.', r == '_', r == '-':
		return false
	default:
		return true
	}
}

// ValidateKafkaBrokers checks that all non-empty broker addresses have valid host:port format.
func ValidateKafkaBrokers(brokers []string) error {
	for _, b := range brokers {
		trimmed := strings.TrimSpace(b)
		if trimmed == "" {
			continue
		}
		host, port, err := net.SplitHostPort(trimmed)
		if err != nil || host == "" || port == "" {
			return fmt.Errorf("invalid kafka broker address %q: %v", b, err)
		}
	}
	return nil
}

type Flags struct {
	ShowVersion bool
}

func NewFlags() *Flags {
	f := new(Flags)

	flag.BoolVar(&f.ShowVersion, "version", false, "Show the app version and exit")
	flag.Parse()

	return f
}
