// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package db

import (
	"context"
	"database/sql"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	sq "github.com/Masterminds/squirrel"
	"github.com/canonical/hook-service/internal/logging"
)

type mockBaseRunner struct {
	queryRowCalled atomic.Int64
}

func (m *mockBaseRunner) Exec(query string, args ...interface{}) (sql.Result, error) {
	return nil, nil
}

func (m *mockBaseRunner) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return nil, nil
}

func (m *mockBaseRunner) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

func (m *mockBaseRunner) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return nil, nil
}

func (m *mockBaseRunner) QueryRow(query string, args ...interface{}) sq.RowScanner {
	m.queryRowCalled.Add(1)
	return noopRowScanner{}
}

func (m *mockBaseRunner) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	m.queryRowCalled.Add(1)
	return &sql.Row{}
}

type noopRowScanner struct{}

func (noopRowScanner) Scan(dest ...interface{}) error { return nil }

type testLogger struct {
	logging.LoggerInterface
	warnfCalled atomic.Int64
	warnfMsg    string
}

func (l *testLogger) Warnf(template string, args ...interface{}) {
	l.warnfCalled.Add(1)
	l.warnfMsg = template
}

func (l *testLogger) Errorf(template string, args ...interface{}) {}
func (l *testLogger) Fatalf(template string, args ...interface{}) {}

func TestStatement_Routing(t *testing.T) {
	primaryRunner := &mockBaseRunner{}
	replicaRunner := &mockBaseRunner{}
	logger := &testLogger{}

	tests := []struct {
		name             string
		setupCtx         func() context.Context
		replicaRunner    sq.BaseRunner
		replicaLagMs     int64
		maxLagMs         int64
		wantPrimaryCalls int64
		wantReplicaCalls int64
	}{
		{
			name:             "read-only context with replica pool routes to replica",
			setupCtx:         func() context.Context { return contextWithReadOnly(context.Background()) },
			replicaRunner:    replicaRunner,
			replicaLagMs:     0,
			maxLagMs:         1000,
			wantReplicaCalls: 1,
			wantPrimaryCalls: 0,
		},
		{
			name:             "read-only context without replica pool routes to primary",
			setupCtx:         func() context.Context { return contextWithReadOnly(context.Background()) },
			replicaRunner:    nil,
			replicaLagMs:     0,
			maxLagMs:         1000,
			wantPrimaryCalls: 1,
			wantReplicaCalls: 0,
		},
		{
			name:             "read-only context with high lag falls back to primary",
			setupCtx:         func() context.Context { return contextWithReadOnly(context.Background()) },
			replicaRunner:    replicaRunner,
			replicaLagMs:     2000,
			maxLagMs:         1000,
			wantPrimaryCalls: 1,
			wantReplicaCalls: 0,
		},
		{
			name:             "no flags routes to primary",
			setupCtx:         func() context.Context { return context.Background() },
			replicaRunner:    replicaRunner,
			replicaLagMs:     0,
			maxLagMs:         1000,
			wantPrimaryCalls: 1,
			wantReplicaCalls: 0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			primaryRunner.queryRowCalled.Store(0)
			replicaRunner.queryRowCalled.Store(0)

			d := &DBClient{
				dbRunner:      primaryRunner,
				replicaRunner: tt.replicaRunner,
				replicaLagMs:  tt.replicaLagMs,
				maxLagMs:      tt.maxLagMs,
				logger:        logger,
			}

			ctx := tt.setupCtx()
			stmt := d.Statement(ctx)
			row := stmt.Select("1").QueryRow()
			_ = row

			if got := primaryRunner.queryRowCalled.Load(); got != tt.wantPrimaryCalls {
				t.Errorf("primary calls = %d, want %d", got, tt.wantPrimaryCalls)
			}
			if got := replicaRunner.queryRowCalled.Load(); got != tt.wantReplicaCalls {
				t.Errorf("replica calls = %d, want %d", got, tt.wantReplicaCalls)
			}
		})
	}
}

func TestStatement_TransactionOverridesReadOnly(t *testing.T) {
	primaryRunner := &mockBaseRunner{}
	txRunner := &mockBaseRunner{}
	logger := &testLogger{}

	tx := &mockTx{runner: txRunner}
	ctx := ContextWithTx(contextWithReadOnly(context.Background()), tx)

	d := &DBClient{
		dbRunner:      primaryRunner,
		replicaRunner: &mockBaseRunner{},
		replicaLagMs:  0,
		maxLagMs:      1000,
		logger:        logger,
	}

	stmt := d.Statement(ctx)
	row := stmt.Select("1").QueryRow()
	_ = row

	if got := txRunner.queryRowCalled.Load(); got != 1 {
		t.Errorf("tx calls = %d, want 1", got)
	}
	if got := primaryRunner.queryRowCalled.Load(); got != 0 {
		t.Errorf("primary calls = %d, want 0", got)
	}
}

type mockTx struct {
	runner *mockBaseRunner
}

func (m *mockTx) Commit() error   { return nil }
func (m *mockTx) Rollback() error { return nil }
func (m *mockTx) Exec(query string, args ...interface{}) (sql.Result, error) {
	return m.runner.Exec(query, args...)
}
func (m *mockTx) ExecContext(ctx context.Context, query string, args ...interface{}) (sql.Result, error) {
	return m.runner.ExecContext(ctx, query, args...)
}
func (m *mockTx) Query(query string, args ...interface{}) (*sql.Rows, error) {
	return m.runner.Query(query, args...)
}
func (m *mockTx) QueryContext(ctx context.Context, query string, args ...interface{}) (*sql.Rows, error) {
	return m.runner.QueryContext(ctx, query, args...)
}
func (m *mockTx) QueryRow(query string, args ...interface{}) sq.RowScanner {
	return m.runner.QueryRow(query, args...)
}
func (m *mockTx) QueryRowContext(ctx context.Context, query string, args ...interface{}) *sql.Row {
	return m.runner.QueryRowContext(ctx, query, args...)
}

