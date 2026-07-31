package postgres_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/testkit"
)

// Ported from:
//
//	repository: upcomers-org/platform@50141367492be46ebf5623f6191a14b94af2f2bd
//	source: apps/app/tests/it/trading/e2e_order_caps.rs:179
//	test: submit_denies_open_on_unfunded_account
//
// Adaptations:
//   - The owner-approved current Go boundary replaces the Rust handler's
//     synchronous AppError and message with the serialized engine's durable
//     rejected / insufficient_funds command result.
//   - The source fixture is replaced by PostgreSQL 19, explicit instrument and
//     book revisions, a manual logical clock, and deterministic identities.
//   - Both an absent USDC balance and an explicit exact zero balance represent
//     the source's unfunded account.
//
// Assertions preserved:
//   - A valid risk-increasing LIMIT BUY 1 @ 100 is rejected when the account is
//     unfunded.
//   - The rejected attempt creates no order or position.
//
// Strengthening:
//   - The exact rejection decision, result, and hash chain are durable and
//     reproduce across transaction rollback, same-sequence replay,
//     later-sequence duplicate delivery, fresh-store recovery, and
//     reconciliation.
//   - Rejection creates no order, fill, position, balance reservation, ledger,
//     domain-event, or realtime effect beyond deterministic setup and command
//     admission.
//
// External contract boundary:
//   - This test does not claim the Rust synchronous AppError, free-margin
//     message, HTTP status, NATS acknowledgment, or realtime continuity.
func TestSubmitDeniesOpenOnUnfundedAccount(t *testing.T) {
	for caseIndex, test := range []struct {
		name         string
		explicitZero bool
	}{
		{name: "absent USDC balance"},
		{name: "explicit zero USDC balance", explicitZero: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := postgresPool(t)
			requirePostgresMajor19(t, pool)
			resetDurableSchemas(t, pool)
			if err := newCurrentTestMigrator(
				t,
				pool,
				os.DirFS(filepath.Join("..", "..", "..", "migrations")),
			).MigrateAndProvision(ctx, 8); err != nil {
				t.Fatalf("migrate unfunded-order database: %v", err)
			}

			store := platformpostgres.NewEngineStore(pool)
			state := engine.NewState(8)
			ids := testkit.NewShardIDSequence(8)
			clock := testkit.NewManualClock(engine.NewLogicalTime(time.Date(
				2026,
				time.July,
				29,
				10+caseIndex,
				0,
				0,
				0,
				time.UTC,
			)))
			applySetup := func(action engine.TradingAction) {
				var decision engine.Decision
				state, decision, _, _ = applyStoredTrading(
					t,
					pool,
					store,
					state,
					ids,
					clock,
					action,
					platformpostgres.ApplyOptions{},
				)
				if decision.CommandResult.Status != engine.CommandStatusAccepted ||
					decision.CommandResult.Reason != "" {
					t.Fatalf("setup action %s = %+v", action.Kind, decision)
				}
			}

			applySetup(engine.TradingAction{
				Kind: engine.TradingActionConfigureInstrument,
				ConfigureInstrument: &engine.ConfigureInstrument{
					InstrumentID:            "BTC-PERP",
					Revision:                1,
					PriceScale:              1,
					QuantityScale:           3,
					SettlementCurrency:      "USDC",
					SettlementCurrencyScale: 2,
					InitialMarginRate:       "0.02",
					MaintenanceMarginRate:   "0.01",
					MaxLeverage:             "50",
					MakerFeeRate:            "0.0002",
					TakerFeeRate:            "0.0005",
				},
			})
			applySetup(engine.TradingAction{
				Kind: engine.TradingActionConfigureAccount,
				ConfigureAccount: &engine.ConfigureAccount{
					AccountID: "account-unfunded",
					OmsMode:   engine.OmsModeNetting,
				},
			})
			if test.explicitZero {
				applySetup(engine.TradingAction{
					Kind: engine.TradingActionAdjustBalance,
					AdjustBalance: &engine.AdjustBalance{
						AccountID:     "account-unfunded",
						Currency:      "USDC",
						CurrencyScale: 2,
						Operation:     engine.BalanceOperationSet,
						Amount:        "0",
					},
				})
			}
			applySetup(engine.TradingAction{
				Kind: engine.TradingActionUpdateBook,
				UpdateBook: &engine.UpdateBook{
					InstrumentID: "BTC-PERP",
					MarkPrice:    "100",
					Bids: []engine.BookLevel{{
						Price: "99.9", Quantity: "10",
					}},
					Asks: []engine.BookLevel{{
						Price: "100", Quantity: "10",
					}},
				},
			})

			assertUnfundedBalanceRepresentation(
				t,
				pool,
				state,
				test.explicitZero,
			)
			setupState := state
			setupEffects := readUnfundedDurableEffects(t, pool)

			orderID := engine.IDFromSequence(engine.ID{}, 179+uint64(caseIndex))
			action := engine.TradingAction{
				Kind: engine.TradingActionSubmitOrder,
				SubmitOrder: &engine.SubmitOrder{
					OrderID:      orderID,
					AccountID:    "account-unfunded",
					InstrumentID: "BTC-PERP",
					Side:         engine.SideBuy,
					Type:         engine.OrderTypeLimit,
					TimeInForce:  engine.TimeInForceGTC,
					Quantity:     "1",
					Price:        "100",
				},
			}
			input := nextStoredInput(t, state, ids, clock, action)
			seedPendingCommand(t, pool, input, action)
			seedUnfundedReplayResponse(t, pool, input.InputID)
			admittedEffects := readUnfundedDurableEffects(t, pool)
			if admittedEffects.messagingOutbox != setupEffects.messagingOutbox+1 {
				t.Fatalf(
					"admission outbox rows = %d, want setup %d + 1",
					admittedEffects.messagingOutbox,
					setupEffects.messagingOutbox,
				)
			}
			assertUnfundedCommandLifecycle(
				t,
				pool,
				input.InputID,
				"pending",
				"in_progress",
				0,
				0,
			)

			resolvedInput := input
			marketSequence, ok := state.MarketSequence()
			if !ok {
				t.Fatal("explicit book did not establish market authority")
			}
			resolvedInput.MarketSequence = marketSequence
			wantState, wantDecision, err := engine.ApplyTrading(
				state,
				resolvedInput,
				action,
			)
			if err != nil {
				t.Fatalf("derive exact unfunded decision: %v", err)
			}
			assertUnfundedRejection(t, orderID, wantDecision)

			faultedState, _, duplicate, err := store.ApplyTrading(
				ctx,
				state,
				input,
				action,
				platformpostgres.ApplyOptions{
					Faults: testkit.NewFaults(
						platformpostgres.FailpointAfterPersistBeforeCommit,
					),
				},
			)
			if !errors.Is(err, platformpostgres.ErrInjectedFault) || duplicate {
				t.Fatalf(
					"precommit result duplicate=%t error=%v, want rollback fault",
					duplicate,
					err,
				)
			}
			if faultedState.Hash() != setupState.Hash() ||
				faultedState.NextStreamSequence() !=
					setupState.NextStreamSequence() {
				t.Fatalf(
					"precommit state = %s/%d, want %s/%d",
					faultedState.Hash(),
					faultedState.NextStreamSequence(),
					setupState.Hash(),
					setupState.NextStreamSequence(),
				)
			}
			assertUnfundedDurableEffects(
				t,
				pool,
				admittedEffects,
				"precommit rollback",
			)
			assertUnfundedCommandLifecycle(
				t,
				pool,
				input.InputID,
				"pending",
				"in_progress",
				0,
				0,
			)

			retryStore := platformpostgres.NewEngineStore(pool)
			recoveredBeforeRetry, err := retryStore.RecoverTradingState(ctx, 8)
			if err != nil {
				t.Fatalf("recover after precommit rollback: %v", err)
			}
			if !recoveredBeforeRetry.Ready() ||
				recoveredBeforeRetry.Hash() != setupState.Hash() ||
				recoveredBeforeRetry.NextStreamSequence() !=
					setupState.NextStreamSequence() {
				t.Fatalf(
					"post-fault recovery ready=%t hash=%s next=%d, want true/%s/%d",
					recoveredBeforeRetry.Ready(),
					recoveredBeforeRetry.Hash(),
					recoveredBeforeRetry.NextStreamSequence(),
					setupState.Hash(),
					setupState.NextStreamSequence(),
				)
			}

			state, decision, duplicate, err := retryStore.ApplyTrading(
				ctx,
				recoveredBeforeRetry,
				input,
				action,
				platformpostgres.ApplyOptions{},
			)
			if err != nil || duplicate {
				t.Fatalf(
					"retry rejection duplicate=%t error=%v",
					duplicate,
					err,
				)
			}
			assertDecisionEqual(t, "exact unfunded decision", wantDecision, decision)
			if state.Hash() != wantState.Hash() ||
				state.NextStreamSequence() != wantState.NextStreamSequence() {
				t.Fatalf(
					"committed rejection state = %s/%d, want %s/%d",
					state.Hash(),
					state.NextStreamSequence(),
					wantState.Hash(),
					wantState.NextStreamSequence(),
				)
			}
			assertUnfundedRejection(t, orderID, decision)
			assertStoredTerminalDecision(t, pool, input.InputID, decision)
			assertUnfundedCommandLifecycle(
				t,
				pool,
				input.InputID,
				"rejected",
				"completed",
				1,
				0,
			)
			committedEffects := admittedEffects
			committedEffects.inputReceipts++
			assertUnfundedDurableEffects(
				t,
				pool,
				committedEffects,
				"committed rejection",
			)
			assertUnfundedBalanceRepresentation(
				t,
				pool,
				state,
				test.explicitZero,
			)

			sameState, sameDecision, duplicate, err := retryStore.ApplyTrading(
				ctx,
				state,
				input,
				action,
				platformpostgres.ApplyOptions{},
			)
			if err != nil || !duplicate ||
				sameState.Hash() != state.Hash() ||
				sameState.NextStreamSequence() != state.NextStreamSequence() {
				t.Fatalf(
					"same-sequence replay duplicate=%t hash=%s next=%d error=%v",
					duplicate,
					sameState.Hash(),
					sameState.NextStreamSequence(),
					err,
				)
			}
			assertDecisionEqual(
				t,
				"same-sequence immutable rejection",
				decision,
				sameDecision,
			)
			assertUnfundedDurableEffects(
				t,
				pool,
				committedEffects,
				"same-sequence replay",
			)

			republishedInput := input
			republishedInput.StreamSequence = state.NextStreamSequence()
			republishedState, republishedDecision, duplicate, err :=
				retryStore.ApplyTrading(
					ctx,
					state,
					republishedInput,
					action,
					platformpostgres.ApplyOptions{},
				)
			if err != nil || !duplicate ||
				republishedDecision.DuplicateOfDecisionHash !=
					decision.DecisionHash ||
				republishedDecision.PreviousStateHash != state.Hash() ||
				republishedDecision.NextStateHash != republishedState.Hash() ||
				republishedDecision.StreamSequence !=
					republishedInput.StreamSequence ||
				republishedState.Hash() == state.Hash() ||
				republishedState.NextStreamSequence() !=
					state.NextStreamSequence()+1 {
				t.Fatalf(
					"later-sequence replay duplicate=%t decision=%+v state=%s/%d error=%v",
					duplicate,
					republishedDecision,
					republishedState.Hash(),
					republishedState.NextStreamSequence(),
					err,
				)
			}
			assertNoEconomicEffects(t, republishedDecision)
			finalEffects := committedEffects
			finalEffects.duplicateReceipts++
			assertUnfundedDurableEffects(
				t,
				pool,
				finalEffects,
				"later-sequence duplicate",
			)
			assertStoredDuplicateDecision(
				t,
				pool,
				input.InputID,
				republishedInput.StreamSequence,
				republishedDecision,
			)
			assertUnfundedCommandLifecycle(
				t,
				pool,
				input.InputID,
				"rejected",
				"completed",
				1,
				1,
			)

			freshStore := platformpostgres.NewEngineStore(pool)
			recovered, err := freshStore.RecoverTradingState(ctx, 8)
			if err != nil {
				t.Fatalf("fresh-store recovery: %v", err)
			}
			if !recovered.Ready() ||
				recovered.Hash() != republishedState.Hash() ||
				recovered.NextStreamSequence() !=
					republishedState.NextStreamSequence() {
				t.Fatalf(
					"fresh recovery ready=%t hash=%s next=%d, want true/%s/%d",
					recovered.Ready(),
					recovered.Hash(),
					recovered.NextStreamSequence(),
					republishedState.Hash(),
					republishedState.NextStreamSequence(),
				)
			}
			assertUnfundedBalanceRepresentation(
				t,
				pool,
				recovered,
				test.explicitZero,
			)
			assertUnfundedDurableEffects(
				t,
				pool,
				finalEffects,
				"fresh recovery",
			)

			report, err := freshStore.ReconcileShard(ctx, 8)
			if err != nil ||
				!report.Ready ||
				report.ReceiptCount !=
					uint64(republishedInput.StreamSequence-1) ||
				report.DuplicateDeliveryCount != 1 ||
				report.NextStreamSequence !=
					republishedState.NextStreamSequence() ||
				report.DeliveryMismatchCount != 0 ||
				report.LedgerMismatchCount != 0 ||
				report.UnbalancedGroupCount != 0 ||
				report.OrderFillMismatchCount != 0 ||
				report.PositionMismatchCount != 0 ||
				report.CommandMismatchCount != 0 ||
				report.ProtectionMismatchCount != 0 ||
				report.FundingMismatchCount != 0 ||
				report.ConfigurationMismatchCount != 0 ||
				report.MarketMismatchCount != 0 ||
				report.MessagingMismatchCount != 0 ||
				report.RealtimeMismatchCount != 0 ||
				report.RealtimeQuarantinedCount != 0 {
				t.Fatalf("unfunded rejection reconciliation = %+v, error %v", report, err)
			}
		})
	}
}

