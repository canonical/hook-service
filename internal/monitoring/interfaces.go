// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package monitoring

//go:generate mockgen -build_flags=--mod=mod -package monitoring -destination ./mock_monitor.go -source=./interfaces.go

type MonitorInterface interface {
	GetService() string
	SetResponseTimeMetric(map[string]string, float64) error
	SetDependencyAvailability(map[string]string, float64) error
}

