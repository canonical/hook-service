// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"go.opentelemetry.io/otel/trace/noop"
	"go.uber.org/mock/gomock"
	"google.golang.org/protobuf/proto"

	v1 "github.com/canonical/hook-service/gen/authorization/service/api/v1"
)

//go:generate mockgen -build_flags=--mod=mod -package kafka -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package kafka -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package kafka -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package kafka -destination ./mock_kafka.go -source=./interfaces.go

func TestPermissionPublisher_PublishWrite(t *testing.T) {
	tests := []struct {
		name        string
		subject     string
		relation    string
		object      string
		mockWriter  func(*MockKafkaWriterInterface)
		mockMonitor func(*MockMonitorInterface)
		wantErr     bool
		errMsg      string
	}{
		{
			name:     "successful publish write",
			subject:  "user:user-123",
			relation: "owner",
			object:   "group-in-claim:grp-456",
			mockWriter: func(w *MockKafkaWriterInterface) {
				w.EXPECT().WriteMessages(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, msgs ...kafkago.Message) error {
					if len(msgs) != 1 {
						t.Fatalf("expected 1 message, got %d", len(msgs))
					}
					msg := msgs[0]
					if string(msg.Key) != "group-in-claim:grp-456" {
						t.Errorf("expected msg key 'group-in-claim:grp-456', got %q", string(msg.Key))
					}
					var env v1.PermissionUpdateEnvelope
					if err := proto.Unmarshal(msg.Value, &env); err != nil {
						t.Fatalf("failed to unmarshal proto envelope: %v", err)
					}
					if env.GetVersion() != "1" {
						t.Errorf("expected version '1', got %q", env.GetVersion())
					}
					if env.GetService() != "hook-service" {
						t.Errorf("expected service 'hook-service', got %q", env.GetService())
					}
					if env.GetMessageId() == "" {
						t.Errorf("expected non-empty message_id")
					}
					expectedPrefix := "user:user-123:owner:group-in-claim:grp-456:write:"
					if !strings.HasPrefix(env.GetIdempotencyKey(), expectedPrefix) {
						t.Errorf("expected idempotency_key starting with %q, got %q", expectedPrefix, env.GetIdempotencyKey())
					}
					if !strings.HasSuffix(env.GetIdempotencyKey(), env.GetMessageId()) {
						t.Errorf("expected idempotency_key ending with message_id %q, got %q", env.GetMessageId(), env.GetIdempotencyKey())
					}
					if env.GetEventTime() == nil {
						t.Errorf("expected non-nil event_time")
					}
					if len(env.GetOperations()) != 1 {
						t.Fatalf("expected 1 operation, got %d", len(env.GetOperations()))
					}
					op := env.GetOperations()[0]
					if op.GetOp() != v1.PermissionOp_PERMISSION_OP_WRITE {
						t.Errorf("expected PERMISSION_OP_WRITE, got %v", op.GetOp())
					}
					if op.GetSubject() != "user:user-123" {
						t.Errorf("expected subject 'user:user-123', got %q", op.GetSubject())
					}
					if op.GetRelation() != "owner" {
						t.Errorf("expected relation 'owner', got %q", op.GetRelation())
					}
					if op.GetObject() != "group-in-claim:grp-456" {
						t.Errorf("expected object 'group-in-claim:grp-456', got %q", op.GetObject())
					}
					return nil
				})
			},
			mockMonitor: func(m *MockMonitorInterface) {
				m.EXPECT().SetDependencyAvailability(map[string]string{"component": "kafka"}, float64(1)).Return(nil)
			},
			wantErr: false,
		},
		{
			name:     "publish write failure from writer",
			subject:  "user:user-123",
			relation: "member",
			object:   "group-in-claim:grp-456",
			mockWriter: func(w *MockKafkaWriterInterface) {
				w.EXPECT().WriteMessages(gomock.Any(), gomock.Any()).Return(errors.New("kafka connection refused"))
			},
			mockMonitor: func(m *MockMonitorInterface) {
				m.EXPECT().SetDependencyAvailability(map[string]string{"component": "kafka"}, float64(0)).Return(nil)
			},
			wantErr: true,
			errMsg:  "failed to publish permission operations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockWriter := NewMockKafkaWriterInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)

			noopTracer := noop.NewTracerProvider().Tracer("test")
			mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
				DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
					return noopTracer.Start(ctx, spanName)
				})

			if tt.mockWriter != nil {
				tt.mockWriter(mockWriter)
			}
			if tt.mockMonitor != nil {
				tt.mockMonitor(mockMonitor)
			}

			publisher := NewPermissionPublisher(mockWriter, mockTracer, mockMonitor, mockLogger)
			err := publisher.PublishWrite(context.Background(), tt.subject, tt.relation, tt.object)

			if (err != nil) != tt.wantErr {
				t.Fatalf("PublishWrite() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
			}
		})
	}
}

