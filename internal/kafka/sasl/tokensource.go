// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sasl

import (
	"cmp"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/clientcredentials"
	"golang.org/x/sync/singleflight"

	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/tracing"
)

// DefaultEarlyRenewalMargin is the early renewal threshold (60 seconds)
// before token expiry to ensure cached tokens used for dialing are adequately fresh.
const DefaultEarlyRenewalMargin = 60 * time.Second

// DefaultFetchTimeout is the maximum duration for outbound token fetch requests.
const DefaultFetchTimeout = 10 * time.Second

// ErrTokenFetchTimeout is the error cause when outbound token acquisition times out.
var ErrTokenFetchTimeout = errors.New("oauth token fetch timed out")

// TokenSourceConfig configures an in-memory proactively renewed TokenSource.
type TokenSourceConfig struct {
	TokenURL      string
	ClientID      string
	ClientSecret  string
	Scopes        []string
	Audience      string
	HTTPClient    *http.Client
	RefreshMargin time.Duration
	FetchTimeout  time.Duration
}

var _ TokenProvider = (*TokenSource)(nil)

// TokenSource caches OAuth2 access tokens in-memory and proactively renews them
// before expiry with thread-safe single-flight synchronization.
type TokenSource struct {
	mu            sync.RWMutex
	cachedToken   *oauth2.Token
	refreshMargin time.Duration
	fetchTimeout  time.Duration
	fetcher       func(ctx context.Context) (*oauth2.Token, error)
	sfg           singleflight.Group

	tokenURL string
	clientID string
	audience string

	tracer  tracing.TracingInterface
	monitor TokenMetricsInterface
	logger  logging.LoggerInterface
}

// NewTokenSource creates a TokenSource configured with client credentials.
func NewTokenSource(
	cfg TokenSourceConfig,
	tracer tracing.TracingInterface,
	monitor TokenMetricsInterface,
	logger logging.LoggerInterface,
) *TokenSource {
	conf := &clientcredentials.Config{
		ClientID:     cfg.ClientID,
		ClientSecret: cfg.ClientSecret,
		TokenURL:     cfg.TokenURL,
		Scopes:       cfg.Scopes,
		AuthStyle:    oauth2.AuthStyleInHeader,
	}
	if cfg.Audience != "" {
		conf.EndpointParams = url.Values{"audience": {cfg.Audience}}
	}

	fetcher := func(ctx context.Context) (*oauth2.Token, error) {
		if cfg.HTTPClient != nil {
			ctx = context.WithValue(ctx, oauth2.HTTPClient, cfg.HTTPClient)
		}
		return conf.Token(ctx)
	}

	ts := NewTokenSourceWithFetcher(
		fetcher,
		cmp.Or(cfg.RefreshMargin, DefaultEarlyRenewalMargin),
		cmp.Or(cfg.FetchTimeout, DefaultFetchTimeout),
		tracer,
		monitor,
		logger,
	)
	ts.tokenURL = cfg.TokenURL
	ts.clientID = cfg.ClientID
	ts.audience = cfg.Audience
	return ts
}

// NewTokenSourceWithFetcher creates a TokenSource with a custom token fetcher function.
func NewTokenSourceWithFetcher(
	fetcher func(ctx context.Context) (*oauth2.Token, error),
	refreshMargin time.Duration,
	fetchTimeout time.Duration,
	tracer tracing.TracingInterface,
	monitor TokenMetricsInterface,
	logger logging.LoggerInterface,
) *TokenSource {
	if tracer == nil {
		tracer = tracing.NewNoopTracer()
	}
	if monitor == nil {
		monitor = NoopTokenMetrics{}
	}
	if logger == nil {
		logger = logging.NewNoopLogger()
	}
	return &TokenSource{
		refreshMargin: cmp.Or(refreshMargin, DefaultEarlyRenewalMargin),
		fetchTimeout:  cmp.Or(fetchTimeout, DefaultFetchTimeout),
		fetcher:       fetcher,
		tracer:        tracer,
		monitor:       monitor,
		logger:        logger,
	}
}

