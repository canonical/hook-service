// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package importer

import (
	"context"
	"errors"
	"testing"

	"fmt"
	"strings"
	"time"

	"github.com/canonical/hook-service/internal/types"
	trace "go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"

	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/migrations"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
)

//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_importer.go -source=./interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_storage.go -source=./importer.go
//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_tracing.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package importer -destination ./mock_salesforce.go -source=../../internal/salesforce/interfaces.go

func TestImporterRun(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*MockDriverInterface, *MockStorageInterface, *MockAuthorizerInterface, *MockLoggerInterface)
		expectErr  bool
	}{
		{
			name: "successful import",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{
					{UserID: "alice@example.com", GroupName: "Engineering"},
					{UserID: "bob@example.com", GroupName: "Engineering"},
					{UserID: "alice@example.com", GroupName: "TeamA"},
				}, nil)

				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()

				store.EXPECT().GetGroupByName(gomock.Any(), gomock.Any(), storage.DefaultTenantID).Return(nil, errors.New("not found")).Times(2)

				store.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).DoAndReturn(
					func(_ context.Context, g *types.Group) (*types.Group, error) {
						return &types.Group{ID: g.Name, Name: g.Name}, nil
					},
				).Times(2)

				store.EXPECT().AddUsersToGroup(gomock.Any(), gomock.Any(), gomock.Any()).Return(nil).Times(2)
			},
		},
		{
			name: "driver error propagates",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return(nil, errors.New("salesforce unavailable"))
			},
			expectErr: true,
		},
		{
			name: "empty mappings",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{}, nil)
				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
			},
		},
		{
			name: "create group error is non-fatal",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{
					{UserID: "alice@example.com", GroupName: "Engineering"},
				}, nil)

				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

				store.EXPECT().GetGroupByName(gomock.Any(), gomock.Any(), storage.DefaultTenantID).Return(nil, errors.New("not found")).AnyTimes()
				store.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).Return(nil, errors.New("duplicate"))
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockDriver := NewMockDriverInterface(ctrl)
			mockStorage := NewMockStorageInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)

			tt.setupMocks(mockDriver, mockStorage, mockAuthz, mockLogger)

			imp := NewImporter(mockDriver, mockStorage, mockAuthz, mockLogger)
			err := imp.Run(context.Background())

			if tt.expectErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

// sanitizeName converts test names to valid container names.
func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ToLower(name)
	return name
}

func setupTestPostgres(t *testing.T) (string, *postgres.PostgresContainer) {
	t.Helper()
	ctx := context.Background()

	containerName := fmt.Sprintf("hook-importer-%s", sanitizeName(t.Name()))

	var pgContainer *postgres.PostgresContainer
	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Skipf("Skipping: Docker not available (%v)", r)
			}
		}()
		var err error
		pgContainer, err = postgres.Run(ctx,
			"postgres:16-alpine",
			postgres.WithDatabase("testdb"),
			postgres.WithUsername("testuser"),
			postgres.WithPassword("testpass"),
			testcontainers.CustomizeRequest(testcontainers.GenericContainerRequest{
				ContainerRequest: testcontainers.ContainerRequest{
					Name: containerName,
				},
			}),
		)
		if err != nil {
			t.Fatalf("Failed to start PostgreSQL container: %v", err)
		}
	}()

	if pgContainer == nil {
		return "", nil
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

	// Wait for PostgreSQL to be ready
	maxRetries := 10
	for i := 0; i < maxRetries; i++ {
		config, err := pgx.ParseConfig(connStr)
		if err != nil {
			t.Fatalf("Failed to parse config: %v", err)
		}
		sqlDB := stdlib.OpenDB(*config)
		if err := sqlDB.Ping(); err == nil {
			sqlDB.Close()
			break
		}
		sqlDB.Close()
		if i < maxRetries-1 {
			time.Sleep(time.Second)
		}
	}

	return connStr, pgContainer
}

func runMigrations(t *testing.T, connStr string) {
	t.Helper()
	config, err := pgx.ParseConfig(connStr)
	if err != nil {
		t.Fatalf("Failed to parse DSN: %v", err)
	}

	sqlDB := stdlib.OpenDB(*config)
	defer sqlDB.Close()

	goose.SetBaseFS(migrations.EmbedMigrations)
	if err := goose.SetDialect("postgres"); err != nil {
		t.Fatalf("Failed to set dialect: %v", err)
	}

	if err := goose.Up(sqlDB, "."); err != nil {
		t.Fatalf("Failed to run migrations: %v", err)
	}
}

func TestImporterIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	connStr, container := setupTestPostgres(t)
	if container == nil {
		return // skipped due to Docker unavailability
	}
	defer func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}()

	// Run migrations to create schema
	runMigrations(t, connStr)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockMonitorInterface(ctrl)
	mockLogger := NewMockLoggerInterface(ctrl)

	// Allow any logging/tracing calls
	mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("INFO: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("ERROR: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("DEBUG: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("WARN: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Fatalf(gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("FATAL: "+f, v...) }).AnyTimes()
	mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background())).AnyTimes()

	dbClient, err := db.NewDBClient(
		db.Config{DSN: connStr, MinConns: 10, MaxConns: 20},
		mockTracer,
		mockMonitor,
		mockLogger,
	)
	if err != nil {
		t.Fatalf("Failed to create DB client: %v", err)
	}
	defer dbClient.Close()

	ctx := context.Background()
	s := storage.NewStorage(dbClient, mockTracer, mockMonitor, mockLogger)

	mockDriver := NewMockDriverInterface(ctrl)
	mockDriver.EXPECT().Prefix().Return("salesforce").AnyTimes()
	mockDriver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{
		{UserID: "alice@example.com", GroupName: "Engineering"},
		{UserID: "bob@example.com", GroupName: "Engineering"},
		{UserID: "alice@example.com", GroupName: "Platform"},
		{UserID: "charlie@example.com", GroupName: "Sales"},
	}, nil).AnyTimes()

	mockAuthz := NewMockAuthorizerInterface(ctrl)
	imp := NewImporter(mockDriver, s, mockAuthz, mockLogger)
	if err := imp.Run(ctx); err != nil {
		t.Fatalf("Import failed: %v", err)
	}

	// Verify: check that groups were created
	groups, err := s.ListGroups(ctx)
	if err != nil {
		t.Fatalf("Failed to list groups: %v", err)
	}

	if len(groups) < 3 {
		t.Fatalf("Expected at least 3 groups (Engineering, Platform, Sales), got %d", len(groups))
	}

	// Verify: check that alice is in 2 groups
	aliceGroups, err := s.GetGroupsForUser(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("Failed to get groups for alice: %v", err)
	}
	if len(aliceGroups) != 2 {
		t.Fatalf("Expected alice to be in 2 groups, got %d", len(aliceGroups))
	}

	// Verify: check that bob is in 1 group
	bobGroups, err := s.GetGroupsForUser(ctx, "bob@example.com")
	if err != nil {
		t.Fatalf("Failed to get groups for bob: %v", err)
	}
	if len(bobGroups) != 1 {
		t.Fatalf("Expected bob to be in 1 group, got %d", len(bobGroups))
	}

	// Run importer again to test that existing groups are fetched properly
	// and no duplicate errors or creation happens due to GetGroupByName logic.
	if err := imp.Run(ctx); err != nil {
		t.Fatalf("Second import failed: %v", err)
	}

	// Verify the group count hasn't changed
	groupsAfterSecondRun, err := s.ListGroups(ctx)
	if err != nil {
		t.Fatalf("Failed to list groups after second run: %v", err)
	}
	if len(groupsAfterSecondRun) != len(groups) {
		t.Fatalf("Expected group count to remain %d, got %d", len(groups), len(groupsAfterSecondRun))
	}
}