type unfundedDurableEffects struct {
	inputReceipts        int
	duplicateReceipts    int
	orders               int
	fills                int
	positions            int
	balances             int
	ledgerTransactions   int
	ledgerEntries        int
	messagingOutbox      int
	engineDomainOutbox   int
	realtimeSequences    int
	realtimePublications int
}

func requirePostgresMajor19(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	var major int
	if err := pool.QueryRow(context.Background(), `
		SELECT current_setting('server_version_num')::integer / 10000`,
	).Scan(&major); err != nil {
		t.Fatalf("read PostgreSQL major: %v", err)
	}
	if major != 19 {
		t.Fatalf("PostgreSQL major = %d, want 19", major)
	}
}

func assertUnfundedRejection(
	t *testing.T,
	orderID engine.ID,
	decision engine.Decision,
) {
	t.Helper()
	if decision.CommandResult.Status != engine.CommandStatusRejected ||
		decision.CommandResult.Reason != engine.RejectionInsufficientFunds {
		t.Fatalf(
			"unfunded result = %+v, want rejected/insufficient_funds",
			decision.CommandResult,
		)
	}
	if decision.DecisionHash.IsZero() ||
		decision.NextStateHash.IsZero() ||
		decision.EffectsHash.IsZero() {
		t.Fatalf("unfunded rejection lacks exact hashes: %+v", decision)
	}
	assertNoEconomicEffects(t, decision)
	if _, ok := decisionOrder(decision, orderID); ok {
		t.Fatalf("unfunded rejection contains order %s", orderID)
	}
}

