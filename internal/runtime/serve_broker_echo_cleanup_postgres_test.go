package runtime

import (
	"bytes"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
	"github.com/upcomers-org/platformgo/migrations"
)

var serveCleanupLoginSequence atomic.Uint64

func TestServeOwnsBrokerEchoReplayCleanupOnPostgres19(t *testing.T) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	natsURL := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	centrifugoURL := os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_URL")
	centrifugoAPIKey := os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_API_KEY")
	centrifugoTokenSecret := os.Getenv(
		"PLATFORMGO_TEST_CENTRIFUGO_TOKEN_SECRET",
	)
	if databaseURL == "" {
		t.Log("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
		return
	}
	if natsURL == "" || centrifugoURL == "" || centrifugoAPIKey == "" ||
		len(centrifugoTokenSecret) < 32 {
		t.Log("real NATS and Centrifugo integration configuration is unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	if err := postgresfixture.ResetDurableSchemas(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := ensureReplayCleanupRuntimeRoles(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		migrations.Files,
	).MigrateAndProvision(ctx, 74); err != nil {
		t.Fatal(err)
	}
	apiDatabaseURL := createServeCleanupAPILogin(
		t,
		ctx,
		admin,
		databaseURL,
	)

	const (
		startupExpiredRows = 205
		liveRows           = 3
	)
	seedServeCleanupRows(
		t,
		ctx,
		admin,
		"startup",
		startupExpiredRows,
		liveRows,
	)
	liveBefore := readLiveBrokerEchoRows(t, ctx, admin)
	config := serveCleanupConfig(
		apiDatabaseURL,
		natsURL,
		centrifugoURL,
		centrifugoAPIKey,
		centrifugoTokenSecret,
	)

	t.Run("startup periodic and fatal cleanup", func(t *testing.T) {
		ticks := make(chan time.Time, 1)
		scheduleStarted := make(chan time.Duration, 1)
		scheduleStopped := make(chan struct{}, 1)
		config.RESTAddress = unusedServeCleanupAddress(t, ctx)
		config.GRPCAddress = unusedServeCleanupAddress(t, ctx)
		runContext, stopRun := context.WithCancel(ctx)
		defer stopRun()
		result := make(chan error, 1)
		go func() {
			result <- serve(
				runContext,
				config,
				func(interval time.Duration) (<-chan time.Time, func()) {
					scheduleStarted <- interval
					return ticks, func() {
						scheduleStopped <- struct{}{}
					}
				},
			)
		}()

		select {
		case interval := <-scheduleStarted:
			if interval != time.Minute {
				t.Fatalf("cleanup interval = %s, want 1m", interval)
			}
		case err := <-result:
			t.Fatalf("serve stopped before cleanup ownership: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}

		requireServeCleanupExpiredCount(t, ctx, admin, 0)
		liveAfterStartup := readLiveBrokerEchoRows(t, ctx, admin)
		if len(liveAfterStartup) != liveRows ||
			!slices.EqualFunc(
				liveAfterStartup,
				liveBefore,
				equalServeCleanupLiveRow,
			) {
			t.Fatalf(
				"startup cleanup changed live rows: before=%#v after=%#v",
				liveBefore,
				liveAfterStartup,
			)
		}
		waitForServeCleanupHealth(t, ctx, config.RESTAddress)
		waitForServeCleanupTCP(t, ctx, config.GRPCAddress)

		seedServeCleanupRows(t, ctx, admin, "periodic", 4, 0)
		ticks <- time.Unix(1, 0)
		waitForServeCleanupExpiredCount(t, ctx, admin, 0)
		liveAfterPeriodic := readLiveBrokerEchoRows(t, ctx, admin)
		if !slices.EqualFunc(
			liveAfterPeriodic,
			liveBefore,
			equalServeCleanupLiveRow,
		) {
			t.Fatalf(
				"periodic cleanup changed live rows: before=%#v after=%#v",
				liveBefore,
				liveAfterPeriodic,
			)
		}

		if _, err := admin.Exec(ctx, `
			REVOKE EXECUTE ON FUNCTION
				identity.purge_expired_broker_echo_replays(integer)
			FROM platformgo_api`); err != nil {
			t.Fatal(err)
		}
		privilegeRevoked := true
		defer func(cleanupContext context.Context) {
			if privilegeRevoked {
				_, _ = admin.Exec(cleanupContext, `
					GRANT EXECUTE ON FUNCTION
						identity.purge_expired_broker_echo_replays(integer)
					TO platformgo_api`)
			}
		}(context.WithoutCancel(ctx))
		seedServeCleanupRows(t, ctx, admin, "fatal", 1, 0)
		ticks <- time.Unix(2, 0)
		select {
		case serveErr := <-result:
			if serveErr == nil ||
				!strings.Contains(
					serveErr.Error(),
					"broker-echo replay cleanup",
				) {
				t.Fatalf("fatal cleanup serve error = %v", serveErr)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		if _, err := admin.Exec(ctx, `
			GRANT EXECUTE ON FUNCTION
				identity.purge_expired_broker_echo_replays(integer)
			TO platformgo_api`); err != nil {
			t.Fatal(err)
		}
		privilegeRevoked = false
		waitForServeCleanupListenerClosed(t, ctx, config.RESTAddress)
		waitForServeCleanupListenerClosed(t, ctx, config.GRPCAddress)
		select {
		case <-scheduleStopped:
		default:
			t.Fatal("fatal cleanup did not stop the injected schedule")
		}
	})

	t.Run("cancellation", func(t *testing.T) {
		ticks := make(chan time.Time)
		scheduleStarted := make(chan struct{}, 1)
		scheduleStopped := make(chan struct{}, 1)
		config.RESTAddress = unusedServeCleanupAddress(t, ctx)
		config.GRPCAddress = unusedServeCleanupAddress(t, ctx)
		runContext, stopRun := context.WithCancel(ctx)
		result := make(chan error, 1)
		go func() {
			result <- serve(
				runContext,
				config,
				func(time.Duration) (<-chan time.Time, func()) {
					scheduleStarted <- struct{}{}
					return ticks, func() {
						scheduleStopped <- struct{}{}
					}
				},
			)
		}()
		select {
		case <-scheduleStarted:
		case err := <-result:
			t.Fatalf("serve stopped before cancellation proof: %v", err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		waitForServeCleanupHealth(t, ctx, config.RESTAddress)
		waitForServeCleanupTCP(t, ctx, config.GRPCAddress)
		stopRun()
		select {
		case err := <-result:
			if err != nil {
				t.Fatalf("canceled serve = %v", err)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		waitForServeCleanupListenerClosed(t, ctx, config.RESTAddress)
		waitForServeCleanupListenerClosed(t, ctx, config.GRPCAddress)
		select {
		case <-scheduleStopped:
		default:
			t.Fatal("cancellation did not stop the injected schedule")
		}
	})
}

func TestServeRejectsInvalidLiveBrokerEchoReplayAuthorityOnPostgres19(
	t *testing.T,
) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	natsURL := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	centrifugoURL := os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_URL")
	centrifugoAPIKey := os.Getenv("PLATFORMGO_TEST_CENTRIFUGO_API_KEY")
	centrifugoTokenSecret := os.Getenv(
		"PLATFORMGO_TEST_CENTRIFUGO_TOKEN_SECRET",
	)
	if databaseURL == "" {
		t.Log("PLATFORMGO_TEST_POSTGRES_DSN is not configured")
		return
	}
	if natsURL == "" || centrifugoURL == "" || centrifugoAPIKey == "" ||
		len(centrifugoTokenSecret) < 32 {
		t.Log("real NATS and Centrifugo integration configuration is unavailable")
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(admin.Close)
	if err := postgresfixture.ResetDurableSchemas(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := ensureReplayCleanupRuntimeRoles(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		migrations.Files,
	).MigrateAndProvision(ctx, 74); err != nil {
		t.Fatal(err)
	}
	apiDatabaseURL := createServeCleanupAPILogin(
		t,
		ctx,
		admin,
		databaseURL,
	)
	config := serveCleanupConfig(
		apiDatabaseURL,
		natsURL,
		centrifugoURL,
		centrifugoAPIKey,
		centrifugoTokenSecret,
	)

	for _, test := range []struct {
		name             string
		body             string
		createdOffset    string
		lifetime         string
		dropConstraint   bool
		wantInvalid      int64
		wantOverlong     int64
		wantErrorSnippet string
	}{
		{
			name:             "malformed exact response",
			body:             "x",
			createdOffset:    "0 seconds",
			lifetime:         "24 hours",
			dropConstraint:   true,
			wantInvalid:      1,
			wantErrorSnippet: "invalid live responses",
		},
		{
			name:             "remaining lifetime exceeds policy",
			body:             `{"id":"019fa562-2c4f-4b7e-8db3-990000000002"}` + "\n",
			createdOffset:    "1 hour",
			lifetime:         "24 hours",
			wantOverlong:     1,
			wantErrorSnippet: "beyond the maximum remaining lifetime",
		},
	} {
		t.Run(test.name, func(t *testing.T) {
			resetServeCoverageIntegritySchema(t, ctx, admin)
			if test.dropConstraint {
				if _, err := admin.Exec(ctx, `
					ALTER TABLE identity.broker_echo_replays
					DROP CONSTRAINT
						broker_echo_replays_have_valid_exact_response`); err != nil {
					t.Fatal(err)
				}
			}
			seedServeCoverageIntegrityRow(
				t,
				ctx,
				admin,
				test.name,
				test.body,
				test.createdOffset,
				test.lifetime,
			)

			apiPool, err := pgxpool.New(ctx, apiDatabaseURL)
			if err != nil {
				t.Fatal(err)
			}
			coverage, err := platformpostgres.NewCompatibilityStore(
				apiPool,
			).BrokerEchoReplayCoverage(ctx)
			apiPool.Close()
			if err != nil {
				t.Fatal(err)
			}
			if coverage.InvalidLiveRows != test.wantInvalid ||
				coverage.OverlongLiveRows != test.wantOverlong {
				t.Fatalf(
					"integrity coverage invalid=%d overlong=%d, want %d/%d",
					coverage.InvalidLiveRows,
					coverage.OverlongLiveRows,
					test.wantInvalid,
					test.wantOverlong,
				)
			}

			config.RESTAddress = unusedServeCleanupAddress(t, ctx)
			config.GRPCAddress = unusedServeCleanupAddress(t, ctx)
			runContext, stopRun := context.WithCancel(ctx)
			scheduleStarted := make(chan struct{}, 1)
			result := make(chan error, 1)
			go func() {
				result <- serve(
					runContext,
					config,
					func(time.Duration) (<-chan time.Time, func()) {
						scheduleStarted <- struct{}{}
						return make(chan time.Time), func() {}
					},
				)
			}()
			select {
			case serveErr := <-result:
				stopRun()
				if serveErr == nil ||
					!strings.Contains(
						serveErr.Error(),
						test.wantErrorSnippet,
					) {
					t.Fatalf("serve integrity error = %v", serveErr)
				}
			case <-scheduleStarted:
				stopRun()
				<-result
				t.Fatal("invalid replay authority started cleanup ownership")
			case <-ctx.Done():
				stopRun()
				t.Fatal(ctx.Err())
			}
			select {
			case <-scheduleStarted:
				t.Fatal("invalid replay authority created cleanup schedule")
			default:
			}
			waitForServeCleanupListenerClosed(t, ctx, config.RESTAddress)
			waitForServeCleanupListenerClosed(t, ctx, config.GRPCAddress)
		})
	}

	t.Run("valid exact response starts without mutation", func(t *testing.T) {
		resetServeCoverageIntegritySchema(t, ctx, admin)
		seedServeCoverageIntegrityRow(
			t,
			ctx,
			admin,
			"valid",
			`{"id":"019fa562-2c4f-4b7e-8db3-990000000003"}`+"\n",
			"0 seconds",
			"24 hours",
		)
		before := readLiveBrokerEchoRows(t, ctx, admin)
		if len(before) != 1 {
			t.Fatalf("valid replay rows before serve = %d, want 1", len(before))
		}

		config.RESTAddress = unusedServeCleanupAddress(t, ctx)
		config.GRPCAddress = unusedServeCleanupAddress(t, ctx)
		runContext, stopRun := context.WithCancel(ctx)
		scheduleStarted := make(chan struct{}, 1)
		result := make(chan error, 1)
		go func() {
			result <- serve(
				runContext,
				config,
				func(time.Duration) (<-chan time.Time, func()) {
					scheduleStarted <- struct{}{}
					return make(chan time.Time), func() {}
				},
			)
		}()
		select {
		case <-scheduleStarted:
		case serveErr := <-result:
			t.Fatalf("valid replay authority failed startup: %v", serveErr)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
		waitForServeCleanupHealth(t, ctx, config.RESTAddress)
		waitForServeCleanupTCP(t, ctx, config.GRPCAddress)
		after := readLiveBrokerEchoRows(t, ctx, admin)
		if !slices.EqualFunc(before, after, equalServeCleanupLiveRow) {
			t.Fatalf(
				"valid replay changed during startup: before=%#v after=%#v",
				before,
				after,
			)
		}
		stopRun()
		select {
		case serveErr := <-result:
			if serveErr != nil {
				t.Fatalf("valid replay cancellation = %v", serveErr)
			}
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	})
}

func resetServeCoverageIntegritySchema(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
) {
	t.Helper()
	if err := postgresfixture.ResetDurableSchemas(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := ensureReplayCleanupRuntimeRoles(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		migrations.Files,
	).MigrateAndProvision(ctx, 74); err != nil {
		t.Fatal(err)
	}
}

func seedServeCoverageIntegrityRow(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
	label string,
	body string,
	createdOffset string,
	lifetime string,
) {
	t.Helper()
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		) VALUES (
			'broker-echo' || chr(31) ||
				'urn:xb:apikey:coverage-integrity',
			sha256(convert_to($1, 'UTF8')),
			sha256(convert_to($1 || '-request', 'UTF8')),
			200,
			'{"Content-Type":["application/json"]}'::jsonb,
			convert_to($2, 'UTF8'),
			statement_timestamp() + $3::interval,
			statement_timestamp() + $3::interval + $4::interval
		)`,
		label,
		body,
		createdOffset,
		lifetime,
	); err != nil {
		t.Fatal(err)
	}
}

func serveCleanupConfig(
	databaseURL string,
	natsURL string,
	centrifugoURL string,
	centrifugoAPIKey string,
	centrifugoTokenSecret string,
) Config {
	return Config{
		DatabaseURL: databaseURL,
		NATSURL:     natsURL,
		NATSStreamLimits: platformnats.StreamLimits{
			Replicas:        1,
			MaxMessages:     1_000_000,
			MaxBytes:        2 << 30,
			MaxMessageBytes: 1 << 20,
			MaxAge:          30 * 24 * time.Hour,
			DuplicateWindow: 24 * time.Hour,
		},
		AllowedOrigin:     "*",
		ClientTokenSecret: []byte("serve-cleanup-client-token-secret-32"),
		APIKeyReplayKeys: []APIKeyReplayKey{{
			ID: "serve-cleanup-v1",
			Key: [32]byte{
				1, 2, 3, 4, 5, 6, 7, 8,
			},
		}},
		APIKeyReplayActiveID:  "serve-cleanup-v1",
		BrokerCredentials:     []edge.BrokerCredential{},
		CentrifugoAPIURL:      centrifugoURL,
		CentrifugoAPIKey:      centrifugoAPIKey,
		CentrifugoTokenSecret: []byte(centrifugoTokenSecret),
		CentrifugoTokenTTL:    time.Hour,
		ShardID:               engine.ShardID(74),
	}
}

func seedServeCleanupRows(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
	label string,
	expiredRows int,
	liveRows int,
) {
	t.Helper()
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		)
		SELECT
			'broker-echo' || chr(31) ||
				'urn:xb:apikey:serve-cleanup-' || $1 || '-' || item,
			sha256(convert_to($1 || '-key-' || item, 'UTF8')),
			sha256(convert_to($1 || '-request-' || item, 'UTF8')),
			200,
			'{"Content-Type":["application/json"]}'::jsonb,
			convert_to(
				'{"id":"019fa562-2c4f-4b7e-8db3-' ||
				lpad(($2 + item)::text, 12, '0') || E'"}\n',
				'UTF8'
			),
			statement_timestamp() - interval '48 hours',
			statement_timestamp() - interval '24 hours'
		  FROM generate_series(1, $3::integer) AS item`,
		label,
		len(label)*100_000,
		expiredRows,
	); err != nil {
		t.Fatal(err)
	}
	if _, err := admin.Exec(ctx, `
		INSERT INTO identity.broker_echo_replays (
			scope,
			idempotency_key_hash,
			request_hash,
			response_status,
			response_headers,
			response_body,
			created_at,
			expires_at
		)
		SELECT
			'broker-echo' || chr(31) ||
				'urn:xb:apikey:serve-cleanup-live-' || item,
			sha256(convert_to('live-key-' || item, 'UTF8')),
			sha256(convert_to('live-request-' || item, 'UTF8')),
			200,
			jsonb_build_object(
				'Content-Type',
				jsonb_build_array('application/json'),
				'X-Live',
				jsonb_build_array(item::text)
			),
			convert_to(
				'{"id":"019fa562-2c4f-4b7e-8db3-' ||
				lpad((900000 + item)::text, 12, '0') || E'"}\n',
				'UTF8'
			),
			statement_timestamp(),
			statement_timestamp() + interval '24 hours'
		  FROM generate_series(1, $1::integer) AS item`,
		liveRows,
	); err != nil {
		t.Fatal(err)
	}
}

func equalServeCleanupLiveRow(left, right brokerEchoLiveRow) bool {
	return left.scope == right.scope &&
		bytes.Equal(left.keyHash, right.keyHash) &&
		bytes.Equal(left.requestHash, right.requestHash) &&
		left.status == right.status &&
		left.headers == right.headers &&
		bytes.Equal(left.body, right.body) &&
		left.createdAt.Equal(right.createdAt) &&
		left.expiresAt.Equal(right.expiresAt)
}

func requireServeCleanupExpiredCount(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
	want int,
) {
	t.Helper()
	var count int
	if err := admin.QueryRow(ctx, `
		SELECT count(*)
		  FROM identity.broker_echo_replays
		 WHERE expires_at <= statement_timestamp()`).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("expired broker-echo rows = %d, want %d", count, want)
	}
}

func waitForServeCleanupExpiredCount(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
	want int,
) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var count int
		err := admin.QueryRow(ctx, `
			SELECT count(*)
			  FROM identity.broker_echo_replays
			 WHERE expires_at <= statement_timestamp()`).Scan(&count)
		if err == nil && count == want {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf(
				"wait for expired broker-echo rows = %d: count=%d err=%v: %v",
				want,
				count,
				err,
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}

func waitForServeCleanupHealth(t *testing.T, ctx context.Context, address string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	url := "http://" + address + "/healthz"
	for {
		// #nosec G704 -- address is an in-process loopback listener.
		request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			t.Fatal(err)
		}
		// #nosec G704 -- request targets the loopback listener above.
		response, err := http.DefaultClient.Do(request)
		if err == nil {
			_ = response.Body.Close()
			if response.StatusCode == http.StatusOK {
				return
			}
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for REST health: %v", ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForServeCleanupTCP(t *testing.T, ctx context.Context, address string) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := (&net.Dialer{}).DialContext(ctx, "tcp", address)
		if err == nil {
			_ = connection.Close()
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for listener %s: %v", address, ctx.Err())
		case <-ticker.C:
		}
	}
}

func waitForServeCleanupListenerClosed(
	t *testing.T,
	ctx context.Context,
	address string,
) {
	t.Helper()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := (&net.Dialer{
			Timeout: 50 * time.Millisecond,
		}).DialContext(ctx, "tcp", address)
		if err != nil {
			return
		}
		_ = connection.Close()
		select {
		case <-ctx.Done():
			t.Fatalf("listener %s remained open: %v", address, ctx.Err())
		case <-ticker.C:
		}
	}
}

func unusedServeCleanupAddress(t *testing.T, ctx context.Context) string {
	t.Helper()
	listener, err := (&net.ListenConfig{}).Listen(
		ctx,
		"tcp",
		"127.0.0.1:0",
	)
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	if err := listener.Close(); err != nil {
		t.Fatal(err)
	}
	return address
}

func createServeCleanupAPILogin(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
	databaseURL string,
) string {
	t.Helper()
	name := fmt.Sprintf(
		"platformgo_test_serve_cleanup_%d_%d",
		os.Getpid(),
		serveCleanupLoginSequence.Add(1),
	)
	password := "platformgo-serve-cleanup-test-password"
	if _, err := admin.Exec(
		ctx,
		"CREATE ROLE "+pgx.Identifier{name}.Sanitize()+
			" LOGIN PASSWORD '"+password+"'"+
			" NOSUPERUSER NOCREATEDB NOCREATEROLE"+
			" NOREPLICATION NOBYPASSRLS IN ROLE platformgo_api",
	); err != nil {
		t.Fatal(err)
	}
	cleanupContext := context.WithoutCancel(ctx)
	t.Cleanup(func() {
		_, err := admin.Exec(
			cleanupContext,
			"DROP ROLE IF EXISTS "+pgx.Identifier{name}.Sanitize(),
		)
		if err != nil {
			t.Errorf("drop serve cleanup runtime login: %v", err)
		}
	})
	parsedURL, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	parsedURL.User = url.UserPassword(name, password)
	return parsedURL.String()
}
