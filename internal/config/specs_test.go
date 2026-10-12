// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kelseyhightower/envconfig"
)

func TestDeploymentModeValues(t *testing.T) {
	if ModePlatform != "platform" {
		t.Errorf("ModePlatform = %q, want %q", ModePlatform, "platform")
	}
	if ModeStandalone != "standalone" {
		t.Errorf("ModeStandalone = %q, want %q", ModeStandalone, "standalone")
	}
	if ModePlatform == ModeStandalone {
		t.Error("deployment modes must be distinguishable")
	}
}

func TestFederatedServiceNameDefault(t *testing.T) {
	t.Setenv("DSN", "postgres://localhost/test")

	specs := new(EnvSpec)
	if err := envconfig.Process("", specs); err != nil {
		t.Fatalf("envconfig.Process() error = %v", err)
	}

	if specs.FederatedServiceName != DefaultFederatedServiceName {
		t.Errorf("FederatedServiceName = %q, want %q", specs.FederatedServiceName, DefaultFederatedServiceName)
	}
	if DefaultFederatedServiceName != "hook-service" {
		t.Errorf("DefaultFederatedServiceName = %q, want %q", DefaultFederatedServiceName, "hook-service")
	}
}

func TestFederatedServiceNameConfigured(t *testing.T) {
	t.Setenv("DSN", "postgres://localhost/test")
	t.Setenv("FEDERATED_SERVICE_NAME", "portal")

	specs := new(EnvSpec)
	if err := envconfig.Process("", specs); err != nil {
		t.Fatalf("envconfig.Process() error = %v", err)
	}

	if specs.FederatedServiceName != "portal" {
		t.Errorf("FederatedServiceName = %q, want %q", specs.FederatedServiceName, "portal")
	}
}

func TestPermissionsTopic(t *testing.T) {
	tests := []struct {
		name                 string
		federatedServiceName string
		want                 string
	}{
		{
			name:                 "default name reproduces the existing topic",
			federatedServiceName: DefaultFederatedServiceName,
			want:                 "permissions.hook-service",
		},
		{
			name:                 "portal name",
			federatedServiceName: "portal",
			want:                 "permissions.portal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PermissionsTopic(tt.federatedServiceName); got != tt.want {
				t.Errorf("PermissionsTopic(%q) = %q, want %q", tt.federatedServiceName, got, tt.want)
			}
		})
	}
}

func TestValidateFederatedServiceName(t *testing.T) {
	tests := []struct {
		name                 string
		federatedServiceName string
		wantErr              bool
	}{
		{
			name:                 "default name",
			federatedServiceName: "hook-service",
			wantErr:              false,
		},
		{
			name:                 "portal name",
			federatedServiceName: "portal",
			wantErr:              false,
		},
		{
			name:                 "dots underscores and digits allowed",
			federatedServiceName: "svc_1.a-b",
			wantErr:              false,
		},
		{
			name:                 "empty",
			federatedServiceName: "",
			wantErr:              true,
		},
		{
			name:                 "whitespace only",
			federatedServiceName: "   ",
			wantErr:              true,
		},
		{
			name:                 "tab only",
			federatedServiceName: "\t",
			wantErr:              true,
		},
		{
			name:                 "trailing space",
			federatedServiceName: "portal ",
			wantErr:              true,
		},
		{
			name:                 "internal space",
			federatedServiceName: "hook service",
			wantErr:              true,
		},
		{
			name:                 "slash",
			federatedServiceName: "hook/service",
			wantErr:              true,
		},
		{
			name:                 "longest name yielding a legal topic",
			federatedServiceName: strings.Repeat("a", MaxTopicNameLength-len("permissions.")),
			wantErr:              false,
		},
		{
			name:                 "name one character too long for a legal topic",
			federatedServiceName: strings.Repeat("a", MaxTopicNameLength-len("permissions.")+1),
			wantErr:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFederatedServiceName(tt.federatedServiceName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateFederatedServiceName(%q) error = %v, wantErr %v", tt.federatedServiceName, err, tt.wantErr)
			}
		})
	}
}

