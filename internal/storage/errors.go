// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package storage

import (
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5/pgconn"
)

// Sentinel errors for storage operations.
var (
	ErrNotFound            = errors.New("resource not found")
	ErrDuplicateKey        = errors.New("duplicate key violation")
	ErrForeignKeyViolation = errors.New("foreign key violation")
)

// PostgreSQL error codes
const (
	pgErrCodeUniqueViolation     = "23505"
	pgErrCodeForeignKeyViolation = "23503"
)

// IsDuplicateKeyError checks if the error is a PostgreSQL unique constraint violation.
func IsDuplicateKeyError(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgErrCodeUniqueViolation
	}
	return false
}

// IsForeignKeyViolation checks if the error is a PostgreSQL foreign key violation.
func IsForeignKeyViolation(err error) bool {
	var pgErr *pgconn.PgError
	if errors.As(err, &pgErr) {
		return pgErr.Code == pgErrCodeForeignKeyViolation
	}
	return false
}

// WrapDuplicateKeyError wraps a duplicate key error with context about which constraint was violated.
func WrapDuplicateKeyError(err error, context string) error {
	if !IsDuplicateKeyError(err) {
		return err
	}
	return fmt.Errorf("%s: %w", context, ErrDuplicateKey)
}

// WrapForeignKeyError wraps a foreign key violation with context.
func WrapForeignKeyError(err error, context string) error {
	if !IsForeignKeyViolation(err) {
		return err
	}
	return fmt.Errorf("%s: %w", context, ErrForeignKeyViolation)
}
