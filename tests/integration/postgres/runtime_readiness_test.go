package postgres_test

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestRuntimeReadinessRequiresPostRecoveryAndPreDrainLeases(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, 0, 0
		)`); err != nil {
		t.Fatal(err)
	}
	engineStore := platformpostgres.NewEngineStore(pool)
	ownership, err := engineStore.AcquireShardOwnership(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ownership.Close(context.Background()) }()
	messagingStore := platformpostgres.NewMessagingStore(pool)
	outboxOwnership, err := messagingStore.AcquireOutboxPublisher(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outboxOwnership.Close(context.Background()) }()
	outboxReady, err := messagingStore.AcquireOutboxReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outboxReady.Close(context.Background()) }()
	compatibilityStore := platformpostgres.NewCompatibilityStore(pool)
	if err := compatibilityStore.RuntimeCommandReady(ctx, 7); err == nil {
		t.Fatal("ownership locks reported ready before recovery completed")
	}
	submission, err := application.NewOrderSubmission(
		platformpostgres.NewCommandJournal(pool),
		application.OrderSubmissionConfig{
			ShardID:        7,
			IdempotencyTTL: time.Hour,
			Readiness: func(checkContext context.Context) error {
				return compatibilityStore.RuntimeCommandReady(checkContext, 7)
			},
		},
	)
	if err != nil {
		t.Fatal(err)
	}
	_, err = submission.SubmitOrder(
		ctx,
		edge.Principal{Subject: "user-1", Audience: edge.AudienceClient},
		"account-1",
		"recovery-barrier",
		edge.SubmitOrderRequest{
			IntentID: "intent-1", Symbol: "BTC-PERP", Side: "BUY",
			Type: "MARKET", Quantity: "1",
		},
	)
	if err == nil {
		t.Fatal("mutation was admitted before engine recovery completed")
	}
	var effects int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.commands)
			+ (SELECT count(*) FROM trading.idempotency_records)
			+ (SELECT count(*) FROM messaging.outbox)`,
	).Scan(&effects); err != nil {
		t.Fatal(err)
	}
	if effects != 0 {
		t.Fatalf("pre-recovery mutation created %d durable effects", effects)
	}
	engineReady, err := engineStore.AcquireEngineReady(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := compatibilityStore.RuntimeCommandReady(ctx, 7); err != nil {
		t.Fatalf("post-recovery leases not ready: %v", err)
	}
	if err := engineReady.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := compatibilityStore.RuntimeCommandReady(ctx, 7); err == nil {
		t.Fatal("engine drain retained readiness")
	}
	engineReady, err = engineStore.AcquireEngineReady(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = engineReady.Close(context.Background()) }()
	if err := outboxReady.Close(ctx); err != nil {
		t.Fatal(err)
	}
	if err := compatibilityStore.RuntimeCommandReady(ctx, 7); err == nil {
		t.Fatal("outbox drain retained readiness")
	}
}

