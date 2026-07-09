// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package db

//go:generate mockgen -build_flags=--mod=mod -package db -destination ./mock_tracing.go -source=../tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package db -destination ./mock_monitor.go -source=../monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package db -destination ./mock_logger.go -source=../logging/interfaces.go

import (
	"testing"

	"go.uber.org/mock/gomock"
)

func TestOffset(t *testing.T) {
	tests := []struct {
		name      string
		pageParam int64
		pageSize  uint64
		want      uint64
	}{
		{
			name:      "Process first page correctly",
			pageParam: 1,
			pageSize:  10,
			want:      0,
		},
		{
			name:      "Process second page correctly",
			pageParam: 2,
			pageSize:  10,
			want:      10,
		},
		{
			name:      "Handle zero page param (default to 1)",
			pageParam: 0,
			pageSize:  10,
			want:      0,
		},
		{
			name:      "Handle negative page param (default to 1)",
			pageParam: -1,
			pageSize:  10,
			want:      0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Offset(tt.pageParam, tt.pageSize); got != tt.want {
				t.Errorf("Offset() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPageSize(t *testing.T) {
	tests := []struct {
		name      string
		sizeParam int64
		want      uint64
	}{
		{
			name:      "Process valid size",
			sizeParam: 50,
			want:      50,
		},
		{
			name:      "Handle zero size (default)",
			sizeParam: 0,
			want:      defaultPageSize,
		},
		{
			name:      "Handle negative size (default)",
			sizeParam: -5,
			want:      defaultPageSize,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PageSize(tt.sizeParam); got != tt.want {
				t.Errorf("PageSize() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestNewDBClient_DSNValidationFailure(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockMonitorInterface(ctrl)
	mockLogger := NewMockLoggerInterface(ctrl)

	fatalCalled := false
	mockLogger.EXPECT().Fatalf(gomock.Any(), gomock.Any()).Do(func(template string, args ...interface{}) {
		fatalCalled = true
	}).AnyTimes()

	cfg := Config{
		DSN: "invalid-dsn",
	}

	// recover from potential panic if NewDBClient continues after "Fatal"
	defer func() {
		if r := recover(); r != nil {
			// expected if we let it continue with invalid config
		}
	}()

	_, _ = NewDBClient(cfg, mockTracer, mockMonitor, mockLogger)

	if !fatalCalled {
		t.Error("Expected logger.Fatalf to be called for invalid DSN")
	}
}
