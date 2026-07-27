package runtime

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	"github.com/upcomers-org/platformgo/contracts"
	platformv1 "github.com/upcomers-org/platformgo/contracts/gen/platform/v1"
	"github.com/upcomers-org/platformgo/internal/adapters/centrifugo"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/migrations"
	"google.golang.org/grpc"
)

const (
	outboxBatchSize          = 100
	outboxLease              = 30 * time.Second
	outboxRetry              = time.Second
	realtimeBatchSize        = 1
	realtimeLease            = 30 * time.Second
	realtimeRetry            = time.Second
	realtimeMaxAttempts      = uint32(10)
	realtimeFinalizeTimeout  = 5 * time.Second
	apiKeyReplayCleanupBatch = 100
	runtimeSchemaRevision    = "20260725001100_phase3_committed_realtime_outbox"
)

type databaseRuntimeRole string

const (
	databaseRoleAPI       databaseRuntimeRole = "platformgo_api"
	databaseRoleEngine    databaseRuntimeRole = "platformgo_engine"
	databaseRoleOutbox    databaseRuntimeRole = "platformgo_outbox"
	databaseRoleProjector databaseRuntimeRole = "platformgo_projector"
	databaseRoleRealtime  databaseRuntimeRole = "platformgo_realtime"
)

// Migrate applies immutable forward migrations and provisions the configured
// deployment shard.
func Migrate(ctx context.Context, config Config) error {
	if err := config.ValidateFor("migrate"); err != nil {
		return err
	}
	pool, err := openMigratorPostgres(ctx, config.DatabaseURL)
	if err != nil {
		return err
	}
	defer pool.Close()
	return platformpostgres.NewMigrator(pool, migrations.Files).
		MigrateAndProvision(ctx, config.ShardID)
}

// Doctor verifies every authoritative or delivery dependency without mutating
// external state.
func Doctor(ctx context.Context, config Config) error {
	if err := config.ValidateFor("doctor"); err != nil {
		return err
	}
	pool, err := openPostgres(ctx, config.DatabaseURL, databaseRoleAPI)
	if err != nil {
		return err
	}
	defer pool.Close()
	natsConnection, _, err := openNATS(config.NATSURL)
	if err != nil {
		return err
	}
	defer natsConnection.Close()
	if err := flushNATS(ctx, natsConnection); err != nil {
		return fmt.Errorf("doctor: NATS: %w", err)
	}
	if config.CentrifugoAPIURL != "" {
		gateway, err := centrifugo.New(centrifugo.Config{
			APIURL: config.CentrifugoAPIURL, APIKey: config.CentrifugoAPIKey,
			TokenSecret: doctorTokenSecret(config.CentrifugoTokenSecret),
			// Doctor only uses the health endpoint. Supply a fixed constructor
			// value instead of requiring token-issuance configuration.
			TokenTTL: time.Minute,
		})
		if err != nil {
			return fmt.Errorf("doctor: %w", err)
		}
		if err := gateway.Healthy(ctx); err != nil {
			return fmt.Errorf("doctor: %w", err)
		}
	}
	return nil
}

// Serve runs REST and gRPC over separate listeners with shared durable command
// admission and graceful cancellation.
func Serve(ctx context.Context, config Config) error {
	return serve(
		ctx,
		config,
		func(interval time.Duration) (<-chan time.Time, func()) {
			ticker := time.NewTicker(interval)
			return ticker.C, ticker.Stop
		},
	)
}

type replayCleanupScheduleFactory func(
	time.Duration,
) (<-chan time.Time, func())

