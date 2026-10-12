// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"flag"
	"fmt"
	"net"
	"net/url"
	"strings"
	"time"
)

// Kafka authentication types and configuration defaults.
const (
	KafkaAuthTypeNone        = "none"
	KafkaAuthTypeOAuthBearer = "oauthbearer"

	DefaultKafkaAuthType      = KafkaAuthTypeNone
	DefaultKafkaOAuthAudience = "kafka"
)

// Kafka configuration sentinel errors.
var (
	ErrMissingOAuthTokenURL     = errors.New("missing kafka oauth token url")
	ErrMissingOAuthClientID     = errors.New("missing kafka oauth client id")
	ErrMissingOAuthClientSecret = errors.New("missing kafka oauth client secret")
	ErrUnsupportedKafkaAuthType = errors.New("unsupported kafka auth type")
)

// KafkaSecurityError represents a validation error on a specific Kafka configuration field.
type KafkaSecurityError struct {
	Field string
	Err   error
}

// Error returns a string representation of the validation error.
func (e *KafkaSecurityError) Error() string {
	return fmt.Sprintf("invalid kafka security config for %s: %v", e.Field, e.Err)
}

// Unwrap returns the underlying error.
func (e *KafkaSecurityError) Unwrap() error {
	return e.Err
}

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

	KafkaAuthType              string   `envconfig:"kafka_auth_type" default:"none"`
	KafkaOAuthTokenURL         string   `envconfig:"kafka_oauth_token_url" default:""`
	KafkaOAuthClientID         string   `envconfig:"kafka_oauth_client_id" default:""`
	KafkaOAuthClientSecret     string   `envconfig:"kafka_oauth_client_secret" default:""`
	KafkaOAuthScopes           []string `envconfig:"kafka_oauth_scopes" default:""`
	KafkaOAuthAudience         string   `envconfig:"kafka_oauth_audience" default:"kafka"`
	KafkaOAuthCAFile           string   `envconfig:"kafka_oauth_ca_file" default:""`
	KafkaTLSEnabled            bool     `envconfig:"kafka_tls_enabled" default:"false"`
	KafkaTLSCAFile             string   `envconfig:"kafka_tls_ca_file" default:""`
	KafkaTLSInsecureSkipVerify bool     `envconfig:"kafka_tls_insecure_skip_verify" default:"false"`

	FederatedServiceName string `envconfig:"federated_service_name" default:"hook-service"`

	HookMaxConcurrent int `envconfig:"hook_max_concurrent" default:"150"`
}

// redactedPlaceholder is used to mask sensitive values in string representations.
const redactedPlaceholder = "******"

// redactSecret replaces non-empty secret strings with a masked placeholder.
func redactSecret(s string) string {
	if s == "" {
		return ""
	}
	return redactedPlaceholder
}

// redactDSN parses and masks credentials inside a data source name.
func redactDSN(raw string) string {
	if strings.TrimSpace(raw) == "" {
		return ""
	}
	if u, err := url.Parse(raw); err == nil && u.Scheme != "" {
		return u.Redacted()
	}
	return redactedPlaceholder
}

// envSpecDisplay is an unexported type with the same memory layout as EnvSpec
// but without String or GoString methods, avoiding recursion during formatting.
type envSpecDisplay EnvSpec

// displayCopy returns a copy of EnvSpec with sensitive fields redacted for display.
func (e *EnvSpec) displayCopy() envSpecDisplay {
	if e == nil {
		return envSpecDisplay{}
	}
	clone := envSpecDisplay(*e)
	clone.ApiToken = redactSecret(e.ApiToken)
	clone.OpenfgaApiToken = redactSecret(e.OpenfgaApiToken)
	clone.SalesforceConsumerSecret = redactSecret(e.SalesforceConsumerSecret)
	clone.KafkaOAuthClientSecret = redactSecret(e.KafkaOAuthClientSecret)
	clone.DSN = redactDSN(e.DSN)
	clone.ReplicaDSN = redactDSN(e.ReplicaDSN)
	return clone
}

// String returns a string representation of EnvSpec with sensitive fields redacted.
func (e *EnvSpec) String() string {
	if e == nil {
		return "<nil>"
	}
	disp := e.displayCopy()
	return fmt.Sprintf("&%+v", disp)
}

// GoString returns a Go-syntax representation of EnvSpec with sensitive fields redacted.
func (e *EnvSpec) GoString() string {
	if e == nil {
		return "(*config.EnvSpec)(nil)"
	}
	disp := e.displayCopy()
	return strings.Replace(fmt.Sprintf("&%#v", disp), "envSpecDisplay", "EnvSpec", 1)
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
	return "permissions." + federatedServiceName
}

