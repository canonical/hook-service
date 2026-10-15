// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"

	v1 "github.com/canonical/authorization-service/api/v1"
	"github.com/google/uuid"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

// SchemaVersion is the envelope payload schema version.
const SchemaVersion = "1"

var _ PermissionPublisherInterface = (*PermissionPublisher)(nil)

// PermissionPublisher publishes permission updates to a Kafka topic using franz-go.
type PermissionPublisher struct {
	client      *kgo.Client
	serviceName string
	topic       string

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// NewPermissionPublisher constructs a new PermissionPublisher over a caller-supplied *kgo.Client.
// Both serviceName and topic are required and are used verbatim: this package holds no
// deployment-specific default, because substituting one would silently attribute a deployment's
// events to another service.
func NewPermissionPublisher(
	client *kgo.Client,
	serviceName string,
	topic string,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *PermissionPublisher {
	return &PermissionPublisher{
		client:      client,
		serviceName: serviceName,
		topic:       topic,
		tracer:      tracer,
		monitor:     monitor,
		logger:      logger,
	}
}

// buildEnvelope constructs the protobuf PermissionUpdateEnvelope and generates a unique
// idempotency key whose batch hash part is order-independent.
func buildEnvelope(serviceName string, ops []Operation) (*v1.PermissionUpdateEnvelope, string) {
	messageID := uuid.New().String()
	var idempotencyKey string
	if len(ops) == 1 {
		opName := "write"
		if ops[0].Op == v1.PermissionOp_PERMISSION_OP_DELETE {
			opName = "delete"
		}
		idempotencyKey = fmt.Sprintf("%s:%s:%s:%s:%s", ops[0].Subject, ops[0].Relation, ops[0].Object, opName, messageID)
	} else {
		opStrs := make([]string, len(ops))
		for i, op := range ops {
			opStrs[i] = fmt.Sprintf("%d:%s:%s:%s", op.Op, op.Subject, op.Relation, op.Object)
		}
		slices.Sort(opStrs)

		hasher := sha256.New()
		for _, s := range opStrs {
			hasher.Write([]byte(s + ";"))
		}
		hashHex := hex.EncodeToString(hasher.Sum(nil))
		idempotencyKey = fmt.Sprintf("%s:batch:%s:%s", ops[0].Object, hashHex[:16], messageID)
	}

	protoOps := make([]*v1.PermissionOperation, len(ops))
	for i, op := range ops {
		protoOps[i] = &v1.PermissionOperation{
			Op:       op.Op,
			Subject:  op.Subject,
			Relation: op.Relation,
			Object:   op.Object,
		}
	}

	envelope := &v1.PermissionUpdateEnvelope{
		Version:        SchemaVersion,
		Service:        serviceName,
		MessageId:      messageID,
		IdempotencyKey: idempotencyKey,
		EventTime:      timestamppb.Now(),
		Operations:     protoOps,
	}

	return envelope, ops[0].Object
}

// PublishOperations publishes one or more permission operations in a single envelope asynchronously.
// If ops is empty, it returns nil immediately without publishing.
func (p *PermissionPublisher) PublishOperations(ctx context.Context, ops ...Operation) error {
	if len(ops) == 0 {
		return nil
	}

	ctx, span := p.tracer.Start(ctx, "kafka.PermissionPublisher.PublishOperations")
	defer span.End()

	span.SetAttributes(
		attribute.String("messaging.system", "kafka"),
		attribute.String("messaging.destination", p.topic),
		attribute.String("messaging.operation", "publish"),
		attribute.Int("permission.operation_count", len(ops)),
	)

	err := p.publish(ctx, ops...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to publish permission operations")
		return fmt.Errorf("failed to publish permission operations: %v", err)
	}

	span.SetStatus(codes.Ok, "permission operations published")
	return nil
}

// PublishWrite publishes a write operation for the given subject, relation, and object tuple.
func (p *PermissionPublisher) PublishWrite(ctx context.Context, subject, relation, object string) error {
	return p.PublishOperations(ctx, Operation{
		Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
		Subject:  subject,
		Relation: relation,
		Object:   object,
	})
}

// PublishDelete publishes a delete operation for the given subject, relation, and object tuple.
func (p *PermissionPublisher) PublishDelete(ctx context.Context, subject, relation, object string) error {
	return p.PublishOperations(ctx, Operation{
		Op:       v1.PermissionOp_PERMISSION_OP_DELETE,
		Subject:  subject,
		Relation: relation,
		Object:   object,
	})
}

// publish marshals a PermissionUpdateEnvelope and enqueues it to franz-go for asynchronous dispatch.
func (p *PermissionPublisher) publish(ctx context.Context, ops ...Operation) error {
	if len(ops) == 0 {
		return nil
	}

	envelope, partitionKey := buildEnvelope(p.serviceName, ops)

	payload, err := proto.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal permission envelope: %v", err)
	}

	record := &kgo.Record{
		Topic: p.topic,
		Key:   []byte(partitionKey),
		Value: payload,
	}

	p.client.TryProduce(context.WithoutCancel(ctx), record, p.onDelivery)

	return nil
}

// onDelivery logs an error if an asynchronous record delivery fails.
func (p *PermissionPublisher) onDelivery(_ *kgo.Record, err error) {
	if err != nil {
		p.logger.Errorf("failed to deliver permission record: %v", err)
	}
}

// Ping verifies that Kafka brokers are reachable and can authenticate.
func (p *PermissionPublisher) Ping(ctx context.Context) error {
	return p.client.Ping(ctx)
}

// Close flushes any in-flight records bounded by DefaultCloseFlushTimeout and closes the franz-go client.
func (p *PermissionPublisher) Close() error {
	ctx, cancel := context.WithTimeout(context.Background(), DefaultCloseFlushTimeout)
	defer cancel()

	flushErr := p.client.Flush(ctx)
	p.client.Close()

	if flushErr != nil {
		return fmt.Errorf("failed to flush in-flight records on close: %v", flushErr)
	}
	return nil
}
