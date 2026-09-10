// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"slices"
	"time"

	"github.com/google/uuid"
	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	v1 "github.com/canonical/hook-service/gen/authorization/service/api/v1"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

const (
	// DefaultPermissionsTopic is the default Kafka topic for permission updates.
	DefaultPermissionsTopic = "hook-service.permissions"
	// ServiceName is the service identifier published in envelopes.
	ServiceName = "hook-service"
	// SchemaVersion is the envelope payload schema version.
	SchemaVersion = "1"
)

var _ PermissionPublisherInterface = (*PermissionPublisher)(nil)

// PermissionPublisher publishes permission updates to a Kafka topic.
type PermissionPublisher struct {
	writer KafkaWriterInterface

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// NewKafkaWriter creates a configured kafka.Writer.
func NewKafkaWriter(brokers []string, topic string) *kafkago.Writer {
	if topic == "" {
		topic = DefaultPermissionsTopic
	}
	return &kafkago.Writer{
		Addr:                   kafkago.TCP(brokers...),
		Topic:                  topic,
		Balancer:               &kafkago.Hash{},
		RequiredAcks:           kafkago.RequireAll,
		MaxAttempts:            2,
		WriteTimeout:           2 * time.Second,
		BatchTimeout:           10 * time.Millisecond,
		AllowAutoTopicCreation: false,
	}
}

// NewPermissionPublisher constructs a new PermissionPublisher.
func NewPermissionPublisher(
	writer KafkaWriterInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) *PermissionPublisher {
	return &PermissionPublisher{
		writer:  writer,
		tracer:  tracer,
		monitor: monitor,
		logger:  logger,
	}
}

// PublishOperations publishes one or more permission operations in a single envelope.
// If ops is empty, it returns nil immediately without publishing.
func (p *PermissionPublisher) PublishOperations(ctx context.Context, ops ...Operation) error {
	if len(ops) == 0 {
		return nil
	}

	ctx, span := p.tracer.Start(ctx, "kafka.PermissionPublisher.PublishOperations")
	defer span.End()

	span.SetAttributes(
		attribute.String("messaging.system", "kafka"),
		attribute.String("messaging.destination", DefaultPermissionsTopic),
		attribute.String("messaging.operation", "publish"),
		attribute.Int("permission.operation_count", len(ops)),
	)

	err := p.publish(ctx, ops...)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "failed to publish permission operations")
		if p.monitor != nil {
			_ = p.monitor.SetDependencyAvailability(map[string]string{"component": "kafka"}, 0)
		}
		return fmt.Errorf("failed to publish permission operations: %v", err)
	}

	if p.monitor != nil {
		_ = p.monitor.SetDependencyAvailability(map[string]string{"component": "kafka"}, 1)
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

// publish marshals and sends a PermissionUpdateEnvelope message to Kafka.
func (p *PermissionPublisher) publish(ctx context.Context, ops ...Operation) error {
	if len(ops) == 0 {
		return nil
	}

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
		Service:        ServiceName,
		MessageId:      messageID,
		IdempotencyKey: idempotencyKey,
		EventTime:      timestamppb.Now(),
		Operations:     protoOps,
	}

	payload, err := proto.Marshal(envelope)
	if err != nil {
		return fmt.Errorf("failed to marshal permission envelope: %v", err)
	}

	msg := kafkago.Message{
		Key:   []byte(ops[0].Object),
		Value: payload,
		Time:  time.Now(),
	}

	if err := p.writer.WriteMessages(ctx, msg); err != nil {
		return fmt.Errorf("failed to write kafka message: %v", err)
	}

	return nil
}

// Close closes the underlying Kafka writer.
func (p *PermissionPublisher) Close() error {
	if p.writer != nil {
		return p.writer.Close()
	}
	return nil
}