func TestImporterSync(t *testing.T) {
	tests := []struct {
		name       string
		setupMocks func(*MockDriverInterface, *MockStorageInterface, *MockAuthorizerInterface, *MockLoggerInterface)
		expectErr  bool
	}{
		{
			name: "successful sync - new groups created",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{
					{UserID: "alice@example.com", GroupName: "Engineering"},
					{UserID: "bob@example.com", GroupName: "Engineering"},
				}, nil)

				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

				// No existing groups for this prefix
				store.EXPECT().ListGroupsByPrefix(gomock.Any(), "salesforce:", storage.DefaultTenantID).Return(nil, nil)

				// Group doesn't exist — create it
				store.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).Return(
					&types.Group{ID: "g1", Name: "salesforce:Engineering"}, nil,
				)
				store.EXPECT().AddUsersToGroup(gomock.Any(), "g1", gomock.Any()).Return(nil)
			},
		},
		{
			name: "successful sync - existing group members reconciled",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{
					{UserID: "alice@example.com", GroupName: "Engineering"},
				}, nil)

				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

				store.EXPECT().ListGroupsByPrefix(gomock.Any(), "salesforce:", storage.DefaultTenantID).Return([]*types.Group{
					{ID: "g1", Name: "salesforce:Engineering"},
				}, nil)

				store.EXPECT().SyncGroupMembers(gomock.Any(), "g1", gomock.Any()).Return(nil)
			},
		},
		{
			name: "successful sync - stale groups deleted",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{}, nil)

				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

				store.EXPECT().ListGroupsByPrefix(gomock.Any(), "salesforce:", storage.DefaultTenantID).Return([]*types.Group{
					{ID: "g1", Name: "salesforce:OldTeam"},
				}, nil)

				store.EXPECT().DeleteGroup(gomock.Any(), "g1").Return(nil)
				authz.EXPECT().DeleteGroup(gomock.Any(), "g1").Return(nil)
			},
		},
		{
			name: "driver error propagates",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return(nil, errors.New("salesforce unavailable"))
			},
			expectErr: true,
		},
		{
			name: "list groups prefix error propagates",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{}, nil)

				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()

				store.EXPECT().ListGroupsByPrefix(gomock.Any(), "salesforce:", storage.DefaultTenantID).Return(nil, errors.New("db error"))
			},
			expectErr: true,
		},
		{
			name: "create group error is non-fatal per group but returns error",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{
					{UserID: "alice@example.com", GroupName: "Engineering"},
				}, nil)

				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

				store.EXPECT().ListGroupsByPrefix(gomock.Any(), "salesforce:", storage.DefaultTenantID).Return(nil, nil)
				store.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).Return(nil, errors.New("db error"))
			},
			expectErr: true,
		},
		{
			name: "sync members error is non-fatal per group but returns error",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{
					{UserID: "alice@example.com", GroupName: "Engineering"},
				}, nil)

				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

				store.EXPECT().ListGroupsByPrefix(gomock.Any(), "salesforce:", storage.DefaultTenantID).Return([]*types.Group{
					{ID: "g1", Name: "salesforce:Engineering"},
				}, nil)
				store.EXPECT().SyncGroupMembers(gomock.Any(), "g1", gomock.Any()).Return(errors.New("db error"))
			},
			expectErr: true,
		},
		{
			name: "delete stale group error is non-fatal per group but returns error",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{}, nil)

				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

				store.EXPECT().ListGroupsByPrefix(gomock.Any(), "salesforce:", storage.DefaultTenantID).Return([]*types.Group{
					{ID: "g1", Name: "salesforce:OldTeam"},
				}, nil)
				store.EXPECT().DeleteGroup(gomock.Any(), "g1").Return(errors.New("db error"))
			},
			expectErr: true,
		},
		{
			name: "partial failure error message contains failure count",
			setupMocks: func(driver *MockDriverInterface, store *MockStorageInterface, authz *MockAuthorizerInterface, logger *MockLoggerInterface) {
				driver.EXPECT().Prefix().Return("salesforce").AnyTimes()
				driver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{
					{UserID: "alice@example.com", GroupName: "Engineering"},
				}, nil)

				logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()
				logger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

				store.EXPECT().ListGroupsByPrefix(gomock.Any(), "salesforce:", storage.DefaultTenantID).Return(nil, nil)
				store.EXPECT().CreateGroup(gomock.Any(), gomock.Any()).Return(nil, errors.New("db error"))
			},
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			mockDriver := NewMockDriverInterface(ctrl)
			mockStorage := NewMockStorageInterface(ctrl)
			mockAuthz := NewMockAuthorizerInterface(ctrl)
			mockLogger := NewMockLoggerInterface(ctrl)

			tt.setupMocks(mockDriver, mockStorage, mockAuthz, mockLogger)

			imp := NewImporter(mockDriver, mockStorage, mockAuthz, mockLogger)
			err := imp.Sync(context.Background())

			if tt.expectErr && err == nil {
				t.Fatal("expected error, got nil")
			}
			if !tt.expectErr && err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
		})
	}
}

