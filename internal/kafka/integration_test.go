// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka_test

import (
	"context"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	tc_kafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	trace "go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	v1 "github.com/canonical/hook-service/gen/authorization/service/api/v1"
	"github.com/canonical/hook-service/internal/kafka"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

type noopTracer struct{}

func (n *noopTracer) Start(ctx context.Context, _ string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	return ctx, trace.SpanFromContext(ctx)
}

type noopMonitor struct{}

func (m *noopMonitor) GetService() string {
	return "hook-service"
}

func (m *noopMonitor) SetResponseTimeMetric(_ map[string]string, _ float64) error {
	return nil
}

func (m *noopMonitor) SetDependencyAvailability(_ map[string]string, _ float64) error {
	return nil
}

var _ tracing.TracingInterface = (*noopTracer)(nil)
var _ monitoring.MonitorInterface = (*noopMonitor)(nil)


func setupKafkaContainer(t *testing.T) (string, func()) {
	t.Helper()
	ctx := context.Background()

	var kafkaContainer *tc_kafka.KafkaContainer
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Skipf("Skipping: Docker not available (%v)", r)
			}
		}()
		var err error
		kafkaContainer, err = tc_kafka.Run(ctx, "confluentinc/confluent-local:7.5.0")
		if err != nil {
			t.Skipf("Skipping: Docker/Kafka container failed to start: %v", err)
		}
	}()

	if kafkaContainer == nil {
		t.Skip("Skipping: Kafka container is nil")
	}

	brokers, err := kafkaContainer.Brokers(ctx)
	if err != nil || len(brokers) == 0 {
		_ = kafkaContainer.Terminate(ctx)
		t.Skipf("Skipping: failed to get Kafka brokers: %v", err)
	}

	client := &kafkago.Client{
		Addr:    kafkago.TCP(brokers[0]),
		Timeout: 5 * time.Second,
	}
	_, _ = client.CreateTopics(ctx, &kafkago.CreateTopicsRequest{
		Topics: []kafkago.TopicConfig{
			{
				Topic:             kafka.DefaultPermissionsTopic,
				NumPartitions:     1,
				ReplicationFactor: 1,
			},
		},
	})

	cleanup := func() {
		_ = kafkaContainer.Terminate(context.Background())
	}

	return brokers[0], cleanup
}


