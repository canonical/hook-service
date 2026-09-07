// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka_test

import (
	"context"
	"fmt"
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
	"github.com/canonical/hook-service/internal/types"
	"github.com/canonical/hook-service/pkg/authentication"
	"github.com/canonical/hook-service/pkg/groups"
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

type inMemoryDB struct {
	groups  map[string]*types.Group
	members map[string][]string
	owners  map[string][]string
}

func newInMemoryDB() *inMemoryDB {
	return &inMemoryDB{
		groups:  make(map[string]*types.Group),
		members: make(map[string][]string),
		owners:  make(map[string][]string),
	}
}

func (db *inMemoryDB) ListGroups(_ context.Context) ([]*types.Group, error) {
	var result []*types.Group
	for _, g := range db.groups {
		result = append(result, g)
	}
	return result, nil
}

func (db *inMemoryDB) CreateGroup(_ context.Context, g *types.Group) (*types.Group, error) {
	id := fmt.Sprintf("grp-%d", len(db.groups)+1)
	created := &types.Group{
		ID:          id,
		Name:        g.Name,
		TenantId:    g.TenantId,
		Description: g.Description,
		Type:        g.Type,
		CreatedAt:   time.Now(),
		UpdatedAt:   time.Now(),
	}
	db.groups[id] = created
	return created, nil
}

func (db *inMemoryDB) GetGroup(_ context.Context, id string) (*types.Group, error) {
	if g, ok := db.groups[id]; ok {
		return g, nil
	}
	return nil, fmt.Errorf("group not found")
}

func (db *inMemoryDB) UpdateGroup(_ context.Context, id string, g *types.Group) (*types.Group, error) {
	if existing, ok := db.groups[id]; ok {
		existing.Name = g.Name
		existing.Description = g.Description
		return existing, nil
	}
	return nil, fmt.Errorf("group not found")
}

func (db *inMemoryDB) DeleteGroup(_ context.Context, id string) error {
	delete(db.groups, id)
	delete(db.members, id)
	delete(db.owners, id)
	return nil
}

func (db *inMemoryDB) AddGroupOwner(_ context.Context, groupID, userID string) error {
	db.owners[groupID] = append(db.owners[groupID], userID)
	return nil
}

func (db *inMemoryDB) ListOwnersInGroup(_ context.Context, groupID string) ([]string, error) {
	return db.owners[groupID], nil
}

func (db *inMemoryDB) AddUsersToGroup(_ context.Context, groupID string, users []string) error {
	db.members[groupID] = append(db.members[groupID], users...)
	return nil
}

func (db *inMemoryDB) ListUsersInGroup(_ context.Context, groupID string) ([]string, error) {
	return db.members[groupID], nil
}

func (db *inMemoryDB) RemoveUsersFromGroup(_ context.Context, groupID string, users []string) error {
	current := db.members[groupID]
	var updated []string
	removeSet := make(map[string]bool)
	for _, u := range users {
		removeSet[u] = true
	}
	for _, u := range current {
		if !removeSet[u] {
			updated = append(updated, u)
		}
	}
	db.members[groupID] = updated
	return nil
}

func (db *inMemoryDB) GetGroupsForUser(_ context.Context, userID string) ([]*types.Group, error) {
	return nil, nil
}

func (db *inMemoryDB) UpdateGroupsForUser(_ context.Context, userID string, groupIDs []string) error {
	return nil
}

func (db *inMemoryDB) StreamGroupsForUser(_ context.Context, tenantID, userID string, fn func(*types.Group) error) error {
	return nil
}

func (db *inMemoryDB) StreamUsersInGroup(_ context.Context, tenantID, groupID string, fn func(string) error) error {
	return nil
}

type noopAuthorizer struct{}

func (a *noopAuthorizer) DeleteGroup(_ context.Context, _ string) error {
	return nil
}

var _ groups.DatabaseInterface = (*inMemoryDB)(nil)
var _ groups.AuthorizerInterface = (*noopAuthorizer)(nil)

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
}

