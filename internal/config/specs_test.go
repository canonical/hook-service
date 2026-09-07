// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"testing"
)

func TestValidateKafkaBrokers(t *testing.T) {
	tests := []struct {
		name    string
		brokers []string
		wantErr bool
	}{
		{
			name:    "empty slice",
			brokers: []string{},
			wantErr: false,
		},
		{
			name:    "nil slice",
			brokers: nil,
			wantErr: false,
		},
		{
			name:    "valid single broker",
			brokers: []string{"localhost:9092"},
			wantErr: false,
		},
		{
			name:    "valid multiple brokers",
			brokers: []string{"kafka-1:9092", "kafka-2:9092", "10.0.0.1:9094"},
			wantErr: false,
		},
		{
			name:    "valid with whitespace",
			brokers: []string{" localhost:9092 "},
			wantErr: false,
		},
		{
			name:    "invalid missing port",
			brokers: []string{"localhost"},
			wantErr: true,
		},
		{
			name:    "invalid empty host",
			brokers: []string{":9092"},
			wantErr: true,
		},
		{
			name:    "invalid format",
			brokers: []string{"http://localhost:9092"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateKafkaBrokers(tt.brokers)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateKafkaBrokers() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
