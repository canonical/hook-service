// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func generateBrokerTestCertPEM(t *testing.T) []byte {
	t.Helper()
	priv, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("failed to generate ecdsa key: %v", err)
	}

	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject: pkix.Name{
			CommonName: "test-broker-ca",
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

func TestBuildBrokerTLSConfig(t *testing.T) {
	_ = t.Context()

	t.Run("empty caFile falls back to system truststore", func(t *testing.T) {
		cfg, err := BuildBrokerTLSConfig("", false)
		if err != nil {
			t.Fatalf("BuildBrokerTLSConfig(\"\", false) error = %v", err)
		}
		if cfg == nil {
			t.Fatal("expected non-nil tls.Config")
		}
		if cfg.RootCAs != nil {
			t.Errorf("expected nil RootCAs for system truststore fallback, got %v", cfg.RootCAs)
		}
		if cfg.InsecureSkipVerify {
			t.Error("expected InsecureSkipVerify = false")
		}
		if cfg.MinVersion != tls.VersionTLS12 {
			t.Errorf("cfg.MinVersion = %x, want %x", cfg.MinVersion, tls.VersionTLS12)
		}
	})

	t.Run("insecureSkipVerify is respected when true", func(t *testing.T) {
		cfg, err := BuildBrokerTLSConfig("", true)
		if err != nil {
			t.Fatalf("BuildBrokerTLSConfig(\"\", true) error = %v", err)
		}
		if !cfg.InsecureSkipVerify {
			t.Error("expected InsecureSkipVerify = true")
		}
	})

	t.Run("valid caFile creates dedicated pool", func(t *testing.T) {
		certPEM := generateBrokerTestCertPEM(t)
		tmpDir := t.TempDir()
		caPath := filepath.Join(tmpDir, "broker-ca.pem")
		if err := os.WriteFile(caPath, certPEM, 0600); err != nil {
			t.Fatalf("failed to write test cert: %v", err)
		}

		cfg, err := BuildBrokerTLSConfig(caPath, false)
		if err != nil {
			t.Fatalf("BuildBrokerTLSConfig() error = %v", err)
		}
		if cfg.RootCAs == nil {
			t.Fatal("expected non-nil RootCAs for dedicated pool")
		}
	})

	t.Run("missing caFile returns wrapped path error", func(t *testing.T) {
		nonExistentFile := filepath.Join(t.TempDir(), "missing-ca.pem")
		cfg, err := BuildBrokerTLSConfig(nonExistentFile, false)
		if err == nil {
			t.Fatal("expected error for missing file, got nil")
		}
		if cfg != nil {
			t.Errorf("expected nil config on error, got %v", cfg)
		}

		// Verify with Go 1.26+ errors.AsType
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

		cfg, err := BuildBrokerTLSConfig(badFile, false)
		if err == nil {
			t.Fatal("expected error for invalid PEM, got nil")
		}
		if cfg != nil {
			t.Errorf("expected nil config on error, got %v", cfg)
		}
		if !strings.Contains(err.Error(), "no valid PEM certificates found") {
			t.Errorf("err = %q, want containing %q", err.Error(), "no valid PEM certificates found")
		}
	})
}
