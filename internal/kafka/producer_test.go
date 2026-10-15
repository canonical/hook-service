// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	v1 "github.com/canonical/authorization-service/api/v1"
	"github.com/twmb/franz-go/pkg/kfake"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.opentelemetry.io/otel/attribute"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	"github.com/canonical/hook-service/internal/logging"
)

//go:generate mockgen -build_flags=--mod=mod -package kafka -destination ./mock_tracing_test.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package kafka -destination ./mock_monitor_test.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package kafka -destination ./mock_logger_test.go -source=../../internal/logging/interfaces.go

const (
	testServiceName = "hook-service"
	testTopic       = "permissions.hook-service"
)

// newFakeCluster starts an in-memory Kafka cluster for hermetic unit testing without containers.
func newFakeCluster(t *testing.T, extraTopics ...string) []string {
	t.Helper()
	opts := []kfake.Opt{kfake.NumBrokers(1), kfake.SeedTopics(3, testTopic)}
	for _, topic := range extraTopics {
		if topic != "" && topic != testTopic {
			opts = append(opts, kfake.SeedTopics(3, topic))
		}
	}
	c, err := kfake.NewCluster(opts...)
	if err != nil {
		t.Fatalf("failed to start kfake cluster: %v", err)
	}
	t.Cleanup(c.Close)
	return c.ListenAddrs()
}

