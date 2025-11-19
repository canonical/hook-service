package web

import (
	"net/http"

	v0_authz "github.com/canonical/identity-platform-api/v0/authorization"
	v0_groups "github.com/canonical/identity-platform-api/v0/authz_groups"
	chi "github.com/go-chi/chi/v5"
	middleware "github.com/go-chi/chi/v5/middleware"
	"github.com/grpc-ecosystem/grpc-gateway/v2/runtime"
	"golang.org/x/net/context"
	"google.golang.org/protobuf/encoding/protojson"

	"github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/http/types"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/salesforce"
	"github.com/canonical/hook-service/internal/tracing"
	authz_api "github.com/canonical/hook-service/pkg/authorization"
	groups_api "github.com/canonical/hook-service/pkg/groups"
	"github.com/canonical/hook-service/pkg/hooks"
	"github.com/canonical/hook-service/pkg/metrics"
	"github.com/canonical/hook-service/pkg/status"
)

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

	authzService := authz_api.NewService(authz_api.NewStorage(), authz, tracer, monitor, logger)
	groupService := groups_api.NewService(groups_api.NewStorage(), authz, tracer, monitor, logger)

	groupClients := []hooks.ClientInterface{}
	if salesforceClient != nil {
		groupClients = append(groupClients, hooks.NewSalesforceClient(salesforceClient, tracer, monitor, logger))
	}

	gRPCGatewayMux := runtime.NewServeMux(
		runtime.WithForwardResponseRewriter(types.ForwardErrorResponseRewriter),
		// Use proto field names (snake_case) in JSON output instead of lowerCamelCase.
		runtime.WithMarshalerOption(runtime.MIMEWildcard, &runtime.JSONPb{
			MarshalOptions: protojson.MarshalOptions{
				UseProtoNames: true,
			},
		}),
	)

	router.Use(middlewares...)

	v0_authz.RegisterAppAuthorizationServiceHandlerServer(context.Background(), gRPCGatewayMux, authz_api.NewGrpcServer(authzService, tracer, monitor, logger))
	v0_groups.RegisterAuthzGroupsServiceHandlerServer(context.Background(), gRPCGatewayMux, groups_api.NewGrpcServer(groupService, tracer, monitor, logger))
	hooks.NewAPI(
		hooks.NewService(groupClients, authz, tracer, monitor, logger),
		authMiddleware,
		tracer,
		monitor,
		logger).RegisterEndpoints(router)
	metrics.NewAPI(logger).RegisterEndpoints(router)
	status.NewAPI(tracer, monitor, logger).RegisterEndpoints(router)

	router.Mount("/api/v0/authz", gRPCGatewayMux)

	return tracing.NewMiddleware(monitor, logger).OpenTelemetry(router)
}