func serve(
	ctx context.Context,
	config Config,
	newReplayCleanupSchedule replayCleanupScheduleFactory,
) error {
	if err := config.ValidateFor("serve"); err != nil {
		return err
	}
	pool, err := openPostgres(ctx, config.DatabaseURL, databaseRoleAPI)
	if err != nil {
		return err
	}
	defer pool.Close()
	natsConnection, js, err := openNATS(config.NATSURL)
	if err != nil {
		return err
	}
	defer natsConnection.Close()
	authenticator, err := edge.NewHMACAuthenticator(edge.HMACAuthenticatorConfig{
		ClientTokenSecret: config.ClientTokenSecret,
		BrokerCredentials: config.BrokerCredentials,
	})
	if err != nil {
		return err
	}
	compatibilityStore := platformpostgres.NewCompatibilityStore(pool)
	verifyAPIKeyPolicy := func(checkContext context.Context) error {
		policy := config.LegacyAPIKeyPolicy
		return compatibilityStore.VerifyAPIKeyPolicy(
			checkContext,
			policy.MaxActivePerOwner,
			policy.RateLimitMaxRequests,
			policy.RateLimitWindowSecs,
			policy.IdempotencyTTLSecs,
		)
	}
	verifyAPIKeyReplayCoverage := func(
		checkContext context.Context,
		logCoverage bool,
	) error {
		coverage, coverageErr := compatibilityStore.APIKeyReplayCoverage(
			checkContext,
		)
		if coverageErr != nil {
			return coverageErr
		}
		configured := make(map[string]struct{}, len(config.APIKeyReplayKeys))
		for _, replayKey := range config.APIKeyReplayKeys {
			configured[replayKey.ID] = struct{}{}
		}
		for _, item := range coverage {
			if _, ok := configured[item.KeyID]; !ok {
				return fmt.Errorf(
					"live API-key replay requires unavailable key %q",
					item.KeyID,
				)
			}
		}
		if logCoverage {
			for _, replayKey := range config.APIKeyReplayKeys {
				var (
					liveCount int64
					oldest    string
				)
				for _, item := range coverage {
					if item.KeyID == replayKey.ID {
						liveCount = item.LiveCount
						oldest = item.OldestExpiresAt
						break
					}
				}
				slog.Info(
					"API-key replay key coverage",
					"key_id", replayKey.ID,
					"live_count", liveCount,
					"oldest_expires_at", oldest,
				)
			}
		}
		return nil
	}
	verifyBrokerEchoReplayCoverage := func(
		checkContext context.Context,
		logCoverage bool,
		requireDrained bool,
		allowStaleExpired bool,
	) (platformpostgres.BrokerEchoReplayCoverage, error) {
		coverage, coverageErr :=
			compatibilityStore.BrokerEchoReplayCoverage(checkContext)
		if coverageErr != nil {
			return platformpostgres.BrokerEchoReplayCoverage{}, coverageErr
		}
		if validationErr := validateBrokerEchoReplayCoverage(
			coverage,
			requireDrained,
			allowStaleExpired,
		); validationErr != nil {
			return platformpostgres.BrokerEchoReplayCoverage{}, validationErr
		}
		if logCoverage {
			slog.Info(
				"broker-echo replay coverage",
				"total_rows", coverage.TotalRows,
				"live_rows", coverage.LiveRows,
				"invalid_live_rows", coverage.InvalidLiveRows,
				"overlong_live_rows", coverage.OverlongLiveRows,
				"expired_rows", coverage.ExpiredRows,
				"maximum_principal_rows", coverage.MaximumPrincipalRows,
				"oldest_live_expires_at", coverage.OldestLiveExpiresAt,
				"oldest_expired_at", coverage.OldestExpiredAt,
				"oldest_expired_age_seconds",
				coverage.OldestExpiredAgeSeconds,
				"max_total_rows", coverage.MaxTotalRows,
				"max_rows_per_principal", coverage.MaxRowsPerPrincipal,
				"purge_batch_size", coverage.PurgeBatchSize,
				"max_batches_per_cycle", coverage.MaxBatchesPerCycle,
				"cleanup_interval_seconds",
				coverage.CleanupIntervalSeconds,
				"cleanup_cycle_timeout_seconds",
				coverage.CleanupCycleTimeoutSeconds,
				"expired_readiness_slo_seconds",
				coverage.ExpiredReadinessSLOSeconds,
			)
		}
		return coverage, nil
	}
	purgeExpiredAPIKeyReplays := func(
		cleanupContext context.Context,
	) (int64, error) {
		return compatibilityStore.PurgeExpiredAPIKeyReplays(
			cleanupContext,
			apiKeyReplayCleanupBatch,
		)
	}
	brokerEchoPolicy, policyErr := verifyBrokerEchoReplayCoverage(
		ctx,
		true,
		false,
		true,
	)
	if policyErr != nil {
		return fmt.Errorf("serve: broker-echo replay policy: %w", policyErr)
	}
	purgeExpiredBrokerEchoReplays := func(
		cleanupContext context.Context,
	) (int64, error) {
		startedAt := time.Now()
		cycleContext, cancelCycle := context.WithTimeout(
			cleanupContext,
			time.Duration(
				brokerEchoPolicy.CleanupCycleTimeoutSeconds,
			)*time.Second,
		)
		defer cancelCycle()
		deleted, cleanupErr := drainExpiredBrokerEchoReplays(
			cycleContext,
			compatibilityStore.PurgeExpiredBrokerEchoReplays,
			brokerEchoPolicy.PurgeBatchSize,
			brokerEchoPolicy.MaxBatchesPerCycle,
		)
		slog.Info(
			"broker-echo replay cleanup cycle completed",
			"deleted", deleted,
			"duration", time.Since(startedAt),
			"error", cleanupErr,
		)
		return deleted, cleanupErr
	}
	verifyReplayCoverage := func(
		coverageContext context.Context,
		logCoverage bool,
		requireBrokerEchoDrained bool,
	) error {
		if coverageErr := verifyAPIKeyReplayCoverage(
			coverageContext,
			logCoverage,
		); coverageErr != nil {
			return fmt.Errorf("API-key replay coverage: %w", coverageErr)
		}
		if _, coverageErr := verifyBrokerEchoReplayCoverage(
			coverageContext,
			logCoverage,
			requireBrokerEchoDrained,
			false,
		); coverageErr != nil {
			return fmt.Errorf("broker-echo replay coverage: %w", coverageErr)
		}
		return nil
	}
	if verifyErr := verifyAPIKeyPolicy(ctx); verifyErr != nil {
		return fmt.Errorf("serve: %w", verifyErr)
	}
	if cleanupErr := runReplayCleanupBatch(
		ctx,
		purgeExpiredAPIKeyReplays,
		purgeExpiredBrokerEchoReplays,
		func(coverageContext context.Context) error {
			return verifyReplayCoverage(coverageContext, true, true)
		},
	); cleanupErr != nil {
		return fmt.Errorf("serve: %w", cleanupErr)
	}
	postgresReady := func(checkContext context.Context) error {
		if pingErr := pool.Ping(checkContext); pingErr != nil {
			return pingErr
		}
		if readyErr := compatibilityStore.RuntimeCommandReady(
			checkContext,
			config.ShardID,
		); readyErr != nil {
			return readyErr
		}
		if policyErr := verifyAPIKeyPolicy(checkContext); policyErr != nil {
			return policyErr
		}
		return verifyReplayCoverage(checkContext, false, false)
	}
	natsReady := func(checkContext context.Context) error {
		if flushErr := flushNATS(checkContext, natsConnection); flushErr != nil {
			return flushErr
		}
		return platformnats.CheckEngineCommandPath(
			checkContext,
			js,
			config.ShardID,
			config.NATSStreamLimits,
		)
	}
	commandReady := func(checkContext context.Context) error {
		if readyErr := postgresReady(checkContext); readyErr != nil {
			return readyErr
		}
		return natsReady(checkContext)
	}
	identityService, err := application.NewIdentity(
		compatibilityStore,
		authenticator,
		application.IdentityConfig{
			CommandReadiness: commandReady,
			APIKeyReplayKeys: applicationReplayKeys(
				config.APIKeyReplayKeys,
			),
			APIKeyReplayActiveKeyID: config.APIKeyReplayActiveID,
		},
	)
	if err != nil {
		return err
	}
	realtime, err := centrifugo.New(centrifugo.Config{
		APIURL: config.CentrifugoAPIURL, APIKey: config.CentrifugoAPIKey,
		TokenSecret: config.CentrifugoTokenSecret, TokenTTL: config.CentrifugoTokenTTL,
	})
	if err != nil {
		return err
	}
	submission, err := application.NewOrderSubmission(
		platformpostgres.NewCommandJournal(pool),
		application.OrderSubmissionConfig{
			ShardID: config.ShardID, IdempotencyTTL: 24 * time.Hour,
			Readiness: commandReady,
		},
	)
	if err != nil {
		return err
	}
	openAPI, err := contracts.OpenAPIDocuments()
	if err != nil {
		return err
	}
	httpEdge := edge.NewServer(edge.ServerConfig{
		Authenticator: authenticator,
		Commands:      submission,
		Realtime:      realtime,
		Identity:      identityService,
		Trading:       compatibilityStore,
		Readiness: []edge.HealthCheck{
			{Name: "postgres", Check: postgresReady},
			{Name: "redis", Check: realtime.Healthy},
			{Name: "rabbitmq", Check: natsReady},
		},
		OpenAPI: openAPI, AllowOrigin: config.AllowedOrigin,
		TrustedProxies: config.TrustedProxies,
	})

	listenConfig := net.ListenConfig{}
	restListener, err := listenConfig.Listen(ctx, "tcp", config.RESTAddress)
	if err != nil {
		return fmt.Errorf("serve REST: %w", err)
	}
	defer func() { _ = restListener.Close() }()
	grpcListener, err := listenConfig.Listen(ctx, "tcp", config.GRPCAddress)
	if err != nil {
		return fmt.Errorf("serve gRPC: %w", err)
	}
	defer func() { _ = grpcListener.Close() }()

	httpServer := &http.Server{
		Handler:           httpEdge.Handler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	grpcServer := grpc.NewServer()
	platformv1.RegisterTradingServiceServer(
		grpcServer,
		edge.NewGRPCServer(authenticator, submission),
	)
	errorsChannel := make(chan error, 2)
	cleanupContext, cancelCleanup := context.WithCancel(ctx)
	go func() {
		if serveErr := httpServer.Serve(restListener); !errors.Is(serveErr, http.ErrServerClosed) {
			errorsChannel <- fmt.Errorf("serve REST: %w", serveErr)
		}
	}()
	go func() {
		if serveErr := grpcServer.Serve(grpcListener); serveErr != nil {
			errorsChannel <- fmt.Errorf("serve gRPC: %w", serveErr)
		}
	}()
	ticks, stopReplayCleanupSchedule := newReplayCleanupSchedule(
		time.Duration(brokerEchoPolicy.CleanupIntervalSeconds) * time.Second,
	)
	cleanupResult := make(chan error, 1)
	go func() {
		defer stopReplayCleanupSchedule()
		cleanupErr := runReplayCleanup(
			cleanupContext,
			ticks,
			purgeExpiredAPIKeyReplays,
			purgeExpiredBrokerEchoReplays,
			func(coverageContext context.Context) error {
				return verifyReplayCoverage(coverageContext, true, false)
			},
		)
		cleanupResult <- cleanupErr
	}()

	var resultErr error
	cleanupStopped := false
	select {
	case <-ctx.Done():
	case serveErr := <-errorsChannel:
		resultErr = serveErr
	case cleanupErr := <-cleanupResult:
		cleanupStopped = true
		if cleanupErr != nil {
			resultErr = cleanupErr
		} else if ctx.Err() == nil {
			resultErr = errors.New("serve: replay cleanup owner stopped")
		}
	}
	cancelCleanup()
	shutdownServers(ctx, httpServer, grpcServer)
	if !cleanupStopped {
		cleanupErr := <-cleanupResult
		if cleanupErr != nil && resultErr == nil && ctx.Err() == nil {
			resultErr = cleanupErr
		}
	}
	return resultErr
}

func runReplayCleanup(
	ctx context.Context,
	ticks <-chan time.Time,
	purgeAPIKeyReplays func(context.Context) (int64, error),
	purgeBrokerEchoReplays func(context.Context) (int64, error),
	verifyCoverage func(context.Context) error,
) error {
	for {
		select {
		case <-ctx.Done():
			return nil
		case _, ok := <-ticks:
			if !ok {
				return nil
			}
			if err := runReplayCleanupBatch(
				ctx,
				purgeAPIKeyReplays,
				purgeBrokerEchoReplays,
				verifyCoverage,
			); err != nil {
				return err
			}
		}
	}
}

func runReplayCleanupBatch(
	ctx context.Context,
	purgeAPIKeyReplays func(context.Context) (int64, error),
	purgeBrokerEchoReplays func(context.Context) (int64, error),
	verifyCoverage func(context.Context) error,
) error {
	apiKeyDeleted, err := purgeAPIKeyReplays(ctx)
	if err != nil {
		return fmt.Errorf("API-key replay cleanup: %w", err)
	}
	slog.Info(
		"expired API-key replay cleanup completed",
		"deleted", apiKeyDeleted,
	)
	brokerEchoDeleted, err := purgeBrokerEchoReplays(ctx)
	if err != nil {
		return fmt.Errorf("broker-echo replay cleanup: %w", err)
	}
	slog.Info(
		"expired broker-echo replay cleanup completed",
		"deleted", brokerEchoDeleted,
	)
	if err := verifyCoverage(ctx); err != nil {
		return fmt.Errorf("replay coverage: %w", err)
	}
	return nil
}

func drainExpiredBrokerEchoReplays(
	ctx context.Context,
	purge func(context.Context, int) (int64, error),
	batchSize int,
	maxBatches int,
) (int64, error) {
	if batchSize < 1 || maxBatches < 1 {
		return 0, errors.New("invalid broker-echo replay cleanup policy")
	}
	var totalDeleted int64
	for batch := 1; batch <= maxBatches; batch++ {
		if err := ctx.Err(); err != nil {
			return totalDeleted, err
		}
		deleted, err := purge(ctx, batchSize)
		if err != nil {
			return totalDeleted, err
		}
		if deleted < 0 || deleted > int64(batchSize) {
			return totalDeleted, fmt.Errorf(
				"broker-echo replay purge returned invalid count %d",
				deleted,
			)
		}
		totalDeleted += deleted
		slog.Info(
			"expired broker-echo replay cleanup batch completed",
			"batch", batch,
			"deleted", deleted,
			"total_deleted", totalDeleted,
		)
		if deleted < int64(batchSize) {
			break
		}
	}
	return totalDeleted, nil
}

func validateBrokerEchoReplayCoverage(
	coverage platformpostgres.BrokerEchoReplayCoverage,
	requireDrained bool,
	allowStaleExpired bool,
) error {
	if coverage.MaxTotalRows < 1 ||
		coverage.MaxRowsPerPrincipal < 1 ||
		coverage.PurgeBatchSize < 1 ||
		coverage.MaxBatchesPerCycle < 1 ||
		coverage.CleanupIntervalSeconds < 1 ||
		coverage.CleanupCycleTimeoutSeconds < 1 ||
		coverage.ExpiredReadinessSLOSeconds < 1 ||
		coverage.MaxRetryAfterSeconds < 1 ||
		int64(coverage.PurgeBatchSize)*
			int64(coverage.MaxBatchesPerCycle) <
			int64(coverage.MaxTotalRows) ||
		coverage.MaxRowsPerPrincipal > coverage.MaxTotalRows {
		return errors.New("invalid broker-echo replay policy")
	}
	if coverage.TotalRows < 0 ||
		coverage.LiveRows < 0 ||
		coverage.InvalidLiveRows < 0 ||
		coverage.OverlongLiveRows < 0 ||
		coverage.ExpiredRows < 0 ||
		coverage.MaximumPrincipalRows < 0 ||
		coverage.TotalRows != coverage.LiveRows+coverage.ExpiredRows ||
		coverage.InvalidLiveRows > coverage.LiveRows ||
		coverage.OverlongLiveRows > coverage.LiveRows ||
		coverage.TotalRows > int64(coverage.MaxTotalRows) ||
		coverage.MaximumPrincipalRows >
			int64(coverage.MaxRowsPerPrincipal) ||
		coverage.MaximumPrincipalRows > coverage.TotalRows {
		return errors.New("invalid broker-echo replay coverage")
	}
	if coverage.InvalidLiveRows != 0 {
		return fmt.Errorf(
			"broker-echo replay authority contains %d invalid live responses",
			coverage.InvalidLiveRows,
		)
	}
	if coverage.OverlongLiveRows != 0 {
		return fmt.Errorf(
			"broker-echo replay authority contains %d live responses beyond the maximum remaining lifetime",
			coverage.OverlongLiveRows,
		)
	}
	if coverage.ExpiredRows == 0 {
		if coverage.OldestExpiredAt != "" ||
			coverage.OldestExpiredAgeSeconds != 0 {
			return errors.New("inconsistent empty broker-echo expired coverage")
		}
	} else {
		if coverage.OldestExpiredAt == "" ||
			coverage.OldestExpiredAgeSeconds < 0 {
			return errors.New("inconsistent broker-echo expired coverage")
		}
		if requireDrained {
			return fmt.Errorf(
				"broker-echo startup cleanup left %d expired rows",
				coverage.ExpiredRows,
			)
		}
		if !allowStaleExpired &&
			coverage.OldestExpiredAgeSeconds >
				int64(coverage.ExpiredReadinessSLOSeconds) {
			return fmt.Errorf(
				"broker-echo expired backlog age %d exceeds readiness SLO %d",
				coverage.OldestExpiredAgeSeconds,
				coverage.ExpiredReadinessSLOSeconds,
			)
		}
	}
	if coverage.LiveRows == 0 && coverage.OldestLiveExpiresAt != "" {
		return errors.New("inconsistent empty broker-echo live coverage")
	}
	if coverage.LiveRows > 0 && coverage.OldestLiveExpiresAt == "" {
		return errors.New("inconsistent broker-echo live coverage")
	}
	return nil
}

func applicationReplayKeys(
	configured []APIKeyReplayKey,
) []application.APIKeyReplayKey {
	keys := make([]application.APIKeyReplayKey, len(configured))
	for index, configuredKey := range configured {
		keys[index] = application.APIKeyReplayKey{
			ID:  configuredKey.ID,
			Key: configuredKey.Key,
		}
	}
	return keys
}

// RunWorkers runs each requested compatible handler until cancellation.
func RunWorkers(
	ctx context.Context,
	config Config,
	handlerNames []string,
) error {
	handlers := normalizeHandlers(handlerNames)
	if len(handlers) == 0 {
		return errors.New("worker: at least one --handlers value is required")
	}
	for _, name := range handlers {
		if name != "outbox-publisher" &&
			name != "realtime-publisher" &&
			name != "event-consumer" &&
			!strings.HasPrefix(name, "event-consumer:") {
			return fmt.Errorf("worker: unsupported handler %q", name)
		}
	}
	databaseRole, err := databaseRoleForHandlers(handlers)
	if err != nil {
		return err
	}
	needsNATS := workerNeedsNATS(handlers)
	if needsNATS {
		if validationErr := config.ValidateFor("worker"); validationErr != nil {
			return validationErr
		}
	} else if validationErr := validateRealtimeWorkerConfig(config); validationErr != nil {
		return validationErr
	}
	pool, err := openPostgres(ctx, config.DatabaseURL, databaseRole)
	if err != nil {
		return err
	}
	defer pool.Close()
	var (
		natsConnection *gonats.Conn
		js             jetstream.JetStream
	)
	if needsNATS {
		natsConnection, js, err = openNATS(config.NATSURL)
		if err != nil {
			return err
		}
		defer natsConnection.Close()
		if streamsErr := ensureStreams(
			ctx,
			js,
			config.ShardID,
			config.NATSStreamLimits,
		); streamsErr != nil {
			return streamsErr
		}
	}

	// Parent cancellation initiates draining, but it does not reach handler
	// contexts until ready leases have been synchronously withdrawn.
	workerContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	defer cancel()
	errorsChannel := make(chan error, len(handlers))
	prepared := make([]preparedWorker, 0, len(handlers))
	for _, name := range handlers {
		var worker preparedWorker
		switch {
		case name == "outbox-publisher":
			worker, err = prepareOutboxWorker(ctx, pool, js)
		case name == "realtime-publisher":
			worker, err = prepareRealtimeWorker(ctx, pool, config)
		case name == "event-consumer" || strings.HasPrefix(name, "event-consumer:"):
			worker, err = prepareEngineWorker(
				ctx,
				pool,
				js,
				config.ShardID,
			)
		}
		if err != nil {
			closePreparedWorkers(ctx, prepared)
			return err
		}
		prepared = append(prepared, worker)
	}
	healthListener, err := (&net.ListenConfig{}).Listen(
		ctx,
		"tcp",
		config.HealthAddress,
	)
	if err != nil {
		closePreparedWorkers(ctx, prepared)
		return fmt.Errorf("worker health listen: %w", err)
	}
	var workerReady atomic.Bool
	readinessChecks := make([]func(context.Context) error, 0, len(prepared))
	for index := range prepared {
		if prepared[index].check != nil {
			readinessChecks = append(readinessChecks, prepared[index].check)
		}
	}
	healthServer := &http.Server{
		Handler: workerHealthHandler(
			&workerReady,
			pool,
			natsConnection,
			readinessChecks,
		),
		ReadHeaderTimeout: 5 * time.Second,
	}
	healthResult := make(chan error, 1)
	go func() {
		serveErr := healthServer.Serve(healthListener)
		if errors.Is(serveErr, http.ErrServerClosed) {
			serveErr = nil
		}
		healthResult <- serveErr
	}()
	for _, worker := range prepared {
		run := worker.run
		go func() {
			errorsChannel <- run(workerContext)
		}()
	}
	workerReady.Store(true)
	var result error
	started := len(prepared)
	healthStopped := false
	select {
	case <-ctx.Done():
	case workerErr := <-errorsChannel:
		started--
		if workerErr != nil {
			result = workerErr
		} else if ctx.Err() == nil {
			result = errors.New("worker: handler exited unexpectedly")
		}
	case healthErr := <-healthResult:
		healthStopped = true
		if healthErr != nil {
			result = fmt.Errorf("worker health server: %w", healthErr)
		} else if ctx.Err() == nil {
			result = errors.New("worker: health server exited unexpectedly")
		}
	}
	workerReady.Store(false)
	for index := range prepared {
		if prepared[index].readiness == nil {
			continue
		}
		if readyErr := prepared[index].readiness.Close(
			context.WithoutCancel(ctx),
		); readyErr != nil && result == nil {
			result = readyErr
		}
	}
	cancel()
	for started > 0 {
		workerErr := <-errorsChannel
		started--
		if workerErr != nil && result == nil && ctx.Err() == nil {
			result = workerErr
		}
	}
	closePreparedWorkers(ctx, prepared)
	shutdownContext, shutdownCancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		10*time.Second,
	)
	if shutdownErr := healthServer.Shutdown(shutdownContext); shutdownErr != nil &&
		result == nil {
		result = fmt.Errorf("shutdown worker health server: %w", shutdownErr)
	}
	shutdownCancel()
	if !healthStopped {
		if healthErr := <-healthResult; healthErr != nil && result == nil {
			result = fmt.Errorf("worker health server: %w", healthErr)
		}
	}
	return result
}