func newTestClient(t *testing.T, brokers ...string) *kgo.Client {
	t.Helper()
	if len(brokers) == 0 {
		brokers = newFakeCluster(t)
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.NoAck()),
		kgo.DisableIdempotentWrite(),
		kgo.RecordRetries(maxRecordRetries),
		kgo.RecordDeliveryTimeout(DefaultRecordDeliveryTimeout),
		kgo.MaxBufferedRecords(DefaultMaxBufferedRecords),
	)
	if err != nil {
		t.Fatalf("failed to create test kgo.Client: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

func newTestConsumer(t *testing.T, brokers []string, topics ...string) *kgo.Client {
	t.Helper()
	if len(topics) == 0 {
		topics = []string{testTopic}
	}
	consumeParts := make(map[string]map[int32]kgo.Offset, len(topics))
	for _, top := range topics {
		consumeParts[top] = map[int32]kgo.Offset{
			0: kgo.NewOffset().AtStart(),
			1: kgo.NewOffset().AtStart(),
			2: kgo.NewOffset().AtStart(),
		}
	}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.ConsumePartitions(consumeParts),
	)
	if err != nil {
		t.Fatalf("failed to create test consumer: %v", err)
	}
	t.Cleanup(client.Close)
	return client
}

func pollTestRecords(t *testing.T, consumer *kgo.Client, expectedCount int) []*kgo.Record {
	t.Helper()
	var records []*kgo.Record
	deadline := time.Now().Add(5 * time.Second)
	for len(records) < expectedCount && time.Now().Before(deadline) {
		pollCtx, pollCancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
		fetches := consumer.PollFetches(pollCtx)
		pollCancel()
		if errs := fetches.Errors(); len(errs) > 0 {
			for _, fetchErr := range errs {
				if !errors.Is(fetchErr.Err, context.DeadlineExceeded) && !errors.Is(fetchErr.Err, context.Canceled) {
					t.Fatalf("consumer poll error: %v", fetchErr.Err)
				}
			}
		}
		fetches.EachRecord(func(rec *kgo.Record) {
			records = append(records, rec)
		})
	}
	if len(records) != expectedCount {
		t.Fatalf("expected %d records, got %d", expectedCount, len(records))
	}
	return records
}


func TestBuildEnvelope_SingleOperation(t *testing.T) {
	tests := []struct {
		name       string
		op         Operation
		wantOp     v1.PermissionOp
		wantPrefix string
		wantObj    string
	}{
		{
			name: "single write operation",
			op: Operation{
				Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
				Subject:  "user:user-123",
				Relation: "owner",
				Object:   "group-in-claim:grp-456",
			},
			wantOp:     v1.PermissionOp_PERMISSION_OP_WRITE,
			wantPrefix: "user:user-123:owner:group-in-claim:grp-456:write:",
			wantObj:    "group-in-claim:grp-456",
		},
		{
			name: "single delete operation",
			op: Operation{
				Op:       v1.PermissionOp_PERMISSION_OP_DELETE,
				Subject:  "user:user-789",
				Relation: "member",
				Object:   "group-in-claim:grp-456",
			},
			wantOp:     v1.PermissionOp_PERMISSION_OP_DELETE,
			wantPrefix: "user:user-789:member:group-in-claim:grp-456:delete:",
			wantObj:    "group-in-claim:grp-456",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			env, partitionKey := buildEnvelope(testServiceName, []Operation{tt.op})
			if env == nil {
				t.Fatal("expected non-nil envelope")
			}
			if partitionKey != tt.wantObj {
				t.Errorf("partitionKey = %q, want %q", partitionKey, tt.wantObj)
			}
			if env.GetVersion() != SchemaVersion {
				t.Errorf("version = %q, want %q", env.GetVersion(), SchemaVersion)
			}
			if env.GetService() != testServiceName {
				t.Errorf("service = %q, want %q", env.GetService(), testServiceName)
			}
			if env.GetMessageId() == "" {
				t.Error("expected non-empty message_id")
			}
			if !strings.HasPrefix(env.GetIdempotencyKey(), tt.wantPrefix) {
				t.Errorf("idempotency_key = %q, want prefix %q", env.GetIdempotencyKey(), tt.wantPrefix)
			}
			if !strings.HasSuffix(env.GetIdempotencyKey(), env.GetMessageId()) {
				t.Errorf("idempotency_key = %q, want suffix %q", env.GetIdempotencyKey(), env.GetMessageId())
			}
			if env.GetEventTime() == nil {
				t.Error("expected non-nil event_time")
			}
			if len(env.GetOperations()) != 1 {
				t.Fatalf("expected 1 operation, got %d", len(env.GetOperations()))
			}
			op := env.GetOperations()[0]
			if op.GetOp() != tt.wantOp {
				t.Errorf("op = %v, want %v", op.GetOp(), tt.wantOp)
			}
			if op.GetSubject() != tt.op.Subject {
				t.Errorf("subject = %q, want %q", op.GetSubject(), tt.op.Subject)
			}
			if op.GetRelation() != tt.op.Relation {
				t.Errorf("relation = %q, want %q", op.GetRelation(), tt.op.Relation)
			}
			if op.GetObject() != tt.op.Object {
				t.Errorf("object = %q, want %q", op.GetObject(), tt.op.Object)
			}
		})
	}
}

