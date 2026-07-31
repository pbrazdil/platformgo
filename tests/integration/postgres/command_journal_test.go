package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestCommandJournalRejectsConflictsAndReplaysCompletedResponse(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 7); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	journal := platformpostgres.NewCommandJournal(pool)
	commandID := engine.IDFromSequence(engine.ID{}, 1)
	logicalTime := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	request := validCommandRequest(
		t,
		commandID,
		"account-1",
		1,
		7,
		logicalTime,
	)
	request.Scope = "account:account-1"
	request.IdempotencyKey = "deposit-1"
	request.RequestHash = sha256.Sum256([]byte("canonical deposit request"))
	first, err := journal.Begin(context.Background(), request)
	if err != nil {
		t.Fatalf("first Begin: %v", err)
	}
	if !first.Created || first.CommandID != commandID ||
		first.State != platformpostgres.IdempotencyInProgress {
		t.Fatalf("first Begin = %+v", first)
	}

	repeated, err := journal.Begin(context.Background(), request)
	if err != nil {
		t.Fatalf("repeated Begin: %v", err)
	}
	if repeated.Created || repeated.CommandID != commandID ||
		repeated.State != platformpostgres.IdempotencyInProgress {
		t.Fatalf("repeated Begin = %+v", repeated)
	}

	conflict := request
	conflict.RequestHash = sha256.Sum256([]byte("different request"))
	if _, err := journal.Begin(context.Background(), conflict); !errors.Is(
		err,
		platformpostgres.ErrIdempotencyConflict,
	) {
		t.Fatalf("conflicting Begin error = %v, want ErrIdempotencyConflict", err)
	}

	completion := platformpostgres.CompleteCommandRequest{
		CommandID: commandID,
		Status:    platformpostgres.CommandAccepted,
		Result:    []byte(`{"Status":"accepted","Reason":""}`),
		Response: platformpostgres.StoredResponse{
			Status:  201,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    []byte(`{"balance":"10"}`),
		},
	}
	if _, err := pool.Exec(context.Background(), `
		UPDATE trading.commands
		   SET status = 'accepted',
		       result = $2,
		       completed_at = clock_timestamp()
		 WHERE command_id = $1`,
		commandID.String(),
		completion.Result,
	); err != nil {
		t.Fatalf("simulate committed engine result: %v", err)
	}
	if err := journal.Complete(context.Background(), completion); err != nil {
		t.Fatalf("Complete: %v", err)
	}

	completed, err := journal.Begin(context.Background(), request)
	if err != nil {
		t.Fatalf("completed Begin: %v", err)
	}
	if completed.Created ||
		completed.State != platformpostgres.IdempotencyCompleted ||
		completed.Response.Status != completion.Response.Status ||
		string(completed.Response.Headers) != string(completion.Response.Headers) ||
		string(completed.Response.Body) != string(completion.Response.Body) {
		t.Fatalf("completed replay = %+v", completed)
	}

	var idempotencyRows int
	var commandRows int
	var outboxRows int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM trading.idempotency_records",
	).Scan(&idempotencyRows); err != nil {
		t.Fatalf("count idempotency records: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM trading.commands",
	).Scan(&commandRows); err != nil {
		t.Fatalf("count commands: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM messaging.outbox",
	).Scan(&outboxRows); err != nil {
		t.Fatalf("count command outbox: %v", err)
	}
	if idempotencyRows != 1 || commandRows != 1 || outboxRows != 1 {
		t.Fatalf(
			"journal rows = idempotency %d, commands %d, outbox %d, want 1 each",
			idempotencyRows,
			commandRows,
			outboxRows,
		)
	}
}

func TestCommandJournalRejectsOutOfOrderAndPrematureCompletion(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 7); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	journal := platformpostgres.NewCommandJournal(pool)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	request := validCommandRequest(
		t,
		engine.IDFromSequence(engine.ID{}, 101),
		"account-1",
		2,
		7,
		now,
	)
	request.Scope = "account:account-1"
	request.IdempotencyKey = "sequence-1"
	request.RequestHash = sha256.Sum256([]byte("sequence-1"))
	if _, err := journal.Begin(
		context.Background(),
		request,
	); !errors.Is(err, platformpostgres.ErrCommandSequenceGap) {
		t.Fatalf("sequence 2 first Begin error = %v, want ErrCommandSequenceGap", err)
	}

	request = validCommandRequest(
		t,
		request.CommandID,
		"account-1",
		1,
		7,
		now,
	)
	request.Scope = "account:account-1"
	request.IdempotencyKey = "sequence-1"
	request.RequestHash = sha256.Sum256([]byte("sequence-1"))
	if _, err := journal.Begin(context.Background(), request); err != nil {
		t.Fatalf("sequence 1 Begin: %v", err)
	}
	if err := journal.Complete(context.Background(), platformpostgres.CompleteCommandRequest{
		CommandID: request.CommandID,
		Status:    platformpostgres.CommandRejected,
		Result:    []byte(`{"Status":"rejected","Reason":"invalid_order"}`),
		Response: platformpostgres.StoredResponse{
			Status:  400,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    []byte(`{"error":"invalid_order"}`),
		},
	}); !errors.Is(err, platformpostgres.ErrCommandCompletionConflict) {
		t.Fatalf(
			"premature Complete error = %v, want ErrCommandCompletionConflict",
			err,
		)
	}

	var commandStatus string
	var idempotencyState string
	if err := pool.QueryRow(context.Background(), `
		SELECT command.status, idempotency.state
		  FROM trading.commands AS command
		  JOIN trading.idempotency_records AS idempotency
		    ON idempotency.command_id = command.command_id
		 WHERE command.command_id = $1`,
		request.CommandID.String(),
	).Scan(&commandStatus, &idempotencyState); err != nil {
		t.Fatalf("read pending command: %v", err)
	}
	if commandStatus != "pending" || idempotencyState != "in_progress" {
		t.Fatalf(
			"premature completion state = command %s idempotency %s",
			commandStatus,
			idempotencyState,
		)
	}
}