func workerHealthHandler(
	ready *atomic.Bool,
	pool *pgxpool.Pool,
	natsConnection *gonats.Conn,
	checks []func(context.Context) error,
) http.Handler {
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/healthz":
			writer.Header().Set("content-type", "text/plain; charset=utf-8")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("ok"))
		case "/readyz":
			if !ready.Load() ||
				pool.Ping(request.Context()) != nil ||
				(natsConnection != nil &&
					flushNATS(request.Context(), natsConnection) != nil) {
				http.Error(writer, "not ready", http.StatusServiceUnavailable)
				return
			}
			for _, check := range checks {
				if check(request.Context()) != nil {
					http.Error(writer, "not ready", http.StatusServiceUnavailable)
					return
				}
			}
			writer.Header().Set("content-type", "text/plain; charset=utf-8")
			writer.WriteHeader(http.StatusOK)
			_, _ = writer.Write([]byte("ok"))
		default:
			http.NotFound(writer, request)
		}
	})
}

func workerNeedsNATS(handlers []string) bool {
	for _, handler := range handlers {
		if handler != "realtime-publisher" {
			return true
		}
	}
	return false
}

func validateRealtimeWorkerConfig(config Config) error {
	missing := make([]string, 0, 4)
	if config.DatabaseURL == "" {
		missing = append(missing, "UZO_DATABASE_URL")
	}
	if config.HealthAddress == "" {
		missing = append(missing, "UZO_HTTP_HEALTH_ADDR")
	}
	if config.CentrifugoAPIURL == "" {
		missing = append(missing, "UZO_REALTIME_API_URL")
	}
	if config.CentrifugoAPIKey == "" {
		missing = append(missing, "UZO_REALTIME_API_KEY")
	}
	if len(missing) != 0 {
		return fmt.Errorf(
			"missing required environment keys: %s",
			strings.Join(missing, ", "),
		)
	}
	return nil
}