func TestBuildEnvelope_BatchOperations(t *testing.T) {
	op1 := Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:u1", Relation: "member", Object: "group-in-claim:grp-1"}
	op2 := Operation{Op: v1.PermissionOp_PERMISSION_OP_DELETE, Subject: "user:u2", Relation: "member", Object: "group-in-claim:grp-1"}

	env, partitionKey := buildEnvelope(testServiceName, []Operation{op1, op2})
	if env == nil {
		t.Fatal("expected non-nil envelope")
	}
	if partitionKey != "group-in-claim:grp-1" {
		t.Errorf("partitionKey = %q, want 'group-in-claim:grp-1'", partitionKey)
	}
	if len(env.GetOperations()) != 2 {
		t.Fatalf("expected 2 operations, got %d", len(env.GetOperations()))
	}
	if !strings.HasPrefix(env.GetIdempotencyKey(), "group-in-claim:grp-1:batch:") {
		t.Errorf("expected batch idempotency key prefix, got %q", env.GetIdempotencyKey())
	}
	if !strings.HasSuffix(env.GetIdempotencyKey(), env.GetMessageId()) {
		t.Errorf("idempotency_key = %q, want suffix %q", env.GetIdempotencyKey(), env.GetMessageId())
	}

	// Verify deterministic hashing regardless of input order
	env1, _ := buildEnvelope(testServiceName, []Operation{op1, op2})
	env2, _ := buildEnvelope(testServiceName, []Operation{op2, op1})

	parts1 := strings.Split(env1.GetIdempotencyKey(), ":")
	parts2 := strings.Split(env2.GetIdempotencyKey(), ":")
	if len(parts1) < 4 || len(parts2) < 4 {
		t.Fatalf("invalid idempotency key parts: %v, %v", parts1, parts2)
	}
	hashPrefix1 := strings.Join(parts1[:len(parts1)-1], ":")
	hashPrefix2 := strings.Join(parts2[:len(parts2)-1], ":")
	if hashPrefix1 != hashPrefix2 {
		t.Errorf("expected identical batch hash prefix regardless of op order, got %q vs %q", hashPrefix1, hashPrefix2)
	}
	if env1.GetIdempotencyKey() == env2.GetIdempotencyKey() {
		t.Errorf("expected unique idempotency keys across distinct envelopes, got identical key %q", env1.GetIdempotencyKey())
	}
}

func TestPermissionPublisher_PublishOperations(t *testing.T) {
	t.Run("empty ops returns nil immediately", func(t *testing.T) {
		client := newTestClient(t)
		publisher := NewPermissionPublisher(client, testServiceName, testTopic, nil, nil, logging.NewNoopLogger())
		err := publisher.PublishOperations(t.Context())
		if err != nil {
			t.Fatalf("expected nil error for empty ops, got %v", err)
		}
	})

	t.Run("successful publish write and delete with client", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		brokers := newFakeCluster(t)
		client := newTestClient(t, brokers...)
		consumer := newTestConsumer(t, brokers)

		mockTracer := NewMockTracingInterface(ctrl)
		mockLogger := NewMockLoggerInterface(ctrl)
		noopTracer := noop.NewTracerProvider().Tracer("test")
		mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
			DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
				return noopTracer.Start(ctx, spanName)
			}).Times(2)

		publisher := NewPermissionPublisher(client, testServiceName, testTopic, mockTracer, nil, mockLogger)

		if err := publisher.PublishWrite(t.Context(), "user:u1", "owner", "group:g1"); err != nil {
			t.Fatalf("PublishWrite error = %v", err)
		}

		if err := publisher.PublishDelete(t.Context(), "user:u2", "member", "group:g1"); err != nil {
			t.Fatalf("PublishDelete error = %v", err)
		}

		records := pollTestRecords(t, consumer, 2)

		// Assert Record 1 (Write)
		rec1 := records[0]
		if string(rec1.Key) != "group:g1" {
			t.Errorf("record 1 key = %q, want 'group:g1'", string(rec1.Key))
		}
		var env1 v1.PermissionUpdateEnvelope
		if err := proto.Unmarshal(rec1.Value, &env1); err != nil {
			t.Fatalf("failed to unmarshal record 1 payload: %v", err)
		}
		if env1.GetService() != testServiceName {
			t.Errorf("record 1 service = %q, want %q", env1.GetService(), testServiceName)
		}
		if env1.GetVersion() != SchemaVersion {
			t.Errorf("record 1 version = %q, want %q", env1.GetVersion(), SchemaVersion)
		}
		if len(env1.GetOperations()) != 1 {
			t.Fatalf("record 1 operations count = %d, want 1", len(env1.GetOperations()))
		}
		op1 := env1.GetOperations()[0]
		if op1.GetOp() != v1.PermissionOp_PERMISSION_OP_WRITE {
			t.Errorf("record 1 op = %v, want WRITE", op1.GetOp())
		}
		if op1.GetSubject() != "user:u1" || op1.GetRelation() != "owner" || op1.GetObject() != "group:g1" {
			t.Errorf("record 1 op details mismatch: subject=%q, relation=%q, object=%q", op1.GetSubject(), op1.GetRelation(), op1.GetObject())
		}

		// Assert Record 2 (Delete)
		rec2 := records[1]
		if string(rec2.Key) != "group:g1" {
			t.Errorf("record 2 key = %q, want 'group:g1'", string(rec2.Key))
		}
		var env2 v1.PermissionUpdateEnvelope
		if err := proto.Unmarshal(rec2.Value, &env2); err != nil {
			t.Fatalf("failed to unmarshal record 2 payload: %v", err)
		}
		if len(env2.GetOperations()) != 1 {
			t.Fatalf("record 2 operations count = %d, want 1", len(env2.GetOperations()))
		}
		op2 := env2.GetOperations()[0]
		if op2.GetOp() != v1.PermissionOp_PERMISSION_OP_DELETE {
			t.Errorf("record 2 op = %v, want DELETE", op2.GetOp())
		}
		if op2.GetSubject() != "user:u2" || op2.GetRelation() != "member" || op2.GetObject() != "group:g1" {
			t.Errorf("record 2 op details mismatch: subject=%q, relation=%q, object=%q", op2.GetSubject(), op2.GetRelation(), op2.GetObject())
		}
	})
}