// KafkaBrokerList returns the configured broker addresses with blank entries removed.
// Both DeploymentMode and the publisher selection derive from this single list, so the
// mode reported to operators cannot diverge from the publisher actually in use.
func (e *EnvSpec) KafkaBrokerList() []string {
	brokers := make([]string, 0, len(e.KafkaBrokers))
	for _, b := range e.KafkaBrokers {
		if trimmed := strings.TrimSpace(b); trimmed != "" {
			brokers = append(brokers, trimmed)
		}
	}
	return brokers
}

// DeploymentMode reports the mode implied by the configuration. It is derived solely
// from whether usable broker addresses are configured, never from the federated
// service name.
func (e *EnvSpec) DeploymentMode() DeploymentMode {
	if len(e.KafkaBrokerList()) == 0 {
		return ModeStandalone
	}
	return ModePlatform
}

// Validate checks the configuration for errors that must prevent startup.
//
// The federated service name is validated regardless of mode: an invalid name is a
// configuration error whether or not the running mode consumes it, and deferring the
// check to platform mode would surface it when brokers are first configured rather
// than when the mistake is made.
//
// Kafka security is validated only in platform mode, where a broker connection is
// actually attempted. In standalone mode an incomplete security block is inert and is
// reported through Warnings instead.
func Validate(specs *EnvSpec) error {
	if specs == nil {
		return nil
	}

	if err := ValidateFederatedServiceName(specs.FederatedServiceName); err != nil {
		return fmt.Errorf("invalid federated service configuration: %w", err)
	}

	if specs.DeploymentMode() == ModeStandalone {
		return nil
	}

	if err := ValidateKafkaBrokers(specs.KafkaBrokerList()); err != nil {
		return fmt.Errorf("invalid kafka configuration: %w", err)
	}
	if err := ValidateKafkaSecurity(specs); err != nil {
		return fmt.Errorf("invalid kafka security configuration: %w", err)
	}
	return nil
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

// NormalizeKafkaAuthType returns the normalized lowercased and trimmed Kafka authentication type,
// defaulting to KafkaAuthTypeNone if empty.
func NormalizeKafkaAuthType(raw string) string {
	trimmed := strings.ToLower(strings.TrimSpace(raw))
	if trimmed == "" {
		return KafkaAuthTypeNone
	}
	return trimmed
}

// hasKafkaSecuritySettings reports whether any Kafka authentication or TLS setting
// deviates from its default, meaning the operator configured broker security that the
// running mode may end up ignoring.
func (e *EnvSpec) hasKafkaSecuritySettings() bool {
	return NormalizeKafkaAuthType(e.KafkaAuthType) != KafkaAuthTypeNone ||
		e.KafkaTLSEnabled ||
		e.KafkaTLSCAFile != "" ||
		e.KafkaOAuthTokenURL != "" ||
		e.KafkaOAuthClientID != "" ||
		e.KafkaOAuthClientSecret != "" ||
		e.KafkaOAuthCAFile != "" ||
		len(e.KafkaOAuthScopes) > 0 ||
		(e.KafkaOAuthAudience != "" && e.KafkaOAuthAudience != DefaultKafkaOAuthAudience) ||
		e.KafkaTLSInsecureSkipVerify
}

// ValidateKafkaSecurity validates the security configuration for Kafka broker communication.
func ValidateKafkaSecurity(specs *EnvSpec) error {
	if specs == nil {
		return nil
	}
	authType := NormalizeKafkaAuthType(specs.KafkaAuthType)
	switch authType {
	case KafkaAuthTypeNone:
		return nil
	case KafkaAuthTypeOAuthBearer:
		var errs []error
		if strings.TrimSpace(specs.KafkaOAuthTokenURL) == "" {
			errs = append(errs, &KafkaSecurityError{Field: "kafka_oauth_token_url", Err: ErrMissingOAuthTokenURL})
		}
		if strings.TrimSpace(specs.KafkaOAuthClientID) == "" {
			errs = append(errs, &KafkaSecurityError{Field: "kafka_oauth_client_id", Err: ErrMissingOAuthClientID})
		}
		if strings.TrimSpace(specs.KafkaOAuthClientSecret) == "" {
			errs = append(errs, &KafkaSecurityError{Field: "kafka_oauth_client_secret", Err: ErrMissingOAuthClientSecret})
		}
		return errors.Join(errs...)
	default:
		return &KafkaSecurityError{
			Field: "kafka_auth_type",
			Err:   fmt.Errorf("%w: %q", ErrUnsupportedKafkaAuthType, authType),
		}
	}
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