type preparedWorker struct {
	run       func(context.Context) error
	readiness *platformpostgres.RoleLease
	close     func(context.Context) error
	check     func(context.Context) error
}

func prepareOutboxWorker(
	ctx context.Context,
	pool *pgxpool.Pool,
	js jetstream.JetStream,
) (preparedWorker, error) {
	store := platformpostgres.NewMessagingStore(pool)
	ownership, err := store.AcquireOutboxPublisher(ctx)
	if err != nil {
		return preparedWorker{}, err
	}
	readiness, err := store.AcquireOutboxReady(ctx)
	if err != nil {
		_ = ownership.Close(context.WithoutCancel(ctx))
		return preparedWorker{}, err
	}
	publisher := platformnats.NewPublisher(js)
	return preparedWorker{
		run: func(runContext context.Context) error {
			return runOutbox(runContext, store, publisher)
		},
		readiness: readiness,
		close:     ownership.Close,
	}, nil
}

func runOutbox(
	ctx context.Context,
	store *platformpostgres.MessagingStore,
	publisher *platformnats.Publisher,
) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case now := <-timer.C:
			operationContext, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				outboxLease,
			)
			_, publishErr := store.PublishOutboxBatch(
				operationContext,
				publisher,
				now.UTC(),
				outboxBatchSize,
				outboxLease,
				outboxRetry,
			)
			cancel()
			if publishErr != nil && ctx.Err() == nil {
				slog.Error("outbox batch failed", "error", publishErr)
			}
			timer.Reset(100 * time.Millisecond)
		}
	}
}