func TestPermissionPublisher_PartitionDeterminism(t *testing.T) {
	ctrl := gomock.NewController(t)

	brokers := newFakeCluster(t)
	client := newTestClient(t, brokers...)
	consumer := newTestConsumer(t, brokers)

	mockTracer := NewMockTracingInterface(ctrl)
	noopTracer := noop.NewTracerProvider().Tracer("test")
	mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
		DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
			return noopTracer.Start(ctx, spanName)
		}).AnyTimes()

	mockLogger := NewMockLoggerInterface(ctrl)
	publisher := NewPermissionPublisher(client, testServiceName, testTopic, mockTracer, nil, mockLogger)

	const count = 5
	for i := 0; i < count; i++ {
		if err := publisher.PublishWrite(t.Context(), "user:alpha", "member", "group:alpha"); err != nil {
			t.Fatalf("failed to publish alpha record %d: %v", i, err)
		}
		if err := publisher.PublishWrite(t.Context(), "user:beta", "member", "group:beta"); err != nil {
			t.Fatalf("failed to publish beta record %d: %v", i, err)
		}
	}

	records := pollTestRecords(t, consumer, count*2)

	var alphaPartition, betaPartition int32 = -1, -1
	for _, rec := range records {
		key := string(rec.Key)
		switch key {
		case "group:alpha":
			if alphaPartition == -1 {
				alphaPartition = rec.Partition
			} else if rec.Partition != alphaPartition {
				t.Errorf("record for group:alpha routed to partition %d, expected consistent partition %d", rec.Partition, alphaPartition)
			}
		case "group:beta":
			if betaPartition == -1 {
				betaPartition = rec.Partition
			} else if rec.Partition != betaPartition {
				t.Errorf("record for group:beta routed to partition %d, expected consistent partition %d", rec.Partition, betaPartition)
			}
		default:
			t.Errorf("unexpected record key: %q", key)
		}
	}
}