func TestKafkaIntegration_GroupServicePipeline(t *testing.T) {
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

	memDB := newInMemoryDB()
	authz := &noopAuthorizer{}
	svc := groups.NewService(memDB, authz, publisher, tracer, monitor, logger)

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

	// 1. Create group with creator context -> publishes owner tuple
	userCtx := authentication.ContextWithUserID(ctx, "creator-456")
	grp, err := svc.CreateGroup(userCtx, &types.Group{Name: "engineering"})
	if err != nil {
		t.Fatalf("failed to create group: %v", err)
	}

	msg, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("failed to read create group message: %v", err)
	}
	var env v1.PermissionUpdateEnvelope
	if err := proto.Unmarshal(msg.Value, &env); err != nil {
		t.Fatalf("failed to unmarshal create group envelope: %v", err)
	}
	if env.Operations[0].Subject != "user:creator-456" || env.Operations[0].Relation != "owner" || env.Operations[0].Object != "group-in-claim:"+grp.ID {
		t.Errorf("unexpected create group tuple: %+v", env.Operations[0])
	}

	// 2. Add users -> publishes single batch envelope with member tuples
	if err := svc.AddUsersToGroup(ctx, grp.ID, []string{"user-alpha", "user-beta"}); err != nil {
		t.Fatalf("failed to add users: %v", err)
	}

	msgAdd, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("failed to read add users msg: %v", err)
	}
	var envAdd v1.PermissionUpdateEnvelope
	if err := proto.Unmarshal(msgAdd.Value, &envAdd); err != nil {
		t.Fatalf("failed to unmarshal add users envelope: %v", err)
	}
	if len(envAdd.Operations) != 2 {
		t.Fatalf("expected 2 operations in add users envelope, got %d", len(envAdd.Operations))
	}
	if envAdd.Operations[0].Subject != "user:user-alpha" || envAdd.Operations[0].Relation != "member" {
		t.Errorf("unexpected alpha tuple: %+v", envAdd.Operations[0])
	}
	if envAdd.Operations[1].Subject != "user:user-beta" || envAdd.Operations[1].Relation != "member" {
		t.Errorf("unexpected beta tuple: %+v", envAdd.Operations[1])
	}

	// 3. Remove user -> publishes delete member tuple
	if err := svc.RemoveUsersFromGroup(ctx, grp.ID, []string{"user-beta"}); err != nil {
		t.Fatalf("failed to remove user: %v", err)
	}

	msgRemove, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("failed to read remove msg: %v", err)
	}
	var envRemove v1.PermissionUpdateEnvelope
	if err := proto.Unmarshal(msgRemove.Value, &envRemove); err != nil {
		t.Fatalf("failed to unmarshal remove msg: %v", err)
	}
	if len(envRemove.Operations) != 1 {
		t.Fatalf("expected 1 operation in remove envelope, got %d", len(envRemove.Operations))
	}
	if envRemove.Operations[0].Op != v1.PermissionOp_PERMISSION_OP_DELETE || envRemove.Operations[0].Subject != "user:user-beta" {
		t.Errorf("unexpected remove tuple: %+v", envRemove.Operations[0])
	}

	// 4. Delete group as domain administrator -> publishes single batch delete envelope for remaining members and owners
	adminCtx := authentication.ContextWithUserID(ctx, "domain-admin-user")
	if err := svc.DeleteGroup(adminCtx, grp.ID); err != nil {
		t.Fatalf("failed to delete group: %v", err)
	}

	msgDelete, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("failed to read delete group msg: %v", err)
	}
	var envDelete v1.PermissionUpdateEnvelope
	if err := proto.Unmarshal(msgDelete.Value, &envDelete); err != nil {
		t.Fatalf("failed to unmarshal delete group envelope: %v", err)
	}
	if len(envDelete.Operations) != 2 {
		t.Fatalf("expected 2 operations in delete group envelope, got %d", len(envDelete.Operations))
	}
	if envDelete.Operations[0].Op != v1.PermissionOp_PERMISSION_OP_DELETE || envDelete.Operations[0].Subject != "user:user-alpha" {
		t.Errorf("unexpected delete member tuple: %+v", envDelete.Operations[0])
	}
	if envDelete.Operations[1].Op != v1.PermissionOp_PERMISSION_OP_DELETE || envDelete.Operations[1].Subject != "user:creator-456" {
		t.Errorf("unexpected delete owner tuple: %+v", envDelete.Operations[1])
	}
}

func TestKafkaIntegration_UnavailableDoesNotFailGroupCRUD(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	writer := &kafkago.Writer{
		Addr:                   kafkago.TCP("127.0.0.1:1"),
		Topic:                  kafka.DefaultPermissionsTopic,
		RequiredAcks:           kafkago.RequireOne,
		MaxAttempts:            1,
		WriteTimeout:           100 * time.Millisecond,
		AllowAutoTopicCreation: false,
	}

	logger := logging.NewLogger("debug")
	tracer := &noopTracer{}
	monitor := &noopMonitor{}
	publisher := kafka.NewPermissionPublisher(writer, tracer, monitor, logger)
	defer func() { _ = publisher.Close() }()

	memDB := newInMemoryDB()
	authz := &noopAuthorizer{}
	svc := groups.NewService(memDB, authz, publisher, tracer, monitor, logger)

	userCtx := authentication.ContextWithUserID(ctx, "creator-999")
	grp, err := svc.CreateGroup(userCtx, &types.Group{Name: "resilient-group"})
	if err != nil {
		t.Fatalf("CreateGroup failed when Kafka was down: %v", err)
	}

	if err := svc.AddUsersToGroup(ctx, grp.ID, []string{"user-1"}); err != nil {
		t.Fatalf("AddUsersToGroup failed when Kafka was down: %v", err)
	}

	if err := svc.RemoveUsersFromGroup(ctx, grp.ID, []string{"user-1"}); err != nil {
		t.Fatalf("RemoveUsersFromGroup failed when Kafka was down: %v", err)
	}

	if err := svc.DeleteGroup(userCtx, grp.ID); err != nil {
		t.Fatalf("DeleteGroup failed when Kafka was down: %v", err)
	}
}
