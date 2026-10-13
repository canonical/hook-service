// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package kafka

import (
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
)

// BuildBrokerTLSConfig constructs a TLS configuration for Kafka broker transport.
// If caFile is non-empty, a dedicated certificate pool is created containing only the
// certificates from that file, guaranteeing truststore isolation without polluting system pools.
// If caFile is empty, RootCAs remains nil to fall back to the host system truststore.
func BuildBrokerTLSConfig(caFile string, insecureSkipVerify bool) (*tls.Config, error) {
	tlsConfig := &tls.Config{
		MinVersion:         tls.VersionTLS12,
		InsecureSkipVerify: insecureSkipVerify,
	}

	if strings.TrimSpace(caFile) == "" {
		return tlsConfig, nil
	}

	pemData, err := os.ReadFile(caFile)
	if err != nil {
		if pathErr, ok := errors.AsType[*os.PathError](err); ok {
			return nil, fmt.Errorf("failed to read broker ca certificate file %q (path error: %v): %w", pathErr.Path, pathErr.Err, err)
		}
		return nil, fmt.Errorf("failed to read broker ca certificate file %q: %w", caFile, err)
	}

	pool := x509.NewCertPool()
	if ok := pool.AppendCertsFromPEM(pemData); !ok {
		return nil, fmt.Errorf("failed to parse broker ca certificates from %q: no valid PEM certificates found", caFile)
	}

	tlsConfig.RootCAs = pool
	return tlsConfig, nil
}
