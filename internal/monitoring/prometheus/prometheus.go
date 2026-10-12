// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package prometheus

import (
	"fmt"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/prometheus/client_golang/prometheus"
)

type Monitor struct {
	service string

	responseTime           *prometheus.HistogramVec
	dependencyAvailability *prometheus.GaugeVec
	kafkaOAuthTokenRefresh *prometheus.CounterVec
	kafkaOAuthTokenExpiry  prometheus.Gauge

	logger logging.LoggerInterface
}

func (m *Monitor) GetService() string {
	return m.service
}

func (m *Monitor) SetResponseTimeMetric(tags map[string]string, value float64) error {
	if m.responseTime == nil {
		return fmt.Errorf("metric not instantiated")
	}

	observer, err := m.responseTime.GetMetricWith(tags)
	if err != nil {
		return fmt.Errorf("failed to get response time metric: %v", err)
	}

	observer.Observe(value)

	return nil
}

func (m *Monitor) SetDependencyAvailability(tags map[string]string, value float64) error {
	if m.dependencyAvailability == nil {
		return fmt.Errorf("metric not instantiated")
	}

	gauge, err := m.dependencyAvailability.GetMetricWith(tags)
	if err != nil {
		return fmt.Errorf("failed to get dependency availability metric: %v", err)
	}

	gauge.Set(value)

	return nil
}

func (m *Monitor) IncKafkaOAuthTokenRefresh(status string) error {
	if m.kafkaOAuthTokenRefresh == nil {
		return fmt.Errorf("metric not instantiated")
	}

	counter, err := m.kafkaOAuthTokenRefresh.GetMetricWith(map[string]string{"status": status})
	if err != nil {
		return fmt.Errorf("failed to get kafka oauth token refresh metric: %v", err)
	}

	counter.Inc()

	return nil
}

func (m *Monitor) SetKafkaOAuthTokenExpiry(seconds float64) error {
	if m.kafkaOAuthTokenExpiry == nil {
		return fmt.Errorf("metric not instantiated")
	}

	m.kafkaOAuthTokenExpiry.Set(seconds)

	return nil
}


func (m *Monitor) registerHistograms() {
	histograms := make([]*prometheus.HistogramVec, 0)

	labels := map[string]string{
		"service": m.service,
	}

	m.responseTime = prometheus.NewHistogramVec(
		prometheus.HistogramOpts{
			Name:        "http_response_time_seconds",
			Help:        "http_response_time_seconds",
			ConstLabels: labels,
		},
		[]string{"route", "status"},
	)

	histograms = append(histograms, m.responseTime)

	for _, histogram := range histograms {
		err := prometheus.Register(histogram)

		switch err.(type) {
		case nil:
			continue
		case prometheus.AlreadyRegisteredError:
			m.logger.Debugf("metric %v already registered", histogram)
		default:
			m.logger.Errorf("metric %v could not be registered", histogram)
		}
	}
}

func (m *Monitor) registerGauges() {
	collectors := make([]prometheus.Collector, 0)

	labels := map[string]string{
		"service": m.service,
	}

	m.dependencyAvailability = prometheus.NewGaugeVec(
		prometheus.GaugeOpts{
			Name:        "dependency_available",
			Help:        "dependency_available",
			ConstLabels: labels,
		},
		[]string{"component"},
	)
	collectors = append(collectors, m.dependencyAvailability)

	m.kafkaOAuthTokenExpiry = prometheus.NewGauge(
		prometheus.GaugeOpts{
			Name:        "kafka_oauth_token_expiry_seconds",
			Help:        "kafka_oauth_token_expiry_seconds",
			ConstLabels: labels,
		},
	)
	collectors = append(collectors, m.kafkaOAuthTokenExpiry)

	for _, collector := range collectors {
		err := prometheus.Register(collector)

		switch err.(type) {
		case nil:
			continue
		case prometheus.AlreadyRegisteredError:
			m.logger.Debugf("metric %v already registered", collector)
		default:
			m.logger.Errorf("metric %v could not be registered", collector)
		}
	}
}

func (m *Monitor) registerCounters() {
	counters := make([]*prometheus.CounterVec, 0)

	labels := map[string]string{
		"service": m.service,
	}

	m.kafkaOAuthTokenRefresh = prometheus.NewCounterVec(
		prometheus.CounterOpts{
			Name:        "kafka_oauth_token_refresh_total",
			Help:        "kafka_oauth_token_refresh_total",
			ConstLabels: labels,
		},
		[]string{"status"},
	)

	counters = append(counters, m.kafkaOAuthTokenRefresh)

	for _, counter := range counters {
		err := prometheus.Register(counter)

		switch err.(type) {
		case nil:
			continue
		case prometheus.AlreadyRegisteredError:
			m.logger.Debugf("metric %v already registered", counter)
		default:
			m.logger.Errorf("metric %v could not be registered", counter)
		}
	}
}

func NewMonitor(service string, logger logging.LoggerInterface) *Monitor {
	m := new(Monitor)

	m.service = service
	m.logger = logger

	m.registerHistograms()
	m.registerGauges()
	m.registerCounters()

	return m
}

