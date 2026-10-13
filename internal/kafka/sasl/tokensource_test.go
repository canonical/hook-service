// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sasl

//go:generate mockgen -build_flags=--mod=mod -package sasl -destination ./mock_tracing.go -source=../../tracing/interfaces.go
//go:generate mockgen -build_flags=--mod=mod -package sasl -destination ./mock_interfaces.go -source=./interfaces.go

import (
	"context"
	"errors"
	"fmt"
	"runtime"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/codes"
	sdktrace "go.opentelemetry.io/otel/sdk/trace"
	"go.opentelemetry.io/otel/sdk/trace/tracetest"
	"go.opentelemetry.io/otel/trace"
	"go.uber.org/mock/gomock"
	"golang.org/x/oauth2"

	"github.com/canonical/hook-service/internal/logging"
)


func TestTokenSourceCachingAndRenewal(t *testing.T) {
	ctx := t.Context()

	var fetchCount atomic.Int32
	var currentToken atomic.Pointer[oauth2.Token]

	fetcher := func(fetchCtx context.Context) (*oauth2.Token, error) {
		fetchCount.Add(1)
		tok := currentToken.Load()
		if tok == nil {
			return nil, errors.New("no token configured")
		}
		return tok, nil
	}

	ts := NewTokenSourceWithFetcher(fetcher, 60*time.Second, 5*time.Second, nil, nil, nil)

	// 1. Initial fetch with valid token (expires in 10 minutes)
	currentToken.Store(&oauth2.Token{
		AccessToken: "token-v1",
		Expiry:      time.Now().Add(10 * time.Minute),
	})

	token1, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("unexpected error on first token call: %v", err)
	}
	if token1 != "token-v1" {
		t.Errorf("token1 = %q, want %q", token1, "token-v1")
	}
	if got := fetchCount.Load(); got != 1 {
		t.Errorf("fetchCount after first call = %d, want 1", got)
	}

	// 2. Second call should reuse cached token without fetching
	token2, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("unexpected error on second token call: %v", err)
	}
	if token2 != "token-v1" {
		t.Errorf("token2 = %q, want %q", token2, "token-v1")
	}
	if got := fetchCount.Load(); got != 1 {
		t.Errorf("fetchCount after second call = %d, want 1 (cache hit)", got)
	}

	// 3. Update cached token expiry to be within 60s early renewal margin (e.g. 30s remaining)
	setCachedTokenExpiry(ts, time.Now().Add(30*time.Second))

	currentToken.Store(&oauth2.Token{
		AccessToken: "token-v2",
		Expiry:      time.Now().Add(10 * time.Minute),
	})

	token3, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("unexpected error on renewal call: %v", err)
	}
	if token3 != "token-v2" {
		t.Errorf("token3 = %q, want %q", token3, "token-v2")
	}
	if got := fetchCount.Load(); got != 2 {
		t.Errorf("fetchCount after renewal call = %d, want 2", got)
	}

	// 4. Zero expiry token (non-expiring) is cached without renewal
	ts.Invalidate()

	currentToken.Store(&oauth2.Token{
		AccessToken: "token-no-expiry",
		Expiry:      time.Time{},
	})

	token4, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("unexpected error on zero expiry call: %v", err)
	}
	if token4 != "token-no-expiry" {
		t.Errorf("token4 = %q, want %q", token4, "token-no-expiry")
	}

	token5, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("unexpected error on zero expiry second call: %v", err)
	}
	if token5 != "token-no-expiry" {
		t.Errorf("token5 = %q, want %q", token5, "token-no-expiry")
	}
	if got := fetchCount.Load(); got != 3 {
		t.Errorf("fetchCount for non-expiring token = %d, want 3", got)
	}
}

// setCachedTokenExpiry updates the cached token's expiry time for testing.
func setCachedTokenExpiry(ts *TokenSource, expiry time.Time) {
	ts.mu.Lock()
	defer ts.mu.Unlock()
	if ts.cachedToken != nil {
		ts.cachedToken.Expiry = expiry
	}
}