func TestCommandAdmissionCannotCrossReadinessDrainBarrier(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}

	engineStore := platformpostgres.NewEngineStore(pool)
	ownership, err := engineStore.AcquireShardOwnership(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ownership.Close(context.Background()) }()
	engineReady, err := engineStore.AcquireEngineReady(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if engineReady != nil {
			_ = engineReady.Close(context.Background())
		}
	}()
	messagingStore := platformpostgres.NewMessagingStore(pool)
	outboxOwnership, err := messagingStore.AcquireOutboxPublisher(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outboxOwnership.Close(context.Background()) }()
	outboxReady, err := messagingStore.AcquireOutboxReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outboxReady.Close(context.Background()) }()

	readinessStore := platformpostgres.NewCompatibilityStore(pool)
	if err := readinessStore.RuntimeCommandReady(ctx, 7); err != nil {
		t.Fatalf("prepared runtime is not ready: %v", err)
	}

	const admissionGateNamespace int64 = 0x50474144
	gateConnection, err := pool.Acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer gateConnection.Release()
	var gateHeld bool
	if err := gateConnection.QueryRow(
		ctx,
		"SELECT pg_try_advisory_lock($1, $2)",
		admissionGateNamespace,
		int64(0),
	).Scan(&gateHeld); err != nil {
		t.Fatal(err)
	}
	if !gateHeld {
		t.Fatal("test could not hold command admission gate")
	}
	defer func() {
		if gateHeld {
			_, _ = gateConnection.Exec(
				context.Background(),
				"SELECT pg_advisory_unlock($1, $2)",
				admissionGateNamespace,
				int64(0),
			)
		}
	}()

	request := validCommandRequest(
		t,
		engine.IDFromSequence(engine.ID{}, 707),
		"drain-barrier-account",
		1,
		7,
		time.Date(2026, time.July, 25, 19, 0, 0, 0, time.UTC),
	)
	request.RequireRuntimeReady = true
	beginResult := make(chan error, 1)
	go func() {
		_, beginErr := platformpostgres.NewCommandJournal(pool).Begin(
			ctx,
			request,
		)
		beginResult <- beginErr
	}()
	waitForAdvisoryWaiter(
		t,
		ctx,
		pool,
		admissionGateNamespace,
		"ShareLock",
	)

	closeResult := make(chan error, 1)
	go func() {
		closeResult <- engineReady.Close(ctx)
	}()
	for readinessStore.RuntimeCommandReady(ctx, 7) == nil {
		select {
		case <-ctx.Done():
			t.Fatal("engine readiness marker was not withdrawn")
		case <-time.After(time.Millisecond):
		}
	}
	var released bool
	if err := gateConnection.QueryRow(
		ctx,
		"SELECT pg_advisory_unlock($1, $2)",
		admissionGateNamespace,
		int64(0),
	).Scan(&released); err != nil {
		t.Fatal(err)
	}
	if !released {
		t.Fatal("test command admission gate was not released")
	}
	gateHeld = false

	if err := <-beginResult; !errors.Is(err, platformpostgres.ErrRuntimeNotReady) {
		t.Fatalf("Begin error=%v, want ErrRuntimeNotReady", err)
	}
	if err := <-closeResult; err != nil {
		t.Fatal(err)
	}
	engineReady = nil
	var effects int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.idempotency_records)
			+ (SELECT count(*) FROM trading.commands)
			+ (SELECT count(*) FROM messaging.outbox)`,
	).Scan(&effects); err != nil {
		t.Fatal(err)
	}
	if effects != 0 {
		t.Fatalf("post-drain admission created %d durable effects", effects)
	}
}

func TestBeginReplaysCommittedCommandAfterReadinessDrain(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}

	engineStore := platformpostgres.NewEngineStore(pool)
	ownership, err := engineStore.AcquireShardOwnership(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = ownership.Close(context.Background()) }()
	engineReady, err := engineStore.AcquireEngineReady(ctx, 7)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if engineReady != nil {
			_ = engineReady.Close(context.Background())
		}
	}()
	messagingStore := platformpostgres.NewMessagingStore(pool)
	outboxOwnership, err := messagingStore.AcquireOutboxPublisher(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outboxOwnership.Close(context.Background()) }()
	outboxReady, err := messagingStore.AcquireOutboxReady(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = outboxReady.Close(context.Background()) }()

	request := validCommandRequest(
		t,
		engine.IDFromSequence(engine.ID{}, 708),
		"drained-replay-account",
		1,
		7,
		time.Date(2026, time.July, 25, 19, 5, 0, 0, time.UTC),
	)
	request.RequireRuntimeReady = true
	request.Response = application.StoredResponse{
		Status: http.StatusAccepted,
		Headers: []byte(
			`{"content-type":["application/json"],"x-proof":["winner"]}`,
		),
		Body: []byte(`{"commandId":"winner"}` + "\n"),
	}
	journal := platformpostgres.NewCommandJournal(pool)
	winner, err := journal.Begin(ctx, request)
	if err != nil {
		t.Fatal(err)
	}
	if !winner.Created {
		t.Fatal("winning request did not create the command")
	}

	if err := engineReady.Close(ctx); err != nil {
		t.Fatal(err)
	}
	engineReady = nil
	if err := platformpostgres.NewCompatibilityStore(pool).
		RuntimeCommandReady(ctx, 7); err == nil {
		t.Fatal("runtime remained ready after engine drain")
	}

	replayed, err := journal.Begin(ctx, request)
	if err != nil {
		t.Fatalf("identical replay after readiness drain: %v", err)
	}
	if replayed.Created ||
		replayed.CommandID != winner.CommandID ||
		replayed.State != winner.State ||
		replayed.Response.Status != winner.Response.Status ||
		!bytes.Equal(replayed.Response.Headers, winner.Response.Headers) ||
		!bytes.Equal(replayed.Response.Body, winner.Response.Body) {
		t.Fatalf("winner=%#v replayed=%#v", winner, replayed)
	}

	var idempotencyRecords, commands, outboxRows int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM trading.idempotency_records),
			(SELECT count(*) FROM trading.commands),
			(SELECT count(*) FROM messaging.outbox)`,
	).Scan(&idempotencyRecords, &commands, &outboxRows); err != nil {
		t.Fatal(err)
	}
	if idempotencyRecords != 1 || commands != 1 || outboxRows != 1 {
		t.Fatalf(
			"durable effects idempotency=%d commands=%d outbox=%d, want 1 each",
			idempotencyRecords,
			commands,
			outboxRows,
		)
	}
}

