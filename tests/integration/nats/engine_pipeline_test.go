package nats_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
)

func TestCommandOutboxJetStreamEnginePostgresPipeline(t *testing.T) {
	natsURL := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if natsURL == "" || postgresDSN == "" {
		t.Skip("NATS and PostgreSQL integration URLs are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	resetDurableSchemas(t, ctx, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 9); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	connection, err := gonats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatalf("create JetStream context: %v", err)
	}
	limits := platformnats.StreamLimits{
		Replicas:        1,
		MaxMessages:     10_000,
		MaxBytes:        64 << 20,
		MaxMessageBytes: 1 << 20,
		MaxAge:          time.Hour,
		DuplicateWindow: time.Minute,
	}
	if err := platformnats.EnsureStreams(ctx, js, limits); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	resetEngineShardStream(t, ctx, js, 9)
	if err := platformnats.EnsureEngineShardStream(ctx, js, 9, limits); err != nil {
		t.Fatalf("EnsureEngineShardStream: %v", err)
	}

	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureInstrument,
		ConfigureInstrument: &engine.ConfigureInstrument{
			InstrumentID:            "BTC-PERP",
			Revision:                1,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 2,
			InitialMarginRate:       "0.1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             "10",
			MakerFeeRate:            "0.001",
			TakerFeeRate:            "0.002",
		},
	}
	actionPayload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	commandID := engine.IDFromSequence(engine.ID{}, 121)
	logicalTime := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	input := engine.InputEnvelope{
		InputID:              commandID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              9,
		Kind:                 engine.InputKindCommand,
		SourceID:             "command-journal",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(logicalTime),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              actionPayload,
	}
	transportPayload, err := platformnats.EncodeEngineInputMessage(input)
	if err != nil {
		t.Fatalf("EncodeEngineInputMessage: %v", err)
	}
	now := time.Now().UTC()
	begin, err := platformpostgres.NewCommandJournal(pool).Begin(
		ctx,
		platformpostgres.BeginCommandRequest{
			Scope:            "system:instruments",
			IdempotencyKey:   "configure-btc-perp-v1",
			RequestHash:      sha256.Sum256(transportPayload),
			CommandID:        commandID,
			AccountID:        "system",
			AccountSequence:  1,
			CommandType:      string(action.Kind),
			SchemaVersion:    engine.CurrentSchemaVersion,
			CanonicalPayload: actionPayload.Bytes(),
			OutboxSubject:    "engine.input.9.command.v1",
			OutboxPayload:    transportPayload,
			LogicalTime:      logicalTime,
			ExpiresAt:        now.Add(24 * time.Hour),
		},
	)
	if err != nil || !begin.Created {
		t.Fatalf("Begin command = %+v, error %v", begin, err)
	}

	published, err := platformpostgres.NewMessagingStore(pool).PublishOutboxBatch(
		ctx,
		platformnats.NewPublisher(js),
		now.Add(time.Second),
		10,
		time.Minute,
		time.Second,
	)
	if err != nil || published != 1 {
		t.Fatalf("PublishOutboxBatch = %d, error %v", published, err)
	}

	engineStore := platformpostgres.NewEngineStore(pool)
	processor, err := platformnats.NewEngineProcessor(ctx, engineStore, 9)
	if err != nil {
		t.Fatalf("NewEngineProcessor: %v", err)
	}
	t.Cleanup(func() {
		_ = processor.Close(context.Background())
	})
	consumer, err := platformnats.NewEnginePullConsumer(
		ctx,
		js,
		9,
		"engine-shard-9-pipeline",
		time.Second,
	)
	if err != nil {
		t.Fatalf("NewEnginePullConsumer: %v", err)
	}
	errAckLostAfterCommit := errors.New("injected crash after commit before ack")
	var firstDelivery platformnats.InboundMessage
	processed, err := consumer.ProcessOne(
		ctx,
		func(ctx context.Context, inbound platformnats.InboundMessage) error {
			firstDelivery = inbound
			if err := processor.Handle(ctx, inbound); err != nil {
				return err
			}
			return errAckLostAfterCommit
		},
	)
	if !errors.Is(err, errAckLostAfterCommit) || !processed {
		t.Fatalf("commit-before-ack ProcessOne = %t, error %v", processed, err)
	}

	var receiptRows int
	var instrumentRows int
	var commandStatus string
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM engine.input_receipts",
	).Scan(&receiptRows); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM trading.instruments",
	).Scan(&instrumentRows); err != nil {
		t.Fatalf("count instruments: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT status
		  FROM trading.commands
		 WHERE command_id = $1`,
		commandID.String(),
	).Scan(&commandStatus); err != nil {
		t.Fatalf("read command status: %v", err)
	}
	if receiptRows != 1 ||
		instrumentRows != 1 ||
		commandStatus != "accepted" {
		t.Fatalf(
			"pipeline state = receipts %d instruments %d command %s",
			receiptRows,
			instrumentRows,
			commandStatus,
		)
	}

	firstCommittedHash := processor.State().Hash()
	if err := processor.Close(ctx); err != nil {
		t.Fatalf("close processor after injected ack loss: %v", err)
	}
	restarted, err := platformnats.NewEngineProcessor(ctx, engineStore, 9)
	if err != nil {
		t.Fatalf("restart after injected ack loss: %v", err)
	}
	t.Cleanup(func() {
		_ = restarted.Close(context.Background())
	})
	var redelivery platformnats.InboundMessage
	processed, err = consumer.ProcessOne(
		ctx,
		func(ctx context.Context, inbound platformnats.InboundMessage) error {
			redelivery = inbound
			return restarted.Handle(ctx, inbound)
		},
	)
	if err != nil || !processed {
		t.Fatalf("redelivered ProcessOne = %t, error %v", processed, err)
	}
	if redelivery.StreamSequence != firstDelivery.StreamSequence ||
		redelivery.NumDelivered < 2 {
		t.Fatalf(
			"redelivery = sequence %d deliveries %d, want sequence %d and at least 2",
			redelivery.StreamSequence,
			redelivery.NumDelivered,
			firstDelivery.StreamSequence,
		)
	}
	var duplicateReceiptRows int
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM engine.duplicate_delivery_receipts",
	).Scan(&duplicateReceiptRows); err != nil {
		t.Fatalf("count same-stream duplicate receipts: %v", err)
	}
	if duplicateReceiptRows != 0 ||
		restarted.State().Hash() != firstCommittedHash ||
		restarted.State().NextStreamSequence() != 2 {
		t.Fatalf(
			"same-stream redelivery = duplicates %d hash %s next %d, want 0 %s 2",
			duplicateReceiptRows,
			restarted.State().Hash(),
			restarted.State().NextStreamSequence(),
			firstCommittedHash,
		)
	}
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM engine.input_receipts",
	).Scan(&receiptRows); err != nil {
		t.Fatalf("recount receipts: %v", err)
	}
	if err := pool.QueryRow(
		ctx,
		"SELECT count(*) FROM trading.instruments",
	).Scan(&instrumentRows); err != nil {
		t.Fatalf("recount instruments: %v", err)
	}
	if receiptRows != 1 || instrumentRows != 1 {
		t.Fatalf(
			"redelivery duplicated effects: receipts %d instruments %d",
			receiptRows,
			instrumentRows,
		)
	}

	if _, err := platformnats.NewEngineProcessor(
		ctx,
		engineStore,
		9,
	); !errors.Is(err, platformpostgres.ErrWriterConflict) {
		t.Fatalf("second active processor error = %v, want ErrWriterConflict", err)
	}
	if err := restarted.Close(ctx); err != nil {
		t.Fatalf("close restarted shard 9 processor: %v", err)
	}
	resetDurableSchemas(t, ctx, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 10); err != nil {
		t.Fatalf("Migrate duplicate probe: %v", err)
	}

	duplicateLimits := limits
	duplicateLimits.DuplicateWindow = 100 * time.Millisecond
	resetEngineShardStream(t, ctx, js, 10)
	if err := platformnats.EnsureEngineShardStream(
		ctx,
		js,
		10,
		duplicateLimits,
	); err != nil {
		t.Fatalf("EnsureEngineShardStream duplicate probe: %v", err)
	}
	duplicateAction := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "duplicate-account",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	duplicatePayload, err := engine.EncodeTradingAction(duplicateAction)
	if err != nil {
		t.Fatalf("EncodeTradingAction duplicate probe: %v", err)
	}
	duplicateInputID := engine.IDFromSequence(engine.ID{}, 122)
	duplicateTransport, err := platformnats.EncodeEngineInputMessage(
		engine.InputEnvelope{
			InputID:              duplicateInputID,
			SchemaVersion:        engine.CurrentSchemaVersion,
			ShardID:              10,
			Kind:                 engine.InputKindCommand,
			SourceID:             "duplicate-probe",
			SourceSequence:       1,
			LogicalTime:          engine.NewLogicalTime(logicalTime),
			ConfigurationVersion: 1,
			InstrumentVersion:    1,
			Payload:              duplicatePayload,
		},
	)
	if err != nil {
		t.Fatalf("EncodeEngineInputMessage duplicate probe: %v", err)
	}
	if _, err := platformpostgres.NewCommandJournal(pool).Begin(
		ctx,
		platformpostgres.BeginCommandRequest{
			Scope:            "account:duplicate-account",
			IdempotencyKey:   "duplicate-probe",
			RequestHash:      sha256.Sum256(duplicateTransport),
			CommandID:        duplicateInputID,
			AccountID:        "duplicate-account",
			AccountSequence:  1,
			CommandType:      string(duplicateAction.Kind),
			SchemaVersion:    engine.CurrentSchemaVersion,
			CanonicalPayload: duplicatePayload.Bytes(),
			OutboxSubject:    "engine.input.10.command.v1",
			OutboxPayload:    duplicateTransport,
			LogicalTime:      logicalTime,
			ExpiresAt:        now.Add(24 * time.Hour),
		},
	); err != nil {
		t.Fatalf("Begin duplicate probe command: %v", err)
	}
	duplicatePublisher := platformnats.NewPublisher(js)
	duplicateMessagingStore := platformpostgres.NewMessagingStore(pool)
	published, err = duplicateMessagingStore.PublishOutboxBatch(
		ctx,
		duplicatePublisher,
		now.Add(2*time.Second),
		10,
		time.Minute,
		time.Second,
	)
	if err != nil || published != 1 {
		t.Fatalf("publish duplicate probe first copy = %d, error %v", published, err)
	}
	var firstDuplicateSequence uint64
	if err := pool.QueryRow(ctx, `
		SELECT publish_sequence
		  FROM messaging.outbox
		 WHERE message_id = $1`,
		duplicateInputID.String(),
	).Scan(&firstDuplicateSequence); err != nil {
		t.Fatalf("read duplicate probe first sequence: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	secondDuplicateSequence, err := duplicateMessagingStore.RepublishOutbox(
		ctx,
		duplicatePublisher,
		duplicateInputID,
	)
	if err != nil {
		t.Fatalf("publish duplicate probe beyond window: %v", err)
	}
	if secondDuplicateSequence == firstDuplicateSequence {
		t.Fatal("duplicate probe was still inside the server deduplication window")
	}

	duplicateProcessor, err := platformnats.NewEngineProcessor(ctx, engineStore, 10)
	if err != nil {
		t.Fatalf("NewEngineProcessor duplicate probe: %v", err)
	}
	t.Cleanup(func() {
		_ = duplicateProcessor.Close(context.Background())
	})
	duplicateConsumer, err := platformnats.NewEnginePullConsumer(
		ctx,
		js,
		10,
		"engine-shard-10-duplicate-probe",
		time.Second,
	)
	if err != nil {
		t.Fatalf("NewEnginePullConsumer duplicate probe: %v", err)
	}
	for delivery := 1; delivery <= 2; delivery++ {
		processed, err := duplicateConsumer.ProcessOne(ctx, duplicateProcessor.Handle)
		if err != nil || !processed {
			t.Fatalf(
				"duplicate delivery %d = processed %t error %v",
				delivery,
				processed,
				err,
			)
		}
	}
	var businessReceipts int
	var duplicateReceipts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.input_receipts
		 WHERE shard_id = 10`,
	).Scan(&businessReceipts); err != nil {
		t.Fatalf("count duplicate-probe business receipts: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.duplicate_delivery_receipts
		 WHERE shard_id = 10`,
	).Scan(&duplicateReceipts); err != nil {
		t.Fatalf("count duplicate delivery receipts: %v", err)
	}
	if businessReceipts != 1 ||
		duplicateReceipts != 1 ||
		duplicateProcessor.State().NextStreamSequence() != 3 {
		t.Fatalf(
			"beyond-window duplicate = business %d duplicate %d next %d",
			businessReceipts,
			duplicateReceipts,
			duplicateProcessor.State().NextStreamSequence(),
		)
	}
	if err := duplicateProcessor.Close(ctx); err != nil {
		t.Fatalf("close duplicate processor: %v", err)
	}
	resetDurableSchemas(t, ctx, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 11); err != nil {
		t.Fatalf("Migrate poison probe: %v", err)
	}

	resetEngineShardStream(t, ctx, js, 11)
	if err := platformnats.EnsureEngineShardStream(
		ctx,
		js,
		11,
		limits,
	); err != nil {
		t.Fatalf("EnsureEngineShardStream poison probe: %v", err)
	}
	poisonID := engine.IDFromSequence(engine.ID{}, 123)
	if _, err := js.Publish(
		ctx,
		"engine.input.11.command.v1",
		[]byte(`{"unknown":"transport-envelope"}`),
		jetstream.WithMsgID(poisonID.String()),
	); err != nil {
		t.Fatalf("publish poison probe: %v", err)
	}
	poisonProcessor, err := platformnats.NewEngineProcessor(ctx, engineStore, 11)
	if err != nil {
		t.Fatalf("NewEngineProcessor poison probe: %v", err)
	}
	t.Cleanup(func() {
		_ = poisonProcessor.Close(context.Background())
	})
	poisonConsumer, err := platformnats.NewEnginePullConsumer(
		ctx,
		js,
		11,
		"engine-shard-11-poison-probe",
		time.Second,
	)
	if err != nil {
		t.Fatalf("NewEnginePullConsumer poison probe: %v", err)
	}
	processed, err = poisonConsumer.ProcessOne(ctx, poisonProcessor.Handle)
	if !processed || err == nil {
		t.Fatalf("poison delivery = processed %t error %v", processed, err)
	}
	if poisonProcessor.Ready() {
		t.Fatal("poison transport envelope left processor ready")
	}
	var poisonReceipts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.input_receipts
		 WHERE shard_id = 11`,
	).Scan(&poisonReceipts); err != nil {
		t.Fatalf("count poison-probe receipts: %v", err)
	}
	if poisonReceipts != 0 {
		t.Fatalf("poison envelope produced %d business receipts", poisonReceipts)
	}
	if err := poisonProcessor.Close(ctx); err != nil {
		t.Fatalf("close poison processor: %v", err)
	}
	restartedPoison, err := platformnats.NewEngineProcessor(ctx, engineStore, 11)
	if err != nil {
		t.Fatalf("restart poison processor: %v", err)
	}
	if restartedPoison.Ready() {
		t.Fatal("malformed transport envelope became ready after restart")
	}
	if err := restartedPoison.Close(ctx); err != nil {
		t.Fatalf("close restarted poison processor: %v", err)
	}
	var poisonFaults int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.shard_faults
		 WHERE shard_id = 11`,
	).Scan(&poisonFaults); err != nil {
		t.Fatalf("count poison-probe faults: %v", err)
	}
	if poisonFaults != 1 {
		t.Fatalf("poison envelope produced %d durable faults, want 1", poisonFaults)
	}
	resetDurableSchemas(t, ctx, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 15); err != nil {
		t.Fatalf("Migrate subject mismatch probe: %v", err)
	}

	resetEngineShardStream(t, ctx, js, 15)
	if err := platformnats.EnsureEngineShardStream(ctx, js, 15, limits); err != nil {
		t.Fatalf("EnsureEngineShardStream subject mismatch: %v", err)
	}
	subjectMismatchAction := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "subject-mismatch-account",
			OmsMode:   engine.OmsModeNetting,
		},
	}
	subjectMismatchPayload, err := engine.EncodeTradingAction(subjectMismatchAction)
	if err != nil {
		t.Fatalf("EncodeTradingAction subject mismatch: %v", err)
	}
	subjectMismatchID := engine.IDFromSequence(engine.ID{}, 124)
	subjectMismatchInput := engine.InputEnvelope{
		InputID:              subjectMismatchID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              15,
		Kind:                 engine.InputKindCommand,
		SourceID:             "command-journal",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(logicalTime),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              subjectMismatchPayload,
	}
	subjectMismatchTransport, err := platformnats.EncodeEngineInputMessage(
		subjectMismatchInput,
	)
	if err != nil {
		t.Fatalf("EncodeEngineInputMessage subject mismatch: %v", err)
	}
	if _, err := platformpostgres.NewCommandJournal(pool).Begin(
		ctx,
		platformpostgres.BeginCommandRequest{
			Scope:            "account:subject-mismatch-account",
			IdempotencyKey:   "subject-mismatch",
			RequestHash:      sha256.Sum256(subjectMismatchTransport),
			CommandID:        subjectMismatchID,
			AccountID:        "subject-mismatch-account",
			AccountSequence:  1,
			CommandType:      string(subjectMismatchAction.Kind),
			SchemaVersion:    engine.CurrentSchemaVersion,
			CanonicalPayload: subjectMismatchPayload.Bytes(),
			OutboxSubject:    "engine.input.15.command.v1",
			OutboxPayload:    subjectMismatchTransport,
			LogicalTime:      logicalTime,
			ExpiresAt:        now.Add(24 * time.Hour),
		},
	); err != nil {
		t.Fatalf("Begin subject mismatch command: %v", err)
	}
	if _, err := js.Publish(
		ctx,
		"engine.input.15.market.command-journal.v1",
		subjectMismatchTransport,
		jetstream.WithMsgID(subjectMismatchID.String()),
	); err != nil {
		t.Fatalf("publish subject mismatch: %v", err)
	}
	subjectMismatchProcessor, err := platformnats.NewEngineProcessor(
		ctx,
		engineStore,
		15,
	)
	if err != nil {
		t.Fatalf("NewEngineProcessor subject mismatch: %v", err)
	}
	subjectMismatchConsumer, err := platformnats.NewEnginePullConsumer(
		ctx,
		js,
		15,
		"engine-shard-15-subject-mismatch",
		time.Second,
	)
	if err != nil {
		t.Fatalf("NewEnginePullConsumer subject mismatch: %v", err)
	}
	processed, err = subjectMismatchConsumer.ProcessOne(
		ctx,
		subjectMismatchProcessor.Handle,
	)
	if !processed || err == nil || subjectMismatchProcessor.Ready() {
		t.Fatalf(
			"subject mismatch = processed %t ready %t error %v",
			processed,
			subjectMismatchProcessor.Ready(),
			err,
		)
	}
	if err := subjectMismatchProcessor.Close(ctx); err != nil {
		t.Fatalf("close subject mismatch processor: %v", err)
	}
	restartedSubjectMismatch, err := platformnats.NewEngineProcessor(
		ctx,
		engineStore,
		15,
	)
	if err != nil {
		t.Fatalf("restart subject mismatch processor: %v", err)
	}
	t.Cleanup(func() {
		_ = restartedSubjectMismatch.Close(context.Background())
	})
	if restartedSubjectMismatch.Ready() {
		t.Fatal("subject mismatch processor became ready after restart")
	}
	var mismatchAccounts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.accounts
		 WHERE account_id = 'subject-mismatch-account'`,
	).Scan(&mismatchAccounts); err != nil {
		t.Fatalf("count subject mismatch accounts: %v", err)
	}
	if mismatchAccounts != 0 {
		t.Fatalf("subject mismatch configured %d accounts", mismatchAccounts)
	}
	if err := restartedSubjectMismatch.Close(ctx); err != nil {
		t.Fatalf("close restarted subject mismatch processor: %v", err)
	}

	for index, poisonKind := range []string{"different", "missing", "malformed"} {
		resetDurableSchemas(t, ctx, pool)
		shardID := engine.ShardID(16 + index)
		if err := platformpostgres.NewMigrator(
			pool,
			os.DirFS(filepath.Join("..", "..", "..", "migrations")),
		).MigrateAndProvision(ctx, shardID); err != nil {
			t.Fatalf("Migrate %s ID poison probe: %v", poisonKind, err)
		}
		resetEngineShardStream(t, ctx, js, shardID)
		if err := platformnats.EnsureEngineShardStream(
			ctx,
			js,
			shardID,
			limits,
		); err != nil {
			t.Fatalf("EnsureEngineShardStream %s ID poison: %v", poisonKind, err)
		}
		accountID := "message-id-" + poisonKind
		action := engine.TradingAction{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: accountID,
				OmsMode:   engine.OmsModeNetting,
			},
		}
		payload, err := engine.EncodeTradingAction(action)
		if err != nil {
			t.Fatalf("EncodeTradingAction %s ID poison: %v", poisonKind, err)
		}
		inputID := engine.IDFromSequence(engine.ID{}, uint64(125+index))
		input := engine.InputEnvelope{
			InputID:              inputID,
			SchemaVersion:        engine.CurrentSchemaVersion,
			ShardID:              shardID,
			Kind:                 engine.InputKindCommand,
			SourceID:             "command-journal",
			SourceSequence:       1,
			LogicalTime:          engine.NewLogicalTime(logicalTime),
			ConfigurationVersion: 1,
			InstrumentVersion:    1,
			Payload:              payload,
		}
		transport, err := platformnats.EncodeEngineInputMessage(input)
		if err != nil {
			t.Fatalf("EncodeEngineInputMessage %s ID poison: %v", poisonKind, err)
		}
		subject := fmt.Sprintf("engine.input.%d.command.v1", shardID)
		switch poisonKind {
		case "different":
			_, err = js.Publish(
				ctx,
				subject,
				transport,
				jetstream.WithMsgID(
					engine.IDFromSequence(engine.ID{}, uint64(135+index)).String(),
				),
			)
		case "missing":
			_, err = js.Publish(ctx, subject, transport)
		case "malformed":
			message := gonats.NewMsg(subject)
			message.Data = transport
			message.Header.Set(gonats.MsgIdHdr, "not-a-canonical-id")
			_, err = js.PublishMsg(ctx, message)
		}
		if err != nil {
			t.Fatalf("publish %s ID poison: %v", poisonKind, err)
		}
		processor, err := platformnats.NewEngineProcessor(ctx, engineStore, shardID)
		if err != nil {
			t.Fatalf("NewEngineProcessor %s ID poison: %v", poisonKind, err)
		}
		consumer, err := platformnats.NewEnginePullConsumer(
			ctx,
			js,
			shardID,
			fmt.Sprintf("engine-shard-%d-message-id-%s", shardID, poisonKind),
			time.Second,
		)
		if err != nil {
			t.Fatalf("NewEnginePullConsumer %s ID poison: %v", poisonKind, err)
		}
		processed, err := consumer.ProcessOne(ctx, processor.Handle)
		if !processed || err == nil || processor.Ready() {
			t.Fatalf(
				"%s ID poison = processed %t ready %t error %v",
				poisonKind,
				processed,
				processor.Ready(),
				err,
			)
		}
		if err := processor.Close(ctx); err != nil {
			t.Fatalf("close %s ID poison processor: %v", poisonKind, err)
		}
		restarted, err := platformnats.NewEngineProcessor(ctx, engineStore, shardID)
		if err != nil {
			t.Fatalf("restart %s ID poison processor: %v", poisonKind, err)
		}
		if restarted.Ready() {
			t.Fatalf("%s ID poison processor became ready after restart", poisonKind)
		}
		if err := restarted.Close(ctx); err != nil {
			t.Fatalf("close restarted %s ID poison processor: %v", poisonKind, err)
		}
		var faults int
		var accounts int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			  FROM engine.shard_faults
			 WHERE shard_id = $1`,
			int64(shardID),
		).Scan(&faults); err != nil {
			t.Fatalf("count %s ID poison faults: %v", poisonKind, err)
		}
		if err := pool.QueryRow(ctx, `
			SELECT count(*)
			  FROM trading.accounts
			 WHERE account_id = $1`,
			accountID,
		).Scan(&accounts); err != nil {
			t.Fatalf("count %s ID poison accounts: %v", poisonKind, err)
		}
		if faults != 1 || accounts != 0 {
			t.Fatalf(
				"%s ID poison = faults %d accounts %d, want 1 and 0",
				poisonKind,
				faults,
				accounts,
			)
		}
	}
}

func TestLaterAccountCommandCannotBypassOrderedPublication(t *testing.T) {
	natsURL := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if natsURL == "" || postgresDSN == "" {
		t.Skip("NATS and PostgreSQL integration URLs are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	resetDurableSchemas(t, ctx, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 14); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	connection, err := gonats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatalf("create JetStream context: %v", err)
	}
	limits := platformnats.StreamLimits{
		Replicas:        1,
		MaxMessages:     100,
		MaxBytes:        8 << 20,
		MaxMessageBytes: 1 << 20,
		MaxAge:          time.Hour,
		DuplicateWindow: time.Minute,
	}
	resetEngineShardStream(t, ctx, js, 14)
	if err := platformnats.EnsureEngineShardStream(ctx, js, 14, limits); err != nil {
		t.Fatalf("EnsureEngineShardStream: %v", err)
	}

	logicalTime := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	commandIDs := []engine.ID{
		engine.IDFromSequence(engine.ID{}, 701),
		engine.IDFromSequence(engine.ID{}, 702),
	}
	actions := []engine.TradingAction{
		{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: "account-ordered",
				OmsMode:   engine.OmsModeNetting,
			},
		},
		{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: "account-ordered",
				OmsMode:   engine.OmsModeHedging,
			},
		},
	}
	transports := make([][]byte, len(actions))
	journal := platformpostgres.NewCommandJournal(pool)
	for index, action := range actions {
		payload, err := engine.EncodeTradingAction(action)
		if err != nil {
			t.Fatalf("EncodeTradingAction %d: %v", index+1, err)
		}
		input := engine.InputEnvelope{
			InputID:              commandIDs[index],
			SchemaVersion:        engine.CurrentSchemaVersion,
			ShardID:              14,
			Kind:                 engine.InputKindCommand,
			SourceID:             "command-journal",
			SourceSequence:       uint64(index + 1),
			MarketSequence:       uint64(index + 1),
			LogicalTime:          engine.NewLogicalTime(logicalTime.Add(time.Duration(index) * time.Second)),
			ConfigurationVersion: 1,
			InstrumentVersion:    1,
			Payload:              payload,
		}
		transports[index], err = platformnats.EncodeEngineInputMessage(input)
		if err != nil {
			t.Fatalf("EncodeEngineInputMessage %d: %v", index+1, err)
		}
		if _, err := journal.Begin(ctx, platformpostgres.BeginCommandRequest{
			Scope:            "account:account-ordered",
			IdempotencyKey:   commandIDs[index].String(),
			RequestHash:      sha256.Sum256(transports[index]),
			CommandID:        commandIDs[index],
			AccountID:        "account-ordered",
			AccountSequence:  uint64(index + 1),
			CommandType:      string(action.Kind),
			SchemaVersion:    engine.CurrentSchemaVersion,
			CanonicalPayload: payload.Bytes(),
			OutboxSubject:    "engine.input.14.command.v1",
			OutboxPayload:    transports[index],
			LogicalTime:      logicalTime.Add(time.Duration(index) * time.Second),
			ExpiresAt:        logicalTime.Add(24 * time.Hour),
		}); err != nil {
			t.Fatalf("Begin command %d: %v", index+1, err)
		}
	}

	publisher := platformnats.NewPublisher(js)
	messagingStore := platformpostgres.NewMessagingStore(pool)
	mutating := &mutatingCommandPublisher{
		delegate: publisher,
		replacement: platformpostgres.OutboxMessage{
			MessageID:     commandIDs[1],
			Subject:       "engine.input.14.command.v1",
			SchemaVersion: engine.CurrentSchemaVersion,
			Payload:       transports[1],
		},
	}
	published, err := messagingStore.PublishOutboxBatch(
		ctx,
		mutating,
		time.Now().UTC(),
		10,
		time.Minute,
		time.Second,
	)
	if !errors.Is(err, platformnats.ErrUnauthorizedEngineInputPublication) ||
		published != 0 {
		t.Fatalf(
			"mutated ordered publish = %d, error %v, want rejected",
			published,
			err,
		)
	}
	var publishedRows int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM messaging.outbox
		 WHERE published_at IS NOT NULL`,
	).Scan(&publishedRows); err != nil {
		t.Fatalf("count published outbox rows: %v", err)
	}
	if publishedRows != 0 {
		t.Fatalf("mutated claim published %d outbox rows", publishedRows)
	}
	if _, err := publisher.Publish(ctx, platformpostgres.OutboxMessage{
		MessageID: commandIDs[1],
		Subject:   "engine.input.14.command.v1",
		Payload:   transports[1],
	}); !errors.Is(err, platformnats.ErrUnauthorizedEngineInputPublication) {
		t.Fatalf(
			"direct sequence 2 publish error = %v, want ErrUnauthorizedEngineInputPublication",
			err,
		)
	}

	for sequence := 1; sequence <= 2; sequence++ {
		published, err := messagingStore.PublishOutboxBatch(
			ctx,
			publisher,
			time.Now().UTC().Add(time.Duration(sequence+2)*time.Second),
			10,
			time.Minute,
			time.Second,
		)
		if err != nil || published != 1 {
			t.Fatalf(
				"ordered publish %d = %d, error %v",
				sequence,
				published,
				err,
			)
		}
		var streamSequence uint64
		if err := pool.QueryRow(ctx, `
			SELECT publish_sequence
			  FROM messaging.outbox
			 WHERE message_id = $1`,
			commandIDs[sequence-1].String(),
		).Scan(&streamSequence); err != nil {
			t.Fatalf("read command %d publish sequence: %v", sequence, err)
		}
		if streamSequence != uint64(sequence) {
			t.Fatalf(
				"command %d stream sequence = %d, want %d",
				sequence,
				streamSequence,
				sequence,
			)
		}
	}

	engineStore := platformpostgres.NewEngineStore(pool)
	processor, err := platformnats.NewEngineProcessor(ctx, engineStore, 14)
	if err != nil {
		t.Fatalf("NewEngineProcessor: %v", err)
	}
	t.Cleanup(func() {
		_ = processor.Close(context.Background())
	})
	consumer, err := platformnats.NewEnginePullConsumer(
		ctx,
		js,
		14,
		"engine-shard-14-ordered-command",
		time.Second,
	)
	if err != nil {
		t.Fatalf("NewEnginePullConsumer: %v", err)
	}
	for sequence := 1; sequence <= 2; sequence++ {
		processed, err := consumer.ProcessOne(ctx, processor.Handle)
		if err != nil || !processed {
			t.Fatalf(
				"ProcessOne command %d = %t, error %v",
				sequence,
				processed,
				err,
			)
		}
	}
	var terminalCommands int
	var receipts int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.commands
		 WHERE status IN ('accepted', 'rejected')`,
	).Scan(&terminalCommands); err != nil {
		t.Fatalf("count terminal commands: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.input_receipts
		 WHERE shard_id = 14`,
	).Scan(&receipts); err != nil {
		t.Fatalf("count receipts: %v", err)
	}
	if terminalCommands != 2 ||
		receipts != 2 ||
		processor.State().NextStreamSequence() != 3 {
		t.Fatalf(
			"ordered result = commands %d receipts %d next %d",
			terminalCommands,
			receipts,
			processor.State().NextStreamSequence(),
		)
	}
}