func TestTokenSourceConcurrentSingleFlight(t *testing.T) {
	ctx := t.Context()

	const numGoroutines = 20
	var wg sync.WaitGroup
	var fetchCount atomic.Int32
	var callersStarted atomic.Int32

	fetcher := func(fetchCtx context.Context) (*oauth2.Token, error) {
		fetchCount.Add(1)
		for callersStarted.Load() < numGoroutines {
			runtime.Gosched()
		}
		return &oauth2.Token{
			AccessToken: "concurrent-token",
			Expiry:      time.Now().Add(10 * time.Minute),
		}, nil
	}

	ts := NewTokenSourceWithFetcher(fetcher, 60*time.Second, 5*time.Second, nil, nil, nil)

	errCh := make(chan error, numGoroutines)
	for range numGoroutines {
		wg.Go(func() {
			callersStarted.Add(1)
			tok, err := ts.Token(ctx)
			if err != nil {
				errCh <- err
				return
			}
			if tok != "concurrent-token" {
				errCh <- errors.New("unexpected token value")
			}
		})
	}

	wg.Wait()
	close(errCh)

	for err := range errCh {
		t.Fatalf("concurrent Token() failed: %v", err)
	}

	if got := fetchCount.Load(); got != 1 {
		t.Errorf("concurrent fetchCount = %d, want 1 (single-flight)", got)
	}
}

func TestTokenSourceConcurrentSingleFlight_CallerCancellationDoesNotAbortSharedFetch(t *testing.T) {
	ctx := t.Context()

	var fetchCount atomic.Int32
	var once sync.Once
	fetchStarted := make(chan struct{})

	fetcher := func(_ context.Context) (*oauth2.Token, error) {
		fetchCount.Add(1)
		once.Do(func() { close(fetchStarted) })
		time.Sleep(100 * time.Millisecond) // simulate slower fetch
		return &oauth2.Token{
			AccessToken: "shared-token",
			Expiry:      time.Now().Add(10 * time.Minute),
		}, nil
	}

	ts := NewTokenSourceWithFetcher(fetcher, 60*time.Second, 5*time.Second, nil, nil, nil)

	// Caller 1 has a very short deadline (e.g. 20ms)
	shortCtx, shortCancel := context.WithTimeout(ctx, 20*time.Millisecond)
	defer shortCancel()

	var wg sync.WaitGroup
	var caller1Err error
	var caller2Tok string
	var caller2Err error

	wg.Go(func() {
		_, caller1Err = ts.Token(shortCtx)
	})

	// Wait until the singleflight fetch has actually begun
	<-fetchStarted

	wg.Go(func() {
		caller2Tok, caller2Err = ts.Token(ctx)
	})

	wg.Wait()

	// Caller 1 must have failed early due to its deadline
	if !errors.Is(caller1Err, context.DeadlineExceeded) {
		t.Errorf("caller1 error = %v, want %v", caller1Err, context.DeadlineExceeded)
	}

	// Caller 2 must have succeeded with the shared fetch result
	if caller2Err != nil {
		t.Errorf("caller2 unexpected error: %v", caller2Err)
	}
	if caller2Tok != "shared-token" {
		t.Errorf("caller2 token = %q, want %q", caller2Tok, "shared-token")
	}

	// Only 1 fetch should have occurred
	if got := fetchCount.Load(); got != 1 {
		t.Errorf("fetchCount = %d, want 1", got)
	}
}

