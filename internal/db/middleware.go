// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package db

import (
	"context"
	"fmt"
	"net/http"

	"github.com/canonical/hook-service/internal/logging"
)

// TransactionMiddleware creates a middleware that wraps each request in a database transaction.
// The transaction is committed if the handler completes successfully (status < 400).
// The transaction is rolled back if the handler returns an error or status >= 400.
func TransactionMiddleware(db DBClientInterface, logger logging.LoggerInterface) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()

			if r.Method == http.MethodGet || r.Method == http.MethodHead {
				// No need for a transaction on read-only requests
				next.ServeHTTP(w, r)
				return
			}

			db.WithTx(ctx, func(txCtx context.Context) error {
				rw := &responseWriter{
					ResponseWriter: w,
					statusCode:     http.StatusOK,
				}

				next.ServeHTTP(rw, r.WithContext(txCtx))

				if rw.statusCode >= 400 {
					return fmt.Errorf("request failed with status %d", rw.statusCode)
				}

				return nil
			})
		})
	}
}

type responseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.statusCode = code
	rw.ResponseWriter.WriteHeader(code)
}