func prepareRealtimeWorker(
	_ context.Context,
	pool *pgxpool.Pool,
	config Config,
) (preparedWorker, error) {
	if config.CentrifugoAPIURL == "" {
		return preparedWorker{}, errors.New(
			"realtime publisher: UZO_REALTIME_API_URL is required",
		)
	}
	if config.CentrifugoAPIKey == "" {
		return preparedWorker{}, errors.New(
			"realtime publisher: UZO_REALTIME_API_KEY is required",
		)
	}
	gateway, err := centrifugo.NewPublisher(centrifugo.Config{
		APIURL: config.CentrifugoAPIURL, APIKey: config.CentrifugoAPIKey,
	})
	if err != nil {
		return preparedWorker{}, err
	}
	store := platformpostgres.NewRealtimeStore(pool)
	return preparedWorker{
		run: func(runContext context.Context) error {
			return runRealtimePublisher(runContext, store, gateway)
		},
		close: func(context.Context) error { return nil },
		check: func(checkContext context.Context) error {
			if err := gateway.Healthy(checkContext); err != nil {
				return err
			}
			return store.Ready(checkContext)
		},
	}, nil
}

type realtimeFinalizationError struct {
	publication platformpostgres.RealtimePublication
	err         error
}

func (failure *realtimeFinalizationError) Error() string {
	return failure.err.Error()
}