func TestTokenSourceFetchErrors(t *testing.T) {
	ctx := t.Context()

	tests := []struct {
		name          string
		fetcher       func(ctx context.Context) (*oauth2.Token, error)
		wantErr       bool
		wantErrTarget error
		errContains   string
	}{
		{
			name: "upstream fetch error",
			fetcher: func(ctx context.Context) (*oauth2.Token, error) {
				return nil, errors.New("connection refused")
			},
			wantErr:     true,
			errContains: "connection refused",
		},
		{
			name: "empty access token returned",
			fetcher: func(ctx context.Context) (*oauth2.Token, error) {
				return &oauth2.Token{AccessToken: ""}, nil
			},
			wantErr:     true,
			errContains: "received empty oauth token",
		},
		{
			name: "fetch timeout cause triggered",
			fetcher: func(ctx context.Context) (*oauth2.Token, error) {
				<-ctx.Done()
				return nil, context.Cause(ctx)
			},
			wantErr:       true,
			wantErrTarget: ErrTokenFetchTimeout,
			errContains:   "oauth token fetch timed out",
		},
		{
			name: "fetcher panic recovered",
			fetcher: func(ctx context.Context) (*oauth2.Token, error) {
				panic("unexpected nil pointer dereference")
			},
			wantErr:     true,
			errContains: "panic during oauth token fetch: unexpected nil pointer dereference",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ts := NewTokenSourceWithFetcher(tt.fetcher, 60*time.Second, 50*time.Millisecond, nil, nil, nil)
			tok, err := ts.Token(ctx)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ts.Token() error = %v, wantErr %v", err, tt.wantErr)
			}
			if tok != "" && tt.wantErr {
				t.Errorf("ts.Token() returned token %q on error", tok)
			}
			if tt.wantErrTarget != nil && !errors.Is(err, tt.wantErrTarget) {
				t.Errorf("ts.Token() error = %v, want wrapping %v", err, tt.wantErrTarget)
			}
			if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
				t.Errorf("ts.Token() error = %q, want containing %q", err.Error(), tt.errContains)
			}
		})
	}
}

func TestTokenSourceObservability_Success(t *testing.T) {
	ctx := t.Context()
	ctrl := gomock.NewController(t)
	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockTokenMetricsInterface(ctrl)
	logger := logging.NewNoopLogger()

	spanRecorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	sdkTracer := provider.Tracer("test")

	mockTracer.EXPECT().
		Start(gomock.Any(), "kafka.oauth.fetch_token").
		DoAndReturn(func(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
			return sdkTracer.Start(ctx, spanName, opts...)
		})

	fixedExpiry := time.Now().Add(10 * time.Minute).Truncate(time.Second)
	fetcher := func(_ context.Context) (*oauth2.Token, error) {
		return &oauth2.Token{
			AccessToken: "fresh-token",
			Expiry:      fixedExpiry,
		}, nil
	}

	ts := NewTokenSourceWithFetcher(fetcher, 60*time.Second, 5*time.Second, mockTracer, mockMonitor, logger)
	ts.clientID = "test-client"
	ts.tokenURL = "https://hydra.test/oauth2/token"
	ts.audience = "kafka"

	mockMonitor.EXPECT().
		IncKafkaOAuthTokenRefresh("success").
		Return(nil)
	mockMonitor.EXPECT().
		SetKafkaOAuthTokenExpiry(float64(fixedExpiry.Unix())).
		Return(nil)

	tok, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("unexpected error from Token(): %v", err)
	}
	if tok != "fresh-token" {
		t.Errorf("got token %q, want %q", tok, "fresh-token")
	}

	// 2. Cached fetch does not emit new span or refresh metric
	tok2, err2 := ts.Token(ctx)
	if err2 != nil {
		t.Fatalf("unexpected error from cached Token(): %v", err2)
	}
	if tok2 != "fresh-token" {
		t.Errorf("got token %q, want %q", tok2, "fresh-token")
	}

	// Verify span recording
	ended := spanRecorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(ended))
	}
	span := ended[0]
	if span.Name() != "kafka.oauth.fetch_token" {
		t.Errorf("span.Name() = %q, want %q", span.Name(), "kafka.oauth.fetch_token")
	}
	if span.Status().Code != codes.Ok {
		t.Errorf("span.Status().Code = %v, want %v", span.Status().Code, codes.Ok)
	}

	attrs := span.Attributes()
	wantAttrs := map[string]string{
		"oauth.client_id": "test-client",
		"oauth.token_url": "https://hydra.test/oauth2/token",
		"oauth.audience":  "kafka",
	}
	for k, wantVal := range wantAttrs {
		idx := slices.IndexFunc(attrs, func(kv attribute.KeyValue) bool {
			return string(kv.Key) == k
		})
		if idx < 0 {
			t.Errorf("span missing attribute %q", k)
		} else if got := attrs[idx].Value.AsString(); got != wantVal {
			t.Errorf("span attribute %q = %q, want %q", k, got, wantVal)
		}
	}
}

