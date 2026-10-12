// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"strings"
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/canonical/hook-service/internal/kafka/sasl"
	"github.com/canonical/hook-service/internal/monitoring"
)

// newPublisherConfig returns a platform configuration that NewPublisher accepts, which
// individual cases narrow to the setting under test.
func newPublisherConfig() *Config {
	return &Config{
		Brokers:     []string{"localhost:9092"},
		ServiceName: testServiceName,
		Topic:       testTopic,
		AuthType:    AuthTypeNone,
	}
}

// oauthPublisherConfig returns a configuration with complete OAUTHBEARER credentials.
func oauthPublisherConfig() *Config {
	cfg := newPublisherConfig()
	cfg.AuthType = AuthTypeOAuthBearer
	cfg.OAuthTokenURL = "https://auth.example.com/oauth2/token"
	cfg.OAuthClientID = "my-client"
	cfg.OAuthClientSecret = "my-secret"
	return cfg
}

// TestNewPublisherSelectsImplementation covers the requirement that an empty broker list
// is the single signal selecting the no-op publisher. The caller derives the reported
// deployment mode from the same list, so a divergence between the two would have to
// originate here.
func TestNewPublisherSelectsImplementation(t *testing.T) {
	tests := []struct {
		name        string
		cfg         *Config
		wantRealPub bool
	}{
		{
			name: "no brokers selects the noop publisher",
			cfg: &Config{
				ServiceName: testServiceName,
				Topic:       testTopic,
			},
			wantRealPub: false,
		},
		{
			name: "empty broker slice selects the noop publisher",
			cfg: &Config{
				Brokers:     []string{},
				ServiceName: testServiceName,
				Topic:       testTopic,
			},
			wantRealPub: false,
		},
		{
			name:        "a broker selects the permission publisher",
			cfg:         newPublisherConfig(),
			wantRealPub: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			publisher, err := NewPublisher(tt.cfg, NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
			if err != nil {
				t.Fatalf("NewPublisher() unexpected error = %v", err)
			}
			t.Cleanup(func() {
				if err := publisher.Close(); err != nil {
					t.Errorf("Close() error = %v", err)
				}
			})

			_, isReal := publisher.(*PermissionPublisher)
			_, isNoop := publisher.(*NoopPublisher)

			if isReal == isNoop {
				t.Fatalf("publisher is neither exclusively a permission nor a noop publisher: %T", publisher)
			}
			if isReal != tt.wantRealPub {
				t.Errorf("publisher is %T, want real publisher = %v", publisher, tt.wantRealPub)
			}
		})
	}
}

// TestNewPublisherTransportModes asserts that every supported transport combination
// produces a usable publisher.
func TestNewPublisherTransportModes(t *testing.T) {
	tlsWithSkipVerify := func() *Config {
		cfg := oauthPublisherConfig()
		cfg.TLSEnabled = true
		cfg.TLSInsecureSkipVerify = true
		return cfg
	}

	oauthWithScopes := func() *Config {
		cfg := oauthPublisherConfig()
		cfg.OAuthScopes = []string{"openid", "profile"}
		cfg.OAuthAudience = "custom-audience"
		return cfg
	}

	tests := []struct {
		name string
		cfg  *Config
	}{
		{name: "unauthenticated plain tcp", cfg: newPublisherConfig()},
		{name: "oauthbearer without tls", cfg: oauthPublisherConfig()},
		{name: "oauthbearer with scopes and custom audience", cfg: oauthWithScopes()},
		{name: "oauthbearer with tls and skip verify", cfg: tlsWithSkipVerify()},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			publisher, err := NewPublisher(tt.cfg, NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
			if err != nil {
				t.Fatalf("NewPublisher() unexpected error = %v", err)
			}
			t.Cleanup(func() {
				if err := publisher.Close(); err != nil {
					t.Errorf("Close() error = %v", err)
				}
			})

			p, ok := publisher.(*PermissionPublisher)
			if !ok {
				t.Fatalf("publisher is %T, want *PermissionPublisher", publisher)
			}
			if p.topic != tt.cfg.Topic {
				t.Errorf("topic = %q, want %q", p.topic, tt.cfg.Topic)
			}
			if p.serviceName != tt.cfg.ServiceName {
				t.Errorf("serviceName = %q, want %q", p.serviceName, tt.cfg.ServiceName)
			}
		})
	}
}

