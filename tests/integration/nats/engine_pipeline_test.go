package nats_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	gonats "github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestCommandOutboxJetStreamEnginePostgresPipeline(t *testing.T) {
	natsURL := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if natsURL == "" || postgresDSN == "" {
		t.Skip("NATS and PostgreSQL integration URLs are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(
		ctx,
		`DROP SCHEMA IF EXISTS market, messaging, ledger, trading, engine CASCADE`,
	); err != nil {
		t.Fatalf("reset durable schemas: %v", err)
	}
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
		MaxMessages:     10_000,
		MaxBytes:        64 << 20,
		MaxMessageBytes: 1 << 20,
		MaxAge:          time.Hour,
		DuplicateWindow: time.Minute,
	}
	if err := platformnats.EnsureStreams(ctx, js, limits); err != nil {
		t.Fatalf("EnsureStreams: %v", err)
	}
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
	processed, err := consumer.ProcessOne(ctx, processor.Handle)
	if err != nil || !processed {
		t.Fatalf("ProcessOne = %t, error %v", processed, err)
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

	if _, err := platformnats.NewEngineProcessor(
		ctx,
		engineStore,
		9,
	); !errors.Is(err, platformpostgres.ErrWriterConflict) {
		t.Fatalf("second active processor error = %v, want ErrWriterConflict", err)
	}
	if err := processor.Close(ctx); err != nil {
		t.Fatalf("close first engine processor: %v", err)
	}
	restarted, err := platformnats.NewEngineProcessor(ctx, engineStore, 9)
	if err != nil {
		t.Fatalf("restart NewEngineProcessor: %v", err)
	}
	t.Cleanup(func() {
		_ = restarted.Close(context.Background())
	})
	if restarted.State().Hash() != processor.State().Hash() ||
		restarted.State().NextStreamSequence() != 2 {
		t.Fatalf(
			"restarted state = hash %s next %d, want %s and 2",
			restarted.State().Hash(),
			restarted.State().NextStreamSequence(),
			processor.State().Hash(),
		)
	}

	duplicateLimits := limits
	duplicateLimits.DuplicateWindow = 100 * time.Millisecond
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
}

func TestLaterAccountCommandCannotBypassOrderedPublication(t *testing.T) {
	natsURL := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	postgresDSN := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if natsURL == "" || postgresDSN == "" {
		t.Skip("NATS and PostgreSQL integration URLs are required")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	pool, err := pgxpool.New(ctx, postgresDSN)
	if err != nil {
		t.Fatalf("open PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	if _, err := pool.Exec(
		ctx,
		`DROP SCHEMA IF EXISTS market, messaging, ledger, trading, engine CASCADE`,
	); err != nil {
		t.Fatalf("reset durable schemas: %v", err)
	}
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
		MaxBytes:        8 << 20,
		MaxMessageBytes: 1 << 20,
		MaxAge:          time.Hour,
		DuplicateWindow: time.Minute,
	}
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
	if !errors.Is(err, platformnats.ErrUnorderedCommandPublication) ||
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
	}); !errors.Is(err, platformnats.ErrUnorderedCommandPublication) {
		t.Fatalf(
			"direct sequence 2 publish error = %v, want ErrUnorderedCommandPublication",
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
