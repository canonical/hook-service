// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka_test

import (
	"context"
	"fmt"
	"testing"
	"time"

	v1 "github.com/canonical/authorization-service/api/v1"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	trace "go.opentelemetry.io/otel/trace"
	"google.golang.org/protobuf/proto"

	"github.com/canonical/hook-service/internal/config"
	"github.com/canonical/hook-service/internal/kafka"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/testhelpers"
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

func createTopic(t *testing.T, broker, topic string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
	defer cancel()

	kClient, err := kgo.NewClient(kgo.SeedBrokers(broker))
	if err != nil {
		t.Fatalf("failed to create client for topic creation: %v", err)
	}
	defer kClient.Close()

	admin := kadm.NewClient(kClient)
	defer admin.Close()

	_, err = admin.CreateTopics(ctx, 1, 1, nil, topic)
	if err != nil {
		t.Fatalf("failed to create topic %q: %v", topic, err)
	}
}

func TestKafkaIntegration_PublishAndConsume(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	broker := testhelpers.SetupKafka(t)
	topic := config.PermissionsTopic("test-hook-service")
	createTopic(t, broker, topic)

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	logger := logging.NewLogger("debug")
	tracer := &noopTracer{}
	monitor := &noopMonitor{}

	cfg := &kafka.Config{
		Brokers:     []string{broker},
		ServiceName: "test-hook-service",
		Topic:       topic,
		AuthType:    kafka.AuthTypeNone,
	}

	publisher, err := kafka.NewPublisher(cfg, tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to construct publisher: %v", err)
	}

	testCases := []struct {
		name    string
		publish func(context.Context, kafka.PermissionPublisherInterface) error
		wantKey string
		wantOps []struct {
			op       v1.PermissionOp
			subject  string
			relation string
			object   string
		}
	}{
		{
			name: "single write operation",
			publish: func(ctx context.Context, p kafka.PermissionPublisherInterface) error {
				return p.PublishWrite(ctx, "user:alice", "owner", "group-in-claim:grp-1")
			},
			wantKey: "group-in-claim:grp-1",
			wantOps: []struct {
				op       v1.PermissionOp
				subject  string
				relation string
				object   string
			}{
				{op: v1.PermissionOp_PERMISSION_OP_WRITE, subject: "user:alice", relation: "owner", object: "group-in-claim:grp-1"},
			},
		},
		{
			name: "single delete operation",
			publish: func(ctx context.Context, p kafka.PermissionPublisherInterface) error {
				return p.PublishDelete(ctx, "user:bob", "member", "group-in-claim:grp-1")
			},
			wantKey: "group-in-claim:grp-1",
			wantOps: []struct {
				op       v1.PermissionOp
				subject  string
				relation string
				object   string
			}{
				{op: v1.PermissionOp_PERMISSION_OP_DELETE, subject: "user:bob", relation: "member", object: "group-in-claim:grp-1"},
			},
		},
		{
			name: "batch operations",
			publish: func(ctx context.Context, p kafka.PermissionPublisherInterface) error {
				return p.PublishOperations(ctx,
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:carol", Relation: "member", Object: "group-in-claim:grp-1"},
					kafka.Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:dave", Relation: "member", Object: "group-in-claim:grp-1"},
				)
			},
			wantKey: "group-in-claim:grp-1",
			wantOps: []struct {
				op       v1.PermissionOp
				subject  string
				relation string
				object   string
			}{
				{op: v1.PermissionOp_PERMISSION_OP_WRITE, subject: "user:carol", relation: "member", object: "group-in-claim:grp-1"},
				{op: v1.PermissionOp_PERMISSION_OP_DELETE, subject: "user:dave", relation: "member", object: "group-in-claim:grp-1"},
			},
		},
	}

	for _, tc := range testCases {
		if err := tc.publish(ctx, publisher); err != nil {
			t.Fatalf("[%s] failed to publish: %v", tc.name, err)
		}
	}

	// Verify that Close() flushes in-flight records cleanly before reading back
	if err := publisher.Close(); err != nil {
		t.Fatalf("failed to close and flush publisher: %v", err)
	}

	// Read messages using franz-go consumer
	consumer, err := kgo.NewClient(
		kgo.SeedBrokers(broker),
		kgo.ConsumePartitions(map[string]map[int32]kgo.Offset{
			topic: {0: kgo.NewOffset().AtStart()},
		}),
	)
	if err != nil {
		t.Fatalf("failed to create consumer client: %v", err)
	}
	defer consumer.Close()

	var records []*kgo.Record
	deadline := time.Now().Add(10 * time.Second)
	for len(records) < len(testCases) && time.Now().Before(deadline) {
		pollCtx, pollCancel := context.WithTimeout(ctx, 1*time.Second)
		fetches := consumer.PollFetches(pollCtx)
		pollCancel()
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, fetchErr := range errs {
				if fetchErr.Err != context.DeadlineExceeded && fetchErr.Err != context.Canceled {
					t.Fatalf("consumer poll error: %v", fetchErr.Err)
				}
			}
		}
		fetches.EachRecord(func(rec *kgo.Record) {
			records = append(records, rec)
		})
	}

	if len(records) != len(testCases) {
		t.Fatalf("expected %d records, got %d", len(testCases), len(records))
	}

	for i, tc := range testCases {
		rec := records[i]
		if string(rec.Key) != tc.wantKey {
			t.Errorf("[%s] key = %q, want %q", tc.name, string(rec.Key), tc.wantKey)
		}

		var env v1.PermissionUpdateEnvelope
		if err := proto.Unmarshal(rec.Value, &env); err != nil {
			t.Fatalf("[%s] failed to unmarshal proto envelope: %v", tc.name, err)
		}

		if env.Version != "1" {
			t.Errorf("[%s] version = %s, want 1", tc.name, env.Version)
		}
		if env.Service != "test-hook-service" {
			t.Errorf("[%s] service = %s, want test-hook-service", tc.name, env.Service)
		}
		if env.MessageId == "" {
			t.Errorf("[%s] expected non-empty message_id", tc.name)
		}
		if env.IdempotencyKey == "" {
			t.Errorf("[%s] expected non-empty idempotency_key", tc.name)
		}

		if len(tc.wantOps) == 1 {
			opName := "write"
			if tc.wantOps[0].op == v1.PermissionOp_PERMISSION_OP_DELETE {
				opName = "delete"
			}
			expectedIdempotencyKey := fmt.Sprintf("%s:%s:%s:%s:%s", tc.wantOps[0].subject, tc.wantOps[0].relation, tc.wantOps[0].object, opName, env.MessageId)
			if env.IdempotencyKey != expectedIdempotencyKey {
				t.Errorf("[%s] idempotency_key = %q, want %q", tc.name, env.IdempotencyKey, expectedIdempotencyKey)
			}
		}

		if len(env.Operations) != len(tc.wantOps) {
			t.Fatalf("[%s] operations count = %d, want %d", tc.name, len(env.Operations), len(tc.wantOps))
		}
		for j, wantOp := range tc.wantOps {
			gotOp := env.Operations[j]
			if gotOp.Op != wantOp.op {
				t.Errorf("[%s] op[%d] = %v, want %v", tc.name, j, gotOp.Op, wantOp.op)
			}
			if gotOp.Subject != wantOp.subject {
				t.Errorf("[%s] op[%d] subject = %q, want %q", tc.name, j, gotOp.Subject, wantOp.subject)
			}
			if gotOp.Relation != wantOp.relation {
				t.Errorf("[%s] op[%d] relation = %q, want %q", tc.name, j, gotOp.Relation, wantOp.relation)
			}
			if gotOp.Object != wantOp.object {
				t.Errorf("[%s] op[%d] object = %q, want %q", tc.name, j, gotOp.Object, wantOp.object)
			}
		}
	}
}
