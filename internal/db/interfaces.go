// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package db

import (
	"context"

	sq "github.com/Masterminds/squirrel"
)

type DBClientInterface interface {
	Statement(context.Context) sq.StatementBuilderType
	TxStatement(context.Context) (TxInterface, sq.StatementBuilderType, error)
	BeginTx(context.Context) (context.Context, TxInterface, error)
	WithTx(context.Context, func(context.Context) error) error
	Close()
}

type TxInterface interface {
	Commit() error
	Rollback() error
	sq.BaseRunner
}
