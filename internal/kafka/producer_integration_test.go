// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	kafkago "github.com/segmentio/kafka-go"
	"github.com/segmentio/kafka-go/protocol"
	"github.com/segmentio/kafka-go/protocol/apiversions"
	"github.com/segmentio/kafka-go/protocol/metadata"
	"github.com/segmentio/kafka-go/protocol/produce"
	"github.com/segmentio/kafka-go/protocol/saslauthenticate"
	"github.com/segmentio/kafka-go/protocol/saslhandshake"

	"github.com/canonical/hook-service/internal/config"
	"github.com/canonical/hook-service/internal/kafka"
	"github.com/canonical/hook-service/internal/kafka/sasl"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring/prometheus"
	"github.com/canonical/hook-service/internal/testhelpers"
	"github.com/canonical/hook-service/internal/tracing"
)

// generateTestCertificates creates an ephemeral in-memory CA and server TLS certificate
// for integration testing TLS and SASL_SSL listeners without static fixtures.
func generateTestCertificates(t *testing.T) (caPEM []byte, serverTLS *tls.Config) {
	t.Helper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate CA private key: %v", err)
	}

	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Test Kafka CA"},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		IsCA:                  true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		BasicConstraintsValid: true,
	}

	caBytes, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create CA certificate: %v", err)
	}
	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caBytes})

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate server private key: %v", err)
	}

	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: "localhost"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.IPv6loopback},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-1 * time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}

	serverBytes, err := x509.CreateCertificate(rand.Reader, serverTemplate, caTemplate, &serverKey.PublicKey, caKey)
	if err != nil {
		t.Fatalf("failed to create server certificate: %v", err)
	}
	serverPEM := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverBytes})
	serverKeyPEM := pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)})

	serverCert, err := tls.X509KeyPair(serverPEM, serverKeyPEM)
	if err != nil {
		t.Fatalf("failed to parse server key pair: %v", err)
	}

	serverTLS = &tls.Config{
		Certificates: []tls.Certificate{serverCert},
	}

	return caPEM, serverTLS
}

// testBroker holds state for an in-process TLS + SASL Kafka mock broker.
type testBroker struct {
	addr        string
	listener    net.Listener
	mu          sync.Mutex
	authTokens  []string
	producedOps int
}

func startTestBroker(t *testing.T, serverTLS *tls.Config, topic string) *testBroker {
	t.Helper()

	rawListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on tcp: %v", err)
	}

	tlsListener := tls.NewListener(rawListener, serverTLS)
	b := &testBroker{
		addr:     rawListener.Addr().String(),
		listener: tlsListener,
	}

	t.Cleanup(func() {
		_ = b.listener.Close()
	})

	host, portStr, err := net.SplitHostPort(b.addr)
	if err != nil {
		t.Fatalf("failed to parse listener addr %q: %v", b.addr, err)
	}
	port, _ := strconv.Atoi(portStr)

	go func() {
		for {
			conn, err := b.listener.Accept()
			if err != nil {
				return
			}
			go b.handleConnection(conn, host, int32(port), topic)
		}
	}()

	return b
}

func (b *testBroker) handleConnection(conn net.Conn, host string, port int32, topic string) {
	defer func() { _ = conn.Close() }()

	for {
		apiVersion, correlationID, _, msg, err := protocol.ReadRequest(conn)
		if err != nil {
			return
		}

		switch req := msg.(type) {
		case *apiversions.Request:
			resp := &apiversions.Response{
				ErrorCode: 0,
				ApiKeys: []apiversions.ApiKeyResponse{
					{ApiKey: int16(protocol.Produce), MinVersion: 0, MaxVersion: 8},
					{ApiKey: int16(protocol.Metadata), MinVersion: 0, MaxVersion: 8},
					{ApiKey: int16(protocol.SaslHandshake), MinVersion: 0, MaxVersion: 1},
					{ApiKey: int16(protocol.ApiVersions), MinVersion: 0, MaxVersion: 2},
					{ApiKey: int16(protocol.SaslAuthenticate), MinVersion: 0, MaxVersion: 1},
				},
			}
			if err := protocol.WriteResponse(conn, apiVersion, correlationID, resp); err != nil {
				return
			}

		case *saslhandshake.Request:
			resp := &saslhandshake.Response{
				ErrorCode:  0,
				Mechanisms: []string{"OAUTHBEARER"},
			}
			if err := protocol.WriteResponse(conn, apiVersion, correlationID, resp); err != nil {
				return
			}

		case *saslauthenticate.Request:
			authStr := string(req.AuthBytes)
			b.mu.Lock()
			b.authTokens = append(b.authTokens, authStr)
			b.mu.Unlock()

			resp := &saslauthenticate.Response{
				ErrorCode:         0,
				ErrorMessage:      "",
				AuthBytes:         nil,
				SessionLifetimeMs: 3600000,
			}
			if err := protocol.WriteResponse(conn, apiVersion, correlationID, resp); err != nil {
				return
			}

		case *metadata.Request:
			resp := &metadata.Response{
				Brokers: []metadata.ResponseBroker{
					{
						NodeID: 1,
						Host:   host,
						Port:   port,
					},
				},
				ControllerID: 1,
				Topics: []metadata.ResponseTopic{
					{
						Name:      topic,
						ErrorCode: 0,
						Partitions: []metadata.ResponsePartition{
							{
								ErrorCode:      0,
								PartitionIndex: 0,
								LeaderID:       1,
							},
						},
					},
				},
			}
			if err := protocol.WriteResponse(conn, apiVersion, correlationID, resp); err != nil {
				return
			}

		case *produce.Request:
			b.mu.Lock()
			b.producedOps++
			b.mu.Unlock()

			resp := &produce.Response{
				Topics: []produce.ResponseTopic{
					{
						Topic: topic,
						Partitions: []produce.ResponsePartition{
							{
								Partition:  0,
								ErrorCode:  0,
								BaseOffset: 0,
							},
						},
					},
				},
			}
			if err := protocol.WriteResponse(conn, apiVersion, correlationID, resp); err != nil {
				return
			}

		default:
			return
		}
	}
}