func TestValidateKafkaBrokers(t *testing.T) {
	tests := []struct {
		name    string
		brokers []string
		wantErr bool
	}{
		{
			name:    "empty slice",
			brokers: []string{},
			wantErr: false,
		},
		{
			name:    "nil slice",
			brokers: nil,
			wantErr: false,
		},
		{
			name:    "valid single broker",
			brokers: []string{"localhost:9092"},
			wantErr: false,
		},
		{
			name:    "valid multiple brokers",
			brokers: []string{"kafka-1:9092", "kafka-2:9092", "10.0.0.1:9094"},
			wantErr: false,
		},
		{
			name:    "valid with whitespace",
			brokers: []string{" localhost:9092 "},
			wantErr: false,
		},
		{
			name:    "invalid missing port",
			brokers: []string{"localhost"},
			wantErr: true,
		},
		{
			name:    "invalid empty host",
			brokers: []string{":9092"},
			wantErr: true,
		},
		{
			name:    "invalid format",
			brokers: []string{"http://localhost:9092"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKafkaBrokers(tt.brokers)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateKafkaBrokers() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func TestKafkaSecurityEnvSpecDefaults(t *testing.T) {
	_ = t.Context()
	t.Setenv("DSN", "postgres://localhost/test")

	specs := new(EnvSpec)
	if err := envconfig.Process("", specs); err != nil {
		t.Fatalf("envconfig.Process() error = %v", err)
	}

	if specs.KafkaAuthType != KafkaAuthTypeNone {
		t.Errorf("KafkaAuthType = %q, want %q", specs.KafkaAuthType, KafkaAuthTypeNone)
	}
	if specs.KafkaOAuthAudience != DefaultKafkaOAuthAudience {
		t.Errorf("KafkaOAuthAudience = %q, want %q", specs.KafkaOAuthAudience, DefaultKafkaOAuthAudience)
	}
	if len(specs.KafkaOAuthScopes) != 0 {
		t.Errorf("KafkaOAuthScopes = %v, want empty", specs.KafkaOAuthScopes)
	}
	if specs.KafkaTLSEnabled {
		t.Error("KafkaTLSEnabled should default to false")
	}
	if specs.KafkaTLSInsecureSkipVerify {
		t.Error("KafkaTLSInsecureSkipVerify should default to false")
	}

	t.Run("comma-separated scopes parsing", func(t *testing.T) {
		t.Setenv("KAFKA_OAUTH_SCOPES", "openid,profile,email")
		var customSpecs EnvSpec
		if err := envconfig.Process("", &customSpecs); err != nil {
			t.Fatalf("envconfig.Process() error = %v", err)
		}
		if len(customSpecs.KafkaOAuthScopes) != 3 {
			t.Fatalf("len(KafkaOAuthScopes) = %d, want 3", len(customSpecs.KafkaOAuthScopes))
		}
		if customSpecs.KafkaOAuthScopes[0] != "openid" || customSpecs.KafkaOAuthScopes[1] != "profile" || customSpecs.KafkaOAuthScopes[2] != "email" {
			t.Errorf("KafkaOAuthScopes = %v, want [openid profile email]", customSpecs.KafkaOAuthScopes)
		}
	})
}

func TestValidateKafkaSecurity(t *testing.T) {
	ctx := t.Context()
	if ctx.Err() != nil {
		t.Fatal("expected test context to be active")
	}

	tests := []struct {
		name          string
		specs         *EnvSpec
		wantErr       bool
		expectedErrIs []error
	}{
		{
			name:    "nil specs succeeds",
			specs:   nil,
			wantErr: false,
		},
		{
			name: "default none auth type succeeds",
			specs: &EnvSpec{
				KafkaAuthType: KafkaAuthTypeNone,
			},
			wantErr: false,
		},
		{
			name: "empty auth type defaults to none and succeeds",
			specs: &EnvSpec{
				KafkaAuthType: "",
			},
			wantErr: false,
		},
		{
			name: "oauthbearer with valid credentials succeeds",
			specs: &EnvSpec{
				KafkaAuthType:          KafkaAuthTypeOAuthBearer,
				KafkaOAuthTokenURL:     "https://hydra.example.com/oauth2/token",
				KafkaOAuthClientID:     "hook-client",
				KafkaOAuthClientSecret: "secret-val",
			},
			wantErr: false,
		},
		{
			name: "oauthbearer with missing token url fails",
			specs: &EnvSpec{
				KafkaAuthType:          KafkaAuthTypeOAuthBearer,
				KafkaOAuthClientID:     "hook-client",
				KafkaOAuthClientSecret: "secret-val",
			},
			wantErr:       true,
			expectedErrIs: []error{ErrMissingOAuthTokenURL},
		},
		{
			name: "oauthbearer with missing client id fails",
			specs: &EnvSpec{
				KafkaAuthType:          KafkaAuthTypeOAuthBearer,
				KafkaOAuthTokenURL:     "https://hydra.example.com/oauth2/token",
				KafkaOAuthClientSecret: "secret-val",
			},
			wantErr:       true,
			expectedErrIs: []error{ErrMissingOAuthClientID},
		},
		{
			name: "oauthbearer with missing client secret fails",
			specs: &EnvSpec{
				KafkaAuthType:      KafkaAuthTypeOAuthBearer,
				KafkaOAuthTokenURL: "https://hydra.example.com/oauth2/token",
				KafkaOAuthClientID: "hook-client",
			},
			wantErr:       true,
			expectedErrIs: []error{ErrMissingOAuthClientSecret},
		},
		{
			name: "oauthbearer with all credentials missing joins all errors",
			specs: &EnvSpec{
				KafkaAuthType: KafkaAuthTypeOAuthBearer,
			},
			wantErr: true,
			expectedErrIs: []error{
				ErrMissingOAuthTokenURL,
				ErrMissingOAuthClientID,
				ErrMissingOAuthClientSecret,
			},
		},
		{
			name: "oauthbearer uppercase (OAUTHBEARER) with valid credentials succeeds",
			specs: &EnvSpec{
				KafkaAuthType:          "OAUTHBEARER",
				KafkaOAuthTokenURL:     "https://hydra.example.com/oauth2/token",
				KafkaOAuthClientID:     "hook-client",
				KafkaOAuthClientSecret: "secret-val",
			},
			wantErr: false,
		},
		{
			name: "oauthbearer with leading and trailing whitespace and newline succeeds",
			specs: &EnvSpec{
				KafkaAuthType:          "  OAuthBearer \n",
				KafkaOAuthTokenURL:     "https://hydra.example.com/oauth2/token",
				KafkaOAuthClientID:     "hook-client",
				KafkaOAuthClientSecret: "secret-val",
			},
			wantErr: false,
		},
		{
			name: "none auth type with whitespace and uppercase succeeds",
			specs: &EnvSpec{
				KafkaAuthType: " NONE \n",
			},
			wantErr: false,
		},
		{
			name: "unsupported auth type fails",
			specs: &EnvSpec{
				KafkaAuthType: "scram-sha-512",
			},
			wantErr:       true,
			expectedErrIs: []error{ErrUnsupportedKafkaAuthType},
		},
		{
			name: "unsupported auth type with whitespace fails and trims error output",
			specs: &EnvSpec{
				KafkaAuthType: "  kerberos \n",
			},
			wantErr:       true,
			expectedErrIs: []error{ErrUnsupportedKafkaAuthType},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_ = t.Context()
			err := ValidateKafkaSecurity(tt.specs)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateKafkaSecurity() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr {
				return
			}

			// Validate with errors.AsType[T] (Go 1.26+)
			if secErr, ok := errors.AsType[*KafkaSecurityError](err); !ok || secErr == nil {
				t.Errorf("expected error to be of type *KafkaSecurityError, got: %T (%v)", err, err)
			}

			// Validate all expected sentinel errors via errors.Is
			for _, expectedTarget := range tt.expectedErrIs {
				if !errors.Is(err, expectedTarget) {
					t.Errorf("expected errors.Is(err, %v) to be true, got err = %v", expectedTarget, err)
				}
			}
		})
	}
}

func TestNormalizeKafkaAuthType(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want string
	}{
		{name: "empty string", raw: "", want: KafkaAuthTypeNone},
		{name: "whitespace only", raw: "   \t\n", want: KafkaAuthTypeNone},
		{name: "lowercase none", raw: "none", want: KafkaAuthTypeNone},
		{name: "uppercase NONE with whitespace", raw: "  NONE  ", want: KafkaAuthTypeNone},
		{name: "canonical lowercase oauthbearer", raw: "oauthbearer", want: KafkaAuthTypeOAuthBearer},
		{name: "uppercase IANA OAUTHBEARER", raw: "OAUTHBEARER", want: KafkaAuthTypeOAuthBearer},
		{name: "mixed case with whitespace and newline", raw: " \tOAuthBearer\n", want: KafkaAuthTypeOAuthBearer},
		{name: "unsupported custom mechanism", raw: " SCRAM-SHA-512 ", want: "scram-sha-512"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeKafkaAuthType(tt.raw); got != tt.want {
				t.Errorf("NormalizeKafkaAuthType(%q) = %q, want %q", tt.raw, got, tt.want)
			}
		})
	}
}

func TestEnvSpecRedaction(t *testing.T) {
	specs := &EnvSpec{
		Port:                     8080,
		LogLevel:                 "debug",
		ApiToken:                 "api-secret-123",
		OpenfgaApiToken:          "fga-secret-456",
		SalesforceConsumerKey:    "sf-consumer-key-xyz",
		SalesforceConsumerSecret: "sf-secret-789",
		DSN:                      "postgres://appuser:dbpass123@localhost:5432/hookdb?sslmode=disable",
		ReplicaDSN:               "postgres://repuser:reppass456@replica.example.com:5433/hookdb?sslmode=disable",
		KafkaOAuthClientID:       "oauth-client-id-abc",
		KafkaOAuthClientSecret:   "oauth-client-secret-def",
		KafkaOAuthAudience:       "kafka",
	}

	rawSecrets := []string{
		"api-secret-123",
		"fga-secret-456",
		"sf-secret-789",
		"oauth-client-secret-def",
		"dbpass123",
		"reppass456",
	}

	formats := []struct {
		name   string
		format string
	}{
		{name: "%v", format: "%v"},
		{name: "%+v", format: "%+v"},
		{name: "%s", format: "%s"},
		{name: "%#v", format: "%#v"},
	}

	for _, fmtCase := range formats {
		t.Run(fmtCase.name, func(t *testing.T) {
			output := fmt.Sprintf(fmtCase.format, specs)

			for _, secret := range rawSecrets {
				if strings.Contains(output, secret) {
					t.Errorf("format %s leaked secret %q in output: %s", fmtCase.name, secret, output)
				}
			}

			if !strings.Contains(output, "******") {
				t.Errorf("format %s missing masked placeholder %q: %s", fmtCase.name, "******", output)
			}
			if !strings.Contains(output, "xxxxx") {
				t.Errorf("format %s missing URL password redaction %q: %s", fmtCase.name, "xxxxx", output)
			}
			if !strings.Contains(output, "oauth-client-id-abc") {
				t.Errorf("format %s should retain non-sensitive KafkaOAuthClientID", fmtCase.name)
			}
			if !strings.Contains(output, "appuser") {
				t.Errorf("format %s should retain DSN username", fmtCase.name)
			}
		})
	}
}

func TestEnvSpecRedactionEmpty(t *testing.T) {
	specs := &EnvSpec{}
	output := fmt.Sprintf("%v", specs)
	if strings.Contains(output, "******") {
		t.Errorf("empty EnvSpec should not contain masked placeholder, got: %s", output)
	}
}

func TestEnvSpecRedactionNil(t *testing.T) {
	var specs *EnvSpec
	if got := fmt.Sprintf("%v", specs); got != "<nil>" {
		t.Errorf("fmt.Sprintf(%%v, nil) = %q, want %q", got, "<nil>")
	}
	if got := fmt.Sprintf("%+v", specs); got != "<nil>" {
		t.Errorf("fmt.Sprintf(%%+v, nil) = %q, want %q", got, "<nil>")
	}
	if got := specs.String(); got != "<nil>" {
		t.Errorf("nil specs.String() = %q, want %q", got, "<nil>")
	}
	if got := fmt.Sprintf("%#v", specs); got != "(*config.EnvSpec)(nil)" {
		t.Errorf("fmt.Sprintf(%%#v, nil) = %q, want %q", got, "(*config.EnvSpec)(nil)")
	}
}

func TestRedactDSN(t *testing.T) {
	tests := []struct {
		name string
		dsn  string
		want string
	}{
		{
			name: "empty DSN",
			dsn:  "",
			want: "",
		},
		{
			name: "whitespace DSN",
			dsn:  "   ",
			want: "",
		},
		{
			name: "postgres URL with password",
			dsn:  "postgres://user:secret@localhost:5432/db?sslmode=disable",
			want: "postgres://user:xxxxx@localhost:5432/db?sslmode=disable",
		},
		{
			name: "postgres URL without user info",
			dsn:  "postgres://localhost:5432/db",
			want: "postgres://localhost:5432/db",
		},
		{
			name: "postgres URL without password",
			dsn:  "postgres://user@localhost:5432/db",
			want: "postgres://user@localhost:5432/db",
		},
		{
			name: "non-URL raw connection string",
			dsn:  "host=localhost port=5432 user=foo password=secret",
			want: "******",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := redactDSN(tt.dsn); got != tt.want {
				t.Errorf("redactDSN(%q) = %q, want %q", tt.dsn, got, tt.want)
			}
		})
	}
}