func TestTokenSourceObservability_Failure(t *testing.T) {
	ctx := t.Context()
	ctrl := gomock.NewController(t)
	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockTokenMetricsInterface(ctrl)
	logger := logging.NewNoopLogger()

	spanRecorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	sdkTracer := provider.Tracer("test")

	mockTracer.EXPECT().
		Start(gomock.Any(), "kafka.oauth.fetch_token").
		DoAndReturn(func(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
			return sdkTracer.Start(ctx, spanName, opts...)
		})

	fetchErr := errors.New("connection refused to token endpoint")
	fetcher := func(_ context.Context) (*oauth2.Token, error) {
		return nil, fetchErr
	}

	ts := NewTokenSourceWithFetcher(fetcher, 60*time.Second, 5*time.Second, mockTracer, mockMonitor, logger)

	mockMonitor.EXPECT().
		IncKafkaOAuthTokenRefresh("error").
		Return(nil)

	_, err := ts.Token(ctx)
	if err == nil {
		t.Fatal("expected error from Token(), got nil")
	}

	ended := spanRecorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(ended))
	}
	span := ended[0]
	if span.Status().Code != codes.Error {
		t.Errorf("span.Status().Code = %v, want %v", span.Status().Code, codes.Error)
	}
	if span.Status().Description != "failed to fetch oauth token" {
		t.Errorf("span.Status().Description = %q, want %q", span.Status().Description, "failed to fetch oauth token")
	}
	if len(span.Events()) == 0 {
		t.Errorf("expected error event recorded on span")
	}
}

func TestTokenSourceObservability_EmptyToken(t *testing.T) {
	ctx := t.Context()
	ctrl := gomock.NewController(t)
	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockTokenMetricsInterface(ctrl)
	logger := logging.NewNoopLogger()

	spanRecorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	sdkTracer := provider.Tracer("test")

	mockTracer.EXPECT().
		Start(gomock.Any(), "kafka.oauth.fetch_token").
		DoAndReturn(func(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
			return sdkTracer.Start(ctx, spanName, opts...)
		})

	fetcher := func(_ context.Context) (*oauth2.Token, error) {
		return &oauth2.Token{AccessToken: ""}, nil
	}

	ts := NewTokenSourceWithFetcher(fetcher, 60*time.Second, 5*time.Second, mockTracer, mockMonitor, logger)

	mockMonitor.EXPECT().
		IncKafkaOAuthTokenRefresh("error").
		Return(nil)

	_, err := ts.Token(ctx)
	if err == nil {
		t.Fatal("expected error on empty token, got nil")
	}

	ended := spanRecorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(ended))
	}
	span := ended[0]
	if span.Status().Code != codes.Error {
		t.Errorf("span.Status().Code = %v, want %v", span.Status().Code, codes.Error)
	}
	if span.Status().Description != "received empty oauth token from provider" {
		t.Errorf("span.Status().Description = %q, want %q", span.Status().Description, "received empty oauth token from provider")
	}
	if len(span.Events()) == 0 {
		t.Errorf("expected error event recorded on span")
	}
}