func TestIntegration_Hydra_OAuthBearerTokenSource(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := t.Context()

	hydraEnv := testhelpers.SetupHydra(t)
	clientID, clientSecret := testhelpers.CreateHydraClient(t, hydraEnv, "oauthbearer-integration-client", "kafka")

	tokenURL := fmt.Sprintf("%s/oauth2/token", hydraEnv.PublicURL)
	testLogger := logging.NewLogger("debug")
	testMonitor := prometheus.NewMonitor("test-kafka-producer", testLogger)
	testTracer := tracing.NewNoopTracer()
	ts := sasl.NewTokenSource(sasl.TokenSourceConfig{
		TokenURL:      tokenURL,
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		Audience:      "kafka",
		RefreshMargin: 60 * time.Second,
		FetchTimeout:  10 * time.Second,
	}, testTracer, testMonitor, testLogger)

	// 1. Initial token acquisition from live Hydra
	tok, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("failed to fetch token from Hydra: %v", err)
	}
	if tok == "" {
		t.Fatal("expected non-empty access token from Hydra")
	}

	// 2. Token caching verification
	tok2, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("failed to fetch cached token: %v", err)
	}
	if tok2 != tok {
		t.Errorf("cached access token changed: got %q, want %q", tok2, tok)
	}

	// 3. Mechanism RFC 7628 GS2 framing verification
	mech := sasl.NewMechanism(ts)
	if mech.Name() != "OAUTHBEARER" {
		t.Errorf("mechanism name = %q, want OAUTHBEARER", mech.Name())
	}

	sess, initialBytes, err := mech.Start(ctx)
	if err != nil {
		t.Fatalf("mech.Start failed: %v", err)
	}

	expectedPrefix := "n,,\x01auth=Bearer "
	expectedSuffix := "\x01\x01"
	initialStr := string(initialBytes)

	if !strings.HasPrefix(initialStr, expectedPrefix) {
		t.Errorf("initial response prefix mismatch: got %q, want prefix %q", initialStr, expectedPrefix)
	}
	if !strings.HasSuffix(initialStr, expectedSuffix) {
		t.Errorf("initial response suffix mismatch: got %q, want suffix %q", initialStr, expectedSuffix)
	}

	bearerContent := strings.TrimSuffix(strings.TrimPrefix(initialStr, expectedPrefix), expectedSuffix)
	if bearerContent != tok {
		t.Errorf("GS2 payload token = %q, want %q", bearerContent, tok)
	}

	// 4. StateMachine completion on empty challenge
	completed, nextBytes, err := sess.Next(ctx, nil)
	if err != nil {
		t.Fatalf("sess.Next on empty challenge failed: %v", err)
	}
	if !completed {
		t.Error("expected state machine to complete on empty challenge")
	}
	if len(nextBytes) != 0 {
		t.Errorf("expected empty nextBytes, got %d bytes", len(nextBytes))
	}
}