func (failure *realtimeFinalizationError) Unwrap() error {
	return failure.err
}

func runRealtimePublisher(
	ctx context.Context,
	store *platformpostgres.RealtimeStore,
	gateway *centrifugo.Gateway,
) error {
	timer := time.NewTimer(0)
	defer timer.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-timer.C:
			operationContext, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				realtimeLease,
			)
			publishErr := publishRealtimeBatch(
				operationContext,
				store,
				gateway,
			)
			cancel()
			var finalizationErr *realtimeFinalizationError
			if errors.As(publishErr, &finalizationErr) {
				return publishErr
			}
			if publishErr != nil && ctx.Err() == nil {
				slog.Error("realtime batch failed", "error", publishErr)
			}
			timer.Reset(100 * time.Millisecond)
		}
	}
}

func publishRealtimeBatch(
	ctx context.Context,
	store *platformpostgres.RealtimeStore,
	gateway *centrifugo.Gateway,
) error {
	publications, err := store.ClaimRealtimeBatch(
		ctx,
		realtimeBatchSize,
		realtimeLease,
	)
	if err != nil {
		return err
	}
	return publishRealtimePublications(ctx, store, gateway, publications)
}

func publishRealtimePublications(
	ctx context.Context,
	store *platformpostgres.RealtimeStore,
	gateway *centrifugo.Gateway,
	publications []platformpostgres.RealtimePublication,
) error {
	var (
		firstErr             error
		firstFinalizationErr error
	)
	for _, publication := range publications {
		envelope := centrifugo.Envelope{
			Type: publication.EventType, AccountID: publication.AccountID,
			Timestamp: publication.Timestamp, Data: publication.Data,
			EventID: publication.EventID, SchemaVersion: publication.SchemaVersion,
			Sequence: publication.Sequence,
		}
		if publishErr := gateway.Publish(
			ctx,
			publication.Channel,
			envelope,
		); publishErr != nil {
			retryable := centrifugo.IsRetryablePublishError(publishErr)
			cycleAttempts := publication.Attempts - publication.RetryAttemptBase
			failureClass := platformpostgres.RealtimeFailureTransient
			quarantine := false
			if !retryable {
				failureClass = platformpostgres.RealtimeFailurePermanent
				quarantine = true
			} else if cycleAttempts >= realtimeMaxAttempts {
				failureClass = platformpostgres.RealtimeFailureRetryExhausted
				quarantine = true
			}
			finalizeContext, cancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				realtimeFinalizeTimeout,
			)
			markErr := store.MarkRealtimeFailed(
				finalizeContext,
				publication,
				realtimeRetryDelay(cycleAttempts),
				failureClass,
				quarantine,
				publishErr,
			)
			cancel()
			if markErr != nil && firstFinalizationErr == nil {
				firstFinalizationErr = &realtimeFinalizationError{
					publication: publication,
					err:         markErr,
				}
			}
			if firstErr == nil {
				firstErr = publishErr
			}
			continue
		}
		finalizeContext, cancel := context.WithTimeout(
			context.WithoutCancel(ctx),
			realtimeFinalizeTimeout,
		)
		markErr := store.MarkRealtimePublished(finalizeContext, publication)
		cancel()
		if markErr != nil {
			cycleAttempts := publication.Attempts - publication.RetryAttemptBase
			failureClass := platformpostgres.RealtimeFailureTransient
			quarantine := false
			if cycleAttempts >= realtimeMaxAttempts {
				failureClass = platformpostgres.RealtimeFailureRetryExhausted
				quarantine = true
			}
			ambiguousErr := fmt.Errorf(
				"centrifugo accepted publication but durable acknowledgment failed: %w",
				markErr,
			)
			fallbackContext, fallbackCancel := context.WithTimeout(
				context.WithoutCancel(ctx),
				realtimeFinalizeTimeout,
			)
			fallbackErr := store.MarkRealtimeFailed(
				fallbackContext,
				publication,
				realtimeRetryDelay(cycleAttempts),
				failureClass,
				quarantine,
				ambiguousErr,
			)
			fallbackCancel()
			if fallbackErr != nil && firstFinalizationErr == nil {
				firstFinalizationErr = &realtimeFinalizationError{
					publication: publication,
					err:         errors.Join(markErr, fallbackErr),
				}
			} else if firstErr == nil {
				firstErr = ambiguousErr
			}
		}
	}
	if firstFinalizationErr != nil {
		return firstFinalizationErr
	}
	return firstErr
}