func TestKafkaIntegration_PublishAndConsume(t *testing.T) {
	broker, cleanup := setupKafkaContainer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	topic := kafka.DefaultPermissionsTopic
	writer := kafka.NewKafkaWriter([]string{broker}, topic)
	logger := logging.NewLogger("debug")
	tracer := &noopTracer{}
	monitor := &noopMonitor{}

	publisher := kafka.NewPermissionPublisher(writer, tracer, monitor, logger)
	defer func() { _ = publisher.Close() }()

	// 1. Test PublishWrite
	if err := publisher.PublishWrite(ctx, "user:alice", "owner", "group-in-claim:grp-1"); err != nil {
		t.Fatalf("failed to publish write event: %v", err)
	}

	// 2. Test PublishDelete
	if err := publisher.PublishDelete(ctx, "user:bob", "member", "group-in-claim:grp-1"); err != nil {
		t.Fatalf("failed to publish delete event: %v", err)
	}

	// 3. Read messages using kafka reader
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     []string{broker},
		Topic:       topic,
		Partition:   0,
		MinBytes:    1,
		MaxBytes:    10e6,
		MaxWait:     500 * time.Millisecond,
		StartOffset: kafkago.FirstOffset,
	})
	defer func() { _ = reader.Close() }()

	// Read message 1 (Write)
	msg1, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("failed to read msg1: %v", err)
	}
	expectedKey1 := "group-in-claim:grp-1"
	if string(msg1.Key) != expectedKey1 {
		t.Errorf("msg1 key = %q, want %q", string(msg1.Key), expectedKey1)
	}

	var env1 v1.PermissionUpdateEnvelope
	if err := proto.Unmarshal(msg1.Value, &env1); err != nil {
		t.Fatalf("failed to unmarshal proto msg1: %v", err)
	}
	expectedIdempotencyKey1 := "user:alice:owner:group-in-claim:grp-1:write:" + env1.MessageId
	if env1.IdempotencyKey != expectedIdempotencyKey1 {
		t.Errorf("env1 idempotency_key = %q, want %q", env1.IdempotencyKey, expectedIdempotencyKey1)
	}
	if len(env1.Operations) != 1 {
		t.Fatalf("msg1 operations count = %d, want 1", len(env1.Operations))
	}
	op1 := env1.Operations[0]
	if op1.Op != v1.PermissionOp_PERMISSION_OP_WRITE {
		t.Errorf("msg1 op = %v, want %v", op1.Op, v1.PermissionOp_PERMISSION_OP_WRITE)
	}
	if op1.Subject != "user:alice" || op1.Relation != "owner" || op1.Object != "group-in-claim:grp-1" {
		t.Errorf("msg1 op tuple = %s %s %s, want user:alice owner group-in-claim:grp-1", op1.Subject, op1.Relation, op1.Object)
	}
	if env1.Version != "1" {
		t.Errorf("msg1 version = %s, want 1", env1.Version)
	}
	if env1.Service != "hook-service" {
		t.Errorf("msg1 service = %s, want hook-service", env1.Service)
	}

	// Read message 2 (Delete)
	msg2, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("failed to read msg2: %v", err)
	}
	expectedKey2 := "group-in-claim:grp-1"
	if string(msg2.Key) != expectedKey2 {
		t.Errorf("msg2 key = %q, want %q", string(msg2.Key), expectedKey2)
	}

	var env2 v1.PermissionUpdateEnvelope
	if err := proto.Unmarshal(msg2.Value, &env2); err != nil {
		t.Fatalf("failed to unmarshal proto msg2: %v", err)
	}
	expectedIdempotencyKey2 := "user:bob:member:group-in-claim:grp-1:delete:" + env2.MessageId
	if env2.IdempotencyKey != expectedIdempotencyKey2 {
		t.Errorf("env2 idempotency_key = %q, want %q", env2.IdempotencyKey, expectedIdempotencyKey2)
	}


	if len(env2.Operations) != 1 {
		t.Fatalf("msg2 operations count = %d, want 1", len(env2.Operations))
	}
	op2 := env2.Operations[0]
	if op2.Op != v1.PermissionOp_PERMISSION_OP_DELETE {
		t.Errorf("msg2 op = %v, want %v", op2.Op, v1.PermissionOp_PERMISSION_OP_DELETE)
	}
	if op2.Subject != "user:bob" || op2.Relation != "member" || op2.Object != "group-in-claim:grp-1" {
		t.Errorf("msg2 op tuple = %s %s %s, want user:bob member group-in-claim:grp-1", op2.Subject, op2.Relation, op2.Object)
	}

	// 3. Test PublishOperations (batch operations)
	if err := publisher.PublishOperations(ctx,
		kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:carol", Relation: "member", Object: "group-in-claim:grp-1"},
		kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:dave", Relation: "member", Object: "group-in-claim:grp-1"},
	); err != nil {
		t.Fatalf("failed to publish batch operations: %v", err)
	}

	// Read message 3 (Batch Operations)
	msg3, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("failed to read msg3: %v", err)
	}
	expectedKey3 := "group-in-claim:grp-1"
	if string(msg3.Key) != expectedKey3 {
		t.Errorf("msg3 key = %q, want %q", string(msg3.Key), expectedKey3)
	}

	var env3 v1.PermissionUpdateEnvelope
	if err := proto.Unmarshal(msg3.Value, &env3); err != nil {
		t.Fatalf("failed to unmarshal proto msg3: %v", err)
	}
	if len(env3.Operations) != 2 {
		t.Fatalf("msg3 operations count = %d, want 2", len(env3.Operations))
	}
	if env3.Operations[0].Op != v1.PermissionOp_PERMISSION_OP_WRITE || env3.Operations[0].Subject != "user:carol" {
		t.Errorf("unexpected op 0 in batch: %+v", env3.Operations[0])
	}
	if env3.Operations[1].Op != v1.PermissionOp_PERMISSION_OP_DELETE || env3.Operations[1].Subject != "user:dave" {
		t.Errorf("unexpected op 1 in batch: %+v", env3.Operations[1])
	}
}