func TestPermissionPublisher_Close(t *testing.T) {
	t.Run("successful close with client", func(t *testing.T) {
		client := newTestClient(t)
		publisher := NewPermissionPublisher(client, testServiceName, testTopic, nil, nil, logging.NewNoopLogger())
		if err := publisher.Close(); err != nil {
			t.Fatalf("expected nil error on close, got: %v", err)
		}
	})

	t.Run("close flushes in-flight buffered records", func(t *testing.T) {
		ctrl := gomock.NewController(t)

		brokers := newFakeCluster(t)
		// ManualFlushing keeps records in buffer until Flush or Close is invoked.
		client, err := kgo.NewClient(
			kgo.SeedBrokers(brokers...),
			kgo.RequiredAcks(kgo.NoAck()),
			kgo.DisableIdempotentWrite(),
			kgo.RecordRetries(maxRecordRetries),
			kgo.ManualFlushing(),
		)
		if err != nil {
			t.Fatalf("failed to create client: %v", err)
		}

		consumer := newTestConsumer(t, brokers)

		mockTracer := NewMockTracingInterface(ctrl)
		mockLogger := NewMockLoggerInterface(ctrl)
		noopTracer := noop.NewTracerProvider().Tracer("test")
		mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
			DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
				return noopTracer.Start(ctx, spanName)
			})

		publisher := NewPermissionPublisher(client, testServiceName, testTopic, mockTracer, nil, mockLogger)
		if err := publisher.PublishWrite(t.Context(), "user:flush", "member", "group:flush"); err != nil {
			t.Fatalf("PublishWrite error = %v", err)
		}

		if err := publisher.Close(); err != nil {
			t.Fatalf("Close error = %v", err)
		}

		records := pollTestRecords(t, consumer, 1)
		if string(records[0].Key) != "group:flush" {
			t.Errorf("flushed record key = %q, want 'group:flush'", string(records[0].Key))
		}
	})
}

func TestPermissionPublisher_Ping(t *testing.T) {
	t.Run("noop publisher ping", func(t *testing.T) {
		noop := NewNoopPublisher(nil, nil, nil)
		if err := noop.Ping(t.Context()); err != nil {
			t.Fatalf("expected nil error on noop ping, got: %v", err)
		}
	})

	t.Run("successful client ping", func(t *testing.T) {
		client := newTestClient(t)
		publisher := NewPermissionPublisher(client, testServiceName, testTopic, nil, nil, logging.NewNoopLogger())
		if err := publisher.Ping(t.Context()); err != nil {
			t.Fatalf("expected nil error on live client ping, got: %v", err)
		}
	})
}

func TestPermissionPublisher_DeclaredServiceIdentity(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		wantService string
	}{
		{
			name:        "default identity",
			serviceName: testServiceName,
			wantService: "hook-service",
		},
		{
			name:        "portal identity",
			serviceName: "portal",
			wantService: "portal",
		},
		{
			name:        "empty identity",
			serviceName: "",
			wantService: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			op := Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:u1", Relation: "can_delete", Object: "group:g1"}
			env, _ := buildEnvelope(tt.serviceName, []Operation{op})
			if env.GetService() != tt.wantService {
				t.Errorf("service = %q, want %q", env.GetService(), tt.wantService)
			}
		})
	}
}

func TestPermissionPublisher_ReportsConfiguredDestination(t *testing.T) {
	tests := []struct {
		name            string
		topic           string
		wantDestination string
	}{
		{
			name:            "default destination",
			topic:           testTopic,
			wantDestination: "permissions.hook-service",
		},
		{
			name:            "portal destination",
			topic:           "permissions.portal",
			wantDestination: "permissions.portal",
		},
		{
			name:            "empty destination",
			topic:           "",
			wantDestination: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)

			client := newTestClient(t)
			mockTracer := NewMockTracingInterface(ctrl)

			recorder := tracetest.NewSpanRecorder()
			provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(recorder))
			sdkTracer := provider.Tracer("test")

			mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
				DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
					return sdkTracer.Start(ctx, spanName)
				})

			mockLogger := NewMockLoggerInterface(ctrl)
			mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).AnyTimes()
			publisher := NewPermissionPublisher(client, testServiceName, tt.topic, mockTracer, nil, mockLogger)
			if err := publisher.PublishWrite(t.Context(), "user:u1", "can_delete", "group:g1"); err != nil {
				t.Fatalf("PublishWrite error = %v", err)
			}

			ended := recorder.Ended()
			if len(ended) != 1 {
				t.Fatalf("expected 1 recorded span, got %d", len(ended))
			}

			attrs := ended[0].Attributes()
			i := slices.IndexFunc(attrs, func(kv attribute.KeyValue) bool {
				return kv.Key == "messaging.destination"
			})
			if i < 0 {
				t.Fatalf("messaging.destination attribute not set on span")
			}
			if got := attrs[i].Value.AsString(); got != tt.wantDestination {
				t.Errorf("messaging.destination = %q, want %q", got, tt.wantDestination)
			}
		})
	}
}