func realtimeRetryDelay(attempts uint32) time.Duration {
	if attempts == 0 {
		return realtimeRetry
	}
	exponent := min(attempts-1, uint32(6))
	return realtimeRetry * (time.Duration(1) << exponent)
}

func prepareEngineWorker(
	ctx context.Context,
	pool *pgxpool.Pool,
	js jetstream.JetStream,
	shardID engine.ShardID,
) (preparedWorker, error) {
	engineStore := platformpostgres.NewEngineStore(pool)
	processor, err := platformnats.NewEngineProcessor(
		ctx,
		engineStore,
		shardID,
	)
	if err != nil {
		return preparedWorker{}, err
	}
	consumer, err := platformnats.NewEnginePullConsumer(
		ctx, js, shardID, fmt.Sprintf("platformgo-engine-%d", shardID), 5*time.Second,
	)
	if err != nil {
		_ = processor.Close(context.WithoutCancel(ctx))
		return preparedWorker{}, err
	}
	readiness, err := engineStore.AcquireEngineReady(ctx, shardID)
	if err != nil {
		_ = processor.Close(context.WithoutCancel(ctx))
		return preparedWorker{}, err
	}
	return preparedWorker{
		run: func(runContext context.Context) error {
			return runEngineConsumer(runContext, consumer, processor)
		},
		readiness: readiness,
		close:     processor.Close,
	}, nil
}

func runEngineConsumer(
	ctx context.Context,
	consumer *platformnats.PullConsumer,
	processor *platformnats.EngineProcessor,
) error {
	for ctx.Err() == nil {
		if _, err := consumer.ProcessOne(ctx, processor.Handle); err != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			return err
		}
	}
	return nil
}

func closePreparedWorkers(
	ctx context.Context,
	workers []preparedWorker,
) {
	for index := len(workers) - 1; index >= 0; index-- {
		if workers[index].readiness != nil {
			_ = workers[index].readiness.Close(context.WithoutCancel(ctx))
		}
		_ = workers[index].close(context.WithoutCancel(ctx))
	}
}

func ensureStreams(
	ctx context.Context,
	js jetstream.JetStream,
	shardID engine.ShardID,
	limits platformnats.StreamLimits,
) error {
	if err := platformnats.EnsureStreams(ctx, js, limits); err != nil {
		return err
	}
	return platformnats.EnsureEngineShardStream(ctx, js, shardID, limits)
}

func databaseRoleForHandlers(
	handlers []string,
) (databaseRuntimeRole, error) {
	var selected databaseRuntimeRole
	for _, handler := range handlers {
		role := databaseRoleEngine
		switch handler {
		case "outbox-publisher":
			role = databaseRoleOutbox
		case "realtime-publisher":
			role = databaseRoleRealtime
		}
		if selected != "" && selected != role {
			return "", errors.New(
				"worker: handlers requiring different PostgreSQL roles " +
					"must run in separate processes",
			)
		}
		selected = role
	}
	if selected == "" {
		return "", errors.New("worker: no PostgreSQL role for empty handler set")
	}
	return selected, nil
}

func openMigratorPostgres(
	ctx context.Context,
	databaseURL string,
) (*pgxpool.Pool, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	return pool, nil
}

