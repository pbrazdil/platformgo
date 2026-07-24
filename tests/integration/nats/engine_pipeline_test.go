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
}