// TestNewPublisherRejectsUnreadableCertificates asserts that a transport the operator
// cannot have intended fails startup rather than silently falling back to weaker
// settings, and that no publisher is returned alongside the error.
func TestNewPublisherRejectsUnreadableCertificates(t *testing.T) {
	brokerCAMissing := func() *Config {
		cfg := newPublisherConfig()
		cfg.TLSEnabled = true
		cfg.TLSCAFile = "/nonexistent/ca/path/cert.pem"
		return cfg
	}

	oauthCAMissing := func() *Config {
		cfg := oauthPublisherConfig()
		cfg.OAuthCAFile = "/nonexistent/oauth/ca/cert.pem"
		return cfg
	}

	tests := []struct {
		name    string
		cfg     *Config
		wantErr string
	}{
		{
			name:    "unreadable broker ca file",
			cfg:     brokerCAMissing(),
			wantErr: "invalid kafka broker TLS configuration",
		},
		{
			name:    "unreadable oauth ca file",
			cfg:     oauthCAMissing(),
			wantErr: "invalid kafka oauth HTTP client configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			publisher, err := NewPublisher(tt.cfg, NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
			if err == nil {
				t.Fatalf("expected an error containing %q, got publisher %T", tt.wantErr, publisher)
			}
			if publisher != nil {
				t.Errorf("expected a nil publisher on error, got %T", publisher)
			}
			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error %q does not contain %q", err.Error(), tt.wantErr)
			}
		})
	}
}

