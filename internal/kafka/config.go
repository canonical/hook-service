// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"cmp"
	"fmt"

	kafkago "github.com/segmentio/kafka-go"

	"github.com/canonical/hook-service/internal/kafka/sasl"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

// AuthType selects the SASL mechanism used on broker connections.
type AuthType string

// Supported SASL mechanisms.
const (
	AuthTypeNone        AuthType = "none"
	AuthTypeOAuthBearer AuthType = "oauthbearer"
)

// DefaultOAuthAudience is the audience claim requested when none is configured.
const DefaultOAuthAudience = "kafka"

// Config describes everything needed to publish permission events. The caller maps its
// own environment schema onto this struct, which keeps this package independent of how
// the application sources configuration.
//
// Brokers is expected to contain only usable addresses and AuthType to be already
// normalized: parsing operator input is the configuration layer's responsibility, and
// an empty Brokers is the single signal that selects the no-op publisher.
//
// ServiceName and Topic are both required and must agree. This package holds no
// deployment-specific default for either, because substituting one would silently
// attribute a deployment's events to another service; the caller derives both from one
// federated service name.
type Config struct {
	Brokers     []string
	ServiceName string
	Topic       string

	AuthType AuthType

	OAuthTokenURL     string
	OAuthClientID     string
	OAuthClientSecret string
	OAuthScopes       []string
	OAuthAudience     string
	OAuthCAFile       string

	TLSEnabled            bool
	TLSCAFile             string
	TLSInsecureSkipVerify bool
}

// NewPublisher returns the publisher implied by cfg: a PermissionPublisher when brokers
// are configured, a NoopPublisher otherwise. The returned publisher owns every resource
// it creates, including the transport, and releases them on Close.
func NewPublisher(
	cfg *Config,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) (PermissionPublisherInterface, error) {
	if len(cfg.Brokers) == 0 {
		return NewNoopPublisher(tracer, monitor, logger), nil
	}

	transport, err := newTransport(cfg, tracer, monitor, logger)
	if err != nil {
		return nil, err
	}

	writer := NewKafkaWriter(cfg.Brokers, cfg.Topic, transport)
	return newPublisher(writer, cfg.ServiceName, cfg.Topic, transport, tracer, monitor, logger), nil
}

// newTransport builds the transport implied by cfg, or nil when neither TLS nor SASL is
// configured, in which case kafka-go's default plain TCP transport applies.
func newTransport(
	cfg *Config,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) (*kafkago.Transport, error) {
	if !cfg.TLSEnabled && cfg.AuthType != AuthTypeOAuthBearer {
		return nil, nil
	}

	transport := &kafkago.Transport{
		IdleTimeout: DefaultTransportIdleTimeout,
	}

	if cfg.TLSEnabled {
		tlsConfig, err := BuildBrokerTLSConfig(cfg.TLSCAFile, cfg.TLSInsecureSkipVerify)
		if err != nil {
			return nil, fmt.Errorf("invalid kafka broker TLS configuration: %w", err)
		}
		transport.TLS = tlsConfig
	}

	if cfg.AuthType == AuthTypeOAuthBearer {
		oauthClient, err := sasl.NewOAuthHTTPClient(cfg.OAuthCAFile)
		if err != nil {
			return nil, fmt.Errorf("invalid kafka oauth HTTP client configuration: %w", err)
		}

		tokenSource := sasl.NewTokenSource(sasl.TokenSourceConfig{
			TokenURL:     cfg.OAuthTokenURL,
			ClientID:     cfg.OAuthClientID,
			ClientSecret: cfg.OAuthClientSecret,
			Scopes:       cfg.OAuthScopes,
			Audience:     cmp.Or(cfg.OAuthAudience, DefaultOAuthAudience),
			HTTPClient:   oauthClient,
		}, tracer, tokenMetrics(monitor), logger)
		transport.SASL = sasl.NewMechanism(tokenSource)
	}

	return transport, nil
}

// tokenMetrics adapts monitor to the SASL token metrics interface, returning nil when
// it does not collect them so the token source falls back to its no-op collector.
func tokenMetrics(monitor monitoring.MonitorInterface) sasl.TokenMetricsInterface {
	if tm, ok := monitor.(sasl.TokenMetricsInterface); ok {
		return tm
	}
	return nil
}
