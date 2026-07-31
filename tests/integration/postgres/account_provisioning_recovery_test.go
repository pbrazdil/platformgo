package postgres_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

func TestBrokerAccountProvisioningRecoversAfterPreCommitFaultAndTimeout(
	t *testing.T,
) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rootPool := postgresPool(t)
	resetDurableSchemas(t, rootPool)
	if err := newCurrentTestMigrator(
		t,
		rootPool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}

	const (
		brokerTenant = "urn:xb:tenant:recovery-partner"
		userID       = "urn:xb:user:recovery-trader"
	)
	if _, err := rootPool.Exec(ctx, `
		INSERT INTO identity.users (
			user_id,
			login,
			normalized_login,
			email,
			normalized_email,
			broker_subject
		) VALUES ($1, 'recovery-trader', 'recovery-trader',
		          'recovery@example.com', 'recovery@example.com', $2)`,
		userID,
		brokerTenant,
	); err != nil {
		t.Fatal(err)
	}

	apiPool := postgresRolePool(t, "platformgo_api")
	enginePool := postgresRolePool(t, "platformgo_engine")
	assertCurrentRole(t, apiPool, "platformgo_api")
	assertCurrentRole(t, enginePool, "platformgo_engine")

	now := time.Date(2026, time.July, 25, 18, 0, 0, 0, time.UTC)
	authenticator, err := edge.NewHMACAuthenticator(
		edge.HMACAuthenticatorConfig{
			ClientTokenSecret: []byte(
				"0123456789abcdef0123456789abcdef",
			),
			Clock: compatibilityClock{value: now},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	apiStore := platformpostgres.NewCompatibilityStore(apiPool)
	timedIdentity, err := application.NewIdentity(
		apiStore,
		authenticator,
		application.IdentityConfig{
			Clock:                      compatibilityClock{value: now},
			AccountProvisioningTimeout: 5 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	principal := edge.Principal{
		Subject:  "urn:xb:apikey:recovery-partner",
		Tenant:   brokerTenant,
		Audience: edge.AudienceBroker,
	}
	request := edge.BrokerAccountRequest{UserID: userID}
	deadlineContext := newControlledDeadlineContext(ctx)
	defer deadlineContext.Expire()
	accountResult := make(chan error, 1)
	go func() {
		_, createErr := timedIdentity.CreateBrokerAccount(
			deadlineContext,
			principal,
			"recovery-key",
			request,
		)
		accountResult <- createErr
	}()

	waitContext, stopWaiting := context.WithTimeout(ctx, 3*time.Second)
	defer stopWaiting()
	var outboxPayload []byte
	for {
		err := rootPool.QueryRow(waitContext, `
			SELECT outbox.payload
			  FROM trading.commands AS command
			  JOIN messaging.outbox AS outbox
			    ON outbox.message_id = command.command_id
			 WHERE command.command_type = 'configure_account'`,
		).Scan(&outboxPayload)
		if err == nil {
			break
		}
		if !errors.Is(err, pgx.ErrNoRows) {
			t.Fatal(err)
		}
		select {
		case <-waitContext.Done():
			t.Fatal("broker account admission did not commit before test deadline")
		case <-time.After(time.Millisecond):
		}
	}
	deadlineContext.Expire()
	if err := <-accountResult; !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf(
			"pre-engine account result error=%v, want deadline exceeded",
			err,
		)
	}
	input, action, err := engine.DecodeInputMessage(outboxPayload)
	if err != nil {
		t.Fatal(err)
	}
	input.StreamSequence = 1

	engineStore := platformpostgres.NewEngineStore(enginePool)
	ownership, err := engineStore.AcquireShardOwnership(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if ownership != nil {
			_ = ownership.Close(context.Background())
		}
	})
	state, err := engineStore.RecoverTradingState(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	faults := testkit.NewFaults(
		platformpostgres.FailpointAfterPersistBeforeCommit,
	)
	next, _, _, err := engineStore.ApplyTrading(
		ctx,
		state,
		input,
		action,
		platformpostgres.ApplyOptions{
			Ownership: ownership,
			Faults:    faults,
		},
	)
	if !errors.Is(err, platformpostgres.ErrInjectedFault) {
		t.Fatalf("faulted engine apply error=%v, want injected fault", err)
	}
	if next.Hash() != state.Hash() {
		t.Fatal("faulted engine apply returned mutated state")
	}
	assertProvisioningGraphCount(t, rootPool, 0)
	if err := ownership.Close(ctx); err != nil {
		t.Fatal(err)
	}
	ownership = nil

	recoveryIdentity, err := application.NewIdentity(
		apiStore,
		authenticator,
		application.IdentityConfig{
			Clock:                      compatibilityClock{value: now},
			AccountProvisioningTimeout: 5 * time.Second,
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	type retryResult struct {
		admission edge.BrokerAccountAdmission
		err       error
	}
	retryChannel := make(chan retryResult, 1)
	go func() {
		admission, createErr := recoveryIdentity.CreateBrokerAccount(
			ctx,
			principal,
			"recovery-key",
			request,
		)
		retryChannel <- retryResult{
			admission: admission,
			err:       createErr,
		}
	}()

	restartedStore := platformpostgres.NewEngineStore(enginePool)
	restartedOwnership, err := restartedStore.AcquireShardOwnership(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if closeErr := restartedOwnership.Close(context.Background()); closeErr != nil {
			t.Errorf("close restarted ownership: %v", closeErr)
		}
	}()
	recovered, err := restartedStore.RecoverTradingState(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	next, decision, duplicate, err := restartedStore.ApplyTrading(
		ctx,
		recovered,
		input,
		action,
		platformpostgres.ApplyOptions{Ownership: restartedOwnership},
	)
	if err != nil {
		t.Fatal(err)
	}
	if duplicate ||
		decision.CommandResult.Status != engine.CommandStatusAccepted ||
		next.Hash() == recovered.Hash() {
		t.Fatalf(
			"restarted apply duplicate=%t decision=%#v state unchanged=%t",
			duplicate,
			decision,
			next.Hash() == recovered.Hash(),
		)
	}

	retry := <-retryChannel
	if retry.err != nil {
		t.Fatal(retry.err)
	}
	assertProvisioningGraphCount(t, rootPool, 3)

	var storedStatus int
	var storedHeaders []byte
	var storedBody []byte
	if err := rootPool.QueryRow(ctx, `
		SELECT response_status, response_headers, response_body
		  FROM trading.idempotency_records
		 WHERE scope = $1
		   AND idempotency_key = 'recovery-key'`,
		"broker-account\x1f"+principal.Subject,
	).Scan(&storedStatus, &storedHeaders, &storedBody); err != nil {
		t.Fatal(err)
	}
	if retry.admission.Response.Status != storedStatus ||
		!bytes.Equal(
			compactJSON(t, retry.admission.Response.Headers),
			compactJSON(t, storedHeaders),
		) ||
		!bytes.Equal(retry.admission.Response.Body, storedBody) {
		t.Fatalf(
			"wire response=%#v durable=(%d, %s, %s)",
			retry.admission.Response,
			storedStatus,
			storedHeaders,
			storedBody,
		)
	}
	replayed, err := recoveryIdentity.CreateBrokerAccount(
		ctx,
		principal,
		"recovery-key",
		request,
	)
	if err != nil {
		t.Fatal(err)
	}
	if replayed.ID != retry.admission.ID ||
		replayed.Response.Status != retry.admission.Response.Status ||
		!bytes.Equal(
			replayed.Response.Headers,
			retry.admission.Response.Headers,
		) ||
		!bytes.Equal(replayed.Response.Body, retry.admission.Response.Body) {
		t.Fatalf(
			"replay=%#v first completed=%#v",
			replayed,
			retry.admission,
		)
	}
}

type controlledDeadlineContext struct {
	context.Context
	done chan struct{}
	once sync.Once
}

func newControlledDeadlineContext(parent context.Context) *controlledDeadlineContext {
	return &controlledDeadlineContext{
		Context: parent,
		done:    make(chan struct{}),
	}
}

func (ctx *controlledDeadlineContext) Done() <-chan struct{} {
	return ctx.done
}

func (ctx *controlledDeadlineContext) Err() error {
	select {
	case <-ctx.done:
		return context.DeadlineExceeded
	default:
		return nil
	}
}

func (ctx *controlledDeadlineContext) Expire() {
	ctx.once.Do(func() {
		close(ctx.done)
	})
}

func compactJSON(t *testing.T, value []byte) []byte {
	t.Helper()
	var compacted bytes.Buffer
	if err := json.Compact(&compacted, value); err != nil {
		t.Fatalf("compact JSON: %v", err)
	}
	return compacted.Bytes()
}

func postgresRolePool(t *testing.T, role string) *pgxpool.Pool {
	t.Helper()
	if role != "platformgo_api" && role != "platformgo_engine" {
		t.Fatalf("unsupported test role %q", role)
	}
	dsn := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if dsn == "" {
		t.Skip(
			"PLATFORMGO_TEST_POSTGRES_DSN is required for PostgreSQL integration tests",
		)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		t.Fatalf("parse PostgreSQL DSN: %v", err)
	}
	config.AfterConnect = func(ctx context.Context, connection *pgx.Conn) error {
		_, execErr := connection.Exec(ctx, "SET ROLE "+role)
		return execErr
	}
	pool, err := pgxpool.NewWithConfig(context.Background(), config)
	if err != nil {
		t.Fatalf("open %s PostgreSQL pool: %v", role, err)
	}
	if err := pool.Ping(context.Background()); err != nil {
		pool.Close()
		t.Fatalf("ping %s PostgreSQL pool: %v", role, err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func assertCurrentRole(
	t *testing.T,
	pool *pgxpool.Pool,
	want string,
) {
	t.Helper()
	var got string
	if err := pool.QueryRow(context.Background(), "SELECT current_user").
		Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("current role=%q, want %q", got, want)
	}
}

func assertProvisioningGraphCount(
	t *testing.T,
	pool *pgxpool.Pool,
	want int,
) {
	t.Helper()
	var got int
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM trading.accounts)
			+ (SELECT count(*) FROM identity.user_accounts)
			+ (SELECT count(*) FROM identity.account_profiles)`,
	).Scan(&got); err != nil {
		t.Fatal(err)
	}
	if got != want {
		t.Fatalf("provisioning graph rows=%d, want %d", got, want)
	}
}