// TestKafkaBrokerList asserts that blank entries are dropped and addresses trimmed, so
// that a single normalized list drives both the deployment mode and the publisher.
func TestKafkaBrokerList(t *testing.T) {
	tests := []struct {
		name    string
		brokers []string
		want    []string
	}{
		{
			name:    "nil slice yields an empty list",
			brokers: nil,
			want:    []string{},
		},
		{
			name:    "empty string entry is dropped",
			brokers: []string{""},
			want:    []string{},
		},
		{
			name:    "whitespace-only entry is dropped",
			brokers: []string{"   ", "\t\n"},
			want:    []string{},
		},
		{
			name:    "surrounding whitespace is trimmed",
			brokers: []string{" localhost:9092 "},
			want:    []string{"localhost:9092"},
		},
		{
			name:    "usable entries survive alongside blanks",
			brokers: []string{"", "kafka-1:9092", "  ", "kafka-2:9092"},
			want:    []string{"kafka-1:9092", "kafka-2:9092"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			specs := &EnvSpec{KafkaBrokers: tt.brokers}
			got := specs.KafkaBrokerList()
			if !slices.Equal(got, tt.want) {
				t.Errorf("KafkaBrokerList() = %v, want %v", got, tt.want)
			}
		})
	}
}

// TestDeploymentMode asserts the mode is derived from usable brokers alone, never from
// the federated service name.
func TestDeploymentMode(t *testing.T) {
	tests := []struct {
		name  string
		specs *EnvSpec
		want  DeploymentMode
	}{
		{
			name:  "no brokers is standalone",
			specs: &EnvSpec{},
			want:  ModeStandalone,
		},
		{
			name:  "blank-only brokers are standalone",
			specs: &EnvSpec{KafkaBrokers: []string{"", "   "}},
			want:  ModeStandalone,
		},
		{
			name:  "a usable broker is platform",
			specs: &EnvSpec{KafkaBrokers: []string{"localhost:9092"}},
			want:  ModePlatform,
		},
		{
			name:  "a usable broker among blanks is platform",
			specs: &EnvSpec{KafkaBrokers: []string{"  ", "localhost:9092"}},
			want:  ModePlatform,
		},
		{
			name:  "a named service without brokers stays standalone",
			specs: &EnvSpec{FederatedServiceName: "portal"},
			want:  ModeStandalone,
		},
		{
			name: "the default service name with brokers is still platform",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaBrokers:         []string{"localhost:9092"},
			},
			want: ModePlatform,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.specs.DeploymentMode(); got != tt.want {
				t.Errorf("DeploymentMode() = %q, want %q", got, tt.want)
			}
		})
	}
}

