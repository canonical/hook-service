// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package config

import (
	"flag"
	"time"
)

// EnvSpec is the basic environment configuration setup needed for the app to start
type EnvSpec struct {
	OtelGRPCEndpoint string `envconfig:"otel_grpc_endpoint"`
	OtelHTTPEndpoint string `envconfig:"otel_http_endpoint"`
	TracingEnabled   bool   `envconfig:"tracing_enabled" default:"true"`

	LogLevel string `envconfig:"log_level" default:"error"`
	Debug    bool   `envconfig:"debug" default:"false"`

	Port int `envconfig:"port" default:"8080"`

	ApiToken string `envconfig:"api_token" default:""`

	OpenfgaApiScheme string `envconfig:"openfga_api_scheme" default:""`
	OpenfgaApiHost   string `envconfig:"openfga_api_host"`
	OpenfgaApiToken  string `envconfig:"openfga_api_token"`
	OpenfgaStoreId   string `envconfig:"openfga_store_id"`
	OpenfgaModelId   string `envconfig:"openfga_authorization_model_id" default:""`

	SalesforceEnabled        bool   `envconfig:"salesforce_enabled" default:"true"`
	SalesforceDomain         string `envconfig:"salesforce_domain"`
	SalesforceConsumerKey    string `envconfig:"salesforce_consumer_key"`
	SalesforceConsumerSecret string `envconfig:"salesforce_consumer_secret"`

	AuthorizationEnabled bool `envconfig:"authorization_enabled" default:"false"`
	OpenFGAWorkersTotal  int  `envconfig:"openfga_workers_total" default:"150"`

	AuthenticationEnabled         bool   `envconfig:"authentication_enabled" default:"true"`
	AuthenticationIssuer          string `envconfig:"authentication_issuer"`
	AuthenticationJwksURL         string `envconfig:"authentication_jwks_url"`
	AuthenticationAllowedSubjects string `envconfig:"authentication_allowed_subjects"`
	AuthenticationRequiredScope   string `envconfig:"authentication_required_scope"`

	DSN string `envconfig:"DSN" required:"true"`

	DBMaxConns        int32         `envconfig:"db_max_conns" default:"25"`
	DBMinConns        int32         `envconfig:"db_min_conns" default:"2"`
	DBMaxConnLifetime time.Duration `envconfig:"db_max_conn_lifetime" default:"1h"`
	DBMaxConnIdleTime time.Duration `envconfig:"db_max_conn_idle_time" default:"30m"`
}

type Flags struct {
	ShowVersion bool
}

func NewFlags() *Flags {
	f := new(Flags)

	flag.BoolVar(&f.ShowVersion, "version", false, "Show the app version and exit")
	flag.Parse()

	return f
}