func decisionOrder(
	decision engine.Decision,
	orderID engine.ID,
) (engine.OrderSnapshot, bool) {
	for _, order := range decision.OrderChanges {
		if order.OrderID == orderID {
			return order, true
		}
	}
	return engine.OrderSnapshot{}, false
}

func assertUnfundedBalanceRepresentation(
	t *testing.T,
	pool *pgxpool.Pool,
	state engine.State,
	explicitZero bool,
) {
	t.Helper()
	balance, stateHasBalance := state.Balance("account-unfunded", "USDC")
	var persisted int
	if err := pool.QueryRow(context.Background(), `
		SELECT count(*)
		  FROM ledger.balances
		 WHERE account_id = 'account-unfunded'
		   AND currency = 'USDC'
		   AND total = 0
		   AND used = 0
		   AND free = 0
		   AND equity = 0`,
	).Scan(&persisted); err != nil {
		t.Fatalf("inspect unfunded balance: %v", err)
	}
	if explicitZero {
		if !stateHasBalance ||
			balance.Total != "0" ||
			balance.Used != "0" ||
			balance.Free != "0" ||
			balance.Equity != "0" ||
			persisted != 1 {
			t.Fatalf(
				"explicit zero balance state=%+v/%t persisted=%d",
				balance,
				stateHasBalance,
				persisted,
			)
		}
		return
	}
	if stateHasBalance || persisted != 0 {
		t.Fatalf(
			"absent balance state=%+v/%t persisted=%d",
			balance,
			stateHasBalance,
			persisted,
		)
	}
}

