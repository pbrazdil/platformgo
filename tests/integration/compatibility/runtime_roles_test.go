package compatibility_test

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	platformruntime "github.com/upcomers-org/platformgo/internal/runtime"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
	"github.com/upcomers-org/platformgo/migrations"
)

var runtimeLoginSequence atomic.Uint64

func TestRuntimeCompositionRejectsPrivilegedAndWrongDatabaseLogins(
	t *testing.T,
) {
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if databaseURL == "" {
		t.Skip("PostgreSQL integration DSN is not configured")
	}
	ctx := context.Background()
	admin, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer admin.Close()
	if err := resetCompatibilityDatabase(ctx, admin); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		admin,
		migrations.Files,
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}

	apiURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
	)
	engineURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_engine",
	)
	multiURL := provisionRuntimeLogin(
		t,
		ctx,
		admin,
		databaseURL,
		"platformgo_api",
		"platformgo_engine",
	)
	unprivilegedURL := provisionRuntimeLogin(t, ctx, admin, databaseURL)
	config := platformruntime.Config{
		NATSURL:          "nats://127.0.0.1:1",
		NATSStreamLimits: runtimeTestStreamLimits(),
		RESTAddress:      unusedAddress(t),
		GRPCAddress:      unusedAddress(t),
		ClientTokenSecret: []byte(
			"0123456789abcdef0123456789abcdef",
		),
		APIKeyReplayKeys: []platformruntime.APIKeyReplayKey{{
			ID: "test-v1",
			Key: [32]byte{
				1, 2, 3, 4, 5, 6, 7, 8,
			},
		}},
		CentrifugoAPIURL: "http://127.0.0.1:1",
		CentrifugoTokenSecret: []byte(
			"abcdef0123456789abcdef0123456789",
		),
		CentrifugoTokenTTL: time.Hour,
	}
	for name, candidateURL := range map[string]string{
		"superuser":       databaseURL,
		"engine role":     engineURL,
		"multiple roles":  multiURL,
		"no runtime role": unprivilegedURL,
	} {
		t.Run(name, func(t *testing.T) {
			config.DatabaseURL = candidateURL
			err := platformruntime.Serve(ctx, config)
			if err == nil ||
				!strings.Contains(err.Error(), "PostgreSQL") {
				t.Fatalf("serve error = %v", err)
			}
		})
	}

	config.DatabaseURL = apiURL
	config.HealthAddress = unusedAddress(t)
	if err := platformruntime.RunWorkers(
		ctx,
		config,
		[]string{"outbox-publisher"},
	); err == nil || !strings.Contains(err.Error(), "must belong only") {
		t.Fatalf("outbox under API login error = %v", err)
	}
}

func resetCompatibilityDatabase(
	ctx context.Context,
	admin *pgxpool.Pool,
) error {
	if err := postgresfixture.ResetDurableSchemas(ctx, admin); err != nil {
		return err
	}
	if _, err := admin.Exec(ctx, `
		DO $$
		BEGIN
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_api'
			) THEN
				CREATE ROLE platformgo_api NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_engine'
			) THEN
				CREATE ROLE platformgo_engine NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_outbox'
			) THEN
				CREATE ROLE platformgo_outbox NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_projector'
			) THEN
				CREATE ROLE platformgo_projector NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_realtime'
			) THEN
				CREATE ROLE platformgo_realtime NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles
				 WHERE rolname = 'platformgo_realtime_repair'
			) THEN
				CREATE ROLE platformgo_realtime_repair NOLOGIN;
			END IF;
			IF NOT EXISTS (
				SELECT 1 FROM pg_roles
				 WHERE rolname = 'platformgo_admin_bootstrap'
			) THEN
				CREATE ROLE platformgo_admin_bootstrap NOLOGIN;
			END IF;
		END;
		$$`); err != nil {
		return fmt.Errorf("provision test runtime roles: %w", err)
	}
	return nil
}

func provisionRuntimeLogin(
	t *testing.T,
	ctx context.Context,
	admin *pgxpool.Pool,
	databaseURL string,
	memberships ...string,
) string {
	t.Helper()
	for _, membership := range memberships {
		switch membership {
		case "platformgo_api", "platformgo_engine", "platformgo_outbox",
			"platformgo_projector", "platformgo_realtime",
			"platformgo_realtime_repair":
		default:
			t.Fatalf("unsupported test membership %q", membership)
		}
	}
	name := fmt.Sprintf(
		"platformgo_test_runtime_%d_%d",
		os.Getpid(),
		runtimeLoginSequence.Add(1),
	)
	statement := "CREATE ROLE " + pgx.Identifier{name}.Sanitize() +
		" LOGIN PASSWORD 'platformgo-runtime-test-password'" +
		" NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"
	if len(memberships) > 0 {
		statement += " IN ROLE " + strings.Join(memberships, ", ")
	}
	if _, err := admin.Exec(ctx, statement); err != nil {
		t.Fatalf("create runtime login: %v", err)
	}
	t.Cleanup(func() {
		cleanupPool, err := pgxpool.New(context.Background(), databaseURL)
		if err != nil {
			t.Errorf("connect to drop runtime login: %v", err)
			return
		}
		defer cleanupPool.Close()
		if _, err := cleanupPool.Exec(
			context.Background(),
			"DROP ROLE IF EXISTS "+pgx.Identifier{name}.Sanitize(),
		); err != nil {
			t.Errorf("drop runtime login: %v", err)
		}
	})
	parsed, err := url.Parse(databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	if parsed.Scheme != "postgres" && parsed.Scheme != "postgresql" {
		t.Fatalf("runtime login helper requires a PostgreSQL URL DSN")
	}
	parsed.User = url.UserPassword(
		name,
		"platformgo-runtime-test-password",
	)
	return parsed.String()
}

func runtimeTestStreamLimits() platformnats.StreamLimits {
	return platformnats.StreamLimits{
		Replicas:        1,
		MaxMessages:     1_000_000,
		MaxBytes:        2 << 30,
		MaxMessageBytes: 1 << 20,
		MaxAge:          30 * 24 * time.Hour,
		DuplicateWindow: 24 * time.Hour,
	}
}
