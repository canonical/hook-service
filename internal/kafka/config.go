// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net"
	"time"

	"github.com/twmb/franz-go/pkg/kerr"
	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl/oauth"

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

// DefaultRecordDeliveryTimeout bounds how long a buffered record can wait for delivery before timing out.
const DefaultRecordDeliveryTimeout = 30 * time.Second

// DefaultMaxBufferedRecords bounds the maximum number of buffered records in memory.
const DefaultMaxBufferedRecords = 10000

// DefaultCloseFlushTimeout bounds how long Close waits to flush buffered records.
const DefaultCloseFlushTimeout = 5 * time.Second

// maxRecordRetries bounds delivery retries for produced records.
const maxRecordRetries = 10

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

// brokerConnectHook invalidates cached OAuth tokens when the broker rejects SASL authentication.
type brokerConnectHook struct {
	tokenSource sasl.TokenProvider
}

// OnBrokerConnect implements kgo.HookBrokerConnect.
func (h *brokerConnectHook) OnBrokerConnect(_ kgo.BrokerMetadata, _ time.Duration, _ net.Conn, err error) {
	if errors.Is(err, kerr.SaslAuthenticationFailed) {
		h.tokenSource.Invalidate()
	}
}

// NewPublisher returns the publisher implied by cfg: a PermissionPublisher when brokers
// are configured, a NoopPublisher otherwise. The returned publisher owns every resource
// it creates and releases them on Close.
func NewPublisher(
	cfg *Config,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) (PermissionPublisherInterface, error) {
	if len(cfg.Brokers) == 0 {
		return NewNoopPublisher(tracer, monitor, logger), nil
	}

	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.RequiredAcks(kgo.NoAck()),
		kgo.DisableIdempotentWrite(),
		kgo.RecordRetries(maxRecordRetries),
		kgo.RecordDeliveryTimeout(DefaultRecordDeliveryTimeout),
		kgo.MaxBufferedRecords(DefaultMaxBufferedRecords),
	}

	if cfg.TLSEnabled {
		tlsConfig, err := BuildBrokerTLSConfig(cfg.TLSCAFile, cfg.TLSInsecureSkipVerify)
		if err != nil {
			return nil, fmt.Errorf("invalid kafka broker TLS configuration: %v", err)
		}
		opts = append(opts, kgo.DialTLSConfig(tlsConfig))
	}

	if cfg.AuthType == AuthTypeOAuthBearer {
		oauthClient, err := sasl.NewOAuthHTTPClient(cfg.OAuthCAFile)
		if err != nil {
			return nil, fmt.Errorf("invalid kafka oauth HTTP client configuration: %v", err)
		}

		tokenSource := sasl.NewTokenSource(sasl.TokenSourceConfig{
			TokenURL:     cfg.OAuthTokenURL,
			ClientID:     cfg.OAuthClientID,
			ClientSecret: cfg.OAuthClientSecret,
			Scopes:       cfg.OAuthScopes,
			Audience:     cmp.Or(cfg.OAuthAudience, DefaultOAuthAudience),
			HTTPClient:   oauthClient,
		}, tracer, tokenMetrics(monitor), logger)

		saslMech := oauth.Oauth(func(ctx context.Context) (oauth.Auth, error) {
			token, err := tokenSource.Token(ctx)
			if err != nil {
				return oauth.Auth{}, err
			}
			return oauth.Auth{Token: token}, nil
		})

		opts = append(opts,
			kgo.SASL(saslMech),
			kgo.WithHooks(&brokerConnectHook{tokenSource: tokenSource}),
		)
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("failed to create kafka client: %v", err)
	}

	return NewPermissionPublisher(client, cfg.ServiceName, cfg.Topic, tracer, monitor, logger), nil
}

// tokenMetrics adapts monitor to the SASL token metrics interface, returning nil when
// it does not collect them so the token source falls back to its no-op collector.
func tokenMetrics(monitor monitoring.MonitorInterface) sasl.TokenMetricsInterface {
	if tm, ok := monitor.(sasl.TokenMetricsInterface); ok {
		return tm
	}
	return nil
}
