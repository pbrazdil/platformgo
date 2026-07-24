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
	duplicateMessage := platformpostgres.OutboxMessage{
		MessageID: duplicateInputID,
		Subject:   "engine.input.10.command.v1",
		Payload:   duplicateTransport,
	}
	firstDuplicateSequence, err := platformnats.NewPublisher(js).Publish(
		ctx,
		duplicateMessage,
	)
	if err != nil {
		t.Fatalf("publish duplicate probe first copy: %v", err)
	}
	time.Sleep(150 * time.Millisecond)
	secondDuplicateSequence, err := platformnats.NewPublisher(js).Publish(
		ctx,
		duplicateMessage,
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
	if _, err := platformnats.NewPublisher(js).Publish(
		ctx,
		platformpostgres.OutboxMessage{
			MessageID: poisonID,
			Subject:   "engine.input.11.command.v1",
			Payload:   []byte(`{"unknown":"transport-envelope"}`),
		},
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
}
