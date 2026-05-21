// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package db

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"sync/atomic"
	"time"

	sq "github.com/Masterminds/squirrel"
	"github.com/exaring/otelpgx"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/prometheus/client_golang/prometheus"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

const (
	defaultPage      uint64 = 1
	defaultPageSize  uint64 = 100
	defaultTxTimeout        = time.Second * 60
)

type TxContextKey struct{}
type LazyTxContextKey struct{}
type ReadOnlyContextKey struct{}

var txContextKey TxContextKey
var lazyTxContextKey LazyTxContextKey
var readOnlyContextKey ReadOnlyContextKey

var (
	replicaQueries = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hook_service_replica_queries_total",
		Help: "Total number of queries routed to the replica",
	})
	replicaLagGauge = prometheus.NewGauge(prometheus.GaugeOpts{
		Name: "hook_service_replica_lag_ms",
		Help: "Current replication lag in milliseconds",
	})
	primaryFallbacks = prometheus.NewCounter(prometheus.CounterOpts{
		Name: "hook_service_primary_fallback_total",
		Help: "Total number of fallbacks to the primary pool",
	})
)

func registerMetrics(logger logging.LoggerInterface) {
	for _, collector := range []prometheus.Collector{replicaQueries, replicaLagGauge, primaryFallbacks} {
		err := prometheus.Register(collector)
		switch err.(type) {
		case nil:
			continue
		case prometheus.AlreadyRegisteredError:
			logger.Debugf("metric %v already registered", collector)
		default:
			logger.Errorf("metric %v could not be registered", collector)
		}
	}
}

type Config struct {
	DSN             string
	MaxConns        int32
	MinConns        int32
	MaxConnLifetime time.Duration
	MaxConnIdleTime time.Duration
	TracingEnabled  bool

	ReplicaDSN              string
	ReplicaMaxConns         int32
	ReplicaMinConns         int32
	ReplicaMaxConnLifetime  time.Duration
	ReplicaMaxConnIdleTime  time.Duration
	MaxReplicaLagMs         int64
	ReplicaPoolSizeMultiplier float64
}

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
	cancel    context.CancelFunc
}

// get returns the transaction, creating it lazily on first call.
func (lt *lazyTx) get() (TxInterface, error) {
	if lt.tx != nil {
		return lt.tx, nil
	}

	// Use background context to prevent transaction from being auto-rolled back
	// when the request context is canceled.
	// We add a timeout to ensure the transaction doesn't hang indefinitely.
	ctx, cancel := context.WithTimeout(context.Background(), defaultTxTimeout)
	tx, err := lt.db.BeginTx(ctx, &sql.TxOptions{Isolation: sql.LevelReadCommitted, ReadOnly: false})
	if err != nil {
		cancel()
		return nil, err
	}

	lt.tx = tx
	lt.cancel = cancel
	return tx, nil
}

// isStarted returns true if the transaction has been created.
func (lt *lazyTx) isStarted() bool {
	return lt.tx != nil
}