func readUnfundedDurableEffects(
	t *testing.T,
	pool *pgxpool.Pool,
) unfundedDurableEffects {
	t.Helper()
	var got unfundedDurableEffects
	if err := pool.QueryRow(context.Background(), `
		SELECT
			(SELECT count(*) FROM engine.input_receipts),
			(SELECT count(*) FROM engine.duplicate_delivery_receipts),
			(SELECT count(*) FROM trading.orders),
			(SELECT count(*) FROM trading.fills),
			(SELECT count(*) FROM trading.positions),
			(SELECT count(*) FROM ledger.balances),
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries),
			(SELECT count(*) FROM messaging.outbox),
			(SELECT count(*)
			   FROM messaging.outbox
			  WHERE producer_class = 'engine'),
			(SELECT count(*) FROM realtime.channel_sequences),
			(SELECT count(*) FROM realtime.publications)`,
	).Scan(
		&got.inputReceipts,
		&got.duplicateReceipts,
		&got.orders,
		&got.fills,
		&got.positions,
		&got.balances,
		&got.ledgerTransactions,
		&got.ledgerEntries,
		&got.messagingOutbox,
		&got.engineDomainOutbox,
		&got.realtimeSequences,
		&got.realtimePublications,
	); err != nil {
		t.Fatalf("inspect unfunded durable effects: %v", err)
	}
	return got
}

