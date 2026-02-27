package db

import (
	"context"
	"testing"

	"github.com/canonical/hook-service/internal/logging"
	"go.opentelemetry.io/otel/trace"
)

// MockLogger to capture Fatalf calls
type MockLogger struct {
	logging.LoggerInterface
	FatalfFunc func(template string, args ...interface{})
}

func (m *MockLogger) Fatalf(template string, args ...interface{}) {
	if m.FatalfFunc != nil {
		m.FatalfFunc(template, args...)
	}
}

func (m *MockLogger) Errorf(template string, args ...interface{}) {
	// no-op for now or allow mocking if needed
}

// Manual Mocks for Tracing and Monitoring to avoid code generation issues

type MockTracer struct{}

func (m *MockTracer) Start(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
	return ctx, trace.SpanFromContext(ctx)
}

type MockMonitor struct{}

func (m *MockMonitor) GetService() string { return "test-service" }
func (m *MockMonitor) SetResponseTimeMetric(labels map[string]string, value float64) error {
	return nil
}
func (m *MockMonitor) SetDependencyAvailability(labels map[string]string, value float64) error {
	return nil
}

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
	mockTracer := &MockTracer{}
	mockMonitor := &MockMonitor{}

	fatalCalled := false
	mockLogger := &MockLogger{
		FatalfFunc: func(template string, args ...interface{}) {
			fatalCalled = true
		},
	}

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
