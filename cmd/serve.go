// Copyright 2026 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0-only

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	tenantpb "github.com/canonical/identity-platform-api/v0/tenant"
	"github.com/kelseyhightower/envconfig"
	"github.com/spf13/cobra"
	"golang.org/x/sync/errgroup"
	"google.golang.org/grpc"

	pb "github.com/canonical/hook-service/gen/hook/groups/v1"
	"github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/config"
	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/kafka"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring"
	"github.com/canonical/hook-service/internal/monitoring/prometheus"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/pool"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tenants"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/pkg/authentication"
	groups_api "github.com/canonical/hook-service/pkg/groups"
	"github.com/canonical/hook-service/pkg/web"
)

var serveCmd = &cobra.Command{
	Use:   "serve",
	Short: "serve starts the web server",
	Long:  `Launch the web application, list of environment variables is available in the readme`,
	Run: func(cmd *cobra.Command, args []string) {
		main()
	},
}

func init() {
	rootCmd.AddCommand(serveCmd)
}

var errShutdownSignal = errors.New("shutdown signal received")

// publisherSetup is the outcome of selecting a permission publisher: the publisher
// itself, the deployment mode its selection implies, and the function that releases it.
type publisherSetup struct {
	publisher groups_api.PermissionPublisherInterface
	mode      config.DeploymentMode
	close     func() error
}

// newPublisherSetup selects the permission publisher and the deployment mode implied by
// the configured brokers, reports the active mode, and warns when the configured
// federated service name disagrees with that mode. The publisher and the mode are
// produced together so the reported mode cannot diverge from the publisher in use.
//
// The federated service name is validated regardless of mode: an invalid name is a
// configuration error whether or not the running mode consumes it, and deferring the
// check to platform mode would surface it when brokers are first configured rather
// than when the mistake is made.
func newPublisherSetup(
	specs *config.EnvSpec,
	tracer tracing.TracingInterface,
	monitor monitoring.MonitorInterface,
	logger logging.LoggerInterface,
) (*publisherSetup, error) {
	if err := config.ValidateFederatedServiceName(specs.FederatedServiceName); err != nil {
		return nil, fmt.Errorf("invalid federated service configuration: %w", err)
	}

	namedDeployment := specs.FederatedServiceName != config.DefaultFederatedServiceName

	if len(specs.KafkaBrokers) == 0 {
		publisher := kafka.NewNoopPublisher(tracer, monitor, logger)
		logger.Infof(
			"Running in %s mode: permission event publishing is disabled (no brokers configured)",
			config.ModeStandalone,
		)
		if namedDeployment {
			logger.Warnf(
				"Federated service name %q is configured but no brokers are set; running in %s mode and publishing nothing",
				specs.FederatedServiceName, config.ModeStandalone,
			)
		}
		return &publisherSetup{
			publisher: publisher,
			mode:      config.ModeStandalone,
			close:     publisher.Close,
		}, nil
	}

	if err := config.ValidateKafkaBrokers(specs.KafkaBrokers); err != nil {
		return nil, fmt.Errorf("invalid kafka configuration: %w", err)
	}

	topic := config.PermissionsTopic(specs.FederatedServiceName)
	writer := kafka.NewKafkaWriter(specs.KafkaBrokers, topic)
	publisher := kafka.NewPermissionPublisher(writer, specs.FederatedServiceName, topic, tracer, monitor, logger)
	logger.Infof(
		"Running in %s mode: federated service %q, permission topic %q, brokers %v",
		config.ModePlatform, specs.FederatedServiceName, topic, specs.KafkaBrokers,
	)
	if !namedDeployment {
		logger.Warnf(
			"Running in %s mode under the default federated service name %q; permission events will be attributed to the standalone service",
			config.ModePlatform, specs.FederatedServiceName,
		)
	}
	return &publisherSetup{
		publisher: publisher,
		mode:      config.ModePlatform,
		close:     publisher.Close,
	}, nil
}

