// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sasl

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"math/big"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func generateTestCertPEM(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ecdsa key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			CommonName: "test-oauth-ca",
		},
		NotBefore:             time.Now().Add(-1 * time.Hour),
		NotAfter:              time.Now().Add(1 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
	}

	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &priv.PublicKey, priv)
	if err != nil {
		t.Fatalf("failed to create certificate: %v", err)
	}

	return pem.EncodeToMemory(&pem.Block{
		Type:  "CERTIFICATE",
		Bytes: der,
	})
}

func TestLoadOAuthCertPool(t *testing.T) {
	_ = t.Context()

	t.Run("empty path returns nil pool", func(t *testing.T) {
		pool, err := LoadOAuthCertPool("")
		if err != nil {
			t.Fatalf("LoadOAuthCertPool(\"\") error = %v", err)
		}
		if pool != nil {
			t.Errorf("expected nil pool for empty path, got %v", pool)
		}
	})

	t.Run("valid PEM cert loads dedicated pool", func(t *testing.T) {
		certBytes := generateTestCertPEM(t)
		tmpDir := t.TempDir()
		certFile := filepath.Join(tmpDir, "ca.pem")
		if err := os.WriteFile(certFile, certBytes, 0600); err != nil {
			t.Fatalf("failed to write test cert: %v", err)
		}

		pool, err := LoadOAuthCertPool(certFile)
		if err != nil {
			t.Fatalf("LoadOAuthCertPool() error = %v", err)
		}
		if pool == nil {
			t.Fatal("expected non-nil pool")
		}
	})

	t.Run("missing file returns wrapped path error", func(t *testing.T) {
		nonExistentFile := filepath.Join(t.TempDir(), "nonexistent.pem")
		pool, err := LoadOAuthCertPool(nonExistentFile)
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
		if pool != nil {
			t.Errorf("expected nil pool on error, got %v", pool)
		}

		// Verify using Go 1.26+ errors.AsType
		if pathErr, ok := errors.AsType[*os.PathError](err); !ok || pathErr == nil {
			t.Errorf("expected error to wrap *os.PathError, got: %T (%v)", err, err)
		}
	})

	t.Run("invalid non-PEM file returns error", func(t *testing.T) {
		tmpDir := t.TempDir()
		badFile := filepath.Join(tmpDir, "bad.pem")
		if err := os.WriteFile(badFile, []byte("NOT A VALID CERTIFICATE"), 0600); err != nil {
			t.Fatalf("failed to write bad file: %v", err)
		}

		pool, err := LoadOAuthCertPool(badFile)
		if err == nil {
			t.Fatal("expected error for invalid PEM, got nil")
		}
		if pool != nil {
			t.Errorf("expected nil pool on error, got %v", pool)
		}
		if !strings.Contains(err.Error(), "no valid PEM certificates found") {
			t.Errorf("err = %q, want containing %q", err.Error(), "no valid PEM certificates found")
		}
	})
}

func TestNewOAuthHTTPClient(t *testing.T) {
	_ = t.Context()

	t.Run("empty caFile returns client with default transport", func(t *testing.T) {
		client, err := NewOAuthHTTPClient("")
		if err != nil {
			t.Fatalf("NewOAuthHTTPClient(\"\") error = %v", err)
		}
		if client == nil {
			t.Fatal("expected non-nil client")
		}
		if client.Timeout != DefaultOAuthHTTPTimeout {
			t.Errorf("client.Timeout = %v, want %v", client.Timeout, DefaultOAuthHTTPTimeout)
		}
	})

	t.Run("valid caFile returns client with custom transport and root CAs", func(t *testing.T) {
		certBytes := generateTestCertPEM(t)
		tmpDir := t.TempDir()
		certFile := filepath.Join(tmpDir, "ca.pem")
		if err := os.WriteFile(certFile, certBytes, 0600); err != nil {
			t.Fatalf("failed to write test cert: %v", err)
		}

		client, err := NewOAuthHTTPClient(certFile)
		if err != nil {
			t.Fatalf("NewOAuthHTTPClient() error = %v", err)
		}
		if client == nil {
			t.Fatal("expected non-nil client")
		}
		transport, ok := client.Transport.(*http.Transport)
		if !ok || transport == nil {
			t.Fatalf("expected *http.Transport, got %T", client.Transport)
		}
		if transport.Proxy == nil {
			t.Error("expected transport.Proxy to be preserved from DefaultTransport")
		}
		if !transport.ForceAttemptHTTP2 {
			t.Error("expected ForceAttemptHTTP2 to be preserved as true")
		}
		if transport.TLSClientConfig == nil || transport.TLSClientConfig.RootCAs == nil {
			t.Fatal("expected non-nil TLSClientConfig with RootCAs")
		}
		if transport.TLSClientConfig.MinVersion != tls.VersionTLS12 {
			t.Errorf("MinVersion = %v, want %v", transport.TLSClientConfig.MinVersion, tls.VersionTLS12)
		}
	})

	t.Run("proxy environment variables are honored", func(t *testing.T) {
		t.Setenv("HTTPS_PROXY", "http://proxy.test:8080")

		certBytes := generateTestCertPEM(t)
		tmpDir := t.TempDir()
		certFile := filepath.Join(tmpDir, "ca.pem")
		if err := os.WriteFile(certFile, certBytes, 0600); err != nil {
			t.Fatalf("failed to write test cert: %v", err)
		}

		client, err := NewOAuthHTTPClient(certFile)
		if err != nil {
			t.Fatalf("NewOAuthHTTPClient() error = %v", err)
		}

		transport, ok := client.Transport.(*http.Transport)
		if !ok || transport == nil {
			t.Fatalf("expected *http.Transport, got %T", client.Transport)
		}

		req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, "https://hydra.test/oauth2/token", nil)
		if err != nil {
			t.Fatalf("http.NewRequestWithContext() error = %v", err)
		}

		proxyURL, err := transport.Proxy(req)
		if err != nil {
			t.Fatalf("transport.Proxy() error = %v", err)
		}
		if proxyURL == nil {
			t.Fatal("expected non-nil proxy URL when HTTPS_PROXY is set")
		}
		if got := proxyURL.String(); got != "http://proxy.test:8080" {
			t.Errorf("proxyURL = %q, want %q", got, "http://proxy.test:8080")
		}
	})

	t.Run("missing caFile propagates error", func(t *testing.T) {
		client, err := NewOAuthHTTPClient("/nonexistent/oauth/ca.pem")
		if err == nil {
			t.Fatal("expected error for missing caFile")
		}
		if client != nil {
			t.Errorf("expected nil client on error, got %v", client)
		}
	})
}