func TestTransactionMiddleware_ReadOnlyInjection(t *testing.T) {
	tests := []struct {
		name           string
		method         string
		expectReadOnly bool
	}{
		{
			name:           "GET request gets read-only context",
			method:         http.MethodGet,
			expectReadOnly: true,
		},
		{
			name:           "HEAD request gets read-only context",
			method:         http.MethodHead,
			expectReadOnly: true,
		},
		{
			name:           "POST request does not get read-only context",
			method:         http.MethodPost,
			expectReadOnly: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var capturedCtx context.Context

			handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				capturedCtx = r.Context()
				w.WriteHeader(http.StatusOK)
			})

			mockDB := &mockDBClientForMiddleware{}
			middleware := TransactionMiddleware(mockDB, &testLogger{})

			req := httptest.NewRequest(tt.method, "/test", nil)
			rec := httptest.NewRecorder()

			middleware(handler).ServeHTTP(rec, req)

			if got := readOnlyFromContext(capturedCtx); got != tt.expectReadOnly {
				t.Errorf("expectReadOnly = %t, got %t", tt.expectReadOnly, got)
			}
		})
	}
}

type mockDBClientForMiddleware struct{}

func (m *mockDBClientForMiddleware) Statement(ctx context.Context) sq.StatementBuilderType {
	return sq.StatementBuilder.PlaceholderFormat(sq.Dollar)
}
func (m *mockDBClientForMiddleware) TxStatement(ctx context.Context) (TxInterface, sq.StatementBuilderType, error) {
	return nil, sq.StatementBuilderType{}, nil
}
func (m *mockDBClientForMiddleware) BeginTx(ctx context.Context) (context.Context, TxInterface, error) {
	return ctx, nil, nil
}
func (m *mockDBClientForMiddleware) WithTx(ctx context.Context, fn func(context.Context) error) error {
	return fn(ctx)
}
func (m *mockDBClientForMiddleware) Close() {}

func TestWithReadOnly(t *testing.T) {
	ctx := context.Background()
	if readOnlyFromContext(ctx) {
		t.Error("expected context to not be read-only")
	}

	ctx = WithReadOnly(ctx)
	if !readOnlyFromContext(ctx) {
		t.Error("expected context to be read-only")
	}
}

func TestNewDBClient_EmptyReplicaDSN_PrimaryOnly(t *testing.T) {
	logger := &testLogger{}
	d := &DBClient{
		dbRunner:      &mockBaseRunner{},
		replicaRunner: nil,
		replicaLagMs:  0,
		maxLagMs:      1000,
		logger:        logger,
	}

	assert.Nil(t, d.replicaRunner)
	assert.Nil(t, d.replicaDB)
	assert.Nil(t, d.replicaPool)

	stmt := d.Statement(context.Background())
	row := stmt.Select("1").QueryRow()
	_ = row
}

func TestClose_WithAndWithoutReplica(t *testing.T) {
	tests := []struct {
		name       string
		hasReplica bool
	}{
		{name: "close with replica", hasReplica: true},
		{name: "close without replica", hasReplica: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DBClient{}

			if tt.hasReplica {
				cancelCalled := false
				d.replicaCancel = func() { cancelCalled = true }
				d.replicaDB = nil
				d.replicaPool = nil

				d.Close()
				if !cancelCalled {
					t.Error("expected replicaCancel to be called")
				}
			} else {
				d.Close()
			}
		})
	}
}

func TestStatement_LagFallbackLogs(t *testing.T) {
	primaryRunner := &mockBaseRunner{}
	replicaRunner := &mockBaseRunner{}
	logger := &testLogger{}

	d := &DBClient{
		dbRunner:      primaryRunner,
		replicaRunner: replicaRunner,
		replicaLagMs:  5000,
		maxLagMs:      1000,
		logger:        logger,
	}

	ctx := contextWithReadOnly(context.Background())
	stmt := d.Statement(ctx)
	row := stmt.Select("1").QueryRow()
	_ = row

	if got := primaryRunner.queryRowCalled.Load(); got != 1 {
		t.Errorf("primary calls = %d, want 1", got)
	}
	if got := logger.warnfCalled.Load(); got <= 0 {
		t.Errorf("warnf calls = %d, want > 0", got)
	}
}

func TestReadOnlyContextHelpers(t *testing.T) {
	t.Run("contextWithReadOnly sets and readOnlyFromContext retrieves", func(t *testing.T) {
		ctx := contextWithReadOnly(context.Background())
		if !readOnlyFromContext(ctx) {
			t.Error("expected context to be read-only")
		}
	})

	t.Run("readOnlyFromContext returns false for empty context", func(t *testing.T) {
		if readOnlyFromContext(context.Background()) {
			t.Error("expected context to not be read-only")
		}
	})

	t.Run("WithReadOnly public helper works", func(t *testing.T) {
		ctx := WithReadOnly(context.Background())
		if !readOnlyFromContext(ctx) {
			t.Error("expected context to be read-only")
		}
	})
}