func serve() error {
	specs := new(config.EnvSpec)
	if err := envconfig.Process("", specs); err != nil {
		panic(fmt.Errorf("issues with environment sourcing: %s", err))
	}

	logger := logging.NewLogger(specs.LogLevel)
	logger.Debugf("env vars: %v", specs)
	defer logger.Sync()

	monitor := prometheus.NewMonitor("hook-service", logger)
	tracer := tracing.NewTracer(tracing.NewConfig(specs.TracingEnabled, specs.OtelGRPCEndpoint, specs.OtelHTTPEndpoint, logger))

	dbConfig := db.Config{
		DSN:                       specs.DSN,
		MaxConns:                  specs.DBMaxConns,
		MinConns:                  specs.DBMinConns,
		MaxConnLifetime:           specs.DBMaxConnLifetime,
		MaxConnIdleTime:           specs.DBMaxConnIdleTime,
		TracingEnabled:            specs.TracingEnabled,
		ReplicaDSN:                specs.ReplicaDSN,
		ReplicaMaxConns:           specs.ReplicaDBMaxConns,
		ReplicaMinConns:           specs.ReplicaDBMinConns,
		ReplicaMaxConnLifetime:    specs.ReplicaDBMaxConnLifetime,
		ReplicaMaxConnIdleTime:    specs.ReplicaDBMaxConnIdleTime,
		MaxReplicaLagMs:           specs.MaxReplicaLagMs,
		ReplicaPoolSizeMultiplier: specs.ReplicaPoolSizeMultiplier,
	}
	dbClient, err := db.NewDBClient(dbConfig, tracer, monitor, logger)
	if err != nil {
		return fmt.Errorf("failed to create database client: %v", err)
	}
	defer dbClient.Close()
	s := storage.NewStorage(dbClient, tracer, monitor, logger)
	s.SetStreamTimeout(specs.StreamTimeout)

	var authorizer *authorization.Authorizer
	if specs.AuthorizationEnabled {
		ofga := openfga.NewClient(
			openfga.NewConfig(
				specs.OpenfgaApiScheme,
				specs.OpenfgaApiHost,
				specs.OpenfgaStoreId,
				specs.OpenfgaApiToken,
				specs.OpenfgaModelId,
				specs.Debug,
				tracer,
				monitor,
				logger,
			),
		)
		authorizer = authorization.NewAuthorizer(
			ofga,
			tracer,
			monitor,
			logger,
		)
		logger.Info("Authorization is enabled")
		if authorizer.ValidateModel(context.Background()) != nil {
			panic("Invalid authorization model provided")
		}
	} else {
		authorizer = authorization.NewAuthorizer(
			openfga.NewNoopClient(tracer, monitor, logger),
			tracer,
			monitor,
			logger,
		)
		logger.Info("Using noop authorizer")
	}

	var tenantValidator tenants.TenantValidatorInterface
	if specs.TenantServiceGRPCAddress != "" {
		tenantServiceConn, err := tenants.NewGRPCConn(specs.TenantServiceGRPCAddress, specs.TenantServiceTLSEnabled)
		if err != nil {
			return err
		}
		defer tenantServiceConn.Close()

		tenantValidator = tenants.NewClient(
			tenantpb.NewTenantServiceClient(tenantServiceConn),
			specs.TenantServiceGRPCTimeout,
			tracer,
			monitor,
			logger,
		)
		logger.Infof("Tenant validation enabled (tenant-service: %s, tls: %v, timeout: %s)", specs.TenantServiceGRPCAddress, specs.TenantServiceTLSEnabled, specs.TenantServiceGRPCTimeout)
	} else {
		tenantValidator = tenants.NewNoopValidator()
		logger.Info("Tenant validation disabled (no TENANT_SERVICE_GRPC_ADDRESS)")
	}

	var jwtVerifier authentication.TokenVerifierInterface
	if specs.AuthenticationEnabled {
		var allowedSubjects []string
		if specs.AuthenticationAllowedSubjects != "" {
			subjects := strings.Split(specs.AuthenticationAllowedSubjects, ",")
			for _, s := range subjects {
				trimmed := strings.TrimSpace(s)
				if trimmed != "" {
					allowedSubjects = append(allowedSubjects, trimmed)
				}
			}
		}

		var err error
		jwtVerifier, err = authentication.NewJWTAuthenticator(
			context.Background(),
			specs.AuthenticationIssuer,
			specs.AuthenticationJwksURL,
			allowedSubjects,
			specs.AuthenticationRequiredScope,
			tracer,
			monitor,
			logger,
		)
		if err != nil {
			return fmt.Errorf("failed to setup JWT authenticator: %v", err)
		}
	} else {
		logger.Info("JWT authentication is disabled")
		jwtVerifier = authentication.NewNoopVerifier()
	}

	wpool := pool.NewWorkerPool(specs.HookMaxConcurrent, tracer, monitor, logger)
	defer wpool.Stop()

	publishing, err := newPublisherSetup(specs, tracer, monitor, logger)
	if err != nil {
		return err
	}
	defer func() {
		if err := publishing.close(); err != nil {
			logger.Errorf("failed to close kafka publisher: %v", err)
		}
	}()
	publisher, deploymentMode := publishing.publisher, publishing.mode

	router := web.NewRouter(
		specs.ApiToken,
		specs.AuthenticationEnabled,
		wpool,
		s,
		dbClient,
		authorizer,
		tenantValidator,
		jwtVerifier,
		publisher,
		deploymentMode,
		tracer,
		monitor,
		logger,
	)

	groupService := groups_api.NewService(s, authorizer, publisher, tracer, monitor, logger)

	httpServer := &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%v", specs.Port),
		WriteTimeout: time.Second * 60,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      router,
	}

	grpcSrv := grpc.NewServer(
		grpc.MaxConcurrentStreams(specs.GRPCMaxConcurrentStreams),
		grpc.ChainUnaryInterceptor(
			db.UnaryReplicaRoutingInterceptor(),
		),
		grpc.ChainStreamInterceptor(
			authentication.NewGrpcInterceptor(jwtVerifier, tracer, monitor, logger).StreamAuthenticate(),
			db.StreamReplicaRoutingInterceptor(),
		),
	)
	mappingServer := groups_api.NewMappingGrpcServer(groupService, tracer, monitor, logger)
	pb.RegisterGroupsMappingServiceServer(grpcSrv, mappingServer)

	grpcLis, err := net.Listen("tcp", fmt.Sprintf("0.0.0.0:%v", specs.GRPCPort))
	if err != nil {
		return fmt.Errorf("failed to listen on gRPC port %v: %v", specs.GRPCPort, err)
	}

	logger.Infof("Starting HTTP server on port %v", specs.Port)
	logger.Infof("Starting gRPC server on port %v", specs.GRPCPort)

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)

	eg, ctx := errgroup.WithContext(context.Background())

	eg.Go(func() error {
		logger.Security().SystemStartup()

		errChan := make(chan error, 1)
		go func() {
			errChan <- httpServer.ListenAndServe()
		}()

		select {
		case err := <-errChan:
			if err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("HTTP server error: %w", err)
			}
			return nil
		case <-ctx.Done():
			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer shutdownCancel()
			if err := httpServer.Shutdown(shutdownCtx); err != nil {
				return fmt.Errorf("HTTP server shutdown error: %w", err)
			}
			return nil
		}
	})

	eg.Go(func() error {
		errChan := make(chan error, 1)
		go func() {
			errChan <- grpcSrv.Serve(grpcLis)
		}()

		select {
		case err := <-errChan:
			if err != nil && !errors.Is(err, grpc.ErrServerStopped) {
				return fmt.Errorf("gRPC server error: %w", err)
			}
			return nil
		case <-ctx.Done():
			stopped := make(chan struct{})
			go func() {
				grpcSrv.GracefulStop()
				close(stopped)
			}()

			shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer shutdownCancel()

			select {
			case <-stopped:
			case <-shutdownCtx.Done():
				grpcSrv.Stop()
			}
			return nil
		}
	})

	eg.Go(func() error {
		select {
		case <-sigCh:
			return errShutdownSignal
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	if err := eg.Wait(); err == nil || errors.Is(err, errShutdownSignal) {
		logger.Security().SystemShutdown()
		return nil
	}

	return err
}

func main() {
	if err := serve(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error: %v\n", err)
		os.Exit(1)
	}
}