func TestCommandJournalDurablyBindsAccountToOneShard(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 7); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	first := validCommandRequest(
		t,
		engine.IDFromSequence(engine.ID{}, 121),
		"account-1",
		1,
		7,
		now,
	)
	if _, err := platformpostgres.NewCommandJournal(pool).Begin(
		context.Background(),
		first,
	); err != nil {
		t.Fatalf("first shard-bound Begin: %v", err)
	}

	// A new journal instance represents a process restart. The durable mapping,
	// rather than process memory, must admit the next same-shard command.
	second := validCommandRequest(
		t,
		engine.IDFromSequence(engine.ID{}, 122),
		"account-1",
		2,
		7,
		now.Add(time.Second),
	)
	if _, err := platformpostgres.NewCommandJournal(pool).Begin(
		context.Background(),
		second,
	); err != nil {
		t.Fatalf("same-shard Begin after restart: %v", err)
	}

	wrongShard := validCommandRequest(
		t,
		engine.IDFromSequence(engine.ID{}, 123),
		"account-1",
		3,
		8,
		now.Add(2*time.Second),
	)
	if _, err := platformpostgres.NewCommandJournal(pool).Begin(
		context.Background(),
		wrongShard,
	); !errors.Is(err, platformpostgres.ErrDeploymentShardConflict) {
		t.Fatalf(
			"cross-shard Begin error = %v, want ErrDeploymentShardConflict",
			err,
		)
	}

	var assignedShard int64
	if err := pool.QueryRow(
		context.Background(),
		"SELECT shard_id FROM engine.account_shards WHERE account_id = 'account-1'",
	).Scan(&assignedShard); err != nil {
		t.Fatalf("read durable account shard: %v", err)
	}
	if assignedShard != 7 {
		t.Fatalf("durable account shard = %d, want 7", assignedShard)
	}
	for _, query := range []string{
		"SELECT count(*) FROM trading.idempotency_records",
		"SELECT count(*) FROM trading.commands",
		"SELECT count(*) FROM messaging.outbox",
	} {
		var count int
		if err := pool.QueryRow(context.Background(), query).Scan(&count); err != nil {
			t.Fatalf("count cross-shard rollback rows: %v", err)
		}
		if count != 2 {
			t.Fatalf("%s = %d, want 2", query, count)
		}
	}
	if _, err := pool.Exec(
		context.Background(),
		"UPDATE engine.account_shards SET shard_id = 8 WHERE account_id = 'account-1'",
	); err == nil {
		t.Fatal("immutable account shard assignment was updated")
	}
}