// TestNewPublisherOwnsTransport asserts that Close releases the transport the publisher
// built, so the caller never has to track it, and that repeated calls stay safe.
func TestNewPublisherOwnsTransport(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cfg := oauthPublisherConfig()
	cfg.TLSEnabled = true
	cfg.TLSInsecureSkipVerify = true

	publisher, err := NewPublisher(cfg, NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
	if err != nil {
		t.Fatalf("NewPublisher() unexpected error = %v", err)
	}

	p, ok := publisher.(*PermissionPublisher)
	if !ok {
		t.Fatalf("publisher is %T, want *PermissionPublisher", publisher)
	}
	if p.transport == nil {
		t.Fatal("expected the publisher to own the transport it built")
	}

	if err := publisher.Close(); err != nil {
		t.Fatalf("Close() unexpected error = %v", err)
	}
	// Releasing twice must stay safe: the deferred close in the composition root can
	// run after an explicit shutdown path has already closed the publisher.
	if err := publisher.Close(); err != nil {
		t.Fatalf("Close() on a closed publisher unexpected error = %v", err)
	}
}

// TestNewPublisherPlainTransportIsUnowned asserts that a plain TCP publisher holds no
// transport, so Close has nothing extra to release.
func TestNewPublisherPlainTransportIsUnowned(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	publisher, err := NewPublisher(newPublisherConfig(), NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
	if err != nil {
		t.Fatalf("NewPublisher() unexpected error = %v", err)
	}
	t.Cleanup(func() {
		if err := publisher.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})

	p, ok := publisher.(*PermissionPublisher)
	if !ok {
		t.Fatalf("publisher is %T, want *PermissionPublisher", publisher)
	}
	if p.transport != nil {
		t.Errorf("expected no owned transport for plain TCP, got %+v", p.transport)
	}
}

// TestNewTransport asserts that the transport carries exactly the TLS and SASL
// configuration the config implies, and that plain TCP yields the nil transport that
// selects kafka-go's default.
func TestNewTransport(t *testing.T) {
	tests := []struct {
		name     string
		cfg      *Config
		wantNil  bool
		wantTLS  bool
		wantSASL bool
	}{
		{
			name:    "plain tcp without auth returns no transport",
			cfg:     &Config{AuthType: AuthTypeNone},
			wantNil: true,
		},
		{
			name:    "empty auth type without tls returns no transport",
			cfg:     &Config{},
			wantNil: true,
		},
		{
			name: "tls alone configures tls only",
			cfg: &Config{
				TLSEnabled:            true,
				TLSInsecureSkipVerify: true,
			},
			wantTLS: true,
		},
		{
			name: "oauthbearer alone configures sasl only",
			cfg: &Config{
				AuthType:          AuthTypeOAuthBearer,
				OAuthTokenURL:     "https://auth.example.com/oauth2/token",
				OAuthClientID:     "client-id",
				OAuthClientSecret: "client-secret",
			},
			wantSASL: true,
		},
		{
			name: "oauthbearer with tls configures both",
			cfg: &Config{
				AuthType:              AuthTypeOAuthBearer,
				OAuthTokenURL:         "https://auth.example.com/oauth2/token",
				OAuthClientID:         "client-id",
				OAuthClientSecret:     "client-secret",
				TLSEnabled:            true,
				TLSInsecureSkipVerify: true,
			},
			wantTLS:  true,
			wantSASL: true,
		},
		{
			// A CA file is never read while TLS is disabled, so an unreadable path
			// cannot fail startup. The configuration layer reports it as ignored.
			name: "broker ca file is not loaded when tls is disabled",
			cfg: &Config{
				AuthType:          AuthTypeOAuthBearer,
				OAuthTokenURL:     "https://auth.example.com/oauth2/token",
				OAuthClientID:     "client-id",
				OAuthClientSecret: "client-secret",
				TLSCAFile:         "/nonexistent/broker-ca.pem",
			},
			wantSASL: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			transport, err := newTransport(tt.cfg, NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
			if err != nil {
				t.Fatalf("newTransport() unexpected error = %v", err)
			}

			if tt.wantNil {
				if transport != nil {
					t.Fatalf("expected no transport, got %+v", transport)
				}
				return
			}

			if transport == nil {
				t.Fatal("expected a transport, got nil")
			}
			if transport.IdleTimeout != DefaultTransportIdleTimeout {
				t.Errorf("IdleTimeout = %v, want %v", transport.IdleTimeout, DefaultTransportIdleTimeout)
			}
			if tt.wantTLS != (transport.TLS != nil) {
				t.Errorf("transport.TLS = %+v, want configured = %v", transport.TLS, tt.wantTLS)
			}
			if tt.wantSASL != (transport.SASL != nil) {
				t.Errorf("transport.SASL = %+v, want configured = %v", transport.SASL, tt.wantSASL)
			}
		})
	}
}

// TestNewTransportDefaultsOAuthAudience asserts the audience fallback, so a deployment
// that omits it still requests the token audience Kafka brokers validate.
func TestNewTransportDefaultsOAuthAudience(t *testing.T) {
	if DefaultOAuthAudience != "kafka" {
		t.Errorf("DefaultOAuthAudience = %q, want %q", DefaultOAuthAudience, "kafka")
	}

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cfg := &Config{
		AuthType:          AuthTypeOAuthBearer,
		OAuthTokenURL:     "https://auth.example.com/oauth2/token",
		OAuthClientID:     "client-id",
		OAuthClientSecret: "client-secret",
	}

	transport, err := newTransport(cfg, NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
	if err != nil {
		t.Fatalf("newTransport() unexpected error = %v", err)
	}
	if transport.SASL == nil {
		t.Fatal("expected a SASL mechanism on the transport")
	}
}

// tokenMetricMonitor is a monitor that also collects SASL token metrics, standing in
// for the application's Prometheus monitor. The generated MockMonitorInterface only
// implements monitoring.MonitorInterface, so it covers the opposite case.
type tokenMetricMonitor struct {
	monitoring.MonitorInterface
}

func (tokenMetricMonitor) IncKafkaOAuthTokenRefresh(string) error { return nil }
func (tokenMetricMonitor) SetKafkaOAuthTokenExpiry(float64) error { return nil }

var _ sasl.TokenMetricsInterface = tokenMetricMonitor{}

// TestTokenMetrics asserts that a monitor collecting SASL token metrics is passed
// through, and that one which does not yields nil so the token source falls back to its
// own no-op collector rather than receiving a half-implemented monitor.
func TestTokenMetrics(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	if got := tokenMetrics(tokenMetricMonitor{}); got == nil {
		t.Error("expected a monitor implementing the metrics interface to be passed through")
	}
	if got := tokenMetrics(NewMockMonitorInterface(ctrl)); got != nil {
		t.Errorf("expected nil for a monitor without token metrics, got %T", got)
	}
}