func TestNoopPublisher(t *testing.T) {
	noop := NewNoopPublisher(nil, nil, nil)
	ctx := t.Context()

	if err := noop.PublishWrite(ctx, "user:1", "owner", "group-in-claim:1"); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if err := noop.PublishDelete(ctx, "user:1", "owner", "group-in-claim:1"); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if err := noop.PublishOperations(ctx); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if err := noop.Close(); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}

// bufferedRecordHook captures the record passed to franz-go's internal buffer.
type bufferedRecordHook struct {
	buffered chan *kgo.Record
}

// OnProduceRecordBuffered implements kgo.HookProduceRecordBuffered.
func (h *bufferedRecordHook) OnProduceRecordBuffered(r *kgo.Record) {
	h.buffered <- r
}

func TestPermissionPublisher_ContextCancellationDoesNotAbortRecord(t *testing.T) {
	ctrl := gomock.NewController(t)

	brokers := newFakeCluster(t)
	consumer := newTestConsumer(t, brokers)
	hook := &bufferedRecordHook{buffered: make(chan *kgo.Record, 1)}
	client, err := kgo.NewClient(
		kgo.SeedBrokers(brokers...),
		kgo.RequiredAcks(kgo.NoAck()),
		kgo.DisableIdempotentWrite(),
		kgo.RecordRetries(maxRecordRetries),
		kgo.RecordDeliveryTimeout(DefaultRecordDeliveryTimeout),
		kgo.WithHooks(hook),
	)
	if err != nil {
		t.Fatalf("failed to create test kgo.Client: %v", err)
	}
	t.Cleanup(client.Close)

	mockTracer := NewMockTracingInterface(ctrl)
	noopTracer := noop.NewTracerProvider().Tracer("test")
	mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
		DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
			return noopTracer.Start(ctx, spanName)
		})

	mockLogger := NewMockLoggerInterface(ctrl)
	publisher := NewPermissionPublisher(client, testServiceName, testTopic, mockTracer, nil, mockLogger)

	ctx, cancel := context.WithCancel(t.Context())
	if err := publisher.PublishWrite(ctx, "user:u1", "owner", "group:g1"); err != nil {
		t.Fatalf("PublishWrite error = %v", err)
	}
	cancel()

	var rec *kgo.Record
	select {
	case rec = <-hook.buffered:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for record to be buffered")
	}

	if ctx.Err() == nil {
		t.Fatal("expected caller context to be canceled")
	}
	if rec.Context == nil {
		t.Fatal("expected record to have a context attached")
	}
	if err := rec.Context.Err(); err != nil {
		t.Fatalf("expected detached record context to NOT be canceled, got: %v", err)
	}

	delivered := pollTestRecords(t, consumer, 1)
	if string(delivered[0].Key) != "group:g1" {
		t.Errorf("delivered record key = %q, want 'group:g1'", string(delivered[0].Key))
	}
}

func TestPermissionPublisher_OnDelivery(t *testing.T) {
	ctrl := gomock.NewController(t)

	client := newTestClient(t)
	mockLogger := NewMockLoggerInterface(ctrl)
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).Times(1)

	publisher := NewPermissionPublisher(client, testServiceName, testTopic, nil, nil, mockLogger)
	publisher.onDelivery(nil, errors.New("delivery failed"))

	publisher.onDelivery(nil, nil)
}