func TestEngineProcessorFailsClosedAfterOwnershipSessionLoss(t *testing.T) {
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if postgresDSN == "" {
		t.Skip("PostgreSQL integration URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	resetDurableSchemas(t, ctx, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 29); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const shardID engine.ShardID = 29
	store := platformpostgres.NewEngineStore(pool)
	former, err := platformnats.NewEngineProcessor(ctx, store, shardID)
	if err != nil {
		t.Fatalf("NewEngineProcessor former owner: %v", err)
	}
	t.Cleanup(func() {
		_ = former.Close(context.Background())
	})

	var ownerPID uint32
	if err := pool.QueryRow(ctx, `
		SELECT pid
		  FROM pg_locks
		 WHERE locktype = 'advisory'
		   AND objid = $1::oid
		   AND granted`,
		uint32(shardID),
	).Scan(&ownerPID); err != nil {
		t.Fatalf("find shard owner backend: %v", err)
	}
	var terminated bool
	if err := pool.QueryRow(
		ctx,
		"SELECT pg_terminate_backend($1)",
		ownerPID,
	).Scan(&terminated); err != nil || !terminated {
		t.Fatalf("terminate shard owner backend = %t, error %v", terminated, err)
	}

	replacement, err := platformnats.NewEngineProcessor(ctx, store, shardID)
	if err != nil {
		t.Fatalf("NewEngineProcessor replacement owner: %v", err)
	}
	t.Cleanup(func() {
		_ = replacement.Close(context.Background())
	})
	if former.Ready() {
		t.Fatal("former processor remained ready after ownership session loss")
	}
	if !replacement.Ready() {
		t.Fatal("replacement processor did not become ready")
	}
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureInstrument,
		ConfigureInstrument: &engine.ConfigureInstrument{
			InstrumentID:            "LOCK-LOSS-PERP",
			Revision:                1,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 2,
			InitialMarginRate:       "0.1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             "10",
			MakerFeeRate:            "0",
			TakerFeeRate:            "0",
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	inputID := engine.IDFromSequence(engine.ID{}, 291)
	encoded, err := platformnats.EncodeEngineInputMessage(engine.InputEnvelope{
		InputID:              inputID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              shardID,
		Kind:                 engine.InputKindConfiguration,
		SourceID:             "configuration",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(time.Unix(1, 0).UTC()),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	})
	if err != nil {
		t.Fatalf("EncodeEngineInputMessage: %v", err)
	}
	inbound := platformnats.InboundMessage{
		MessageID:      inputID,
		Subject:        "engine.input.29.config.v1",
		Data:           encoded,
		StreamSequence: 1,
	}
	err = former.Handle(ctx, inbound)
	if !errors.Is(err, platformpostgres.ErrWriterConflict) {
		t.Fatalf("former Handle error = %v, want ErrWriterConflict", err)
	}
	assertNATSPipelineRowCount(t, ctx, pool, "engine.shard_faults", 0)
	assertNATSPipelineRowCount(t, ctx, pool, "engine.shard_checkpoints", 0)
	if err := replacement.Handle(ctx, inbound); err != nil {
		t.Fatalf("replacement Handle: %v", err)
	}
	assertNATSPipelineRowCount(t, ctx, pool, "engine.input_receipts", 1)
	assertNATSPipelineRowCount(t, ctx, pool, "engine.shard_checkpoints", 1)
	assertNATSPipelineRowCount(t, ctx, pool, "trading.instruments", 1)
}

func TestReconciliationCannotWriteAroundActiveProcessor(t *testing.T) {
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if postgresDSN == "" {
		t.Skip("PostgreSQL integration URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	resetDurableSchemas(t, ctx, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 30); err != nil {
		t.Fatalf("Migrate: %v", err)
	}

	const shardID engine.ShardID = 30
	store := platformpostgres.NewEngineStore(pool)
	processor, err := platformnats.NewEngineProcessor(ctx, store, shardID)
	if err != nil {
		t.Fatalf("NewEngineProcessor: %v", err)
	}
	closed := false
	t.Cleanup(func() {
		if !closed {
			_ = processor.Close(context.Background())
		}
	})
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureInstrument,
		ConfigureInstrument: &engine.ConfigureInstrument{
			InstrumentID:            "RECONCILE-PERP",
			Revision:                1,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 2,
			InitialMarginRate:       "0.1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             "10",
			MakerFeeRate:            "0",
			TakerFeeRate:            "0",
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	inputID := engine.IDFromSequence(engine.ID{}, 301)
	encoded, err := platformnats.EncodeEngineInputMessage(engine.InputEnvelope{
		InputID:              inputID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              shardID,
		Kind:                 engine.InputKindConfiguration,
		SourceID:             "configuration",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(time.Unix(1, 0).UTC()),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	})
	if err != nil {
		t.Fatalf("EncodeEngineInputMessage: %v", err)
	}
	inbound := platformnats.InboundMessage{
		MessageID:      inputID,
		Subject:        "engine.input.30.config.v1",
		Data:           encoded,
		StreamSequence: 1,
	}
	healthDone := make(chan struct{})
	healthStopped := make(chan struct{})
	go func() {
		defer close(healthStopped)
		for {
			select {
			case <-healthDone:
				return
			default:
				_ = processor.Ready()
				_ = processor.State()
			}
		}
	}()
	if err := processor.Handle(ctx, inbound); err != nil {
		t.Fatalf("Handle: %v", err)
	}
	close(healthDone)
	<-healthStopped
	if _, err := pool.Exec(ctx, `
		UPDATE trading.instruments
		   SET revision = revision + 1
		 WHERE instrument_id = 'RECONCILE-PERP'`,
	); err != nil {
		t.Fatalf("corrupt instrument projection: %v", err)
	}

	if _, err := store.ReconcileShard(ctx, shardID); !errors.Is(
		err,
		platformpostgres.ErrWriterConflict,
	) {
		t.Fatalf("live-owner reconciliation error = %v, want ErrWriterConflict", err)
	}
	if !processor.Ready() {
		t.Fatal("processor became unready even though reconciliation committed nothing")
	}
	assertNATSPipelineRowCount(t, ctx, pool, "engine.shard_faults", 0)
	if err := processor.Close(ctx); err != nil {
		t.Fatalf("close active processor: %v", err)
	}
	closed = true

	report, err := store.ReconcileShard(ctx, shardID)
	if !errors.Is(err, platformpostgres.ErrReconciliationMismatch) ||
		report.Ready ||
		report.ConfigurationMismatchCount == 0 {
		t.Fatalf("offline reconciliation = %+v, error %v", report, err)
	}
	assertNATSPipelineRowCount(t, ctx, pool, "engine.shard_faults", 1)
	restarted, err := platformnats.NewEngineProcessor(ctx, store, shardID)
	if err != nil {
		t.Fatalf("restart after reconciliation halt: %v", err)
	}
	t.Cleanup(func() {
		_ = restarted.Close(context.Background())
	})
	if restarted.Ready() {
		t.Fatal("restarted processor became ready after reconciliation halt")
	}
}

func TestAPIOutboxCannotPublishNonCommandEngineInput(t *testing.T) {
	natsURL := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if natsURL == "" || postgresDSN == "" {
		t.Skip("NATS and PostgreSQL integration URLs are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	resetDurableSchemas(t, ctx, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, 23); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	connection, err := gonats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatalf("create JetStream context: %v", err)
	}
	limits := platformnats.StreamLimits{
		Replicas:        1,
		MaxMessages:     100,
		MaxBytes:        1 << 20,
		MaxMessageBytes: 1 << 20,
		MaxAge:          time.Hour,
		DuplicateWindow: time.Minute,
	}
	const shardID engine.ShardID = 23
	resetEngineShardStream(t, ctx, js, shardID)
	if err := platformnats.EnsureEngineShardStream(ctx, js, shardID, limits); err != nil {
		t.Fatalf("EnsureEngineShardStream: %v", err)
	}

	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureInstrument,
		ConfigureInstrument: &engine.ConfigureInstrument{
			InstrumentID:            "API-FORGE-PERP",
			Revision:                1,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 2,
			InitialMarginRate:       "0.1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             "10",
			MakerFeeRate:            "0",
			TakerFeeRate:            "0",
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	inputID := engine.IDFromSequence(engine.ID{}, 231)
	encoded, err := platformnats.EncodeEngineInputMessage(engine.InputEnvelope{
		InputID:              inputID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              shardID,
		Kind:                 engine.InputKindConfiguration,
		SourceID:             "api-forge",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(time.Unix(1, 0).UTC()),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              payload,
	})
	if err != nil {
		t.Fatalf("EncodeEngineInputMessage: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin API transaction: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE platformgo_api"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("set API role: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES ($1, 'engine.input.23.config.v1', 1, $2)`,
		inputID.String(),
		encoded,
	); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("insert forged API outbox row: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit forged API outbox row: %v", err)
	}

	publishNow := time.Now().UTC().Add(time.Second)
	messagingStore := platformpostgres.NewMessagingStore(pool)
	publisher := platformnats.NewPublisher(js)
	published, err := messagingStore.PublishOutboxBatch(
		ctx,
		publisher,
		publishNow,
		1,
		time.Minute,
		time.Second,
	)
	if published != 0 ||
		!errors.Is(err, platformnats.ErrUnauthorizedEngineInputPublication) {
		t.Fatalf("forged publish = %d, error %v", published, err)
	}
	var publishedAt *time.Time
	var publishSequence *uint64
	if err := pool.QueryRow(ctx, `
		SELECT published_at, publish_sequence
		  FROM messaging.outbox
		 WHERE message_id = $1`,
		inputID.String(),
	).Scan(&publishedAt, &publishSequence); err != nil {
		t.Fatalf("inspect forged outbox row: %v", err)
	}
	if publishedAt != nil || publishSequence != nil {
		t.Fatalf(
			"forged outbox publication = published_at %v sequence %v",
			publishedAt,
			publishSequence,
		)
	}
	stream, err := js.Stream(ctx, platformnats.EngineInputsStream+"_23")
	if err != nil {
		t.Fatalf("open engine stream: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("inspect engine stream: %v", err)
	}
	if info.State.Msgs != 0 {
		t.Fatalf("forged engine stream messages = %d, want 0", info.State.Msgs)
	}
	assertNATSPipelineRowCount(t, ctx, pool, "engine.input_receipts", 0)
	assertNATSPipelineRowCount(t, ctx, pool, "engine.shard_checkpoints", 0)
	assertNATSPipelineRowCount(t, ctx, pool, "trading.instruments", 0)
}

func TestAPIOutboxCannotPublishDomainEvent(t *testing.T) {
	natsURL := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if natsURL == "" || postgresDSN == "" {
		t.Skip("NATS and PostgreSQL integration URLs are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	resetDurableSchemas(t, ctx, pool)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("Migrate: %v", err)
	}
	connection, err := gonats.Connect(natsURL)
	if err != nil {
		t.Fatalf("connect NATS: %v", err)
	}
	t.Cleanup(connection.Close)
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatalf("create JetStream context: %v", err)
	}
	limits := platformnats.StreamLimits{
		Replicas:        1,
		MaxMessages:     100,
		MaxBytes:        1 << 20,
		MaxMessageBytes: 1 << 20,
		MaxAge:          time.Hour,
		DuplicateWindow: time.Minute,
	}
	if err := platformnats.EnsureStreams(ctx, js, limits); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
	messageID := engine.IDFromSequence(engine.ID{}, 232)
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin API transaction: %v", err)
	}
	if _, err := tx.Exec(ctx, "SET LOCAL ROLE platformgo_api"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("set API role: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload
		) VALUES (
			$1, 'domain.v1.order.filled', 1,
			'{"messageId":"forged"}'::jsonb
		)`,
		messageID.String(),
	); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("insert forged API domain event: %v", err)
	}
	if _, err := tx.Exec(ctx, "SAVEPOINT producer_class_probe"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("create producer-class savepoint: %v", err)
	}
	if _, err := tx.Exec(ctx, `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload, producer_class
		) VALUES (
			$1, 'domain.v1.order.filled', 1,
			'{"messageId":"forged"}'::jsonb, 'engine'
		)`,
		engine.IDFromSequence(engine.ID{}, 233).String(),
	); err == nil {
		_ = tx.Rollback(ctx)
		t.Fatal("API role set engine producer class")
	}
	if _, err := tx.Exec(ctx, "ROLLBACK TO SAVEPOINT producer_class_probe"); err != nil {
		_ = tx.Rollback(ctx)
		t.Fatalf("rollback producer-class probe: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit forged API domain event: %v", err)
	}

	publishNow := time.Now().UTC().Add(time.Second)
	messagingStore := platformpostgres.NewMessagingStore(pool)
	publisher := platformnats.NewPublisher(js)
	published, err := messagingStore.PublishOutboxBatch(
		ctx,
		publisher,
		publishNow,
		1,
		time.Minute,
		time.Second,
	)
	if published != 0 ||
		!errors.Is(err, platformnats.ErrUnauthorizedDomainEventPublication) {
		t.Fatalf("forged domain publish = %d, error %v", published, err)
	}
	stream, err := js.Stream(ctx, platformnats.DomainEventsStream)
	if err != nil {
		t.Fatalf("open domain stream: %v", err)
	}
	info, err := stream.Info(ctx)
	if err != nil {
		t.Fatalf("inspect domain stream: %v", err)
	}
	if info.State.Msgs != 0 {
		t.Fatalf("forged domain stream messages = %d, want 0", info.State.Msgs)
	}

	if _, err := pool.Exec(ctx, `
		UPDATE messaging.outbox
		   SET next_attempt_at = clock_timestamp() + interval '1 hour'
		 WHERE message_id = $1`,
		messageID.String(),
	); err != nil {
		t.Fatalf("defer API event retry: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER TABLE messaging.outbox
			DROP CONSTRAINT outbox_engine_receipt_fkey`,
	); err != nil {
		t.Fatalf("prepare unbound engine-event corruption: %v", err)
	}
	unboundMessageID := engine.IDFromSequence(engine.ID{}, 234)
	unboundInputID := engine.IDFromSequence(engine.ID{}, 235)
	if _, err := pool.Exec(ctx, `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload, producer_class,
			engine_shard_id, engine_input_id
		) VALUES (
			$1, 'domain.v1.order.filled', 1,
			jsonb_build_object(
				'messageId', $1::uuid::text,
				'correlationId', $2::uuid::text
			),
			'engine', 99, $2
		)`,
		unboundMessageID.String(),
		unboundInputID.String(),
	); err != nil {
		t.Fatalf("insert unbound engine-event corruption: %v", err)
	}
	published, err = messagingStore.PublishOutboxBatch(
		ctx,
		publisher,
		publishNow.Add(2*time.Second),
		1,
		time.Minute,
		time.Second,
	)
	if published != 0 ||
		!errors.Is(err, platformnats.ErrUnauthorizedDomainEventPublication) {
		t.Fatalf("unbound engine domain publish = %d, error %v", published, err)
	}
	info, err = stream.Info(ctx)
	if err != nil {
		t.Fatalf("inspect domain stream after unbound event: %v", err)
	}
	if info.State.Msgs != 0 {
		t.Fatalf("unbound engine event published %d messages", info.State.Msgs)
	}
}

func TestMalformedTransportInputsRemainDurablyHaltedAfterRestart(t *testing.T) {
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if postgresDSN == "" {
		t.Skip("PostgreSQL integration URL is required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	actionPayload, err := engine.EncodeTradingAction(engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: "transport-poison",
			OmsMode:   engine.OmsModeNetting,
		},
	})
	if err != nil {
		t.Fatalf("EncodeTradingAction: %v", err)
	}
	tests := []struct {
		name        string
		mutate      func(engine.InputMessage, []byte) []byte
		headerError error
	}{
		{
			name:   "malformed JSON",
			mutate: func(engine.InputMessage, []byte) []byte { return []byte(`{`) },
		},
		{
			name: "unknown field",
			mutate: func(engine.InputMessage, []byte) []byte {
				return []byte(`{"unknown":"transport-envelope"}`)
			},
		},
		{
			name: "multiple JSON values",
			mutate: func(_ engine.InputMessage, encoded []byte) []byte {
				return append(encoded, []byte(`{}`)...)
			},
		},
		{
			name: "unknown kind",
			mutate: func(message engine.InputMessage, _ []byte) []byte {
				message.Kind = "unknown"
				encoded, _ := json.Marshal(message)
				return encoded
			},
		},
		{
			name: "unknown schema",
			mutate: func(message engine.InputMessage, _ []byte) []byte {
				message.SchemaVersion = 99
				encoded, _ := json.Marshal(message)
				return encoded
			},
		},
		{
			name: "invalid ID",
			mutate: func(message engine.InputMessage, _ []byte) []byte {
				message.MessageID = "invalid"
				encoded, _ := json.Marshal(message)
				return encoded
			},
		},
		{
			name: "invalid logical time",
			mutate: func(message engine.InputMessage, _ []byte) []byte {
				message.LogicalTime = "not-a-time"
				encoded, _ := json.Marshal(message)
				return encoded
			},
		},
		{
			name: "invalid action payload",
			mutate: func(message engine.InputMessage, _ []byte) []byte {
				message.CanonicalActionPayload = []byte(`{`)
				encoded, _ := json.Marshal(message)
				return encoded
			},
		},
		{
			name:        "missing header",
			mutate:      func(_ engine.InputMessage, encoded []byte) []byte { return encoded },
			headerError: errors.New("missing Nats-Msg-Id"),
		},
		{
			name:        "malformed header",
			mutate:      func(_ engine.InputMessage, encoded []byte) []byte { return encoded },
			headerError: errors.New("invalid canonical Nats-Msg-Id"),
		},
	}
	store := platformpostgres.NewEngineStore(pool)
	for index, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			resetDurableSchemas(t, ctx, pool)
			shardID := engine.ShardID(40 + index)
			if err := platformpostgres.NewMigrator(
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).MigrateAndProvision(ctx, shardID); err != nil {
				t.Fatalf("Migrate: %v", err)
			}
			inputID := engine.IDFromSequence(engine.ID{}, uint64(400+index))
			message := engine.InputMessage{
				MessageID:              inputID.String(),
				SchemaVersion:          engine.CurrentSchemaVersion,
				ShardID:                uint32(shardID),
				Kind:                   "command",
				SourceID:               "command-journal",
				SourceSequence:         1,
				LogicalTime:            time.Unix(1, 0).UTC().Format(time.RFC3339Nano),
				ConfigurationVersion:   1,
				InstrumentVersion:      1,
				CanonicalActionPayload: actionPayload.Bytes(),
			}
			encoded, err := json.Marshal(message)
			if err != nil {
				t.Fatalf("marshal valid transport envelope: %v", err)
			}
			inbound := platformnats.InboundMessage{
				MessageID:      inputID,
				MessageIDError: test.headerError,
				Subject:        fmt.Sprintf("engine.input.%d.command.v1", shardID),
				Data:           test.mutate(message, encoded),
				StreamSequence: 1,
			}
			if test.headerError != nil {
				inbound.MessageID = engine.ID{}
			}
			processor, err := platformnats.NewEngineProcessor(ctx, store, shardID)
			if err != nil {
				t.Fatalf("NewEngineProcessor: %v", err)
			}
			if err := processor.Handle(ctx, inbound); err == nil || processor.Ready() {
				t.Fatalf("poison Handle error = %v, ready %t", err, processor.Ready())
			}
			if err := processor.Close(ctx); err != nil {
				t.Fatalf("close poison processor: %v", err)
			}
			restarted, err := platformnats.NewEngineProcessor(ctx, store, shardID)
			if err != nil {
				t.Fatalf("restart poison processor: %v", err)
			}
			if restarted.Ready() {
				t.Fatal("poison processor became ready after restart")
			}
			if err := restarted.Handle(ctx, inbound); !errors.Is(err, engine.ErrShardNotReady) {
				t.Fatalf("poison redelivery error = %v, want ErrShardNotReady", err)
			}
			if err := restarted.Close(ctx); err != nil {
				t.Fatalf("close restarted poison processor: %v", err)
			}
			var faults int
			var detail string
			if err := pool.QueryRow(ctx, `
				SELECT count(*), min(error_detail)
				  FROM engine.shard_faults
				 WHERE shard_id = $1`,
				int64(shardID),
			).Scan(&faults, &detail); err != nil {
				t.Fatalf("inspect durable poison fault: %v", err)
			}
			if faults != 1 ||
				!strings.Contains(detail, "body_sha256=") ||
				!strings.Contains(detail, "subject=") {
				t.Fatalf("durable poison fault = count %d detail %q", faults, detail)
			}
		})
	}
}

func resetDurableSchemas(t *testing.T, ctx context.Context, pool *pgxpool.Pool) {
	t.Helper()

	if err := postgresfixture.ResetDurableSchemas(ctx, pool); err != nil {
		t.Fatalf("reset durable schemas: %v", err)
	}
	if _, err := pool.Exec(
		ctx,
		`DO $$
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
		END;
		$$`,
	); err != nil {
		t.Fatalf("provision test runtime roles: %v", err)
	}
}

func assertNATSPipelineRowCount(
	t *testing.T,
	ctx context.Context,
	pool *pgxpool.Pool,
	table string,
	want int,
) {
	t.Helper()
	var got int
	if err := pool.QueryRow(ctx, "SELECT count(*) FROM "+table).Scan(&got); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	if got != want {
		t.Fatalf("%s rows = %d, want %d", table, got, want)
	}
}

type mutatingCommandPublisher struct {
	delegate    *platformnats.Publisher
	replacement platformpostgres.OutboxMessage
}

func (publisher *mutatingCommandPublisher) Publish(
	ctx context.Context,
	message platformpostgres.OutboxMessage,
) (uint64, error) {
	message.MessageID = publisher.replacement.MessageID
	message.Subject = publisher.replacement.Subject
	message.SchemaVersion = publisher.replacement.SchemaVersion
	message.Payload = append([]byte(nil), publisher.replacement.Payload...)
	return publisher.delegate.Publish(ctx, message)
}
