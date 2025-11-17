// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	sq "github.com/Masterminds/squirrel"
	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

const (
	defaultPage     uint64 = 1
	defaultPageSize uint64 = 100
)

type TxContextKey struct{}
type LazyTxContextKey struct{}

var txContextKey TxContextKey
var lazyTxContextKey LazyTxContextKey

// Offset calculates the offset for pagination based on the provided page parameter and page size.
func Offset(pageParam int64, pageSize uint64) uint64 {
	if pageParam <= 0 {
		return (defaultPage - 1) * pageSize
	}
	return uint64(pageParam-1) * pageSize
}

// PageSize calculates the page size for pagination based on the provided size parameter.
func PageSize(sizeParam int64) uint64 {
	if sizeParam <= 0 {
		return defaultPageSize
	}
	return uint64(sizeParam)
}

// lazyTx wraps transaction state for lazy initialization.
type lazyTx struct {
	db        *sql.DB
	tx        TxInterface
	logger    logging.LoggerInterface
	committed bool
}

// get returns the transaction, creating it lazily on first call.
func (lt *lazyTx) get(ctx context.Context) (TxInterface, error) {
	if lt.tx != nil {
		return lt.tx, nil
	}

	// Use background context to prevent transaction from being auto-rolled back
	// when the request context is canceled
	tx, err := lt.db.BeginTx(context.Background(), &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: false})
	if err != nil {
		return nil, err
	}

	lt.tx = tx
	return tx, nil
}

// isStarted returns true if the transaction has been created.
func (lt *lazyTx) isStarted() bool {
	return lt.tx != nil
}

type DBClient struct {
	// pool is the native PGX pool we hold to allow closing
	pool *pgxpool.Pool
	// db original instance to handle transactions
	db *sql.DB
	// dbRunner is the runner instance of choice (either original DB or db with query cache, cannot be used for transactions)
	dbRunner sq.BaseRunner

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// Statement provides a StatementBuilderType configured to use the DBClient's database connection.
// If a transaction exists in the context, it will be used (created lazily on first use).
func (d *DBClient) Statement(ctx context.Context) sq.StatementBuilderType {
	// Check for lazy transaction first
	if lazyTx := lazyTxFromContext(ctx); lazyTx != nil {
		tx, err := lazyTx.get(ctx)
		if err != nil {
			// Log error but fall back to regular connection
			d.logger.Errorf("failed to create lazy transaction: %v", err)
		} else {
			return sq.StatementBuilder.
				PlaceholderFormat(sq.Dollar).
				RunWith(tx)
		}
	}

	// Check for regular transaction
	if tx := TxFromContext(ctx); tx != nil {
		return sq.StatementBuilder.
			PlaceholderFormat(sq.Dollar).
			RunWith(tx)
	}

	return sq.StatementBuilder.
		PlaceholderFormat(sq.Dollar).
		RunWith(d.dbRunner)
}

// TxStatement provides a StatementBuilderType configured to use a transaction.
func (d *DBClient) TxStatement(ctx context.Context) (TxInterface, sq.StatementBuilderType, error) {
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: false})
	if err != nil {
		return nil, sq.StatementBuilderType{}, err
	}

	return tx, sq.StatementBuilder.PlaceholderFormat(sq.Dollar).RunWith(tx), nil
}

// BeginTx starts a new transaction and returns a context with the transaction attached.
func (d *DBClient) BeginTx(ctx context.Context) (context.Context, TxInterface, error) {
	tx, err := d.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: false})
	if err != nil {
		return ctx, nil, err
	}

	return ContextWithTx(ctx, tx), tx, nil
}

// ContextWithTx returns a new context with the transaction attached.
func ContextWithTx(ctx context.Context, tx TxInterface) context.Context {
	return context.WithValue(ctx, txContextKey, tx)
}

// TxFromContext extracts a transaction from the context, returning nil if none exists.
func TxFromContext(ctx context.Context) TxInterface {
	if tx, ok := ctx.Value(txContextKey).(TxInterface); ok {
		return tx
	}
	return nil
}

// lazyTxFromContext extracts a lazy transaction holder from the context.
func lazyTxFromContext(ctx context.Context) *lazyTx {
	if lt, ok := ctx.Value(lazyTxContextKey).(*lazyTx); ok {
		return lt
	}
	return nil
}

// contextWithLazyTx returns a new context with a lazy transaction holder attached.
func contextWithLazyTx(ctx context.Context, lt *lazyTx) context.Context {
	return context.WithValue(ctx, lazyTxContextKey, lt)
}

// WithTx executes a function within a transaction context.
// The transaction is created lazily on first database access.
// If the function returns an error, the transaction is rolled back.
// Otherwise, the transaction is committed.
// If no database operations occurred, no transaction is created or committed.
func (d *DBClient) WithTx(ctx context.Context, fn func(context.Context) error) error {
	lt := &lazyTx{
		db:     d.db,
		logger: d.logger,
	}
	txCtx := contextWithLazyTx(ctx, lt)

	defer func() {
		// Only rollback if transaction was started and not committed
		if lt.isStarted() && !lt.committed {
			if err := lt.tx.Rollback(); err != nil && errors.Is(err, sql.ErrTxDone) {
				d.logger.Errorf("failed to rollback transaction: %v", err)
			}
		}
	}()

	if err := fn(txCtx); err != nil {
		return err
	}

	// Only commit if transaction was actually started
	if lt.isStarted() {
		if err := lt.tx.Commit(); err != nil {
			return fmt.Errorf("failed to commit transaction: %v", err)
		}
		lt.committed = true
	}

	return nil
}

func (d *DBClient) Close() {
	if d.db != nil {
		_ = d.db.Close()
	}

	if d.pool != nil {
		d.pool.Close()
	}
}

// NewDBClient creates a new DBClient instance with the provided DSN and configuration options.
func NewDBClient(dsn string, queryCacheEnabled bool, tracingEnabled bool, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *DBClient {
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		logger.Fatalf("DSN validation failed, shutting down, err: %v", err)
	}

	if tracingEnabled {
		// otelpgx.NewTracer will use default global TracerProvider, just like our tracer struct
		config.ConnConfig.Tracer = otelpgx.NewTracer()
	}

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		logger.Fatalf("DB pool creation failed, shutting down, err: %v", err)
	}

	if tracingEnabled {
		// when tracing is enabled, also collect metrics
		if err := otelpgx.RecordStats(pool); err != nil {
			logger.Fatalf("unable to start metrics collection for database: %v", err)
		}
	}

	db := stdlib.OpenDBFromPool(pool)
	if err := db.Ping(); err != nil {
		logger.Fatalf("DB connection failed, shutting down, err: %v", err)
	}

	d := new(DBClient)
	d.pool = pool
	d.db = db
	d.dbRunner = db

	if queryCacheEnabled {
		d.dbRunner = sq.NewStmtCache(db)
	}

	d.tracer = tracer
	d.monitor = monitor
	d.logger = logger

	return d
}