func TestIntegration_SASL_SSL_Publish(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	ctx := t.Context()

	// 1. Start live Ory Hydra container & create client
	hydraEnv := testhelpers.SetupHydra(t)
	clientID, clientSecret := testhelpers.CreateHydraClient(t, hydraEnv, "sasl-ssl-publisher-client", "kafka")

	// 2. Setup TLS certificate authority & test broker
	caPEM, serverTLS := generateTestCertificates(t)
	tempDir := t.TempDir()
	caFile := filepath.Join(tempDir, "broker-ca.pem")
	if err := os.WriteFile(caFile, caPEM, 0o600); err != nil {
		t.Fatalf("failed to write broker CA file: %v", err)
	}

	topic := config.PermissionsTopic("integration-test-service")
	broker := startTestBroker(t, serverTLS, topic)

	// 3. Build broker TLS config with truststore isolation
	tlsConfig, err := kafka.BuildBrokerTLSConfig(caFile, false)
	if err != nil {
		t.Fatalf("failed to build broker TLS config: %v", err)
	}

	// 4. Initialize Hydra TokenSource and SASL Mechanism
	tokenURL := fmt.Sprintf("%s/oauth2/token", hydraEnv.PublicURL)
	testLoggerSSL := logging.NewLogger("debug")
	testMonitorSSL := prometheus.NewMonitor("test-kafka-producer-ssl", testLoggerSSL)
	testTracerSSL := tracing.NewNoopTracer()
	ts := sasl.NewTokenSource(sasl.TokenSourceConfig{
		TokenURL:      tokenURL,
		ClientID:      clientID,
		ClientSecret:  clientSecret,
		Audience:      "kafka",
		RefreshMargin: 60 * time.Second,
		FetchTimeout:  10 * time.Second,
	}, testTracerSSL, testMonitorSSL, testLoggerSSL)
	mech := sasl.NewMechanism(ts)

	// 5. Construct authenticated kafkago.Transport with TLS and SASL
	transport := &kafkago.Transport{
		TLS:         tlsConfig,
		SASL:        mech,
		IdleTimeout: 5 * time.Minute,
	}
	t.Cleanup(transport.CloseIdleConnections)

	writer := kafka.NewKafkaWriter([]string{broker.addr}, topic, transport)
	var completionErr error
	writer.Completion = func(messages []kafkago.Message, err error) {
		if err != nil {
			completionErr = err
		}
	}
	tracer := &noopTracer{}
	logger := logging.NewLogger("debug")
	monitor := &noopMonitor{}

	publisher := kafka.NewPermissionPublisher(writer, "integration-test-service", topic, tracer, monitor, logger)

	// 6. Publish write message
	if err := publisher.PublishWrite(ctx, "user:alice", "owner", "group-in-claim:grp-test"); err != nil {
		t.Fatalf("failed to publish write event over SASL_SSL: %v", err)
	}

	// Close publisher to flush pending async batch
	if err := publisher.Close(); err != nil {
		t.Fatalf("failed to close publisher: %v", err)
	}

	if completionErr != nil {
		t.Fatalf("completion error occurred: %v", completionErr)
	}

	tok, err := ts.Token(ctx)
	if err != nil {
		t.Fatalf("failed to get token: %v", err)
	}

	expectedGS2 := fmt.Sprintf("n,,\x01auth=Bearer %s\x01\x01", tok)

	// Wait up to 5s for the asynchronous produce request to arrive at the broker
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		broker.mu.Lock()
		count := broker.producedOps
		broker.mu.Unlock()
		if count > 0 {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}

	// 7. Verify broker validated TLS and received valid SASL OAUTHBEARER authentication
	broker.mu.Lock()
	defer broker.mu.Unlock()

	if len(broker.authTokens) == 0 {
		t.Fatal("broker received zero SASL authentication requests")
	}

	foundValidAuth := false
	for _, auth := range broker.authTokens {
		if auth == expectedGS2 {
			foundValidAuth = true
			break
		}
	}
	if !foundValidAuth {
		t.Errorf("broker did not receive expected GS2 token: got %v, want %q", broker.authTokens, expectedGS2)
	}

	if broker.producedOps == 0 {
		t.Errorf("broker received %d produce requests, want >= 1", broker.producedOps)
	}
}

func TestIntegration_KafkaWriter_WithCustomTransport(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping integration test in short mode")
	}

	broker, cleanup := setupKafkaContainer(t)
	defer cleanup()

	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	topic := config.PermissionsTopic(config.DefaultFederatedServiceName)

	// Custom transport configured with IdleTimeout
	customTransport := &kafkago.Transport{
		IdleTimeout: kafka.DefaultTransportIdleTimeout,
	}
	writer := kafka.NewKafkaWriter([]string{broker}, topic, customTransport)
	if writer.Transport != customTransport {
		t.Errorf("writer.Transport = %v, want %v", writer.Transport, customTransport)
	}

	tracer := &noopTracer{}
	logger := logging.NewLogger("debug")
	monitor := &noopMonitor{}

	publisher := kafka.NewPermissionPublisher(writer, config.DefaultFederatedServiceName, topic, tracer, monitor, logger)
	t.Cleanup(func() {
		_ = publisher.Close()
	})

	if err := publisher.PublishWrite(ctx, "user:bob", "editor", "group-in-claim:grp-2"); err != nil {
		t.Fatalf("failed to publish write event: %v", err)
	}

	// Read message back to confirm write succeeded
	reader := kafkago.NewReader(kafkago.ReaderConfig{
		Brokers:     []string{broker},
		Topic:       topic,
		Partition:   0,
		MinBytes:    1,
		MaxBytes:    10e6,
		MaxWait:     500 * time.Millisecond,
		StartOffset: kafkago.FirstOffset,
	})
	t.Cleanup(func() {
		_ = reader.Close()
	})

	msg, err := reader.ReadMessage(ctx)
	if err != nil {
		t.Fatalf("failed to read published message: %v", err)
	}
	if string(msg.Key) != "group-in-claim:grp-2" {
		t.Errorf("message key = %q, want %q", string(msg.Key), "group-in-claim:grp-2")
	}
}