func TestPermissionPublisher_PublishDelete(t *testing.T) {
	tests := []struct {
		name        string
		subject     string
		relation    string
		object      string
		mockWriter  func(*MockKafkaWriterInterface)
		mockMonitor func(*MockMonitorInterface)
		wantErr     bool
		errMsg      string
	}{
		{
			name:     "successful publish delete",
			subject:  "user:user-789",
			relation: "member",
			object:   "group-in-claim:grp-456",
			mockWriter: func(w *MockKafkaWriterInterface) {
				w.EXPECT().WriteMessages(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, msgs ...kafkago.Message) error {
					if len(msgs) != 1 {
						t.Fatalf("expected 1 message, got %d", len(msgs))
					}
					var env v1.PermissionUpdateEnvelope
					if err := proto.Unmarshal(msgs[0].Value, &env); err != nil {
						t.Fatalf("failed to unmarshal proto envelope: %v", err)
					}
					expectedPrefix := "user:user-789:member:group-in-claim:grp-456:delete:"
					if !strings.HasPrefix(env.GetIdempotencyKey(), expectedPrefix) {
						t.Errorf("expected idempotency_key starting with %q, got %q", expectedPrefix, env.GetIdempotencyKey())
					}
					if !strings.HasSuffix(env.GetIdempotencyKey(), env.GetMessageId()) {
						t.Errorf("expected idempotency_key ending with message_id %q, got %q", env.GetMessageId(), env.GetIdempotencyKey())
					}
					if len(env.GetOperations()) != 1 {
						t.Fatalf("expected 1 operation, got %d", len(env.GetOperations()))
					}
					op := env.GetOperations()[0]
					if op.GetOp() != v1.PermissionOp_PERMISSION_OP_DELETE {
						t.Errorf("expected PERMISSION_OP_DELETE, got %v", op.GetOp())
					}
					return nil
				})
			},
			mockMonitor: func(m *MockMonitorInterface) {
				m.EXPECT().SetDependencyAvailability(map[string]string{"component": "kafka"}, float64(1)).Return(nil)
			},
			wantErr: false,
		},
		{
			name:     "publish delete failure from writer",
			subject:  "user:user-789",
			relation: "member",
			object:   "group-in-claim:grp-456",
			mockWriter: func(w *MockKafkaWriterInterface) {
				w.EXPECT().WriteMessages(gomock.Any(), gomock.Any()).Return(errors.New("kafka timeout"))
			},
			mockMonitor: func(m *MockMonitorInterface) {
				m.EXPECT().SetDependencyAvailability(map[string]string{"component": "kafka"}, float64(0)).Return(nil)
			},
			wantErr: true,
			errMsg:  "failed to publish permission operations",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockWriter := NewMockKafkaWriterInterface(ctrl)
			mockTracer := NewMockTracingInterface(ctrl)
			mockMonitor := NewMockMonitorInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)

			noopTracer := noop.NewTracerProvider().Tracer("test")
			mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
				DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
					return noopTracer.Start(ctx, spanName)
				})

			if tt.mockWriter != nil {
				tt.mockWriter(mockWriter)
			}
			if tt.mockMonitor != nil {
				tt.mockMonitor(mockMonitor)
			}

			publisher := NewPermissionPublisher(mockWriter, mockTracer, mockMonitor, mockLogger)
			err := publisher.PublishDelete(context.Background(), tt.subject, tt.relation, tt.object)

			if (err != nil) != tt.wantErr {
				t.Fatalf("PublishDelete() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tt.wantErr && !strings.Contains(err.Error(), tt.errMsg) {
				t.Errorf("expected error containing %q, got %q", tt.errMsg, err.Error())
			}
		})
	}
}