func assertUnfundedDurableEffects(
	t *testing.T,
	pool *pgxpool.Pool,
	want unfundedDurableEffects,
	stage string,
) {
	t.Helper()
	got := readUnfundedDurableEffects(t, pool)
	if got != want {
		t.Fatalf("%s durable effects = %+v, want %+v", stage, got, want)
	}
}

func assertUnfundedCommandLifecycle(
	t *testing.T,
	pool *pgxpool.Pool,
	commandID engine.ID,
	wantCommandStatus string,
	wantIdempotencyState string,
	wantReceiptCount int,
	wantDuplicateReceiptCount int,
) {
	t.Helper()
	var (
		commandStatus    string
		commandResultSet bool
		idempotencyState string
		receiptCount     int
		duplicateCount   int
		responseStatus   int
		responseBody     []byte
	)
	if err := pool.QueryRow(context.Background(), `
		SELECT
			command.status,
			command.result IS NOT NULL,
			idempotency.state,
			replay.response_status,
			replay.response_body,
			(SELECT count(*)
			   FROM engine.input_receipts
			  WHERE input_id = command.command_id),
			(SELECT count(*)
			   FROM engine.duplicate_delivery_receipts
			  WHERE input_id = command.command_id)
		  FROM trading.commands AS command
		  JOIN trading.idempotency_records AS idempotency
		    ON idempotency.command_id = command.command_id
		  JOIN trading.command_replay_responses AS replay
		    ON replay.command_id = command.command_id
		 WHERE command.command_id = $1`,
		commandID.String(),
	).Scan(
		&commandStatus,
		&commandResultSet,
		&idempotencyState,
		&responseStatus,
		&responseBody,
		&receiptCount,
		&duplicateCount,
	); err != nil {
		t.Fatalf("inspect unfunded command lifecycle: %v", err)
	}
	wantResultSet := wantCommandStatus != "pending"
	if commandStatus != wantCommandStatus ||
		commandResultSet != wantResultSet ||
		idempotencyState != wantIdempotencyState ||
		responseStatus != 202 ||
		string(responseBody) != `{"status":"accepted"}` ||
		receiptCount != wantReceiptCount ||
		duplicateCount != wantDuplicateReceiptCount {
		t.Fatalf(
			"unfunded lifecycle = command %s/result %t idempotency %s receipts %d/%d, "+
				"want %s/%t %s %d/%d",
			commandStatus,
			commandResultSet,
			idempotencyState,
			receiptCount,
			duplicateCount,
			wantCommandStatus,
			wantResultSet,
			wantIdempotencyState,
			wantReceiptCount,
			wantDuplicateReceiptCount,
		)
	}
}

func seedUnfundedReplayResponse(
	t *testing.T,
	pool *pgxpool.Pool,
	commandID engine.ID,
) {
	t.Helper()
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO trading.command_replay_responses (
			command_id,
			response_status,
			response_headers,
			response_body
		) VALUES ($1, 202, '{}'::jsonb, $2)`,
		commandID.String(),
		[]byte(`{"status":"accepted"}`),
	); err != nil {
		t.Fatalf("seed unfunded command replay response: %v", err)
	}
}
