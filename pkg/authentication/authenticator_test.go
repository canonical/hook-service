// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package authentication

//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_logger.go -source=../../internal/logging/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_monitor.go -source=../../internal/monitoring/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_tracer.go -source=../../internal/tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package authentication -destination ./mock_verifier.go -source=./interfaces.go
