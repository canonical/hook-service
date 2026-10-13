// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package e2e_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	v1 "github.com/canonical/authorization-service/api/v1"
	trace "go.opentelemetry.io/otel/trace"

	"github.com/canonical/hook-service/internal/kafka"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/tracing"
)

type noopTracer struct{}

func (n *noopTracer) Start(ctx context.Context, _ string, _ ...trace.SpanStartOption) (context.Context, trace.Span) {
	return ctx, trace.SpanFromContext(ctx)
}

type noopMonitor struct{}

func (m *noopMonitor) GetService() string {
	return "hook-service"
}

func (m *noopMonitor) SetResponseTimeMetric(_ map[string]string, _ float64) error {
	return nil
}

func (m *noopMonitor) SetDependencyAvailability(_ map[string]string, _ float64) error {
	return nil
}

var _ tracing.TracingInterface = (*noopTracer)(nil)
var _ monitoring.MonitorInterface = (*noopMonitor)(nil)

func getE2EConfig(t *testing.T) (brokers []string, caFile string, tokenURL string) {
	t.Helper()
	if os.Getenv("KAFKA_AUTH_E2E") == "" {
		t.Skip("skipping Kafka Auth E2E test; set KAFKA_AUTH_E2E=1 to run")
	}

	brokerAddr := os.Getenv("KAFKA_BROKERS")
	if brokerAddr == "" {
		brokerAddr = "localhost:9096"
	}
	brokers = []string{brokerAddr}

	tokenURL = os.Getenv("KAFKA_OAUTH_TOKEN_URL")
	if tokenURL == "" {
		tokenURL = "http://localhost:4444/oauth2/token"
	}

	caFile = os.Getenv("KAFKA_TLS_CA_FILE")
	if caFile == "" {
		// Look for docker/kafka-auth-e2e/certs/ca.crt
		defaultCA := filepath.Join("..", "..", "docker", "kafka-auth-e2e", "certs", "ca.crt")
		if abs, err := filepath.Abs(defaultCA); err == nil && fileExists(abs) {
			caFile = abs
		} else {
			t.Fatalf("CA file not found. Set KAFKA_TLS_CA_FILE or run scripts/setup-kafka-auth-e2e.sh first")
		}
	}

	return brokers, caFile, tokenURL
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func TestE2E_KafkaAuth_Positive_Publish(t *testing.T) {
	brokers, caFile, tokenURL := getE2EConfig(t)

	logger := logging.NewLogger("debug")
	tracer := &noopTracer{}
	monitor := &noopMonitor{}

	cfg := &kafka.Config{
		Brokers:           brokers,
		ServiceName:       "hook-service",
		Topic:             "permissions.hook-service",
		AuthType:          kafka.AuthTypeOAuthBearer,
		OAuthTokenURL:     tokenURL,
		OAuthClientID:     "hook-service",
		OAuthClientSecret: "hook-service-secret",
		OAuthAudience:     "kafka",
		TLSEnabled:        true,
		TLSCAFile:         caFile,
	}

	publisher, err := kafka.NewPublisher(cfg, tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to construct publisher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	op := kafka.Operation{
		Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
		Relation: "member",
		Subject:  "user:alice@canonical.com",
		Object:   "group:engineering",
	}

	if err := publisher.PublishOperations(ctx, op); err != nil {
		t.Fatalf("failed to publish operation: %v", err)
	}

	if err := publisher.Close(); err != nil {
		t.Fatalf("failed to flush and close publisher: %v", err)
	}
}

func TestE2E_KafkaAuth_Batch_Envelope(t *testing.T) {
	brokers, caFile, tokenURL := getE2EConfig(t)

	logger := logging.NewLogger("debug")
	tracer := &noopTracer{}
	monitor := &noopMonitor{}

	cfg := &kafka.Config{
		Brokers:           brokers,
		ServiceName:       "hook-service",
		Topic:             "permissions.hook-service",
		AuthType:          kafka.AuthTypeOAuthBearer,
		OAuthTokenURL:     tokenURL,
		OAuthClientID:     "hook-service",
		OAuthClientSecret: "hook-service-secret",
		OAuthAudience:     "kafka",
		TLSEnabled:        true,
		TLSCAFile:         caFile,
	}

	publisher, err := kafka.NewPublisher(cfg, tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to construct publisher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	ops := []kafka.Operation{
		{
			Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
			Relation: "member",
			Subject:  "user:bob@canonical.com",
			Object:   "group:engineering",
		},
		{
			Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
			Relation: "member",
			Subject:  "user:carol@canonical.com",
			Object:   "group:engineering",
		},
		{
			Op:       v1.PermissionOp_PERMISSION_OP_DELETE,
			Relation: "member",
			Subject:  "user:dave@canonical.com",
			Object:   "group:engineering",
		},
	}

	if err := publisher.PublishOperations(ctx, ops...); err != nil {
		t.Fatalf("failed to publish batch operations: %v", err)
	}

	if err := publisher.Close(); err != nil {
		t.Fatalf("failed to flush and close publisher: %v", err)
	}
}

func TestE2E_KafkaAuth_TokenCaching_And_Reuse(t *testing.T) {
	brokers, caFile, tokenURL := getE2EConfig(t)

	logger := logging.NewLogger("debug")
	tracer := &noopTracer{}
	monitor := &noopMonitor{}

	cfg := &kafka.Config{
		Brokers:           brokers,
		ServiceName:       "hook-service",
		Topic:             "permissions.hook-service",
		AuthType:          kafka.AuthTypeOAuthBearer,
		OAuthTokenURL:     tokenURL,
		OAuthClientID:     "hook-service",
		OAuthClientSecret: "hook-service-secret",
		OAuthAudience:     "kafka",
		TLSEnabled:        true,
		TLSCAFile:         caFile,
	}

	publisher, err := kafka.NewPublisher(cfg, tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to construct publisher: %v", err)
	}
	t.Cleanup(func() {
		if closeErr := publisher.Close(); closeErr != nil {
			t.Errorf("failed to close publisher: %v", closeErr)
		}
	})

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for i := range []int{0, 1, 2} {
		op := kafka.Operation{
			Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
			Relation: "member",
			Subject:  "user:cached@canonical.com",
			Object:   "group:engineering",
		}
		if err := publisher.PublishOperations(ctx, op); err != nil {
			t.Fatalf("iteration %d: failed to publish: %v", i, err)
		}
		time.Sleep(100 * time.Millisecond)
	}
}

func TestE2E_KafkaAuth_Negative_Untrusted_TLS_CA(t *testing.T) {
	brokers, _, tokenURL := getE2EConfig(t)

	logger := logging.NewLogger("debug")
	tracer := &noopTracer{}
	monitor := &noopMonitor{}

	// Generate a valid self-signed CA certificate that did not sign the Kafka broker cert
	tempDir := t.TempDir()
	dummyCA := filepath.Join(tempDir, "dummy-ca.crt")

	priv, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Untrusted-Root-CA"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create cert: %v", err)
	}
	certPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	if err := os.WriteFile(dummyCA, certPEM, 0600); err != nil {
		t.Fatalf("failed to write dummy CA: %v", err)
	}

	cfg := &kafka.Config{
		Brokers:               brokers,
		ServiceName:           "hook-service",
		Topic:                 "permissions.hook-service",
		AuthType:              kafka.AuthTypeOAuthBearer,
		OAuthTokenURL:         tokenURL,
		OAuthClientID:         "hook-service",
		OAuthClientSecret:     "hook-service-secret",
		OAuthAudience:         "kafka",
		TLSEnabled:            true,
		TLSCAFile:             dummyCA,
		TLSInsecureSkipVerify: false,
	}

	publisher, err := kafka.NewPublisher(cfg, tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to construct publisher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	op := kafka.Operation{
		Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
		Relation: "member",
		Subject:  "user:alice@canonical.com",
		Object:   "group:engineering",
	}

	publishErr := publisher.PublishOperations(ctx, op)
	closeErr := publisher.Close()

	var finalErr error
	if publishErr != nil {
		finalErr = publishErr
	} else {
		finalErr = closeErr
	}

	if finalErr == nil {
		t.Fatal("expected TLS verification failure with untrusted CA, but got success")
	}

	errStr := finalErr.Error()
	if !strings.Contains(errStr, "unknown authority") &&
		!strings.Contains(errStr, "certificate") &&
		!strings.Contains(errStr, "tls") &&
		!strings.Contains(errStr, "x509") {
		t.Fatalf("expected certificate verification error, got: %v", finalErr)
	}
}

func TestE2E_KafkaAuth_Negative_Invalid_OAuth_Client_Secret(t *testing.T) {
	brokers, caFile, tokenURL := getE2EConfig(t)

	logger := logging.NewLogger("debug")
	tracer := &noopTracer{}
	monitor := &noopMonitor{}

	cfg := &kafka.Config{
		Brokers:           brokers,
		ServiceName:       "hook-service",
		Topic:             "permissions.hook-service",
		AuthType:          kafka.AuthTypeOAuthBearer,
		OAuthTokenURL:     tokenURL,
		OAuthClientID:     "hook-service",
		OAuthClientSecret: "wrong-secret-that-does-not-exist",
		OAuthAudience:     "kafka",
		TLSEnabled:        true,
		TLSCAFile:         caFile,
	}

	publisher, err := kafka.NewPublisher(cfg, tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to construct publisher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	op := kafka.Operation{
		Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
		Relation: "member",
		Subject:  "user:alice@canonical.com",
		Object:   "group:engineering",
	}

	publishErr := publisher.PublishOperations(ctx, op)
	closeErr := publisher.Close()

	var finalErr error
	if publishErr != nil {
		finalErr = publishErr
	} else {
		finalErr = closeErr
	}

	if finalErr == nil {
		t.Fatal("expected authentication error with invalid secret, but got success")
	}

	errStr := strings.ToLower(finalErr.Error())
	if !strings.Contains(errStr, "401") &&
		!strings.Contains(errStr, "unauthorized") &&
		!strings.Contains(errStr, "invalid") &&
		!strings.Contains(errStr, "oauth") &&
		!strings.Contains(errStr, "sasl") {
		t.Fatalf("expected 401 Unauthorized or OAuth error, got: %v", finalErr)
	}
}

func TestE2E_KafkaAuth_Negative_Wrong_Audience(t *testing.T) {
	brokers, caFile, tokenURL := getE2EConfig(t)

	logger := logging.NewLogger("debug")
	tracer := &noopTracer{}
	monitor := &noopMonitor{}

	// wrong-audience-client is issued tokens with aud="some-other-service", not "kafka"
	cfg := &kafka.Config{
		Brokers:           brokers,
		ServiceName:       "hook-service",
		Topic:             "permissions.hook-service",
		AuthType:          kafka.AuthTypeOAuthBearer,
		OAuthTokenURL:     tokenURL,
		OAuthClientID:     "wrong-audience-client",
		OAuthClientSecret: "wrong-audience-secret",
		OAuthAudience:     "some-other-service",
		TLSEnabled:        true,
		TLSCAFile:         caFile,
	}

	publisher, err := kafka.NewPublisher(cfg, tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to construct publisher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	op := kafka.Operation{
		Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
		Relation: "member",
		Subject:  "user:alice@canonical.com",
		Object:   "group:engineering",
	}

	publishErr := publisher.PublishOperations(ctx, op)
	closeErr := publisher.Close()

	var finalErr error
	if publishErr != nil {
		finalErr = publishErr
	} else {
		finalErr = closeErr
	}

	if finalErr == nil {
		t.Fatal("expected SASL handshake rejection due to wrong audience claim, but got success")
	}

	errStr := strings.ToLower(finalErr.Error())
	if !strings.Contains(errStr, "sasl") &&
		!strings.Contains(errStr, "auth") &&
		!strings.Contains(errStr, "failed") {
		t.Fatalf("expected SASL authentication failure, got: %v", finalErr)
	}
}

func TestE2E_KafkaAuth_Negative_Unauthorized_Topic_ACL(t *testing.T) {
	brokers, caFile, tokenURL := getE2EConfig(t)

	logger := logging.NewLogger("debug")
	tracer := &noopTracer{}
	monitor := &noopMonitor{}

	// unauthorized-client authenticates cleanly against Hydra with aud="kafka",
	// but has NO write ACL in Kafka on topic permissions.hook-service
	cfg := &kafka.Config{
		Brokers:           brokers,
		ServiceName:       "hook-service",
		Topic:             "permissions.hook-service",
		AuthType:          kafka.AuthTypeOAuthBearer,
		OAuthTokenURL:     tokenURL,
		OAuthClientID:     "unauthorized-client",
		OAuthClientSecret: "unauthorized-secret",
		OAuthAudience:     "kafka",
		TLSEnabled:        true,
		TLSCAFile:         caFile,
	}

	publisher, err := kafka.NewPublisher(cfg, tracer, monitor, logger)
	if err != nil {
		t.Fatalf("failed to construct publisher: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	op := kafka.Operation{
		Op:       v1.PermissionOp_PERMISSION_OP_WRITE,
		Relation: "member",
		Subject:  "user:alice@canonical.com",
		Object:   "group:engineering",
	}

	publishErr := publisher.PublishOperations(ctx, op)
	closeErr := publisher.Close()

	var finalErr error
	if publishErr != nil {
		finalErr = publishErr
	} else {
		finalErr = closeErr
	}

	if finalErr == nil {
		t.Fatal("expected topic authorization denial due to missing Kafka ACL, but got success")
	}

	errStr := strings.ToLower(finalErr.Error())
	if !strings.Contains(errStr, "authorization failed") &&
		!strings.Contains(errStr, "topic authorization") &&
		!strings.Contains(errStr, "[29]") &&
		!strings.Contains(errStr, "unknown topic or partition") &&
		!strings.Contains(errStr, "[3]") {
		t.Fatalf("expected Topic Authorization Failed or Unknown Topic denial, got: %v", finalErr)
	}
}
