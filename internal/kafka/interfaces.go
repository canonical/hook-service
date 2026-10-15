// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"context"

	v1 "github.com/canonical/authorization-service/api/v1"
)

// Operation represents a single permission update operation.
type Operation struct {
	Op       v1.PermissionOp
	Subject  string
	Relation string
	Object   string
}

// PermissionPublisherInterface publishes authorization permission updates to Kafka.
type PermissionPublisherInterface interface {
	PublishWrite(ctx context.Context, subject, relation, object string) error
	PublishDelete(ctx context.Context, subject, relation, object string) error
	PublishOperations(ctx context.Context, ops ...Operation) error
	Ping(ctx context.Context) error
	Close() error
}
