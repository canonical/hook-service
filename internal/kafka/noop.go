// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"context"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

var _ PermissionPublisherInterface = (*NoopPublisher)(nil)

// NoopPublisher is a no-op implementation of PermissionPublisherInterface.
type NoopPublisher struct{}

// NewNoopPublisher creates a new NoopPublisher.
func NewNoopPublisher(
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *NoopPublisher {
	return &NoopPublisher{}
}

// PublishWrite does nothing and returns nil.
func (n *NoopPublisher) PublishWrite(ctx context.Context, subject, relation, object string) error {
	return nil
}

// PublishDelete does nothing and returns nil.
func (n *NoopPublisher) PublishDelete(ctx context.Context, subject, relation, object string) error {
	return nil
}

// PublishOperations does nothing and returns nil.
func (n *NoopPublisher) PublishOperations(ctx context.Context, ops ...Operation) error {
	return nil
}

// Close does nothing and returns nil.
func (n *NoopPublisher) Close() error {
	return nil
}
