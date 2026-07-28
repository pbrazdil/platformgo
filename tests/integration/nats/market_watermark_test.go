package nats_test

import (
	"context"
	"crypto/sha256"
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
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

func TestOrderSubmissionBindsCommittedMarketWatermarkThroughJetStream(
	t *testing.T,
) {
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
	const shardID = engine.ShardID(31)
	if err := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).MigrateAndProvision(ctx, shardID); err != nil {
		t.Fatalf("migrate market-watermark database: %v", err)
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
		DuplicateWindow: 100 * time.Millisecond,
	}
	if err := platformnats.EnsureStreams(ctx, js, limits); err != nil {
		t.Fatalf("ensure shared streams: %v", err)
	}
	resetEngineShardStream(t, ctx, js, shardID)
	if err := platformnats.EnsureEngineShardStream(
		ctx,
		js,
		shardID,
		limits,
	); err != nil {
		t.Fatalf("ensure engine shard stream: %v", err)
	}

	store := platformpostgres.NewEngineStore(pool)
	processor, err := platformnats.NewEngineProcessor(ctx, store, shardID)
	if err != nil {
		t.Fatalf("create engine processor: %v", err)
	}
	t.Cleanup(func() {
		_ = processor.Close(context.Background())
	})
	consumer, err := platformnats.NewEnginePullConsumer(
		ctx,
		js,
		shardID,
		"engine-shard-31-market-watermark",
		time.Second,
	)
	if err != nil {
		t.Fatalf("create engine consumer: %v", err)
	}
	directPayloads := make(map[string][]byte)
	publishDirect := func(
		inputID engine.ID,
		kind engine.InputKind,
		sourceID string,
		subject string,
		action engine.TradingAction,
	) uint64 {
		t.Helper()
		payload, encodeErr := engine.EncodeTradingAction(action)
		if encodeErr != nil {
			t.Fatalf("encode %s action: %v", action.Kind, encodeErr)
		}
		encoded, encodeErr := platformnats.EncodeEngineInputMessage(
			engine.InputEnvelope{
				InputID:              inputID,
				SchemaVersion:        engine.CurrentSchemaVersion,
				ShardID:              shardID,
				Kind:                 kind,
				SourceID:             sourceID,
				SourceSequence:       processor.State().NextStreamSequence(),
				LogicalTime:          engine.NewLogicalTime(time.Unix(int64(processor.State().NextStreamSequence()), 0).UTC()),
				ConfigurationVersion: 1,
				InstrumentVersion:    1,
				Payload:              payload,
			},
		)
		if encodeErr != nil {
			t.Fatalf("encode %s transport: %v", action.Kind, encodeErr)
		}
		directPayloads[inputID.String()] = append([]byte(nil), encoded...)
		ack, publishErr := js.Publish(
			ctx,
			subject,
			encoded,
			jetstream.WithMsgID(inputID.String()),
		)
		if publishErr != nil {
			t.Fatalf("publish %s: %v", action.Kind, publishErr)
		}
		processed, processErr := consumer.ProcessOne(ctx, processor.Handle)
		if processErr != nil || !processed {
			t.Fatalf(
				"process %s = %t error %v",
				action.Kind,
				processed,
				processErr,
			)
		}
		return ack.Sequence
	}

	publishDirect(
		engine.IDFromSequence(engine.ID{}, 3101),
		engine.InputKindConfiguration,
		"configuration",
		"engine.input.31.config.v1",
		engine.TradingAction{
			Kind: engine.TradingActionConfigureInstrument,
			ConfigureInstrument: &engine.ConfigureInstrument{
				InstrumentID:            "BTC-PERP",
				Revision:                1,
				PriceScale:              2,
				QuantityScale:           3,
				SettlementCurrency:      "USDC",
				SettlementCurrencyScale: 2,
				InitialMarginRate:       "0.02",
				MaintenanceMarginRate:   "0.01",
				MaxLeverage:             "50",
				MakerFeeRate:            "0.0002",
				TakerFeeRate:            "0.0005",
			},
		},
	)
	const accountID = "urn:xb:account:market-watermark"
	if _, err := pool.Exec(ctx, `
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ($1, $2)`,
		accountID,
		int64(shardID),
	); err != nil {
		t.Fatalf("bind market-watermark account shard: %v", err)
	}
	publishDirect(
		engine.IDFromSequence(engine.ID{}, 3102),
		engine.InputKindConfiguration,
		"configuration",
		"engine.input.31.config.v1",
		engine.TradingAction{
			Kind: engine.TradingActionConfigureAccount,
			ConfigureAccount: &engine.ConfigureAccount{
				AccountID: accountID,
				OmsMode:   engine.OmsModeNetting,
			},
		},
	)

	journal := platformpostgres.NewCommandJournal(pool)
	logicalTime := time.Date(2026, time.July, 28, 14, 0, 0, 0, time.UTC)
	depositAction := engine.TradingAction{
		Kind: engine.TradingActionAdjustBalance,
		AdjustBalance: &engine.AdjustBalance{
			AccountID:     accountID,
			Currency:      "USDC",
			CurrencyScale: 2,
			Operation:     engine.BalanceOperationDeposit,
			Amount:        "10000",
		},
	}
	depositPayload, err := engine.EncodeTradingAction(depositAction)
	if err != nil {
		t.Fatalf("encode deposit: %v", err)
	}
	depositID := engine.IDFromSequence(engine.ID{}, 3103)
	depositInput := engine.InputEnvelope{
		InputID:              depositID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              shardID,
		Kind:                 engine.InputKindCommand,
		SourceID:             "urn:xb:user:market-watermark",
		SourceSequence:       1,
		LogicalTime:          engine.NewLogicalTime(logicalTime),
		ConfigurationVersion: 1,
		InstrumentVersion:    1,
		Payload:              depositPayload,
	}
	depositTransport, err := platformnats.EncodeEngineInputMessage(depositInput)
	if err != nil {
		t.Fatalf("encode deposit transport: %v", err)
	}
	if _, err := journal.Begin(ctx, platformpostgres.BeginCommandRequest{
		Scope:            "market-watermark:" + accountID,
		IdempotencyKey:   "deposit",
		RequestHash:      sha256.Sum256(depositPayload.Bytes()),
		CommandID:        depositID,
		AccountID:        accountID,
		AccountSequence:  1,
		CommandType:      string(depositAction.Kind),
		SchemaVersion:    engine.CurrentSchemaVersion,
		CanonicalPayload: depositPayload.Bytes(),
		OutboxSubject:    "engine.input.31.command.v1",
		OutboxPayload:    depositTransport,
		LogicalTime:      logicalTime,
		ExpiresAt:        logicalTime.Add(24 * time.Hour),
	}); err != nil {
		t.Fatalf("admit deposit command: %v", err)
	}
	publisher := platformnats.NewPublisher(js)
	messaging := platformpostgres.NewMessagingStore(pool)
	if published, err := messaging.PublishOutboxBatch(
		ctx,
		publisher,
		time.Now().UTC().Add(time.Second),
		100,
		time.Minute,
		time.Second,
	); err != nil || published != 1 {
		t.Fatalf("publish deposit command = %d error %v", published, err)
	}
	if processed, err := consumer.ProcessOne(ctx, processor.Handle); err != nil || !processed {
		t.Fatalf("process deposit = %t error %v", processed, err)
	}

	firstMarketID := engine.IDFromSequence(engine.ID{}, 3104)
	marketSequence := publishDirect(
		firstMarketID,
		engine.InputKindMarket,
		"hyperliquid",
		"engine.input.31.market.hyperliquid.v1",
		engine.TradingAction{
			Kind: engine.TradingActionUpdateBook,
			UpdateBook: &engine.UpdateBook{
				InstrumentID: "BTC-PERP",
				MarkPrice:    "49999",
				Bids:         []engine.BookLevel{{Price: "49998", Quantity: "10"}},
				Asks:         []engine.BookLevel{{Price: "50001", Quantity: "10"}},
			},
		},
	)
	publishDirect(
		engine.IDFromSequence(engine.ID{}, 3106),
		engine.InputKindConfiguration,
		"configuration",
		"engine.input.31.config.v1",
		engine.TradingAction{
			Kind: engine.TradingActionConfigureInstrument,
			ConfigureInstrument: &engine.ConfigureInstrument{
				InstrumentID:            "ETH-PERP",
				Revision:                1,
				PriceScale:              2,
				QuantityScale:           3,
				SettlementCurrency:      "USDC",
				SettlementCurrencyScale: 2,
				InitialMarginRate:       "0.02",
				MaintenanceMarginRate:   "0.01",
				MaxLeverage:             "50",
				MakerFeeRate:            "0.0002",
				TakerFeeRate:            "0.0005",
			},
		},
	)
	globalMarketSequence := publishDirect(
		engine.IDFromSequence(engine.ID{}, 3107),
		engine.InputKindMarket,
		"hyperliquid",
		"engine.input.31.market.hyperliquid.v1",
		engine.TradingAction{
			Kind: engine.TradingActionUpdateBook,
			UpdateBook: &engine.UpdateBook{
				InstrumentID: "ETH-PERP",
				MarkPrice:    "3000",
				Bids:         []engine.BookLevel{{Price: "2999", Quantity: "10"}},
				Asks:         []engine.BookLevel{{Price: "3001", Quantity: "10"}},
			},
		},
	)
	if globalMarketSequence <= marketSequence {
		t.Fatalf(
			"global market watermark = %d, want greater than BTC book %d",
			globalMarketSequence,
			marketSequence,
		)
	}

	submission, err := application.NewOrderSubmission(
		journal,
		application.OrderSubmissionConfig{
			ShardID:        shardID,
			IdempotencyTTL: 24 * time.Hour,
			Clock: natsPipelineClock{
				value: logicalTime.Add(2 * time.Second),
			},
		},
	)
	if err != nil {
		t.Fatalf("create production order submission: %v", err)
	}
	price := "50000"
	admission, err := submission.SubmitOrder(
		ctx,
		edge.Principal{
			Subject:  "urn:xb:user:market-watermark",
			Audience: edge.AudienceClient,
		},
		accountID,
		"working-order-watermark",
		edge.SubmitOrderRequest{
			IntentID: "working-order-watermark",
			Symbol:   "BTC-PERP",
			Side:     "BUY",
			Type:     "LIMIT",
			Quantity: "1",
			Price:    &price,
		},
	)
	if err != nil {
		t.Fatalf("admit production order: %v", err)
	}
	if published, err := messaging.PublishOutboxBatch(
		ctx,
		publisher,
		time.Now().UTC().Add(2*time.Second),
		100,
		time.Minute,
		time.Second,
	); err != nil || published == 0 {
		t.Fatalf("publish order command and domain backlog = %d error %v", published, err)
	}
	if processed, err := consumer.ProcessOne(ctx, processor.Handle); err != nil || !processed {
		t.Fatalf("process production order = %t error %v", processed, err)
	}

	orderUUID := strings.TrimPrefix(admission.OrderID, "urn:xb:order:")
	var (
		commandIDText           string
		persistedMarketSequence uint64
		used                    string
		free                    string
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			intent.command_id::text,
			(receipt.decision ->> 'MarketSequence')::bigint,
			trim_scale(balance.used)::text,
			trim_scale(balance.free)::text
		  FROM trading.order_intents AS intent
		  JOIN engine.input_receipts AS receipt
		    ON receipt.shard_id = $1
		   AND receipt.input_id = intent.command_id
		  JOIN ledger.balances AS balance
		    ON balance.account_id = intent.account_id
		   AND balance.currency = 'USDC'
		 WHERE intent.order_id = $2`,
		int64(shardID),
		orderUUID,
	).Scan(
		&commandIDText,
		&persistedMarketSequence,
		&used,
		&free,
	); err != nil {
		t.Fatalf("read production order watermark: %v", err)
	}
	if persistedMarketSequence != globalMarketSequence ||
		used != "45" ||
		free != "9955" {
		t.Fatalf(
			"production order = market %d want %d, used/free %s/%s want 45/9955",
			persistedMarketSequence,
			globalMarketSequence,
			used,
			free,
		)
	}
	var firstMarketDecisionSequence uint64
	if err := pool.QueryRow(ctx, `
		SELECT (decision ->> 'MarketSequence')::bigint
		  FROM engine.input_receipts
		 WHERE shard_id = $1 AND input_id = $2`,
		int64(shardID),
		firstMarketID.String(),
	).Scan(&firstMarketDecisionSequence); err != nil {
		t.Fatalf("read first market receipt watermark: %v", err)
	}
	if firstMarketDecisionSequence != marketSequence {
		t.Fatalf(
			"first market receipt watermark = %d, want %d",
			firstMarketDecisionSequence,
			marketSequence,
		)
	}

	stateBeforeRestart := processor.State()
	if err := processor.Close(ctx); err != nil {
		t.Fatalf("close market-watermark processor: %v", err)
	}
	processor, err = platformnats.NewEngineProcessor(ctx, store, shardID)
	if err != nil {
		t.Fatalf("recover market-watermark processor: %v", err)
	}
	if !processor.State().Ready() ||
		processor.State().Hash() != stateBeforeRestart.Hash() ||
		processor.State().NextStreamSequence() !=
			stateBeforeRestart.NextStreamSequence() {
		t.Fatalf(
			"recovered processor = ready %t hash %s/%s next %d/%d",
			processor.State().Ready(),
			processor.State().Hash(),
			stateBeforeRestart.Hash(),
			processor.State().NextStreamSequence(),
			stateBeforeRestart.NextStreamSequence(),
		)
	}
	recoveredMarketSequence, found := processor.State().MarketSequence()
	if !found || recoveredMarketSequence != globalMarketSequence {
		t.Fatalf(
			"recovered market watermark = %d found %t, want %d",
			recoveredMarketSequence,
			found,
			globalMarketSequence,
		)
	}

	laterMarketID := engine.IDFromSequence(engine.ID{}, 3105)
	laterMarketSequence := publishDirect(
		laterMarketID,
		engine.InputKindMarket,
		"hyperliquid",
		"engine.input.31.market.hyperliquid.v1",
		engine.TradingAction{
			Kind: engine.TradingActionUpdateBook,
			UpdateBook: &engine.UpdateBook{
				InstrumentID: "BTC-PERP",
				MarkPrice:    "60000",
				Bids: []engine.BookLevel{{
					Price: "59999", Quantity: "10",
				}},
				Asks: []engine.BookLevel{{
					Price: "60001", Quantity: "10",
				}},
			},
		},
	)
	if laterMarketSequence <= globalMarketSequence {
		t.Fatalf(
			"later market sequence = %d, want greater than %d",
			laterMarketSequence,
			globalMarketSequence,
		)
	}
	var laterMarketDecisionSequence uint64
	if err := pool.QueryRow(ctx, `
		SELECT (decision ->> 'MarketSequence')::bigint
		  FROM engine.input_receipts
		 WHERE shard_id = $1 AND input_id = $2`,
		int64(shardID),
		laterMarketID.String(),
	).Scan(&laterMarketDecisionSequence); err != nil {
		t.Fatalf("read later market receipt watermark: %v", err)
	}
	if laterMarketDecisionSequence != laterMarketSequence {
		t.Fatalf(
			"later market receipt watermark = %d, want %d",
			laterMarketDecisionSequence,
			laterMarketSequence,
		)
	}
	if err := pool.QueryRow(ctx, `
		SELECT trim_scale(used)::text, trim_scale(free)::text
		  FROM ledger.balances
		 WHERE account_id = $1 AND currency = 'USDC'`,
		accountID,
	).Scan(&used, &free); err != nil {
		t.Fatalf("read later-market balance: %v", err)
	}
	if used != "54" || free != "9946" {
		t.Fatalf(
			"later-market balance used/free = %s/%s, want 54/9946",
			used,
			free,
		)
	}

	commandID, err := engine.ParseID(commandIDText)
	if err != nil {
		t.Fatalf("parse production command ID: %v", err)
	}
	var firstCommandPublishSequence uint64
	if err := pool.QueryRow(ctx, `
		SELECT publish_sequence
		  FROM messaging.outbox
		 WHERE message_id = $1`,
		commandID.String(),
	).Scan(&firstCommandPublishSequence); err != nil {
		t.Fatalf("read first command publish sequence: %v", err)
	}
	commandRetryTicker := time.NewTicker(10 * time.Millisecond)
	defer commandRetryTicker.Stop()
	for {
		republishedSequence, republishErr := messaging.RepublishOutbox(
			ctx,
			publisher,
			commandID,
		)
		if republishErr != nil {
			t.Fatalf("republish production order: %v", republishErr)
		}
		if republishedSequence != firstCommandPublishSequence {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for command duplicate window: %v", ctx.Err())
		case <-commandRetryTicker.C:
		}
	}
	if processed, err := consumer.ProcessOne(
		ctx,
		processor.Handle,
	); err != nil || !processed {
		t.Fatalf("process production order redelivery = %t error %v", processed, err)
	}
	var (
		duplicateCount          int
		duplicateMarketSequence uint64
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			count(*),
			max((decision ->> 'MarketSequence')::bigint)
		  FROM engine.duplicate_delivery_receipts
		 WHERE shard_id = $1 AND input_id = $2`,
		int64(shardID),
		commandID.String(),
	).Scan(&duplicateCount, &duplicateMarketSequence); err != nil {
		t.Fatalf("read production order duplicate receipt: %v", err)
	}
	if duplicateCount != 1 ||
		duplicateMarketSequence != globalMarketSequence {
		t.Fatalf(
			"redelivery = count %d market %d, want 1/%d",
			duplicateCount,
			duplicateMarketSequence,
			globalMarketSequence,
		)
	}
	if err := pool.QueryRow(ctx, `
		SELECT trim_scale(used)::text, trim_scale(free)::text
		  FROM ledger.balances
		 WHERE account_id = $1 AND currency = 'USDC'`,
		accountID,
	).Scan(&used, &free); err != nil {
		t.Fatalf("read redelivery balance: %v", err)
	}
	if used != "54" || free != "9946" {
		t.Fatalf(
			"redelivery balance used/free = %s/%s, want 54/9946",
			used,
			free,
		)
	}

	marketRetryTicker := time.NewTicker(10 * time.Millisecond)
	defer marketRetryTicker.Stop()
	var firstMarketRedeliverySequence uint64
	for {
		firstMarketRedelivery, publishErr := js.Publish(
			ctx,
			"engine.input.31.market.hyperliquid.v1",
			directPayloads[firstMarketID.String()],
			jetstream.WithMsgID(firstMarketID.String()),
		)
		if publishErr != nil {
			t.Fatalf("republish first market input: %v", publishErr)
		}
		if !firstMarketRedelivery.Duplicate {
			firstMarketRedeliverySequence = firstMarketRedelivery.Sequence
			break
		}
		select {
		case <-ctx.Done():
			t.Fatalf("wait for market duplicate window: %v", ctx.Err())
		case <-marketRetryTicker.C:
		}
	}
	if firstMarketRedeliverySequence <= laterMarketSequence {
		t.Fatalf(
			"market redelivery sequence = %d, want greater than %d",
			firstMarketRedeliverySequence,
			laterMarketSequence,
		)
	}
	if processed, err := consumer.ProcessOne(
		ctx,
		processor.Handle,
	); err != nil || !processed {
		t.Fatalf("process market redelivery = %t error %v", processed, err)
	}
	var redeliveredMarketSequence uint64
	if err := pool.QueryRow(ctx, `
		SELECT (decision ->> 'MarketSequence')::bigint
		  FROM engine.duplicate_delivery_receipts
		 WHERE shard_id = $1 AND input_id = $2`,
		int64(shardID),
		firstMarketID.String(),
	).Scan(&redeliveredMarketSequence); err != nil {
		t.Fatalf("read market duplicate receipt: %v", err)
	}
	if redeliveredMarketSequence != marketSequence {
		t.Fatalf(
			"market redelivery watermark = %d, want original %d",
			redeliveredMarketSequence,
			marketSequence,
		)
	}
	if err := pool.QueryRow(ctx, `
		SELECT trim_scale(used)::text, trim_scale(free)::text
		  FROM ledger.balances
		 WHERE account_id = $1 AND currency = 'USDC'`,
		accountID,
	).Scan(&used, &free); err != nil {
		t.Fatalf("read market-redelivery balance: %v", err)
	}
	if used != "54" || free != "9946" {
		t.Fatalf(
			"market-redelivery balance used/free = %s/%s, want 54/9946",
			used,
			free,
		)
	}

	invalidMarketID := engine.IDFromSequence(engine.ID{}, 3108)
	invalidMarketAction := engine.TradingAction{
		Kind: engine.TradingActionUpdateBook,
		UpdateBook: &engine.UpdateBook{
			InstrumentID: "BTC-PERP",
			MarkPrice:    "60000",
			Bids:         []engine.BookLevel{{Price: "60001", Quantity: "10"}},
			Asks:         []engine.BookLevel{{Price: "59999", Quantity: "10"}},
		},
	}
	invalidMarketPayload, err := engine.EncodeTradingAction(invalidMarketAction)
	if err != nil {
		t.Fatalf("encode invalid market action: %v", err)
	}
	invalidMarketTransport, err := platformnats.EncodeEngineInputMessage(
		engine.InputEnvelope{
			InputID:              invalidMarketID,
			SchemaVersion:        engine.CurrentSchemaVersion,
			ShardID:              shardID,
			Kind:                 engine.InputKindMarket,
			SourceID:             "hyperliquid",
			SourceSequence:       processor.State().NextStreamSequence(),
			LogicalTime:          engine.NewLogicalTime(time.Unix(3108, 0).UTC()),
			ConfigurationVersion: 1,
			InstrumentVersion:    1,
			Payload:              invalidMarketPayload,
		},
	)
	if err != nil {
		t.Fatalf("encode invalid market transport: %v", err)
	}
	if _, err := js.Publish(
		ctx,
		"engine.input.31.market.hyperliquid.v1",
		invalidMarketTransport,
		jetstream.WithMsgID(invalidMarketID.String()),
	); err != nil {
		t.Fatalf("publish invalid market input: %v", err)
	}
	processed, err := consumer.ProcessOne(ctx, processor.Handle)
	if !processed || err == nil {
		t.Fatalf(
			"invalid market delivery = processed %t error %v",
			processed,
			err,
		)
	}
	if processor.Ready() {
		t.Fatal("invalid market input left processor ready")
	}
	var (
		invalidMarketReceipts int
		invalidMarketFaults   int
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM engine.input_receipts
			  WHERE shard_id = $1 AND input_id = $2),
			(SELECT count(*) FROM engine.shard_faults
			  WHERE shard_id = $1 AND input_id = $2)`,
		int64(shardID),
		invalidMarketID.String(),
	).Scan(&invalidMarketReceipts, &invalidMarketFaults); err != nil {
		t.Fatalf("read invalid market durable outcome: %v", err)
	}
	if invalidMarketReceipts != 0 || invalidMarketFaults != 1 {
		t.Fatalf(
			"invalid market durable outcome = receipts %d faults %d, want 0/1",
			invalidMarketReceipts,
			invalidMarketFaults,
		)
	}
}

type natsPipelineClock struct {
	value time.Time
}

func (clock natsPipelineClock) Now() time.Time {
	return clock.value
}