// Token returns a valid OAuth access token, reusing cached tokens if valid
// and outside the proactive early renewal margin. Concurrent token acquisitions
// are coordinated via singleflight to prevent duplicate in-flight requests without
// serializing callers across network I/O.
func (ts *TokenSource) Token(ctx context.Context) (string, error) {
	// 1. Fast-path: check if existing cached token is valid and outside the renewal margin.
	ts.mu.RLock()
	cached := ts.cachedToken
	ts.mu.RUnlock()

	if cached != nil && cached.AccessToken != "" {
		if cached.Expiry.IsZero() || time.Until(cached.Expiry) > ts.refreshMargin {
			return cached.AccessToken, nil
		}
	}

	// 2. Coalesce concurrent token fetches into a single in-flight request via singleflight.
	ch := ts.sfg.DoChan("fetch", func() (result any, err error) {
		// Double-checked locking: re-verify cache in case a preceding fetch already populated it.
		ts.mu.RLock()
		cached := ts.cachedToken
		ts.mu.RUnlock()
		if cached != nil && cached.AccessToken != "" {
			if cached.Expiry.IsZero() || time.Until(cached.Expiry) > ts.refreshMargin {
				return cached.AccessToken, nil
			}
		}

		// Detach from caller context to prevent one caller's cancellation from aborting
		// the shared singleflight fetch, while strictly enforcing outbound fetchTimeout.
		fetchCtx, cancel := context.WithTimeoutCause(context.WithoutCancel(ctx), ts.fetchTimeout, ErrTokenFetchTimeout)
		defer cancel()

		fetchCtx, span := ts.tracer.Start(fetchCtx, "kafka.oauth.fetch_token")
		defer span.End()
		defer func() {
			if r := recover(); r != nil {
				result = nil
				err = fmt.Errorf("panic during oauth token fetch: %v", r)
				span.RecordError(err)
				span.SetStatus(codes.Error, err.Error())
				_ = ts.monitor.IncKafkaOAuthTokenRefresh("error")
				ts.logger.Errorf("panic during oauth token fetch: %v", r)
			}
		}()

		span.SetAttributes(
			attribute.String("oauth.client_id", ts.clientID),
			attribute.String("oauth.token_url", ts.tokenURL),
			attribute.String("oauth.audience", ts.audience),
		)

		// Execute outbound token acquisition and validate response.
		tok, fetchErr := ts.fetcher(fetchCtx)
		if fetchErr != nil {
			span.RecordError(fetchErr)
			span.SetStatus(codes.Error, "failed to fetch oauth token")
			_ = ts.monitor.IncKafkaOAuthTokenRefresh("error")
			ts.logger.Errorf("failed to acquire Kafka OAuth token: %v", fetchErr)
			return nil, fmt.Errorf("failed to fetch oauth token: %w", fetchErr)
		}
		if tok == nil || tok.AccessToken == "" {
			emptyErr := errors.New("received empty oauth token from provider")
			span.RecordError(emptyErr)
			span.SetStatus(codes.Error, emptyErr.Error())
			_ = ts.monitor.IncKafkaOAuthTokenRefresh("error")
			ts.logger.Errorf("received empty Kafka OAuth token from provider")
			return nil, emptyErr
		}

		// Record successful refresh telemetry and update cache.
		span.SetStatus(codes.Ok, "token acquired successfully")
		_ = ts.monitor.IncKafkaOAuthTokenRefresh("success")
		if !tok.Expiry.IsZero() {
			_ = ts.monitor.SetKafkaOAuthTokenExpiry(float64(tok.Expiry.Unix()))
		}
		ts.logger.Debugf("successfully acquired Kafka OAuth token")

		ts.mu.Lock()
		ts.cachedToken = tok
		ts.mu.Unlock()

		return tok.AccessToken, nil
	})

	// 3. Await singleflight fetch result or abort early if caller context expires.
	select {
	case <-ctx.Done():
		return "", ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			ts.mu.RLock()
			cached := ts.cachedToken
			ts.mu.RUnlock()
			if cached != nil && cached.AccessToken != "" && (cached.Expiry.IsZero() || time.Now().Before(cached.Expiry)) {
				ts.logger.Warnf("failed to refresh Kafka OAuth token within early renewal margin, falling back to valid cached token: %v", res.Err)
				return cached.AccessToken, nil
			}
			return "", res.Err
		}
		return res.Val.(string), nil
	}
}

// Invalidate purges any cached access token, ensuring subsequent acquisitions fetch a fresh token.
func (ts *TokenSource) Invalidate() {
	ts.mu.Lock()
	ts.cachedToken = nil
	ts.mu.Unlock()
	ts.logger.Debugf("invalidated cached Kafka OAuth token")
}