func TestPermissionPublisher_DeliveryPromiseErrorLogged(t *testing.T) {
	ctrl := gomock.NewController(t)

	fakeCluster, err := kfake.NewCluster(kfake.NumBrokers(1), kfake.SeedTopics(1, testTopic))
	if err != nil {
		t.Fatalf("failed to start kfake: %v", err)
	}
	addrs := fakeCluster.ListenAddrs()
	fakeCluster.Close() // Immediately close cluster so connection fails

	deliveryErrorLogged := make(chan error, 1)
	mockLogger := NewMockLoggerInterface(ctrl)
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).
		Do(func(format string, args ...any) {
			if len(args) > 0 {
				if err, ok := args[0].(error); ok {
					select {
					case deliveryErrorLogged <- err:
					default:
					}
				}
			}
		}).AnyTimes()

	client, err := kgo.NewClient(
		kgo.SeedBrokers(addrs...),
		kgo.RequiredAcks(kgo.NoAck()),
		kgo.DisableIdempotentWrite(),
		kgo.RecordRetries(0),
		kgo.RecordDeliveryTimeout(1*time.Second),
	)
	if err != nil {
		t.Fatalf("failed to create client: %v", err)
	}
	t.Cleanup(client.Close)

	mockTracer := NewMockTracingInterface(ctrl)
	noopTracer := noop.NewTracerProvider().Tracer("test")
	mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
		DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
			return noopTracer.Start(ctx, spanName)
		}).AnyTimes()

	publisher := NewPermissionPublisher(client, testServiceName, testTopic, mockTracer, nil, mockLogger)

	if err := publisher.PublishWrite(t.Context(), "user:fail", "member", "group:fail"); err != nil {
		t.Fatalf("PublishWrite error = %v", err)
	}

	select {
	case err := <-deliveryErrorLogged:
		if err == nil {
			t.Fatal("expected non-nil delivery error logged")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for delivery error to be logged")
	}
}

func TestPermissionPublisher_TryProduce_BufferFullFailsFast(t *testing.T) {
	ctrl := gomock.NewController(t)

	client, err := kgo.NewClient(
		kgo.SeedBrokers(newFakeCluster(t)...),
		kgo.RequiredAcks(kgo.NoAck()),
		kgo.DisableIdempotentWrite(),
		kgo.RecordRetries(maxRecordRetries),
		kgo.MaxBufferedRecords(1),
		kgo.ManualFlushing(),
	)
	if err != nil {
		t.Fatalf("failed to create test kgo.Client: %v", err)
	}
	t.Cleanup(client.Close)

	maxBufferedLogged := make(chan struct{}, 1)
	mockLogger := NewMockLoggerInterface(ctrl)
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any()).
		Do(func(format string, args ...any) {
			if len(args) > 0 {
				if err, ok := args[0].(error); ok && errors.Is(err, kgo.ErrMaxBuffered) {
					select {
					case maxBufferedLogged <- struct{}{}:
					default:
					}
				}
			}
		}).AnyTimes()

	mockTracer := NewMockTracingInterface(ctrl)
	noopTracer := noop.NewTracerProvider().Tracer("test")
	mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
		DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
			return noopTracer.Start(ctx, spanName)
		}).AnyTimes()

	publisher := NewPermissionPublisher(client, testServiceName, testTopic, mockTracer, nil, mockLogger)

	// First record fills the buffer (limit is 1).
	ctx := t.Context()
	if err := publisher.PublishWrite(ctx, "user:1", "member", "group:1"); err != nil {
		t.Fatalf("first PublishWrite failed: %v", err)
	}

	// Second record immediately overflows the buffer. TryProduce must invoke onDelivery
	// with kgo.ErrMaxBuffered without blocking.
	if err := publisher.PublishWrite(ctx, "user:2", "member", "group:2"); err != nil {
		t.Fatalf("second PublishWrite failed: %v", err)
	}

	select {
	case <-maxBufferedLogged:
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for ErrMaxBuffered to be logged on buffer overflow")
	}
}