type DBClient struct {
	pool     *pgxpool.Pool
	db       *sql.DB
	dbRunner sq.BaseRunner

	replicaPool   *pgxpool.Pool
	replicaDB     *sql.DB
	replicaRunner sq.BaseRunner
	replicaLagMs  int64
	maxLagMs      int64

	replicaCancel context.CancelFunc

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// Statement provides a StatementBuilderType configured to use the DBClient's database connection.
// If a transaction exists in the context, it will be used (created lazily on first use).
func (d *DBClient) Statement(ctx context.Context) sq.StatementBuilderType {
	if lazyTx := lazyTxFromContext(ctx); lazyTx != nil {
		tx, err := lazyTx.get()
		if err != nil {
			d.logger.Errorf("failed to create lazy transaction: %v", err)
		} else {
			return sq.StatementBuilder.
				PlaceholderFormat(sq.Dollar).
				RunWith(tx)
		}
	}

	if tx := TxFromContext(ctx); tx != nil {
		return sq.StatementBuilder.
			PlaceholderFormat(sq.Dollar).
			RunWith(tx)
	}

	if readOnlyFromContext(ctx) && d.replicaRunner != nil {
		currentLag := atomic.LoadInt64(&d.replicaLagMs)
		if currentLag > d.maxLagMs {
			d.logger.Warnf("replica lag %dms exceeds threshold %dms, falling back to primary", currentLag, d.maxLagMs)
			primaryFallbacks.Inc()
			return sq.StatementBuilder.
				PlaceholderFormat(sq.Dollar).
				RunWith(d.dbRunner)
		}
		replicaQueries.Inc()
		return sq.StatementBuilder.
			PlaceholderFormat(sq.Dollar).
			RunWith(d.replicaRunner)
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

func contextWithReadOnly(ctx context.Context) context.Context {
	return context.WithValue(ctx, readOnlyContextKey, struct{}{})
}

func readOnlyFromContext(ctx context.Context) bool {
	_, ok := ctx.Value(readOnlyContextKey).(struct{})
	return ok
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
			if err := lt.tx.Rollback(); err != nil && !errors.Is(err, sql.ErrTxDone) {
				d.logger.Errorf("failed to rollback transaction: %v", err)
			}
		}
		if lt.cancel != nil {
			lt.cancel()
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
	if d.replicaCancel != nil {
		d.replicaCancel()
	}

	if d.replicaDB != nil {
		_ = d.replicaDB.Close()
	}

	if d.replicaPool != nil {
		d.replicaPool.Close()
	}

	if d.db != nil {
		_ = d.db.Close()
	}

	if d.pool != nil {
		d.pool.Close()
	}
}

// NewDBClient creates a new DBClient instance with the provided DSN and configuration options.
func NewDBClient(cfg Config, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) (*DBClient, error) {
	config, err := pgxpool.ParseConfig(cfg.DSN)
	if err != nil {
		logger.Fatalf("DSN validation failed, shutting down, err: %v", err)
	}

	if cfg.TracingEnabled {
		// otelpgx.NewTracer will use default global TracerProvider, just like our tracer struct
		config.ConnConfig.Tracer = otelpgx.NewTracer()
	}

	config.MaxConns = cfg.MaxConns
	config.MinConns = cfg.MinConns
	config.MaxConnLifetime = cfg.MaxConnLifetime
	config.MaxConnLifetimeJitter = cfg.MaxConnLifetime / 10 // Add 10% jitter to avoid thundering herd
	config.MaxConnIdleTime = cfg.MaxConnIdleTime

	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		return nil, fmt.Errorf("failed to create db pool: %v", err)
	}

	if cfg.TracingEnabled {
		// when tracing is enabled, also collect metrics
		if err := otelpgx.RecordStats(pool); err != nil {
			return nil, fmt.Errorf("failed to start metrics collection for database: %v", err)
		}
	}

	db := stdlib.OpenDBFromPool(pool)
	pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
	err = db.PingContext(pingCtx)
	pingCancel()
	if err != nil {
		return nil, fmt.Errorf("failed to connect to the database: %v", err)
	}

	d := new(DBClient)
	d.pool = pool
	d.db = db
	d.dbRunner = db

	d.maxLagMs = cfg.MaxReplicaLagMs
	if d.maxLagMs <= 0 {
		d.maxLagMs = 1000
	}

	d.tracer = tracer
	d.monitor = monitor
	d.logger = logger

	registerMetrics(logger)

	if cfg.ReplicaDSN != "" {
		replicaCfg, err := pgxpool.ParseConfig(cfg.ReplicaDSN)
		if err != nil {
			logger.Warnf("failed to parse replica DSN, falling back to primary-only mode: %v", err)
			return d, nil
		}

		if cfg.TracingEnabled {
			replicaCfg.ConnConfig.Tracer = otelpgx.NewTracer()
		}

		replicaMaxConns := cfg.ReplicaMaxConns
		if cfg.ReplicaPoolSizeMultiplier > 0 && replicaMaxConns == 0 {
			replicaMaxConns = int32(float64(cfg.MaxConns) * cfg.ReplicaPoolSizeMultiplier)
		}
		replicaCfg.MaxConns = replicaMaxConns
		replicaCfg.MinConns = cfg.ReplicaMinConns
		replicaCfg.MaxConnLifetime = cfg.ReplicaMaxConnLifetime
		replicaCfg.MaxConnLifetimeJitter = cfg.ReplicaMaxConnLifetime / 10
		replicaCfg.MaxConnIdleTime = cfg.ReplicaMaxConnIdleTime

		replicaPool, err := pgxpool.NewWithConfig(context.Background(), replicaCfg)
		if err != nil {
			logger.Warnf("failed to create replica pool, falling back to primary-only mode: %v", err)
			return d, nil
		}

		replicaDB := stdlib.OpenDBFromPool(replicaPool)
		pingCtx, pingCancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = replicaDB.PingContext(pingCtx)
		pingCancel()
		if err != nil {
			logger.Warnf("failed to ping replica database, falling back to primary-only mode: %v", err)
			replicaDB.Close()
			replicaPool.Close()
			return d, nil
		}

		d.replicaPool = replicaPool
		d.replicaDB = replicaDB
		d.replicaRunner = replicaDB

		lagCtx, lagCancel := context.WithCancel(context.Background())
		d.replicaCancel = lagCancel
		go d.monitorReplicationLag(lagCtx)
	}

	return d, nil
}

func (d *DBClient) monitorReplicationLag(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			queryCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
			var lagMs int64
			err := d.db.QueryRowContext(queryCtx,
				"SELECT COALESCE(MAX(EXTRACT(EPOCH FROM replay_lag) * 1000), 0) FROM pg_stat_replication",
			).Scan(&lagMs)
			cancel()
			if err != nil {
				if isMissingRelation(err) {
					d.logger.Warnf("pg_stat_replication not available (not a primary), stopping replication lag monitor")
					return
				}
				d.logger.Warnf("failed to query replication lag: %v", err)
				continue
			}
			atomic.StoreInt64(&d.replicaLagMs, lagMs)
			replicaLagGauge.Set(float64(lagMs))
		}
	}
}

func isMissingRelation(err error) bool {
	if err == nil {
		return false
	}
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == "42P01"
	}
	return false
}
