// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package db

import (
	"context"
	"fmt"
	"math"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/modules/postgres"
	"go.opentelemetry.io/otel/trace"

	"github.com/canonical/hook-service/migrations"
)

func sanitizeName(name string) string {
	name = strings.ReplaceAll(name, "/", "-")
	name = strings.ReplaceAll(name, " ", "-")
	name = strings.ToLower(name)
	return name
}

func setupTestPostgres(t *testing.T, suffix string) (string, *postgres.PostgresContainer) {
	t.Helper()
	ctx := context.Background()

	containerName := fmt.Sprintf("hook-replica-%s-%d", sanitizeName(t.Name()+suffix), time.Now().UnixNano()%10000)

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
			t.Skipf("Skipping: Docker/PostgreSQL container failed to start: %v", err)
		}
	}()

	if pgContainer == nil {
		return "", nil
	}

	connStr, err := pgContainer.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		t.Fatalf("Failed to get connection string: %v", err)
	}

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

func runMigrationsOnDSN(t *testing.T, connStr string) {
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

func TestIntegration_ReplicaUnconfigured(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	connStr, container := setupTestPostgres(t, "primary")
	if container == nil {
		return
	}
	defer func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}()

	runMigrationsOnDSN(t, connStr)

	logger := &integrationLogger{t: t}

	cfg := Config{
		DSN:             connStr,
		MaxConns:        5,
		MinConns:        1,
		MaxConnLifetime: time.Hour,
		MaxConnIdleTime: 30 * time.Minute,
		MaxReplicaLagMs: 500,
	}

	dbClient, err := NewDBClient(cfg, &noopTracer{}, &noopMonitor{}, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer dbClient.Close()

	// Verify that in primary-only mode (empty ReplicaDSN), replica components are nil
	if dbClient.replicaRunner != nil {
		t.Errorf("expected replicaRunner to be nil, got %v", dbClient.replicaRunner)
	}
	if dbClient.replicaDB != nil {
		t.Errorf("expected replicaDB to be nil, got %v", dbClient.replicaDB)
	}
	if dbClient.replicaPool != nil {
		t.Errorf("expected replicaPool to be nil, got %v", dbClient.replicaPool)
	}
	if got := dbClient.replicaLagMs; got != math.MaxInt64 {
		t.Errorf("expected replicaLagMs to be %d, got %d", math.MaxInt64, got)
	}

	ctx := context.Background()

	stmt := dbClient.Statement(ctx)

	_, _, err = stmt.Select("1").ToSql()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	readOnlyCtx := WithReadOnly(ctx)
	readOnlyStmt := dbClient.Statement(readOnlyCtx)

	_, _, err = readOnlyStmt.Select("1").ToSql()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestIntegration_ReplicaLagFallback(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	// Spin up primary container
	primaryDSN, primaryContainer := setupTestPostgres(t, "primary-lag")
	if primaryContainer == nil {
		return
	}
	defer func() {
		if err := primaryContainer.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate primary container: %v", err)
		}
	}()
	runMigrationsOnDSN(t, primaryDSN)

	// Spin up replica container
	replicaDSN, replicaContainer := setupTestPostgres(t, "replica-lag")
	if replicaContainer == nil {
		return
	}
	defer func() {
		if err := replicaContainer.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate replica container: %v", err)
		}
	}()
	runMigrationsOnDSN(t, replicaDSN)

	// Setup dummy data that differs between primary and replica to distinguish routing.
	primaryConfig, err := pgx.ParseConfig(primaryDSN)
	if err != nil {
		t.Fatalf("failed to parse primary DSN: %v", err)
	}
	primarySQLDB := stdlib.OpenDB(*primaryConfig)
	defer primarySQLDB.Close()

	groupID := "00000000-0000-0000-0000-000000000001"
	_, err = primarySQLDB.Exec(
		"INSERT INTO groups (id, name, tenant_id, description, type) VALUES ($1, $2, $3, $4, $5)",
		groupID, "test-group", "test-tenant", "primary-desc", 0,
	)
	if err != nil {
		t.Fatalf("failed to insert test group into primary: %v", err)
	}

	replicaConfig, err := pgx.ParseConfig(replicaDSN)
	if err != nil {
		t.Fatalf("failed to parse replica DSN: %v", err)
	}
	replicaSQLDB := stdlib.OpenDB(*replicaConfig)
	defer replicaSQLDB.Close()

	_, err = replicaSQLDB.Exec(
		"INSERT INTO groups (id, name, tenant_id, description, type) VALUES ($1, $2, $3, $4, $5)",
		groupID, "test-group", "test-tenant", "replica-desc", 0,
	)
	if err != nil {
		t.Fatalf("failed to insert test group into replica: %v", err)
	}

	logger := &integrationLogger{t: t}
	cfg := Config{
		DSN:             primaryDSN,
		MaxConns:        5,
		MinConns:        1,
		MaxConnLifetime: time.Hour,
		MaxConnIdleTime: 30 * time.Minute,
		ReplicaDSN:      replicaDSN,
		ReplicaMaxConns: 5,
		MaxReplicaLagMs: 500, // threshold is 500ms
	}

	dbClient, err := NewDBClient(cfg, &noopTracer{}, &noopMonitor{}, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer dbClient.Close()

	if dbClient.replicaRunner == nil {
		t.Fatalf("expected replicaRunner to be initialized, got nil")
	}

	// Stop the background replication lag monitor safely
	if dbClient.replicaCancel != nil {
		dbClient.replicaCancel()
	}

	ctx := context.Background()
	readOnlyCtx := WithReadOnly(ctx)

	// Case 1: Low lag (e.g. 0ms) -> should route to replica
	atomic.StoreInt64(&dbClient.replicaLagMs, 0)
	var desc1 string
	err = dbClient.Statement(readOnlyCtx).
		Select("description").
		From("groups").
		Where(sq.Eq{"id": groupID}).
		QueryRowContext(readOnlyCtx).
		Scan(&desc1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc1 != "replica-desc" {
		t.Errorf("expected read from replica ('replica-desc'), got %q", desc1)
	}

	// Case 2: High lag (e.g. 1000ms > threshold 500ms) -> should route to primary
	atomic.StoreInt64(&dbClient.replicaLagMs, 1000)
	var desc2 string
	err = dbClient.Statement(readOnlyCtx).
		Select("description").
		From("groups").
		Where(sq.Eq{"id": groupID}).
		QueryRowContext(readOnlyCtx).
		Scan(&desc2)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if desc2 != "primary-desc" {
		t.Errorf("expected read fallback to primary ('primary-desc'), got %q", desc2)
	}
}

func TestIntegration_MetricsValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping integration test in short mode")
	}

	t.Parallel()

	connStr, container := setupTestPostgres(t, "metrics")
	if container == nil {
		return
	}
	defer func() {
		if err := container.Terminate(context.Background()); err != nil {
			t.Logf("Failed to terminate container: %v", err)
		}
	}()

	runMigrationsOnDSN(t, connStr)

	logger := &integrationLogger{t: t}

	cfg := Config{
		DSN:             connStr,
		MaxConns:        5,
		MinConns:        1,
		MaxConnLifetime: time.Hour,
		MaxConnIdleTime: 30 * time.Minute,
		MaxReplicaLagMs: 1000,
	}

	dbClient, err := NewDBClient(cfg, &noopTracer{}, &noopMonitor{}, logger)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	defer dbClient.Close()

	ctx := context.Background()

	readOnlyCtx := WithReadOnly(ctx)
	stmt := dbClient.Statement(readOnlyCtx)
	_, _, _ = stmt.Select("1").ToSql()

	func() {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("expected no panic, got %v", r)
			}
		}()
		replicaQueries.Inc()
		primaryFallbacks.Inc()
		replicaLagGauge.Set(0)
	}()
}

type integrationLogger struct {
	logging.LoggerInterface
	t *testing.T
}

func (l *integrationLogger) Infof(template string, args ...interface{})  { l.t.Logf("INFO: "+template, args...) }
func (l *integrationLogger) Warnf(template string, args ...interface{})  { l.t.Logf("WARN: "+template, args...) }
func (l *integrationLogger) Errorf(template string, args ...interface{}) { l.t.Logf("ERROR: "+template, args...) }
func (l *integrationLogger) Debugf(template string, args ...interface{}) { l.t.Logf("DEBUG: "+template, args...) }
func (l *integrationLogger) Fatalf(template string, args ...interface{}) { l.t.Fatalf("FATAL: "+template, args...) }

type noopTracer struct{}

func (noopTracer) Start(ctx context.Context, _ string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	return ctx, trace.SpanFromContext(ctx)
}

type noopMonitor struct{}

func (noopMonitor) GetService() string                                    { return "test" }
func (noopMonitor) SetResponseTimeMetric(map[string]string, float64) error { return nil }
func (noopMonitor) SetDependencyAvailability(map[string]string, float64) error { return nil }
