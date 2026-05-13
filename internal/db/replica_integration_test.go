package db

import (
	"context"
	"fmt"
	"strings"
	"testing"
	"time"

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

func TestIntegration_ReplicaLagFallback(t *testing.T) {
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
	if got := dbClient.replicaLagMs; got != 0 {
		t.Errorf("expected replicaLagMs to be 0, got %d", got)
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
