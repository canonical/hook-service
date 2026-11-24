// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"slices"
	"strings"
	"testing"

	"github.com/kelseyhightower/envconfig"
)

// TestWarningsStandalone asserts that settings standalone mode cannot act on are
// reported once, and that a clean standalone configuration is silent.
func TestWarningsStandalone(t *testing.T) {
	t.Setenv("DSN", "postgres://localhost/test")
	t.Setenv("KAFKA_BROKERS", "")
	t.Setenv("KAFKA_AUTH_TYPE", "")
	t.Setenv("KAFKA_OAUTH_AUDIENCE", "")
	defaultStandaloneSpecs := new(EnvSpec)
	if err := envconfig.Process("", defaultStandaloneSpecs); err != nil {
		t.Fatalf("envconfig.Process() error = %v", err)
	}

	const (
		nameWarning = "Federated service name"
		secWarning  = "Kafka authentication/TLS settings are configured"
	)

	tests := []struct {
		name     string
		specs    *EnvSpec
		want     []string
		dontWant []string
	}{
		{
			name:     "clean standalone from environment defaults is silent",
			specs:    defaultStandaloneSpecs,
			dontWant: []string{nameWarning, secWarning},
		},
		{
			name: "named deployment without brokers warns about the name only",
			specs: &EnvSpec{
				FederatedServiceName: "portal",
			},
			want:     []string{nameWarning},
			dontWant: []string{secWarning},
		},
		{
			name: "oauthbearer without brokers warns about ignored security",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaAuthType:        KafkaAuthTypeOAuthBearer,
			},
			want:     []string{secWarning},
			dontWant: []string{nameWarning},
		},
		{
			name: "tls enabled without brokers warns about ignored security",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaTLSEnabled:      true,
			},
			want: []string{secWarning},
		},
		{
			name: "tls ca file without brokers warns about ignored security",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaTLSCAFile:       "/path/to/ca.pem",
			},
			want: []string{secWarning},
		},
		{
			name: "custom oauth audience without brokers warns about ignored security",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaOAuthAudience:   "custom-audience",
			},
			want: []string{secWarning},
		},
		{
			name: "default oauth audience without brokers is silent",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaOAuthAudience:   DefaultKafkaOAuthAudience,
			},
			dontWant: []string{secWarning},
		},
		{
			name: "auth type none with surrounding whitespace is silent",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaAuthType:        "  NONE \n",
			},
			dontWant: []string{secWarning},
		},
		{
			name: "auth type oauthbearer in uppercase warns about ignored security",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaAuthType:        "OAUTHBEARER",
			},
			want: []string{secWarning},
		},
		{
			name: "named deployment with ignored security warns about both",
			specs: &EnvSpec{
				FederatedServiceName: "portal",
				KafkaTLSEnabled:      true,
			},
			want: []string{nameWarning, secWarning},
		},
		{
			name: "blank-only brokers are standalone and warn about ignored security",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaBrokers:         []string{"   ", ""},
				KafkaTLSEnabled:      true,
			},
			want: []string{secWarning},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertWarnings(t, tt.specs, tt.want, tt.dontWant)
		})
	}
}

