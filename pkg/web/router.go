package web

import (
	"fmt"
	"net/http"
	"net/url"

	"github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/salesforce"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/pkg/hooks"
	"github.com/canonical/hook-service/pkg/metrics"
	"github.com/canonical/hook-service/pkg/status"
	chi "github.com/go-chi/chi/v5"
	middleware "github.com/go-chi/chi/v5/middleware"
)

func parseBaseURL(baseUrl string) *url.URL {
	if baseUrl[len(baseUrl)-1] != '/' {
		baseUrl += "/"
	}

	// Check if has app suburl.
	u, err := url.Parse(baseUrl)
	if err != nil {
		panic(fmt.Errorf("invalid BASE_URL: %v", err))
	}

	return u
}

func NewRouter(
	token string,
	salesforceClient salesforce.SalesforceInterface,
	authz authorization.AuthorizerInterface,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) http.Handler {
	router := chi.NewMux()

	middlewares := make(chi.Middlewares, 0)
	middlewares = append(
		middlewares,
		middleware.RequestID,
		monitoring.NewMiddleware(monitor, logger).ResponseTime(),
		middlewareCORS([]string{"*"}),
	)

	if true {
		middlewares = append(
			middlewares,
			middleware.RequestLogger(logging.NewLogFormatter(logger)), // LogFormatter will only work if logger is set to DEBUG level
		)
	}

	var authMiddleware *hooks.AuthMiddleware = nil
	if token != "" {
		authMiddleware = hooks.NewAuthMiddleware(token, tracer, logger)
	}

	groupClients := []hooks.ClientInterface{}
	if salesforceClient != nil {
		groupClients = append(groupClients, hooks.NewSalesforceClient(salesforceClient, tracer, monitor, logger))
	}

	router.Use(middlewares...)

	hooks.NewAPI(
		hooks.NewService(groupClients, authz, tracer, monitor, logger),
		authMiddleware,
		tracer,
		monitor,
		logger).RegisterEndpoints(router)
	metrics.NewAPI(logger).RegisterEndpoints(router)
	status.NewAPI(tracer, monitor, logger).RegisterEndpoints(router)

	return tracing.NewMiddleware(monitor, logger).OpenTelemetry(router)
}