func TestPermissionPublisher_PublishOperations(t *testing.T) {
	t.Run("empty ops is noop", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWriter := NewMockKafkaWriterInterface(ctrl)
		mockTracer := NewMockTracingInterface(ctrl)
		mockMonitor := NewMockMonitorInterface(ctrl)
		mockLogger := NewMockLoggerInterface(ctrl)

		publisher := NewPermissionPublisher(mockWriter, mockTracer, mockMonitor, mockLogger)
		err := publisher.PublishOperations(context.Background())
		if err != nil {
			t.Fatalf("expected nil error for empty ops, got %v", err)
		}
	})

	t.Run("batch operations in single envelope", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWriter := NewMockKafkaWriterInterface(ctrl)
		mockTracer := NewMockTracingInterface(ctrl)
		mockMonitor := NewMockMonitorInterface(ctrl)
		mockLogger := NewMockLoggerInterface(ctrl)

		noopTracer := noop.NewTracerProvider().Tracer("test")
		mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
			DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
				return noopTracer.Start(ctx, spanName)
			})

		mockWriter.EXPECT().WriteMessages(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, msgs ...kafkago.Message) error {
			if len(msgs) != 1 {
				t.Fatalf("expected 1 message, got %d", len(msgs))
			}
			var env v1.PermissionUpdateEnvelope
			if err := proto.Unmarshal(msgs[0].Value, &env); err != nil {
				t.Fatalf("failed to unmarshal proto envelope: %v", err)
			}
			if len(env.GetOperations()) != 2 {
				t.Fatalf("expected 2 operations, got %d", len(env.GetOperations()))
			}
			if env.Operations[0].Subject != "user:u1" || env.Operations[1].Subject != "user:u2" {
				t.Errorf("unexpected subjects in batch envelope: %+v", env.Operations)
			}
			if !strings.HasPrefix(env.GetIdempotencyKey(), "group-in-claim:grp-1:batch:") {
				t.Errorf("expected batch idempotency key prefix 'group-in-claim:grp-1:batch:', got %q", env.GetIdempotencyKey())
			}
			if !strings.HasSuffix(env.GetIdempotencyKey(), env.GetMessageId()) {
				t.Errorf("expected idempotency_key ending with message_id %q, got %q", env.GetMessageId(), env.GetIdempotencyKey())
			}
			return nil
		})

		mockMonitor.EXPECT().SetDependencyAvailability(map[string]string{"component": "kafka"}, float64(1)).Return(nil)

		publisher := NewPermissionPublisher(mockWriter, mockTracer, mockMonitor, mockLogger)
		err := publisher.PublishOperations(context.Background(),
			Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:u1", Relation: "member", Object: "group-in-claim:grp-1"},
			Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:u2", Relation: "member", Object: "group-in-claim:grp-1"},
		)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
	})

	t.Run("batch operations produce identical batch hash prefix regardless of input order and unique keys", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWriter := NewMockKafkaWriterInterface(ctrl)
		mockTracer := NewMockTracingInterface(ctrl)
		mockMonitor := NewMockMonitorInterface(ctrl)
		mockLogger := NewMockLoggerInterface(ctrl)

		noopTracer := noop.NewTracerProvider().Tracer("test")
		mockTracer.EXPECT().Start(gomock.Any(), "kafka.PermissionPublisher.PublishOperations", gomock.Any()).
			DoAndReturn(func(ctx context.Context, spanName string, opts ...any) (context.Context, any) {
				return noopTracer.Start(ctx, spanName)
			}).Times(2)

		var prefix1, prefix2, key1, key2 string
		mockWriter.EXPECT().WriteMessages(gomock.Any(), gomock.Any()).DoAndReturn(func(ctx context.Context, msgs ...kafkago.Message) error {
			var env v1.PermissionUpdateEnvelope
			_ = proto.Unmarshal(msgs[0].Value, &env)
			parts := strings.Split(env.GetIdempotencyKey(), ":")
			if len(parts) >= 4 {
				prefix := strings.Join(parts[:len(parts)-1], ":")
				if prefix1 == "" {
					prefix1 = prefix
					key1 = env.GetIdempotencyKey()
				} else {
					prefix2 = prefix
					key2 = env.GetIdempotencyKey()
				}
			}
			return nil
		}).Times(2)

		mockMonitor.EXPECT().SetDependencyAvailability(map[string]string{"component": "kafka"}, float64(1)).Return(nil).Times(2)

		publisher := NewPermissionPublisher(mockWriter, mockTracer, mockMonitor, mockLogger)
		op1 := Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:u1", Relation: "member", Object: "group-in-claim:grp-1"}
		op2 := Operation{Op: v1.PermissionOp_PERMISSION_OP_WRITE, Subject: "user:u2", Relation: "member", Object: "group-in-claim:grp-1"}

		if err := publisher.PublishOperations(context.Background(), op1, op2); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if err := publisher.PublishOperations(context.Background(), op2, op1); err != nil {
			t.Fatalf("unexpected error: %v", err)
		}

		if prefix1 != prefix2 {
			t.Fatalf("expected identical batch hash prefix regardless of op order, got %q vs %q", prefix1, prefix2)
		}
		if key1 == key2 {
			t.Fatalf("expected unique idempotency keys across distinct publish calls, got identical key %q", key1)
		}
	})
}