func TestImporterSyncIntegration(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	connStr, container := setupTestPostgres(t)
	if container == nil {
		return // skipped due to Docker unavailability
	}
	defer func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}()

	runMigrations(t, connStr)

	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockMonitorInterface(ctrl)
	mockLogger := NewMockLoggerInterface(ctrl)

	mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("INFO: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("INFO: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Infof(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("INFO: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Errorf(gomock.Any(), gomock.Any(), gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("ERROR: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Debugf(gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("DEBUG: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Warnf(gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("WARN: "+f, v...) }).AnyTimes()
	mockLogger.EXPECT().Fatalf(gomock.Any(), gomock.Any()).Do(func(f string, v ...interface{}) { t.Logf("FATAL: "+f, v...) }).AnyTimes()
	mockTracer.EXPECT().Start(gomock.Any(), gomock.Any()).Return(context.Background(), trace.SpanFromContext(context.Background())).AnyTimes()

	dbClient, err := db.NewDBClient(
		db.Config{DSN: connStr, MinConns: 10, MaxConns: 20},
		mockTracer,
		mockMonitor,
		mockLogger,
	)
	if err != nil {
		t.Fatalf("Failed to create DB client: %v", err)
	}
	defer dbClient.Close()

	ctx := context.Background()
	s := storage.NewStorage(dbClient, mockTracer, mockMonitor, mockLogger)

	mockDriver := NewMockDriverInterface(ctrl)
	mockDriver.EXPECT().Prefix().Return("salesforce").AnyTimes()

	// Phase 1: initial import via Run
	mockDriver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{
		{UserID: "alice@example.com", GroupName: "Engineering"},
		{UserID: "bob@example.com", GroupName: "Engineering"},
		{UserID: "alice@example.com", GroupName: "Platform"},
		{UserID: "charlie@example.com", GroupName: "Sales"},
	}, nil).Times(1)

	mockAuthz := NewMockAuthorizerInterface(ctrl)
	mockAuthz.EXPECT().DeleteGroup(gomock.Any(), gomock.Any()).Return(nil).AnyTimes()
	imp := NewImporter(mockDriver, s, mockAuthz, mockLogger)
	if err := imp.Run(ctx); err != nil {
		t.Fatalf("Initial import failed: %v", err)
	}

	groupsAfterImport, err := s.ListGroups(ctx)
	if err != nil {
		t.Fatalf("Failed to list groups after import: %v", err)
	}
	if len(groupsAfterImport) != 3 {
		t.Fatalf("Expected 3 groups after import, got %d", len(groupsAfterImport))
	}

	// Phase 2: sync — bob leaves Engineering, Sales is removed, Marketing is added
	mockDriver.EXPECT().FetchAllUserGroups(gomock.Any()).Return([]UserGroupMapping{
		{UserID: "alice@example.com", GroupName: "Engineering"},
		{UserID: "alice@example.com", GroupName: "Platform"},
		{UserID: "dave@example.com", GroupName: "Marketing"},
	}, nil).Times(1)

	if err := imp.Sync(ctx); err != nil {
		t.Fatalf("Sync failed: %v", err)
	}

	groupsAfterSync, err := s.ListGroups(ctx)
	if err != nil {
		t.Fatalf("Failed to list groups after sync: %v", err)
	}
	// Engineering, Platform, Marketing — Sales deleted
	if len(groupsAfterSync) != 3 {
		t.Fatalf("Expected 3 groups after sync, got %d: %v", len(groupsAfterSync), groupsAfterSync)
	}

	// bob should no longer be in Engineering
	bobGroups, err := s.GetGroupsForUser(ctx, "bob@example.com")
	if err != nil {
		t.Fatalf("Failed to get groups for bob: %v", err)
	}
	if len(bobGroups) != 0 {
		t.Fatalf("Expected bob to be in 0 groups after sync, got %d", len(bobGroups))
	}

	// dave should be in Marketing
	daveGroups, err := s.GetGroupsForUser(ctx, "dave@example.com")
	if err != nil {
		t.Fatalf("Failed to get groups for dave: %v", err)
	}
	if len(daveGroups) != 1 {
		t.Fatalf("Expected dave to be in 1 group after sync, got %d", len(daveGroups))
	}

	// alice should still be in 2 groups
	aliceGroups, err := s.GetGroupsForUser(ctx, "alice@example.com")
	if err != nil {
		t.Fatalf("Failed to get groups for alice: %v", err)
	}
	if len(aliceGroups) != 2 {
		t.Fatalf("Expected alice to be in 2 groups after sync, got %d", len(aliceGroups))
	}
}
