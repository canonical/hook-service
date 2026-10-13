// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sasl

import (
	"context"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/tracing"
)

// TracingInterface aliases the tracing interface for SASL components.
type TracingInterface = tracing.TracingInterface

// TokenMetricsInterface defines the metric collection methods required for SASL OAuth tokens.
type TokenMetricsInterface interface {
	IncKafkaOAuthTokenRefresh(status string) error
	SetKafkaOAuthTokenExpiry(seconds float64) error
}

// NoopTokenMetrics is a no-op implementation of TokenMetricsInterface.
type NoopTokenMetrics struct{}

func (NoopTokenMetrics) IncKafkaOAuthTokenRefresh(string) error  { return nil }
func (NoopTokenMetrics) SetKafkaOAuthTokenExpiry(float64) error { return nil }

var _ TokenMetricsInterface = NoopTokenMetrics{}

// LoggerInterface aliases the logging interface for SASL components.
type LoggerInterface = logging.LoggerInterface

// TokenProvider defines the interface for acquiring OAuth access tokens.
type TokenProvider interface {
	Token(ctx context.Context) (string, error)
	Invalidate()
}

// TokenFunc is a function adapter implementing TokenProvider.
type TokenFunc func(ctx context.Context) (string, error)

// Token calls the underlying token function.
func (f TokenFunc) Token(ctx context.Context) (string, error) {
	return f(ctx)
}

// Invalidate is a no-op implementation for TokenFunc.
func (f TokenFunc) Invalidate() {}
