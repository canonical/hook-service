// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package sasl

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// DefaultOAuthHTTPTimeout is the default request timeout for the OAuth token endpoint.
const DefaultOAuthHTTPTimeout = 10 * time.Second

// LoadOAuthCertPool loads a dedicated CA certificate pool from the specified PEM file path.
// If caFile is empty, it returns nil, indicating the system truststore should be used.
func LoadOAuthCertPool(caFile string) (*x509.CertPool, error) {
	if strings.TrimSpace(caFile) == "" {
		return nil, nil
	}

	pemData, err := os.ReadFile(caFile)
	if err != nil {
		if pathErr, ok := errors.AsType[*os.PathError](err); ok {
			return nil, fmt.Errorf("failed to read oauth ca certificate file %q (path error: %v): %w", pathErr.Path, pathErr.Err, err)
		}
		return nil, fmt.Errorf("failed to read oauth ca certificate file %q: %w", caFile, err)
	}

	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(pemData); !ok {
		return nil, fmt.Errorf("failed to parse oauth ca certificates from %q: no valid PEM certificates found", caFile)
	}

	return pool, nil
}

// NewOAuthHTTPClient constructs an HTTP client for communicating with the OAuth2 token endpoint.
// When caFile is provided, a dedicated certificate pool is loaded to ensure TLS trust isolation.
func NewOAuthHTTPClient(caFile string) (*http.Client, error) {
	pool, err := LoadOAuthCertPool(caFile)
	if err != nil {
		return nil, err
	}

	client := &http.Client{
		Timeout: DefaultOAuthHTTPTimeout,
	}

	if pool != nil {
		transport := http.DefaultTransport.(*http.Transport).Clone()
		transport.TLSClientConfig = &tls.Config{
			RootCAs:    pool,
			MinVersion: tls.VersionTLS12,
		}
		client.Transport = transport
	}

	return client, nil
}
