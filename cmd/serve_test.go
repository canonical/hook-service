// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"testing"

	"go.uber.org/mock/gomock"

	"github.com/canonical/hook-service/internal/config"
	"github.com/canonical/hook-service/internal/kafka"
)

//go:generate mockgen -build_flags=--mod=mod -package cmd -destination ./mock_logger.go -source=../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package cmd -destination ./mock_monitor.go -source=../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package cmd -destination ./mock_tracing.go -source=../internal/tracing/interfaces.go

// TestNewPublisherSetupPairsPublisherWithMode covers the specification requirement that
// the reported mode reflects the publisher selected at startup. The mode and the
// publisher are asserted together, so a future change that reports one while wiring the
// other fails here rather than in production.
func TestNewPublisherSetupPairsPublisherWithMode(t *testing.T) {
	tests := []struct {
		name         string
		serviceName  string
		brokers      []string
		wantMode     config.DeploymentMode
		wantPlatform bool
		wantWarnings int
	}{
		{
			name:         "default name without brokers is standalone and agrees",
			serviceName:  config.DefaultFederatedServiceName,
			brokers:      nil,
			wantMode:     config.ModeStandalone,
			wantPlatform: false,
			wantWarnings: 0,
		},
		{
			name:         "named deployment without brokers is standalone and warns",
			serviceName:  "portal",
			brokers:      nil,
			wantMode:     config.ModeStandalone,
			wantPlatform: false,
			wantWarnings: 1,
		},
		{
			name:         "named deployment with brokers is platform and agrees",
			serviceName:  "portal",
			brokers:      []string{"localhost:9092"},
			wantMode:     config.ModePlatform,
			wantPlatform: true,
			wantWarnings: 0,
		},
		{
			name:         "default name with brokers is platform and warns",
			serviceName:  config.DefaultFederatedServiceName,
			brokers:      []string{"localhost:9092"},
			wantMode:     config.ModePlatform,
			wantPlatform: true,
			wantWarnings: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			logger := NewMockLoggerInterface(ctrl)
			monitor := NewMockMonitorInterface(ctrl)
			tracer := NewMockTracingInterface(ctrl)

			logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
			logger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any()).Times(tt.wantWarnings)

			specs := &config.EnvSpec{
				FederatedServiceName: tt.serviceName,
				KafkaBrokers:         tt.brokers,
			}

			setup, err := newPublisherSetup(specs, tracer, monitor, logger)
			if err != nil {
				t.Fatalf("newPublisherSetup() error = %v", err)
			}
			t.Cleanup(func() {
				if err := setup.close(); err != nil {
					t.Errorf("close() error = %v", err)
				}
			})

			if setup.mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", setup.mode, tt.wantMode)
			}

			_, isPlatform := setup.publisher.(*kafka.PermissionPublisher)
			_, isNoop := setup.publisher.(*kafka.NoopPublisher)

			if isPlatform == isNoop {
				t.Fatalf("publisher is neither exclusively a platform nor a noop publisher: %T", setup.publisher)
			}
			if isPlatform != tt.wantPlatform {
				t.Errorf("publisher is %T, want platform=%v", setup.publisher, tt.wantPlatform)
			}
			// The pairing itself: a platform publisher must never be reported as standalone.
			if isPlatform && setup.mode != config.ModePlatform {
				t.Errorf("platform publisher reported as mode %q", setup.mode)
			}
			if isNoop && setup.mode != config.ModeStandalone {
				t.Errorf("noop publisher reported as mode %q", setup.mode)
			}
		})
	}
}

func TestNewPublisherSetupRejectsInvalidConfiguration(t *testing.T) {
	tests := []struct {
		name        string
		serviceName string
		brokers     []string
	}{
		{
			name:        "empty federated service name",
			serviceName: "",
			brokers:     nil,
		},
		{
			name:        "whitespace-only federated service name",
			serviceName: "   ",
			brokers:     nil,
		},
		{
			name:        "federated service name with invalid characters",
			serviceName: "hook service",
			brokers:     nil,
		},
		{
			name:        "broker address without a port",
			serviceName: config.DefaultFederatedServiceName,
			brokers:     []string{"localhost"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()

			logger := NewMockLoggerInterface(ctrl)
			monitor := NewMockMonitorInterface(ctrl)
			tracer := NewMockTracingInterface(ctrl)

			logger.EXPECT().Infof(gomock.Any(), gomock.Any()).AnyTimes()
			logger.EXPECT().Warnf(gomock.Any(), gomock.Any(), gomock.Any()).AnyTimes()

			specs := &config.EnvSpec{
				FederatedServiceName: tt.serviceName,
				KafkaBrokers:         tt.brokers,
			}

			setup, err := newPublisherSetup(specs, tracer, monitor, logger)
			if err == nil {
				t.Fatalf("expected an error, got setup %+v", setup)
			}
			if setup != nil {
				t.Errorf("expected nil setup on error, got %+v", setup)
			}
		})
	}
}
