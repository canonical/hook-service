// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package config

import (
	"strings"
	"testing"

	"github.com/kelseyhightower/envconfig"
)

func TestDeploymentModeValues(t *testing.T) {
	if ModePlatform != "platform" {
		t.Errorf("ModePlatform = %q, want %q", ModePlatform, "platform")
	}
	if ModeStandalone != "standalone" {
		t.Errorf("ModeStandalone = %q, want %q", ModeStandalone, "standalone")
	}
	if ModePlatform == ModeStandalone {
		t.Error("deployment modes must be distinguishable")
	}
}

func TestFederatedServiceNameDefault(t *testing.T) {
	t.Setenv("DSN", "postgres://localhost/test")

	specs := new(EnvSpec)
	if err := envconfig.Process("", specs); err != nil {
		t.Fatalf("envconfig.Process() error = %v", err)
	}

	if specs.FederatedServiceName != DefaultFederatedServiceName {
		t.Errorf("FederatedServiceName = %q, want %q", specs.FederatedServiceName, DefaultFederatedServiceName)
	}
	if DefaultFederatedServiceName != "hook-service" {
		t.Errorf("DefaultFederatedServiceName = %q, want %q", DefaultFederatedServiceName, "hook-service")
	}
}

func TestFederatedServiceNameConfigured(t *testing.T) {
	t.Setenv("DSN", "postgres://localhost/test")
	t.Setenv("FEDERATED_SERVICE_NAME", "portal")

	specs := new(EnvSpec)
	if err := envconfig.Process("", specs); err != nil {
		t.Fatalf("envconfig.Process() error = %v", err)
	}

	if specs.FederatedServiceName != "portal" {
		t.Errorf("FederatedServiceName = %q, want %q", specs.FederatedServiceName, "portal")
	}
}

func TestPermissionsTopic(t *testing.T) {
	tests := []struct {
		name                 string
		federatedServiceName string
		want                 string
	}{
		{
			name:                 "default name reproduces the existing topic",
			federatedServiceName: DefaultFederatedServiceName,
			want:                 "permissions.hook-service",
		},
		{
			name:                 "portal name",
			federatedServiceName: "portal",
			want:                 "permissions.portal",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PermissionsTopic(tt.federatedServiceName); got != tt.want {
				t.Errorf("PermissionsTopic(%q) = %q, want %q", tt.federatedServiceName, got, tt.want)
			}
		})
	}
}

func TestValidateFederatedServiceName(t *testing.T) {
	tests := []struct {
		name                 string
		federatedServiceName string
		wantErr              bool
	}{
		{
			name:                 "default name",
			federatedServiceName: "hook-service",
			wantErr:              false,
		},
		{
			name:                 "portal name",
			federatedServiceName: "portal",
			wantErr:              false,
		},
		{
			name:                 "dots underscores and digits allowed",
			federatedServiceName: "svc_1.a-b",
			wantErr:              false,
		},
		{
			name:                 "empty",
			federatedServiceName: "",
			wantErr:              true,
		},
		{
			name:                 "whitespace only",
			federatedServiceName: "   ",
			wantErr:              true,
		},
		{
			name:                 "tab only",
			federatedServiceName: "\t",
			wantErr:              true,
		},
		{
			name:                 "trailing space",
			federatedServiceName: "portal ",
			wantErr:              true,
		},
		{
			name:                 "internal space",
			federatedServiceName: "hook service",
			wantErr:              true,
		},
		{
			name:                 "slash",
			federatedServiceName: "hook/service",
			wantErr:              true,
		},
		{
			name:                 "longest name yielding a legal topic",
			federatedServiceName: strings.Repeat("a", MaxTopicNameLength-len("permissions.")),
			wantErr:              false,
		},
		{
			name:                 "name one character too long for a legal topic",
			federatedServiceName: strings.Repeat("a", MaxTopicNameLength-len("permissions.")+1),
			wantErr:              true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateFederatedServiceName(tt.federatedServiceName)
			if (err != nil) != tt.wantErr {
				t.Fatalf("ValidateFederatedServiceName(%q) error = %v, wantErr %v", tt.federatedServiceName, err, tt.wantErr)
			}
		})
	}
}

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