func TestDeploymentShardProvisioningDeterminesConcurrentAuthority(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(context.Background()); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	unconfigured := validCommandRequest(
		t,
		engine.IDFromSequence(engine.ID{}, 130),
		"account-unconfigured",
		1,
		7,
		now,
	)
	if _, err := platformpostgres.NewCommandJournal(pool).Begin(
		context.Background(),
		unconfigured,
	); !errors.Is(err, platformpostgres.ErrDeploymentShardUnconfigured) {
		t.Fatalf("unconfigured Begin error = %v", err)
	}
	if _, err := platformpostgres.NewEngineStore(pool).AcquireShardOwnership(
		context.Background(),
		7,
	); !errors.Is(err, platformpostgres.ErrDeploymentShardUnconfigured) {
		t.Fatalf("unconfigured ownership error = %v", err)
	}
	for _, relation := range []string{
		"engine.deployment_shard",
		"engine.account_shards",
		"engine.shard_ownership_epochs",
		"trading.idempotency_records",
		"trading.commands",
		"messaging.outbox",
	} {
		var count int
		if err := pool.QueryRow(
			context.Background(),
			"SELECT count(*) FROM "+relation,
		).Scan(&count); err != nil {
			t.Fatalf("count %s: %v", relation, err)
		}
		if count != 0 {
			t.Fatalf("%s rows before provisioning = %d, want 0", relation, count)
		}
	}
	migrator := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := migrator.ProvisionDeploymentShard(
		context.Background(),
		7,
	); err != nil {
		t.Fatalf("ProvisionDeploymentShard: %v", err)
	}
	if err := migrator.ProvisionDeploymentShard(
		context.Background(),
		7,
	); err != nil {
		t.Fatalf("idempotent ProvisionDeploymentShard: %v", err)
	}
	if err := migrator.ProvisionDeploymentShard(
		context.Background(),
		8,
	); !errors.Is(err, platformpostgres.ErrDeploymentShardConflict) {
		t.Fatalf("conflicting ProvisionDeploymentShard error = %v", err)
	}
	requests := []platformpostgres.BeginCommandRequest{
		validCommandRequest(
			t,
			engine.IDFromSequence(engine.ID{}, 131),
			"account-race",
			1,
			7,
			now,
		),
		validCommandRequest(
			t,
			engine.IDFromSequence(engine.ID{}, 132),
			"account-race",
			1,
			8,
			now,
		),
	}
	start := make(chan struct{})
	results := make(chan error, len(requests))
	var workers sync.WaitGroup
	for _, request := range requests {
		request := request
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			_, err := platformpostgres.NewCommandJournal(pool).Begin(
				context.Background(),
				request,
			)
			results <- err
		}()
	}
	close(start)
	workers.Wait()
	close(results)

	successes := 0
	conflicts := 0
	for err := range results {
		switch {
		case err == nil:
			successes++
		case errors.Is(err, platformpostgres.ErrDeploymentShardConflict):
			conflicts++
		default:
			t.Fatalf("concurrent assignment leaked unclassified error: %v", err)
		}
	}
	if successes != 1 || conflicts != 1 {
		t.Fatalf(
			"concurrent assignments = successes %d conflicts %d, want 1 each",
			successes,
			conflicts,
		)
	}

	var assignedShardID int64
	var commandRows int
	var idempotencyRows int
	var outboxRows int
	var subjectMatches bool
	if err := pool.QueryRow(context.Background(), `
		SELECT shard_id
		  FROM engine.account_shards
		 WHERE account_id = 'account-race'`,
	).Scan(&assignedShardID); err != nil {
		t.Fatalf("read concurrent assignment: %v", err)
	}
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM trading.commands),
			(SELECT count(*) FROM trading.idempotency_records),
			(SELECT count(*) FROM messaging.outbox),
			EXISTS (
				SELECT 1
				  FROM messaging.outbox
				 WHERE subject = 'engine.input.' || $1::bigint::text || '.command.v1'
			)`,
		assignedShardID,
	).Scan(
		&commandRows,
		&idempotencyRows,
		&outboxRows,
		&subjectMatches,
	); err != nil {
		t.Fatalf("inspect concurrent assignment effects: %v", err)
	}
	if assignedShardID != 7 ||
		commandRows != 1 ||
		idempotencyRows != 1 ||
		outboxRows != 1 ||
		!subjectMatches {
		t.Fatalf(
			"concurrent authority = shard %d commands %d idempotency %d outbox %d subjectMatches %t",
			assignedShardID,
			commandRows,
			idempotencyRows,
			outboxRows,
			subjectMatches,
		)
	}
}

func TestCommandJournalRejectsRedundantMetadataMismatch(t *testing.T) {
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(context.Background(), 7); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	tests := []struct {
		name   string
		mutate func(*platformpostgres.BeginCommandRequest)
	}{
		{
			name: "command type",
			mutate: func(request *platformpostgres.BeginCommandRequest) {
				request.CommandType = string(engine.TradingActionSubmitOrder)
			},
		},
		{
			name: "logical time",
			mutate: func(request *platformpostgres.BeginCommandRequest) {
				request.LogicalTime = request.LogicalTime.Add(time.Second)
			},
		},
		{
			name: "schema version",
			mutate: func(request *platformpostgres.BeginCommandRequest) {
				request.SchemaVersion++
			},
		},
		{
			name: "account lane",
			mutate: func(request *platformpostgres.BeginCommandRequest) {
				request.AccountID = "account-2"
			},
		},
		{
			name: "resolved market sequence",
			mutate: func(request *platformpostgres.BeginCommandRequest) {
				input, _, err := engine.DecodeInputMessage(request.OutboxPayload)
				if err != nil {
					t.Fatalf("DecodeInputMessage: %v", err)
				}
				input.MarketSequence = 1
				request.OutboxPayload, err = engine.EncodeInputMessage(input)
				if err != nil {
					t.Fatalf("EncodeInputMessage: %v", err)
				}
			},
		},
	}
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := validCommandRequest(
				t,
				engine.IDFromSequence(engine.ID{}, uint64(151+index)),
				"account-1",
				1,
				7,
				now,
			)
			test.mutate(&request)
			if _, err := platformpostgres.NewCommandJournal(pool).Begin(
				context.Background(),
				request,
			); !errors.Is(err, platformpostgres.ErrCommandInputConflict) {
				t.Fatalf("Begin error = %v, want ErrCommandInputConflict", err)
			}
		})
	}
	assertRowCount(t, pool, "trading.accounts", 0)
	var commands int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM trading.commands",
	).Scan(&commands); err != nil {
		t.Fatalf("count commands: %v", err)
	}
	if commands != 0 {
		t.Fatalf("commands = %d, want 0", commands)
	}
}

func validCommandRequest(
	t *testing.T,
	commandID engine.ID,
	accountID string,
	accountSequence uint64,
	shardID engine.ShardID,
	logicalTime time.Time,
) platformpostgres.BeginCommandRequest {
	t.Helper()
	action := engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     accountID,
			Currency:      "USDC",
			CurrencyScale: 2,
			Operation:     engine.BalanceOperationDeposit,
			Amount:        "10",
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	input := engine.InputEnvelope{
		InputID:              commandID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              shardID,
		Kind:                 engine.InputKindCommand,
		SourceID:             "command-journal",
		SourceSequence:       accountSequence,
		LogicalTime:          engine.NewLogicalTime(logicalTime),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	}
	outboxPayload, err := engine.EncodeInputMessage(input)
	if err != nil {
		t.Fatalf("EncodeInputMessage: %v", err)
	}
	return platformpostgres.BeginCommandRequest{
		Scope:            "account:" + accountID,
		IdempotencyKey:   commandID.String(),
		RequestHash:      sha256.Sum256(outboxPayload),
		CommandID:        commandID,
		AccountID:        accountID,
		AccountSequence:  accountSequence,
		CommandType:      string(action.Kind),
		SchemaVersion:    input.SchemaVersion,
		CanonicalPayload: payload.Bytes(),
		OutboxSubject:    "engine.input." + fmt.Sprint(shardID) + ".command.v1",
		OutboxPayload:    outboxPayload,
		LogicalTime:      logicalTime,
		ExpiresAt:        logicalTime.Add(24 * time.Hour),
	}
}