func TestPermissionPublisher_Close(t *testing.T) {
	t.Run("successful close", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWriter := NewMockKafkaWriterInterface(ctrl)
		mockWriter.EXPECT().Close().Return(nil)

		publisher := NewPermissionPublisher(mockWriter, nil, nil, nil)
		if err := publisher.Close(); err != nil {
			t.Fatalf("expected nil error on close, got: %v", err)
		}
	})

	t.Run("close error", func(t *testing.T) {
		ctrl := gomock.NewController(t)
		defer ctrl.Finish()

		mockWriter := NewMockKafkaWriterInterface(ctrl)
		mockWriter.EXPECT().Close().Return(errors.New("failed to close writer"))

		publisher := NewPermissionPublisher(mockWriter, nil, nil, nil)
		if err := publisher.Close(); err == nil {
			t.Fatalf("expected error on close, got nil")
		}
	})

	t.Run("nil writer close", func(t *testing.T) {
		publisher := NewPermissionPublisher(nil, nil, nil, nil)
		if err := publisher.Close(); err != nil {
			t.Fatalf("expected nil error on close with nil writer, got: %v", err)
		}
	})
}

func TestNewKafkaWriter(t *testing.T) {
	t.Run("default topic", func(t *testing.T) {
		writer := NewKafkaWriter([]string{"localhost:9092"}, "")
		if writer == nil {
			t.Fatal("expected non-nil kafka writer")
		}
		if writer.Topic != DefaultPermissionsTopic {
			t.Errorf("expected topic %q, got %q", DefaultPermissionsTopic, writer.Topic)
		}
		if writer.RequiredAcks != kafkago.RequireAll {
			t.Errorf("expected RequiredAcks RequireAll, got %v", writer.RequiredAcks)
		}
		if writer.MaxAttempts != 2 {
			t.Errorf("expected MaxAttempts 2, got %d", writer.MaxAttempts)
		}
		if _, ok := writer.Balancer.(*kafkago.Hash); !ok {
			t.Errorf("expected Balancer to be *kafkago.Hash, got %T", writer.Balancer)
		}
		if writer.WriteTimeout != 2*time.Second {
			t.Errorf("expected WriteTimeout 2s, got %v", writer.WriteTimeout)
		}
		if writer.BatchTimeout != 10*time.Millisecond {
			t.Errorf("expected BatchTimeout 10ms, got %v", writer.BatchTimeout)
		}
	})

	t.Run("custom topic", func(t *testing.T) {
		writer := NewKafkaWriter([]string{"localhost:9092"}, "custom.topic")
		if writer == nil {
			t.Fatal("expected non-nil kafka writer")
		}
		if writer.Topic != "custom.topic" {
			t.Errorf("expected topic 'custom.topic', got %q", writer.Topic)
		}
	})
}

func TestNoopPublisher(t *testing.T) {
	noop := NewNoopPublisher(nil, nil, nil)
	ctx := context.Background()

	if err := noop.PublishWrite(ctx, "user:1", "owner", "group-in-claim:1"); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if err := noop.PublishDelete(ctx, "user:1", "owner", "group-in-claim:1"); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
	if err := noop.Close(); err != nil {
		t.Errorf("expected nil error, got: %v", err)
	}
}
