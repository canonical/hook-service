// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package storage

import (
	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

var _ StorageInterface = (*Storage)(nil)

type Storage struct {
	db db.DBClientInterface

	logger  logging.LoggerInterface
	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
}

func NewStorage(c db.DBClientInterface, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *Storage {
	s := new(Storage)

	s.db = c

	s.logger = logger
	s.tracer = tracer
	s.monitor = monitor

	return s
}