func TestTokenSourceObservability_Panic(t *testing.T) {
	ctx := t.Context()
	ctrl := gomock.NewController(t)
	mockTracer := NewMockTracingInterface(ctrl)
	mockMonitor := NewMockTokenMetricsInterface(ctrl)
	logger := logging.NewNoopLogger()

	spanRecorder := tracetest.NewSpanRecorder()
	provider := sdktrace.NewTracerProvider(sdktrace.WithSpanProcessor(spanRecorder))
	sdkTracer := provider.Tracer("test")

	mockTracer.EXPECT().
		Start(gomock.Any(), "kafka.oauth.fetch_token").
		DoAndReturn(func(ctx context.Context, spanName string, opts ...trace.SpanStartOption) (context.Context, trace.Span) {
			return sdkTracer.Start(ctx, spanName, opts...)
		})

	fetcher := func(_ context.Context) (*oauth2.Token, error) {
		panic("boom in tls handshake")
	}

	ts := NewTokenSourceWithFetcher(fetcher, 60*time.Second, 5*time.Second, mockTracer, mockMonitor, logger)

	mockMonitor.EXPECT().
		IncKafkaOAuthTokenRefresh("error").
		Return(nil)

	_, err := ts.Token(ctx)
	if err == nil {
		t.Fatal("expected error from Token(), got nil")
	}
	if !strings.Contains(err.Error(), "panic during oauth token fetch: boom in tls handshake") {
		t.Errorf("error = %q, want containing panic message", err.Error())
	}

	ended := spanRecorder.Ended()
	if len(ended) != 1 {
		t.Fatalf("expected 1 ended span, got %d", len(ended))
	}
	span := ended[0]
	if span.Status().Code != codes.Error {
		t.Errorf("span.Status().Code = %v, want %v", span.Status().Code, codes.Error)
	}
	if !strings.Contains(span.Status().Description, "panic during oauth token fetch: boom in tls handshake") {
		t.Errorf("span.Status().Description = %q, want containing panic message", span.Status().Description)
	}
	if len(span.Events()) == 0 {
		t.Errorf("expected error event recorded on span")
	}
}

func TestTokenSource_Invalidate(t *testing.T) {
	ctx := t.Context()
	var fetchCount atomic.Int32

	fetcher := func(_ context.Context) (*oauth2.Token, error) {
		count := fetchCount.Add(1)
		return &oauth2.Token{
			AccessToken: fmt.Sprintf("token-%d", count),
			TokenType:   "Bearer",
			Expiry:      time.Now().Add(10 * time.Minute),
		}, nil
	}

	ts := NewTokenSourceWithFetcher(fetcher, 60*time.Second, 5*time.Second, nil, nil, nil)

	// First fetch
	token1, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("unexpected error on first fetch: %v", err)
	}
	if token1 != "token-1" {
		t.Errorf("token1 = %q, want %q", token1, "token-1")
	}
	if got := fetchCount.Load(); got != 1 {
		t.Fatalf("fetchCount = %d, want 1", got)
	}

	// Cached fetch
	tokenCached, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("unexpected error on cached fetch: %v", err)
	}
	if tokenCached != "token-1" {
		t.Errorf("tokenCached = %q, want %q", tokenCached, "token-1")
	}
	if got := fetchCount.Load(); got != 1 {
		t.Fatalf("fetchCount = %d, want 1 (should be cached)", got)
	}

	// Invalidate cache
	ts.Invalidate()

	// Post-invalidation fetch
	token2, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("unexpected error after Invalidate: %v", err)
	}
	if token2 != "token-2" {
		t.Errorf("token2 = %q, want %q", token2, "token-2")
	}
	if got := fetchCount.Load(); got != 2 {
		t.Fatalf("fetchCount = %d, want 2", got)
	}
}