func TestBeginSerializesInFlightWinnerAcrossReadinessDrain(t *testing.T) {
	for _, test := range []struct {
		name     string
		conflict bool
	}{
		{name: "identical retry replays"},
		{name: "changed retry conflicts", conflict: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(
				context.Background(),
				10*time.Second,
			)
			defer cancel()
			pool := postgresPool(t)
			resetDurableSchemas(t, pool)
			if err := newCurrentTestMigrator(
				t,
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).MigrateAndProvision(ctx, 7); err != nil {
				t.Fatal(err)
			}

			engineStore := platformpostgres.NewEngineStore(pool)
			ownership, err := engineStore.AcquireShardOwnership(ctx, 7)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = ownership.Close(context.Background()) }()
			engineReady, err := engineStore.AcquireEngineReady(ctx, 7)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if engineReady != nil {
					_ = engineReady.Close(context.Background())
				}
			}()
			messagingStore := platformpostgres.NewMessagingStore(pool)
			outboxOwnership, err := messagingStore.AcquireOutboxPublisher(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				_ = outboxOwnership.Close(context.Background())
			}()
			outboxReady, err := messagingStore.AcquireOutboxReady(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer func() { _ = outboxReady.Close(context.Background()) }()

			const (
				accountLockNamespace     int64 = 0x5047434d
				idempotencyLockNamespace int64 = 0x50474944
			)
			accountID := "in-flight-winner-account"
			lockConnection, err := pool.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer lockConnection.Release()
			var accountLockHeld bool
			if err := lockConnection.QueryRow(ctx, `
				SELECT pg_try_advisory_lock($1, hashtext($2))`,
				accountLockNamespace,
				accountID,
			).Scan(&accountLockHeld); err != nil {
				t.Fatal(err)
			}
			if !accountLockHeld {
				t.Fatal("test could not hold account command lock")
			}
			defer func() {
				if accountLockHeld {
					_, _ = lockConnection.Exec(
						context.Background(),
						"SELECT pg_advisory_unlock($1, hashtext($2))",
						accountLockNamespace,
						accountID,
					)
				}
			}()

			request := validCommandRequest(
				t,
				engine.IDFromSequence(engine.ID{}, 709),
				accountID,
				1,
				7,
				time.Date(2026, time.July, 25, 19, 10, 0, 0, time.UTC),
			)
			request.RequireRuntimeReady = true
			request.Response = application.StoredResponse{
				Status: http.StatusAccepted,
				Headers: []byte(
					`{"content-type":["application/json"],"x-proof":["in-flight-winner"]}`,
				),
				Body: []byte(`{"commandId":"in-flight-winner"}` + "\n"),
			}
			journal := platformpostgres.NewCommandJournal(pool)
			type beginOutcome struct {
				result platformpostgres.BeginCommandResult
				err    error
			}
			winnerResult := make(chan beginOutcome, 1)
			go func() {
				result, beginErr := journal.Begin(ctx, request)
				winnerResult <- beginOutcome{result: result, err: beginErr}
			}()
			waitForAdvisoryWaiterAnyObject(
				t,
				ctx,
				pool,
				accountLockNamespace,
			)

			retryRequest := request
			if test.conflict {
				retryRequest.RequestHash[0] ^= 0xff
			}
			retryResult := make(chan beginOutcome, 1)
			go func() {
				result, beginErr := journal.Begin(ctx, retryRequest)
				retryResult <- beginOutcome{result: result, err: beginErr}
			}()
			waitForAdvisoryWaiterAnyObject(
				t,
				ctx,
				pool,
				idempotencyLockNamespace,
			)

			closeResult := make(chan error, 1)
			go func() {
				closeResult <- engineReady.Close(ctx)
			}()
			readinessStore := platformpostgres.NewCompatibilityStore(pool)
			for readinessStore.RuntimeCommandReady(ctx, 7) == nil {
				select {
				case <-ctx.Done():
					t.Fatal("engine readiness marker was not withdrawn")
				case <-time.After(time.Millisecond):
				}
			}
			var released bool
			if err := lockConnection.QueryRow(
				ctx,
				"SELECT pg_advisory_unlock($1, hashtext($2))",
				accountLockNamespace,
				accountID,
			).Scan(&released); err != nil {
				t.Fatal(err)
			}
			if !released {
				t.Fatal("test account command lock was not released")
			}
			accountLockHeld = false

			winner := <-winnerResult
			if winner.err != nil || !winner.result.Created {
				t.Fatalf(
					"winner result=%#v error=%v",
					winner.result,
					winner.err,
				)
			}
			retry := <-retryResult
			if test.conflict {
				if !errors.Is(
					retry.err,
					platformpostgres.ErrIdempotencyConflict,
				) {
					t.Fatalf(
						"retry error=%v, want ErrIdempotencyConflict",
						retry.err,
					)
				}
			} else if retry.err != nil ||
				retry.result.Created ||
				retry.result.CommandID != winner.result.CommandID ||
				retry.result.State != winner.result.State ||
				retry.result.Response.Status != winner.result.Response.Status ||
				!bytes.Equal(
					retry.result.Response.Headers,
					winner.result.Response.Headers,
				) ||
				!bytes.Equal(
					retry.result.Response.Body,
					winner.result.Response.Body,
				) {
				t.Fatalf(
					"winner=%#v retry=%#v error=%v",
					winner.result,
					retry.result,
					retry.err,
				)
			}
			if err := <-closeResult; err != nil {
				t.Fatal(err)
			}
			engineReady = nil

			var idempotencyRecords, commands, outboxRows int
			if err := pool.QueryRow(ctx, `
				SELECT
					(SELECT count(*) FROM trading.idempotency_records),
					(SELECT count(*) FROM trading.commands),
					(SELECT count(*) FROM messaging.outbox)`,
			).Scan(
				&idempotencyRecords,
				&commands,
				&outboxRows,
			); err != nil {
				t.Fatal(err)
			}
			if idempotencyRecords != 1 || commands != 1 || outboxRows != 1 {
				t.Fatalf(
					"durable effects idempotency=%d commands=%d outbox=%d",
					idempotencyRecords,
					commands,
					outboxRows,
				)
			}
		})
	}
}

func waitForAdvisoryWaiter(
	t *testing.T,
	ctx context.Context,
	pool interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	namespace int64,
	mode string,
) {
	t.Helper()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_locks
				 WHERE locktype = 'advisory'
				   AND classid = $1::oid
				   AND objid = 0::oid
				   AND mode = $2
				   AND NOT granted
			)`,
			namespace,
			mode,
		).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("command admission did not wait on the drain gate")
		case <-time.After(time.Millisecond):
		}
	}
}

func waitForAdvisoryWaiterAnyObject(
	t *testing.T,
	ctx context.Context,
	pool interface {
		QueryRow(context.Context, string, ...any) pgx.Row
	},
	namespace int64,
) {
	t.Helper()
	for {
		var waiting bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_locks
				 WHERE locktype = 'advisory'
				   AND classid = $1::oid
				   AND mode = 'ExclusiveLock'
				   AND NOT granted
			)`,
			namespace,
		).Scan(&waiting); err != nil {
			t.Fatal(err)
		}
		if waiting {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatal("transaction did not wait on the expected advisory lock")
		case <-time.After(time.Millisecond):
		}
	}
}