// TestValidate asserts which misconfigurations block startup in each mode: the
// federated service name is always checked, broker security only where it is used.
func TestValidate(t *testing.T) {
	tests := []struct {
		name      string
		specs     *EnvSpec
		wantErr   string
		targetErr error
	}{
		{
			name: "valid standalone configuration",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
			},
		},
		{
			name: "valid platform configuration",
			specs: &EnvSpec{
				FederatedServiceName: "portal",
				KafkaBrokers:         []string{"localhost:9092"},
			},
		},
		{
			name: "empty federated service name",
			specs: &EnvSpec{
				FederatedServiceName: "",
			},
			wantErr: "invalid federated service configuration",
		},
		{
			name: "whitespace-only federated service name",
			specs: &EnvSpec{
				FederatedServiceName: "   ",
			},
			wantErr: "invalid federated service configuration",
		},
		{
			name: "federated service name with invalid characters",
			specs: &EnvSpec{
				FederatedServiceName: "hook service",
			},
			wantErr: "invalid federated service configuration",
		},
		{
			name: "broker address without a port",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaBrokers:         []string{"localhost"},
			},
			wantErr: "invalid kafka configuration",
		},
		{
			name: "unsupported auth type in platform mode",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaBrokers:         []string{"localhost:9092"},
				KafkaAuthType:        "kerberos",
			},
			wantErr:   "invalid kafka security configuration",
			targetErr: ErrUnsupportedKafkaAuthType,
		},
		{
			name: "oauthbearer missing credentials in platform mode",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaBrokers:         []string{"localhost:9092"},
				KafkaAuthType:        KafkaAuthTypeOAuthBearer,
			},
			wantErr:   "invalid kafka security configuration",
			targetErr: ErrMissingOAuthTokenURL,
		},
		{
			name: "incomplete oauthbearer without brokers is inert",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaAuthType:        KafkaAuthTypeOAuthBearer,
			},
		},
		{
			name: "unsupported auth type without brokers is inert",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaAuthType:        "kerberos",
			},
		},
		{
			name: "an invalid service name is rejected even in standalone mode",
			specs: &EnvSpec{
				FederatedServiceName: "hook service",
				KafkaAuthType:        KafkaAuthTypeOAuthBearer,
			},
			wantErr: "invalid federated service configuration",
		},
		{
			name: "blank-only brokers skip broker and security validation",
			specs: &EnvSpec{
				FederatedServiceName: DefaultFederatedServiceName,
				KafkaBrokers:         []string{"   "},
				KafkaAuthType:        KafkaAuthTypeOAuthBearer,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := Validate(tt.specs)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("Validate() unexpected error = %v", err)
				}
				return
			}

			if err == nil {
				t.Fatalf("expected an error containing %q, got nil", tt.wantErr)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
			if tt.targetErr != nil && !errors.Is(err, tt.targetErr) {
				t.Errorf("expected errors.Is match for %v, got %v", tt.targetErr, err)
			}
		})
	}
}

// TestValidateNil asserts that a nil configuration is not treated as invalid, leaving
// the caller's own sourcing error to surface instead of a misleading validation error.
func TestValidateNil(t *testing.T) {
	if err := Validate(nil); err != nil {
		t.Errorf("Validate(nil) = %v, want nil", err)
	}
}