func TestNewTokenSourceConstructor(t *testing.T) {
	ctrl := gomock.NewController(t)
	tracer := NewMockTracingInterface(ctrl)
	monitor := NewMockTokenMetricsInterface(ctrl)
	logger := logging.NewNoopLogger()

	cfg := TokenSourceConfig{
		TokenURL:      "https://hydra.test/oauth2/token",
		ClientID:      "my-client-id",
		ClientSecret:  "my-secret",
		Scopes:        []string{"kafka", "read"},
		Audience:      "kafka-broker",
		RefreshMargin: 45 * time.Second,
		FetchTimeout:  8 * time.Second,
	}

	ts := NewTokenSource(cfg, tracer, monitor, logger)
	if ts == nil {
		t.Fatal("expected non-nil TokenSource")
	}
	if ts.tokenURL != cfg.TokenURL {
		t.Errorf("ts.tokenURL = %q, want %q", ts.tokenURL, cfg.TokenURL)
	}
	if ts.clientID != cfg.ClientID {
		t.Errorf("ts.clientID = %q, want %q", ts.clientID, cfg.ClientID)
	}
	if ts.audience != cfg.Audience {
		t.Errorf("ts.audience = %q, want %q", ts.audience, cfg.Audience)
	}
	if ts.refreshMargin != 45*time.Second {
		t.Errorf("ts.refreshMargin = %v, want 45s", ts.refreshMargin)
	}
	if ts.fetchTimeout != 8*time.Second {
		t.Errorf("ts.fetchTimeout = %v, want 8s", ts.fetchTimeout)
	}

	// Test default durations when zero
	cfgDefault := TokenSourceConfig{
		TokenURL: "https://hydra.test/oauth2/token",
		ClientID: "client",
	}
	tsDef := NewTokenSource(cfgDefault, nil, nil, nil)
	if tsDef.refreshMargin != DefaultEarlyRenewalMargin {
		t.Errorf("default refreshMargin = %v, want %v", tsDef.refreshMargin, DefaultEarlyRenewalMargin)
	}
	if tsDef.fetchTimeout != DefaultFetchTimeout {
		t.Errorf("default fetchTimeout = %v, want %v", tsDef.fetchTimeout, DefaultFetchTimeout)
	}
}

func TestTokenSourceFallbackToCachedTokenOnFetchError(t *testing.T) {
	ctx := t.Context()

	var fetchErr error
	var currentToken atomic.Pointer[oauth2.Token]
	currentToken.Store(&oauth2.Token{
		AccessToken: "valid-cached-token",
		Expiry:      time.Now().Add(10 * time.Minute),
	})

	fetcher := func(_ context.Context) (*oauth2.Token, error) {
		if fetchErr != nil {
			return nil, fetchErr
		}
		return currentToken.Load(), nil
	}

	ts := NewTokenSourceWithFetcher(fetcher, 60*time.Second, 5*time.Second, nil, nil, nil)

	// 1. Initial acquisition populates cache.
	tok1, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("unexpected error on initial fetch: %v", err)
	}
	if tok1 != "valid-cached-token" {
		t.Fatalf("tok1 = %q, want %q", tok1, "valid-cached-token")
	}

	// 2. Token enters early renewal margin (e.g. 30s remaining before expiry).
	setCachedTokenExpiry(ts, time.Now().Add(30*time.Second))

	// Simulate transient Hydra outage
	fetchErr = errors.New("hydra connection refused")

	// Token() attempts refresh, which fails, but gracefully falls back to unexpired cached token.
	tok2, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("expected graceful fallback to cached token on refresh error, got error: %v", err)
	}
	if tok2 != "valid-cached-token" {
		t.Errorf("tok2 = %q, want %q", tok2, "valid-cached-token")
	}

	// 3. Once the cached token has actually expired, fetch failure must be returned.
	setCachedTokenExpiry(ts, time.Now().Add(-1*time.Second))

	tok3, err := ts.Token(ctx)
	if err == nil {
		t.Fatalf("expected error for expired token when fetcher fails, got token: %q", tok3)
	}
	if !strings.Contains(err.Error(), "hydra connection refused") {
		t.Errorf("err = %v, want containing 'hydra connection refused'", err)
	}

	// 4. Invalidate cache: error must also be returned when no cached token exists.
	ts.Invalidate()
	tok4, err := ts.Token(ctx)
	if err == nil {
		t.Fatalf("expected error after Invalidate, got token: %q", tok4)
	}
}

