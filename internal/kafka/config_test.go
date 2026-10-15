// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/mock/gomock"

	"github.com/canonical/hook-service/internal/kafka/sasl"
	"github.com/canonical/hook-service/internal/monitoring"
)

// newPublisherConfig returns a platform configuration that NewPublisher accepts, which
// individual cases narrow to the setting under test.
func newPublisherConfig(t *testing.T) *Config {
	t.Helper()
	return &Config{
		Brokers:     newFakeCluster(t),
		ServiceName: testServiceName,
		Topic:       testTopic,
		AuthType:    AuthTypeNone,
	}
}

// oauthPublisherConfig returns a configuration with complete OAUTHBEARER credentials.
func oauthPublisherConfig(t *testing.T) *Config {
	t.Helper()
	cfg := newPublisherConfig(t)
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
		cfgFunc     func(t *testing.T) *Config
		wantRealPub bool
	}{
		{
			name: "no brokers selects the noop publisher",
			cfgFunc: func(t *testing.T) *Config {
				return &Config{
					ServiceName: testServiceName,
					Topic:       testTopic,
				}
			},
			wantRealPub: false,
		},
		{
			name: "empty broker slice selects the noop publisher",
			cfgFunc: func(t *testing.T) *Config {
				return &Config{
					Brokers:     []string{},
					ServiceName: testServiceName,
					Topic:       testTopic,
				}
			},
			wantRealPub: false,
		},
		{
			name:        "a broker selects the permission publisher",
			cfgFunc:     newPublisherConfig,
			wantRealPub: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			cfg := tt.cfgFunc(t)
			publisher, err := NewPublisher(cfg, NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
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
	tlsWithSkipVerify := func(t *testing.T) *Config {
		cfg := oauthPublisherConfig(t)
		cfg.TLSEnabled = true
		cfg.TLSInsecureSkipVerify = true
		return cfg
	}

	oauthWithScopes := func(t *testing.T) *Config {
		cfg := oauthPublisherConfig(t)
		cfg.OAuthScopes = []string{"openid", "profile"}
		cfg.OAuthAudience = "custom-audience"
		return cfg
	}

	tests := []struct {
		name    string
		cfgFunc func(t *testing.T) *Config
	}{
		{name: "unauthenticated plain tcp", cfgFunc: newPublisherConfig},
		{name: "oauthbearer without tls", cfgFunc: oauthPublisherConfig},
		{name: "oauthbearer with scopes and custom audience", cfgFunc: oauthWithScopes},
		{name: "oauthbearer with tls and skip verify", cfgFunc: tlsWithSkipVerify},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			cfg := tt.cfgFunc(t)
			publisher, err := NewPublisher(cfg, NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
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
			if p.topic != cfg.Topic {
				t.Errorf("topic = %q, want %q", p.topic, cfg.Topic)
			}
			if p.serviceName != cfg.ServiceName {
				t.Errorf("serviceName = %q, want %q", p.serviceName, cfg.ServiceName)
			}
		})
	}
}

// TestNewPublisherRejectsUnreadableCertificates asserts that a transport the operator
// cannot have intended fails startup rather than silently falling back to weaker
// settings, and that no publisher is returned alongside the error.
func TestNewPublisherRejectsUnreadableCertificates(t *testing.T) {
	brokerCAMissing := func(t *testing.T) *Config {
		cfg := newPublisherConfig(t)
		cfg.TLSEnabled = true
		cfg.TLSCAFile = "/nonexistent/ca/path/cert.pem"
		return cfg
	}

	oauthCAMissing := func(t *testing.T) *Config {
		cfg := oauthPublisherConfig(t)
		cfg.OAuthCAFile = "/nonexistent/oauth/ca/cert.pem"
		return cfg
	}

	tests := []struct {
		name    string
		cfgFunc func(t *testing.T) *Config
		wantErr string
	}{
		{
			name:    "unreadable broker ca file",
			cfgFunc: brokerCAMissing,
			wantErr: "invalid kafka broker TLS configuration",
		},
		{
			name:    "unreadable oauth ca file",
			cfgFunc: oauthCAMissing,
			wantErr: "invalid kafka oauth HTTP client configuration",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			cfg := tt.cfgFunc(t)
			publisher, err := NewPublisher(cfg, NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
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

// TestNewPublisherClosesCleanly asserts that Close flushes and closes the client the publisher
// built, so the caller never has to track it, and that repeated calls stay safe.
func TestNewPublisherClosesCleanly(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	cfg := oauthPublisherConfig(t)
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
	if p.client == nil {
		t.Fatal("expected the publisher to own the client it built")
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

// TestNewPublisherPlainTCP asserts that a plain TCP publisher initializes a valid client
// and closes cleanly.
func TestNewPublisherPlainTCP(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	publisher, err := NewPublisher(newPublisherConfig(t), NewMockTracingInterface(ctrl), NewMockMonitorInterface(ctrl), NewMockLoggerInterface(ctrl))
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
	if p.client == nil {
		t.Fatal("expected initialized client for plain TCP")
	}
}

// mockTokenProvider is a stub TokenProvider for testing hooks.
type mockTokenProvider struct {
	invalidated bool
}

func (m *mockTokenProvider) Token(ctx context.Context) (string, error) {
	return "test-token", nil
}

func (m *mockTokenProvider) Invalidate() {
	m.invalidated = true
}

// TestBrokerConnectHookInvalidatesToken asserts that only SASL authentication errors trigger
// token invalidation on the TokenProvider so subsequent connections get a fresh token.
func TestBrokerConnectHookInvalidatesToken(t *testing.T) {
	tests := []struct {
		name            string
		err             error
		wantInvalidated bool
	}{
		{
			name:            "nil error does not invalidate",
			err:             nil,
			wantInvalidated: false,
		},
		{
			name:            "network connection error does not invalidate",
			err:             errors.New("connection refused"),
			wantInvalidated: false,
		},
		{
			name:            "direct sasl authentication error invalidates",
			err:             kerr.SaslAuthenticationFailed,
			wantInvalidated: true,
		},
		{
			name:            "wrapped sasl authentication error invalidates",
			err:             fmt.Errorf("broker handshake failed: %w", kerr.SaslAuthenticationFailed),
			wantInvalidated: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockTokenSource := &mockTokenProvider{}
			hook := &brokerConnectHook{tokenSource: mockTokenSource}

			hook.OnBrokerConnect(kgo.BrokerMetadata{}, time.Millisecond, nil, tt.err)
			if mockTokenSource.invalidated != tt.wantInvalidated {
				t.Errorf("invalidated = %v, want %v for err = %v", mockTokenSource.invalidated, tt.wantInvalidated, tt.err)
			}
		})
	}
}

// TestDefaultOAuthAudience asserts that the default audience is "kafka".
func TestDefaultOAuthAudience(t *testing.T) {
	if DefaultOAuthAudience != "kafka" {
		t.Errorf("DefaultOAuthAudience = %q, want %q", DefaultOAuthAudience, "kafka")
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