// TestWarningsPlatform asserts that platform mode reports contradictory or
// protection-weakening broker security settings, and nothing else.
func TestWarningsPlatform(t *testing.T) {
	tests := []struct {
		name     string
		specs    *EnvSpec
		want     []string
		dontWant []string
	}{
		{
			name: "default service name with brokers warns about attribution",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaBrokers:         []string{"localhost:9092"},
			},
			want: []string{"under the default federated service name"},
		},
		{
			name: "named deployment with plain tcp is silent",
			specs: &EnvSpec{
				FederatedServiceName: "portal",
				KafkaBrokers:         []string{"localhost:9092"},
				KafkaAuthType:        KafkaAuthTypeNone,
			},
			dontWant: []string{
				"under the default federated service name",
				"without TLS",
				"is false",
				"certificate verification is disabled",
			},
		},
		{
			name: "oauthbearer without tls warns about plaintext tokens",
			specs: &EnvSpec{
				FederatedServiceName: "portal",
				KafkaBrokers:         []string{"localhost:9092"},
				KafkaAuthType:        KafkaAuthTypeOAuthBearer,
			},
			want: []string{"Kafka SASL/OAUTHBEARER authentication is enabled without TLS"},
		},
		{
			name: "tls ca file with tls disabled warns the file is ignored",
			specs: &EnvSpec{
				FederatedServiceName: "portal",
				KafkaBrokers:         []string{"localhost:9092"},
				KafkaTLSCAFile:       "/path/to/ca.pem",
			},
			want: []string{"KAFKA_TLS_CA_FILE is configured but KAFKA_TLS_ENABLED is false"},
		},
		{
			name: "skip verify with tls disabled warns the setting is ignored",
			specs: &EnvSpec{
				FederatedServiceName:       "portal",
				KafkaBrokers:               []string{"localhost:9092"},
				KafkaTLSInsecureSkipVerify: true,
			},
			want: []string{"KAFKA_TLS_INSECURE_SKIP_VERIFY is enabled but KAFKA_TLS_ENABLED is false"},
		},
		{
			name: "oauthbearer without tls and with ca file warns about both",
			specs: &EnvSpec{
				FederatedServiceName: "portal",
				KafkaBrokers:         []string{"localhost:9092"},
				KafkaAuthType:        KafkaAuthTypeOAuthBearer,
				KafkaTLSCAFile:       "/path/to/ca.pem",
			},
			want: []string{
				"Kafka SASL/OAUTHBEARER authentication is enabled without TLS",
				"KAFKA_TLS_CA_FILE is configured but KAFKA_TLS_ENABLED is false",
			},
		},
		{
			name: "tls enabled with skip verify warns only about unverified certificates",
			specs: &EnvSpec{
				FederatedServiceName:       "portal",
				KafkaBrokers:               []string{"localhost:9092"},
				KafkaAuthType:              KafkaAuthTypeOAuthBearer,
				KafkaTLSEnabled:            true,
				KafkaTLSInsecureSkipVerify: true,
			},
			want: []string{"Kafka broker TLS certificate verification is disabled"},
			dontWant: []string{
				"without TLS",
				"is false",
			},
		},
		{
			name: "tls enabled with verification is silent",
			specs: &EnvSpec{
				FederatedServiceName: "portal",
				KafkaBrokers:         []string{"localhost:9092"},
				KafkaAuthType:        KafkaAuthTypeOAuthBearer,
				KafkaTLSEnabled:      true,
			},
			dontWant: []string{
				"without TLS",
				"is false",
				"certificate verification is disabled",
			},
		},
		{
			name: "tls enabled with a ca file is silent",
			specs: &EnvSpec{
				FederatedServiceName: "portal",
				KafkaBrokers:         []string{"localhost:9092"},
				KafkaTLSEnabled:      true,
				KafkaTLSCAFile:       "/path/to/ca.pem",
			},
			dontWant: []string{
				"will be ignored",
				"certificate verification is disabled",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assertWarnings(t, tt.specs, tt.want, tt.dontWant)
		})
	}
}

// TestWarningsNil asserts that a nil configuration produces no advisories rather than
// panicking, so a caller that failed to source configuration still reaches its own
// error handling.
func TestWarningsNil(t *testing.T) {
	if got := Warnings(nil); got != nil {
		t.Errorf("Warnings(nil) = %v, want nil", got)
	}
}

// TestWarningsStandaloneSuppressesPlatformDiagnostics asserts the mode gating: the
// per-setting TLS diagnostics only make sense where the setting is consumed, so
// standalone reports the single aggregate notice instead of all three.
func TestWarningsStandaloneSuppressesPlatformDiagnostics(t *testing.T) {
	specs := &EnvSpec{
		FederatedServiceName:       DefaultFederatedServiceName,
		KafkaAuthType:              KafkaAuthTypeOAuthBearer,
		KafkaTLSCAFile:             "/path/to/ca.pem",
		KafkaTLSInsecureSkipVerify: true,
	}

	got := Warnings(specs)
	if len(got) != 1 {
		t.Fatalf("expected exactly one aggregate warning, got %d: %v", len(got), got)
	}
	if !strings.Contains(got[0], "Kafka authentication/TLS settings are configured") {
		t.Errorf("warning = %q, want the aggregate ignored-security notice", got[0])
	}
}

// assertWarnings checks that every want substring appears in some advisory and no
// dontWant substring appears in any.
func assertWarnings(t *testing.T, specs *EnvSpec, want, dontWant []string) {
	t.Helper()

	got := Warnings(specs)
	contains := func(needle string) bool {
		return slices.ContainsFunc(got, func(warning string) bool {
			return strings.Contains(warning, needle)
		})
	}

	for _, needle := range want {
		if !contains(needle) {
			t.Errorf("expected a warning containing %q, got %v", needle, got)
		}
	}
	for _, needle := range dontWant {
		if contains(needle) {
			t.Errorf("did not expect a warning containing %q, got %v", needle, got)
		}
	}
}
