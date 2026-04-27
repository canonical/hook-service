// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package tenants

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
)

// ErrNotMember indicates the user is not an active member of the tenant.
var ErrNotMember = errors.New("user is not a member of the tenant")

// tenant is the minimal shape returned by the tenant-service lookup endpoint.
type tenant struct {
	ID string `json:"id"`
}

// lookupResponse is the JSON body returned by GET /api/v0/tenants/lookup.
type lookupResponse struct {
	Tenants []tenant `json:"tenants"`
}

// Client calls tenant-service's lookup API to validate membership.
type Client struct {
	baseURL    string
	httpClient *http.Client

	tracer  tracing.TracingInterface
	monitor monitoring.MonitorInterface
	logger  logging.LoggerInterface
}

// NewClient creates a tenant-service client pointed at baseURL.
// timeout caps the total time allowed for each lookup request (DNS + connect + body).
// The client's transport propagates the active OpenTelemetry span as a traceparent
// header so tenant-service can continue the distributed trace.
func NewClient(baseURL string, timeout time.Duration, tracer tracing.TracingInterface, monitor monitoring.MonitorInterface, logger logging.LoggerInterface) *Client {
	transport := otelhttp.NewTransport(&http.Transport{
		MaxConnsPerHost: 50,
	})
	return &Client{
		baseURL: baseURL,
		httpClient: &http.Client{
			Timeout:   timeout,
			Transport: transport,
		},
		tracer:  tracer,
		monitor: monitor,
		logger:  logger,
	}
}

// ValidateMembership checks whether the user identified by identityID is an
// active member of the given tenant. Returns nil if valid, ErrNotMember if
// the user has no active membership, or an error on network/server failure.
func (c *Client) ValidateMembership(ctx context.Context, identityID, tenantID string) error {
	ctx, span := c.tracer.Start(ctx, "tenants.Client.ValidateMembership")
	defer span.End()

	span.SetAttributes(
		attribute.String("identity_id", identityID),
		attribute.String("tenant_id", tenantID),
	)

	lookupURL := fmt.Sprintf("%s/api/v0/tenants/lookup?identity_id=%s",
		c.baseURL, url.QueryEscape(identityID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, lookupURL, nil)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "cannot create request")
		return fmt.Errorf("cannot create request: %v", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "cannot reach tenant-service")
		return fmt.Errorf("cannot reach tenant-service: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		err := fmt.Errorf("tenant-service returned status %d", resp.StatusCode)
		span.RecordError(err)
		span.SetStatus(codes.Error, "tenant-service error response")
		return err
	}

	var result lookupResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		span.RecordError(err)
		span.SetStatus(codes.Error, "cannot decode response")
		return fmt.Errorf("cannot decode tenant-service response: %v", err)
	}

	for _, t := range result.Tenants {
		if t.ID == tenantID {
			span.SetStatus(codes.Ok, "membership validated")
			return nil
		}
	}

	span.SetStatus(codes.Ok, "membership denied")
	return ErrNotMember
}