func openPostgres(
	ctx context.Context,
	databaseURL string,
	expectedRole databaseRuntimeRole,
) (*pgxpool.Pool, error) {
	config, err := pgxpool.ParseConfig(databaseURL)
	if err != nil {
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	config.AfterConnect = func(
		connectionContext context.Context,
		connection *pgx.Conn,
	) error {
		return enforceRuntimeDatabaseRole(
			connectionContext,
			connection,
			expectedRole,
		)
	}
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("connect PostgreSQL: %w", err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrations.Files,
	).VerifyCurrent(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("verify PostgreSQL schema: %w", err)
	}
	return pool, nil
}

func enforceRuntimeDatabaseRole(
	ctx context.Context,
	connection *pgx.Conn,
	expected databaseRuntimeRole,
) error {
	var (
		sessionUser string
		superuser   bool
		bypassRLS   bool
		createRole  bool
		createDB    bool
		replication bool
	)
	err := connection.QueryRow(ctx, `
		SELECT
			rolname,
			rolsuper,
			rolbypassrls,
			rolcreaterole,
			rolcreatedb,
			rolreplication
		FROM pg_roles
		WHERE rolname = session_user
	`).Scan(
		&sessionUser,
		&superuser,
		&bypassRLS,
		&createRole,
		&createDB,
		&replication,
	)
	if err != nil {
		return fmt.Errorf("runtime PostgreSQL role preflight: %w", err)
	}
	if superuser || bypassRLS || createRole || createDB || replication {
		return fmt.Errorf(
			"runtime PostgreSQL role preflight: login %q has privileged attributes",
			sessionUser,
		)
	}
	var memberships []string
	err = connection.QueryRow(ctx, `
		SELECT COALESCE(
			array_agg(candidate.rolname ORDER BY candidate.rolname),
			ARRAY[]::text[]
		)
		FROM pg_roles AS candidate
		WHERE candidate.rolname <> session_user
		  AND pg_has_role(session_user, candidate.oid, 'MEMBER')
	`).Scan(&memberships)
	if err != nil {
		return fmt.Errorf("runtime PostgreSQL membership preflight: %w", err)
	}
	if len(memberships) != 1 || memberships[0] != string(expected) {
		return fmt.Errorf(
			"runtime PostgreSQL membership preflight: login %q must belong only to %q",
			sessionUser,
			expected,
		)
	}
	statement, err := setRoleStatement(expected)
	if err != nil {
		return err
	}
	if _, execErr := connection.Exec(ctx, statement); execErr != nil {
		return fmt.Errorf(
			"runtime PostgreSQL SET ROLE %q: %w",
			expected,
			execErr,
		)
	}
	var (
		currentUser        string
		currentSuperuser   bool
		currentBypassRLS   bool
		currentCreateRole  bool
		currentCreateDB    bool
		currentReplication bool
	)
	err = connection.QueryRow(ctx, `
		SELECT
			rolname,
			rolsuper,
			rolbypassrls,
			rolcreaterole,
			rolcreatedb,
			rolreplication
		FROM pg_roles
		WHERE rolname = current_user
	`).Scan(
		&currentUser,
		&currentSuperuser,
		&currentBypassRLS,
		&currentCreateRole,
		&currentCreateDB,
		&currentReplication,
	)
	if err != nil {
		return fmt.Errorf("runtime PostgreSQL active-role preflight: %w", err)
	}
	if currentUser != string(expected) ||
		currentSuperuser ||
		currentBypassRLS ||
		currentCreateRole ||
		currentCreateDB ||
		currentReplication {
		return fmt.Errorf(
			"runtime PostgreSQL active-role preflight: expected restricted role %q",
			expected,
		)
	}
	if expected == databaseRoleEngine {
		if _, err := connection.Exec(
			ctx,
			`SELECT
				set_config('platformgo.runtime_schema_revision', $1, false),
				set_config(
					'platformgo.engine_decision_hash_version',
					$2,
					false
				)`,
			runtimeSchemaRevision,
			fmt.Sprint(engine.CurrentDecisionHashVersion),
		); err != nil {
			return fmt.Errorf("runtime PostgreSQL schema revision binding: %w", err)
		}
	}
	return nil
}

func setRoleStatement(role databaseRuntimeRole) (string, error) {
	switch role {
	case databaseRoleAPI:
		return "SET ROLE platformgo_api", nil
	case databaseRoleEngine:
		return "SET ROLE platformgo_engine", nil
	case databaseRoleOutbox:
		return "SET ROLE platformgo_outbox", nil
	case databaseRoleProjector:
		return "SET ROLE platformgo_projector", nil
	case databaseRoleRealtime:
		return "SET ROLE platformgo_realtime", nil
	default:
		return "", fmt.Errorf("unsupported runtime PostgreSQL role %q", role)
	}
}

func openNATS(
	natsURL string,
) (*gonats.Conn, jetstream.JetStream, error) {
	connection, err := gonats.Connect(
		natsURL,
		gonats.Name("platformgo"),
		gonats.Timeout(5*time.Second),
		gonats.MaxReconnects(20),
		gonats.ReconnectWait(500*time.Millisecond),
	)
	if err != nil {
		return nil, nil, fmt.Errorf("connect NATS: %w", err)
	}
	js, err := jetstream.New(connection)
	if err != nil {
		connection.Close()
		return nil, nil, fmt.Errorf("connect JetStream: %w", err)
	}
	return connection, js, nil
}

func flushNATS(ctx context.Context, connection *gonats.Conn) error {
	flushContext, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return connection.FlushWithContext(flushContext)
}

func shutdownServers(
	ctx context.Context,
	httpServer *http.Server,
	grpcServer *grpc.Server,
) {
	shutdownContext, cancel := context.WithTimeout(
		context.WithoutCancel(ctx),
		10*time.Second,
	)
	defer cancel()
	_ = httpServer.Shutdown(shutdownContext)
	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()
	select {
	case <-stopped:
	case <-shutdownContext.Done():
		grpcServer.Stop()
	}
}

func normalizeHandlers(raw []string) []string {
	var handlers []string
	for _, value := range raw {
		for _, handler := range strings.Split(value, ",") {
			if handler = strings.TrimSpace(handler); handler != "" {
				handlers = append(handlers, handler)
			}
		}
	}
	return handlers
}

func doctorTokenSecret(configured []byte) []byte {
	if len(configured) >= 32 {
		return configured
	}
	return []byte("doctor-only-placeholder-secret-32b")
}
