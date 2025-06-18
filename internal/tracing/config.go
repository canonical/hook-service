package tracing

import (
	"github.com/canonical/hook-service/internal/logging"
)

type Config struct {
	OtelHTTPEndpoint string
	OtelGRPCEndpoint string
	Logger           logging.LoggerInterface

	Enabled bool
}

func NewConfig(enabled bool, otelGRPCEndpoint, otelHTTPEndpoint string, logger logging.LoggerInterface) *Config {
	c := new(Config)

	c.OtelGRPCEndpoint = otelGRPCEndpoint
	c.OtelHTTPEndpoint = otelHTTPEndpoint
	c.Logger = logger
	c.Enabled = enabled

	return c
}

func NewNoopConfig() *Config {
	c := new(Config)
	c.Enabled = false
	return c
}
