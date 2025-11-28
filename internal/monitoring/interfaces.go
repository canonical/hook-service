// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package monitoring

type MonitorInterface interface {
	GetService() string
	SetResponseTimeMetric(map[string]string, float64) error
	SetDependencyAvailability(map[string]string, float64) error
}
