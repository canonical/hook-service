// Copyright 2025 Canonical Ltd.
// SPDX-License-Identifier: AGPL-3.0

package cmd

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/kelseyhightower/envconfig"
	"github.com/spf13/cobra"

	"github.com/canonical/hook-service/internal/authorization"
	"github.com/canonical/hook-service/internal/config"
	"github.com/canonical/hook-service/internal/db"
	"github.com/canonical/hook-service/internal/logging"
	"github.com/canonical/hook-service/internal/monitoring/prometheus"
	"github.com/canonical/hook-service/internal/openfga"
	"github.com/canonical/hook-service/internal/salesforce"
	"github.com/canonical/hook-service/internal/storage"
	"github.com/canonical/hook-service/internal/tracing"
	"github.com/canonical/hook-service/pkg/authentication"
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
		DSN:             specs.DSN,
		MaxConns:        specs.DBMaxConns,
		MinConns:        specs.DBMinConns,
		MaxConnLifetime: specs.DBMaxConnLifetime,
		MaxConnIdleTime: specs.DBMaxConnIdleTime,
		TracingEnabled:  specs.TracingEnabled,
	}
	dbClient, err := db.NewDBClient(dbConfig, tracer, monitor, logger)
	if err != nil {
		return fmt.Errorf("failed to create database client: %v", err)
	}
	defer dbClient.Close()
	s := storage.NewStorage(dbClient, tracer, monitor, logger)

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

	var sf salesforce.SalesforceInterface
	if specs.SalesforceEnabled {
		sf = salesforce.NewClient(
			specs.SalesforceDomain,
			specs.SalesforceConsumerKey,
			specs.SalesforceConsumerSecret,
		)
	}

	jwtVerifier, err := authentication.NewJWTAuthenticator(
		context.Background(),
		specs.AuthEnabled,
		specs.AuthIssuer,
		specs.AuthJwksURL,
		tracer,
		monitor,
		logger,
	)
	if err != nil {
		return fmt.Errorf("failed to setup JWT authenticator: %v", err)
	}

	router := web.NewRouter(
		specs.ApiToken,
		s,
		dbClient,
		sf,
		authorizer,
		jwtVerifier,
		specs.AuthIssuer,
		specs.AuthAllowedSubjects,
		specs.AuthRequiredScope,
		tracer,
		monitor,
		logger,
	)
	logger.Infof("Starting server on port %v", specs.Port)

	srv := &http.Server{
		Addr:         fmt.Sprintf("0.0.0.0:%v", specs.Port),
		WriteTimeout: time.Second * 60,
		ReadTimeout:  time.Second * 15,
		IdleTimeout:  time.Second * 60,
		Handler:      router,
	}

	var serverError error
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	go func() {
		logger.Security().SystemStartup()
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverError = fmt.Errorf("server error: %w", err)
			c <- os.Interrupt
		}
	}()

	<-c

	// Create a deadline to wait for.
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	logger.Security().SystemShutdown()
	if err := srv.Shutdown(ctx); err != nil {
		serverError = fmt.Errorf("server shutdown error: %w", err)
	}

	return serverError
}

func main() {
	if err := serve(); err != nil {
		fmt.Fprintf(os.Stderr, "Fatal error: %v\n", err)
		os.Exit(1)
	}
}
