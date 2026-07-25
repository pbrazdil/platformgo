package postgres

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"sort"

	"github.com/jackc/pgx/v5"
	"github.com/upcomers-org/platformgo/internal/engine"
)

type projectionMismatchCounts struct {
	configuration uint64
	balance       uint64
	orderFill     uint64
	position      uint64
	command       uint64
	funding       uint64
	market        uint64
	ledger        uint64
	messaging     uint64
}

type versionedInstrument struct {
	Snapshot engine.InstrumentSnapshot
	Version  uint64
}

type versionedAccount struct {
	Snapshot engine.AccountSnapshot
	Version  uint64
}

type versionedRisk struct {
	Snapshot engine.RiskSnapshot
	Version  uint64
}

type sequencedBalance struct {
	Snapshot       engine.BalanceSnapshot
	LedgerSequence uint64
}

type sequencedBook struct {
	Snapshot       engine.BookSnapshot
	StreamSequence uint64
}

type inputFill struct {
	Snapshot engine.FillSnapshot
	InputID  engine.ID
}

type inputFunding struct {
	Snapshot engine.FundingSnapshot
	InputID  engine.ID
}

type durableDomainEvent struct {
	MessageID     engine.ID
	Subject       string
	SchemaVersion uint32
	Payload       []byte
	ProducerClass string
	EngineShardID int64
	EngineInputID string
}

type durableCommand struct {
	CommandID       engine.ID
	AccountID       string
	AccountSequence uint64
	CommandType     string
	SchemaVersion   uint32
	CanonicalAction []byte
	Status          string
	Result          []byte
	LogicalTime     engine.LogicalTime
	OutboxSubject   string
	OutboxSchema    uint32
	OutboxPayload   []byte
	OutboxProducer  string
	OutboxShardID   int64
	OutboxInputID   string
}

type expectedProjections struct {
	instruments map[string]versionedInstrument
	accounts    map[string]versionedAccount
	risks       map[string]versionedRisk
	balances    map[string]sequencedBalance
	books       map[string]sequencedBook
	orders      map[string]engine.OrderSnapshot
	fills       map[string]inputFill
	positions   map[string]engine.PositionSnapshot
	commands    map[string]durableCommand
	funding     map[string]inputFunding
	ledger      map[string]engine.LedgerTransactionSnapshot
	events      map[string]durableDomainEvent
}

func compareDurableProjections(
	ctx context.Context,
	tx pgx.Tx,
	shardID engine.ShardID,
) (projectionMismatchCounts, error) {
	expected, err := loadExpectedProjections(ctx, tx, shardID)
	if err != nil {
		return projectionMismatchCounts{}, err
	}
	var counts projectionMismatchCounts
	if count, err := compareInstruments(ctx, tx, expected.instruments); err != nil {
		return counts, err
	} else {
		counts.configuration += count
	}
	if count, err := compareAccounts(ctx, tx, shardID, expected.accounts); err != nil {
		return counts, err
	} else {
		counts.configuration += count
	}
	if count, err := compareRisks(ctx, tx, shardID, expected.risks); err != nil {
		return counts, err
	} else {
		counts.configuration += count
	}
	if count, err := compareBalances(ctx, tx, shardID, expected.balances); err != nil {
		return counts, err
	} else {
		counts.balance = count
	}
	if count, err := compareBooks(ctx, tx, expected.books); err != nil {
		return counts, err
	} else {
		counts.market = count
	}
	if count, err := compareOrders(ctx, tx, shardID, expected.orders); err != nil {
		return counts, err
	} else {
		counts.orderFill += count
	}
	if count, err := compareFills(ctx, tx, shardID, expected.fills); err != nil {
		return counts, err
	} else {
		counts.orderFill += count
	}
	if count, err := comparePositions(ctx, tx, shardID, expected.positions); err != nil {
		return counts, err
	} else {
		counts.position = count
	}
	if count, err := compareCommands(ctx, tx, shardID, expected.commands); err != nil {
		return counts, err
	} else {
		counts.command = count
	}
	if count, err := compareFunding(ctx, tx, shardID, expected.funding); err != nil {
		return counts, err
	} else {
		counts.funding = count
	}
	if count, err := compareLedger(ctx, tx, shardID, expected.ledger); err != nil {
		return counts, err
	} else {
		counts.ledger = count
	}
	if count, err := compareDomainEvents(ctx, tx, shardID, expected.events); err != nil {
		return counts, err
	} else {
		counts.messaging = count
	}
	return counts, nil
}

func loadExpectedProjections(
	ctx context.Context,
	tx pgx.Tx,
	shardID engine.ShardID,
) (expectedProjections, error) {
	expected := expectedProjections{
		instruments: make(map[string]versionedInstrument),
		accounts:    make(map[string]versionedAccount),
		risks:       make(map[string]versionedRisk),
		balances:    make(map[string]sequencedBalance),
		books:       make(map[string]sequencedBook),
		orders:      make(map[string]engine.OrderSnapshot),
		fills:       make(map[string]inputFill),
		positions:   make(map[string]engine.PositionSnapshot),
		commands:    make(map[string]durableCommand),
		funding:     make(map[string]inputFunding),
		ledger:      make(map[string]engine.LedgerTransactionSnapshot),
		events:      make(map[string]durableDomainEvent),
	}
	rows, err := tx.Query(ctx, `
		SELECT envelope, decision, stream_sequence
		  FROM engine.input_receipts
		 WHERE shard_id = $1
		 ORDER BY stream_sequence`,
		int64(shardID),
	)
	if err != nil {
		return expected, fmt.Errorf("load projection receipts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var envelopeJSON []byte
		var decisionJSON []byte
		var streamSequence uint64
		if err := rows.Scan(&envelopeJSON, &decisionJSON, &streamSequence); err != nil {
			return expected, fmt.Errorf("scan projection receipt: %w", err)
		}
		input, action, err := decodeEnvelope(envelopeJSON)
		if err != nil {
			return expected, fmt.Errorf("decode projection receipt: %w", err)
		}
		var decision engine.Decision
		if err := json.Unmarshal(decisionJSON, &decision); err != nil {
			return expected, fmt.Errorf("decode projection decision: %w", err)
		}
		for _, change := range decision.InstrumentChanges {
			current := expected.instruments[change.InstrumentID]
			current.Snapshot = change
			current.Version++
			expected.instruments[change.InstrumentID] = current
		}
		for _, change := range decision.AccountChanges {
			current := expected.accounts[change.AccountID]
			current.Snapshot = change
			current.Version++
			expected.accounts[change.AccountID] = current
		}
		for _, change := range decision.RiskChanges {
			key := change.AccountID + "\x00" + change.InstrumentID
			current := expected.risks[key]
			current.Snapshot = change
			current.Version++
			expected.risks[key] = current
		}
		for _, change := range decision.BalanceChanges {
			key := change.AccountID + "\x00" + change.Currency
			expected.balances[key] = sequencedBalance{
				Snapshot:       change,
				LedgerSequence: streamSequence,
			}
		}
		for _, change := range decision.BookChanges {
			expected.books[change.InstrumentID] = sequencedBook{
				Snapshot:       change,
				StreamSequence: streamSequence,
			}
		}
		for _, change := range decision.OrderChanges {
			change.AverageFillPrice = decimalOrZero(change.AverageFillPrice)
			expected.orders[change.OrderID.String()] = change
		}
		for _, change := range decision.Fills {
			expected.fills[change.FillID.String()] = inputFill{
				Snapshot: change,
				InputID:  input.InputID,
			}
		}
		for _, change := range decision.PositionChanges {
			expected.positions[change.PositionID.String()] = change
		}
		for _, change := range decision.FundingChanges {
			expected.funding[change.FundingID.String()] = inputFunding{
				Snapshot: change,
				InputID:  input.InputID,
			}
		}
		for _, change := range decision.LedgerChanges {
			sort.Slice(change.Entries, func(left, right int) bool {
				return change.Entries[left].EntryID.String() <
					change.Entries[right].EntryID.String()
			})
			expected.ledger[change.TransactionID.String()] = change
		}
		for _, event := range decision.Events {
			payload, err := json.Marshal(domainEventEnvelope{
				MessageID:        event.EventID.String(),
				SchemaVersion:    engine.CurrentSchemaVersion,
				Kind:             event.Kind,
				CorrelationID:    input.InputID.String(),
				CausationID:      input.InputID.String(),
				AggregateID:      event.AggregateID.String(),
				AggregateVersion: event.AggregateVersion,
				LogicalTime:      event.LogicalTime.String(),
				Payload:          event,
			})
			if err != nil {
				return expected, fmt.Errorf("encode expected domain event: %w", err)
			}
			expected.events[event.EventID.String()] = durableDomainEvent{
				MessageID:     event.EventID,
				Subject:       "domain.v1." + event.Kind,
				SchemaVersion: engine.CurrentSchemaVersion,
				Payload:       payload,
				ProducerClass: "engine",
				EngineShardID: int64(input.ShardID),
				EngineInputID: input.InputID.String(),
			}
		}
		if input.Kind == engine.InputKindCommand {
			resultJSON, err := json.Marshal(decision.CommandResult)
			if err != nil {
				return expected, fmt.Errorf("encode expected command result: %w", err)
			}
			outboxPayload, err := engine.EncodeInputMessage(input)
			if err != nil {
				return expected, fmt.Errorf("encode expected command outbox: %w", err)
			}
			accountID, _ := engine.TradingActionAccountID(action)
			expected.commands[input.InputID.String()] = durableCommand{
				CommandID:       input.InputID,
				AccountID:       accountID,
				AccountSequence: input.SourceSequence,
				CommandType:     string(action.Kind),
				SchemaVersion:   input.SchemaVersion,
				CanonicalAction: input.Payload.Bytes(),
				Status:          string(decision.CommandResult.Status),
				Result:          resultJSON,
				LogicalTime:     input.LogicalTime,
				OutboxSubject: fmt.Sprintf(
					"engine.input.%d.command.v%d",
					input.ShardID,
					input.SchemaVersion,
				),
				OutboxSchema:   input.SchemaVersion,
				OutboxPayload:  outboxPayload,
				OutboxProducer: "api",
				OutboxShardID:  -1,
			}
		}
	}
	if err := rows.Err(); err != nil {
		return expected, fmt.Errorf("iterate projection receipts: %w", err)
	}
	return expected, nil
}

func compareInstruments(
	ctx context.Context,
	tx pgx.Tx,
	expected map[string]versionedInstrument,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT instrument_id, revision, price_scale, quantity_scale,
		       settlement_currency, settlement_currency_scale,
		       trim_scale(initial_margin_rate)::text,
		       trim_scale(maintenance_margin_rate)::text,
		       trim_scale(max_leverage)::text,
		       trim_scale(maker_fee_rate)::text,
		       trim_scale(taker_fee_rate)::text,
		       version
		  FROM trading.instruments`,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual versionedInstrument
		if scanErr := rows.Scan(
			&actual.Snapshot.InstrumentID,
			&actual.Snapshot.Revision,
			&actual.Snapshot.PriceScale,
			&actual.Snapshot.QuantityScale,
			&actual.Snapshot.SettlementCurrency,
			&actual.Snapshot.SettlementCurrencyScale,
			&actual.Snapshot.InitialMarginRate,
			&actual.Snapshot.MaintenanceMarginRate,
			&actual.Snapshot.MaxLeverage,
			&actual.Snapshot.MakerFeeRate,
			&actual.Snapshot.TakerFeeRate,
			&actual.Version,
		); scanErr != nil {
			return 0, scanErr
		}
		key := actual.Snapshot.InstrumentID
		want, found := expected[key]
		if !found || !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, key)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func compareAccounts(
	ctx context.Context,
	tx pgx.Tx,
	_ engine.ShardID,
	expected map[string]versionedAccount,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT account.account_id, account.oms_mode, account.version
		  FROM trading.accounts AS account`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual versionedAccount
		if scanErr := rows.Scan(
			&actual.Snapshot.AccountID,
			&actual.Snapshot.OmsMode,
			&actual.Version,
		); scanErr != nil {
			return 0, scanErr
		}
		key := actual.Snapshot.AccountID
		want, found := expected[key]
		if !found || !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, key)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func compareRisks(
	ctx context.Context,
	tx pgx.Tx,
	_ engine.ShardID,
	expected map[string]versionedRisk,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT risk.account_id, risk.instrument_id, risk.margin_mode,
		       trim_scale(risk.leverage)::text, risk.version
		  FROM trading.risk_configs AS risk`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual versionedRisk
		if scanErr := rows.Scan(
			&actual.Snapshot.AccountID,
			&actual.Snapshot.InstrumentID,
			&actual.Snapshot.MarginMode,
			&actual.Snapshot.Leverage,
			&actual.Version,
		); scanErr != nil {
			return 0, scanErr
		}
		key := actual.Snapshot.AccountID + "\x00" + actual.Snapshot.InstrumentID
		want, found := expected[key]
		if !found || !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, key)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func compareBalances(
	ctx context.Context,
	tx pgx.Tx,
	_ engine.ShardID,
	expected map[string]sequencedBalance,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT balance.account_id, balance.currency,
		       trim_scale(balance.total)::text,
		       trim_scale(balance.used)::text,
		       trim_scale(balance.free)::text,
		       trim_scale(balance.equity)::text,
		       balance.ledger_sequence
		  FROM ledger.balances AS balance`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual sequencedBalance
		if scanErr := rows.Scan(
			&actual.Snapshot.AccountID,
			&actual.Snapshot.Currency,
			&actual.Snapshot.Total,
			&actual.Snapshot.Used,
			&actual.Snapshot.Free,
			&actual.Snapshot.Equity,
			&actual.LedgerSequence,
		); scanErr != nil {
			return 0, scanErr
		}
		key := actual.Snapshot.AccountID + "\x00" + actual.Snapshot.Currency
		want, found := expected[key]
		if !found || !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, key)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func compareBooks(
	ctx context.Context,
	tx pgx.Tx,
	expected map[string]sequencedBook,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT instrument_id, trim_scale(mark_price)::text, bids, asks,
		       stream_sequence
		  FROM market.books`,
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual sequencedBook
		var bidsJSON []byte
		var asksJSON []byte
		if scanErr := rows.Scan(
			&actual.Snapshot.InstrumentID,
			&actual.Snapshot.MarkPrice,
			&bidsJSON,
			&asksJSON,
			&actual.StreamSequence,
		); scanErr != nil {
			return 0, scanErr
		}
		if err := json.Unmarshal(bidsJSON, &actual.Snapshot.Bids); err != nil {
			return 0, err
		}
		if err := json.Unmarshal(asksJSON, &actual.Snapshot.Asks); err != nil {
			return 0, err
		}
		key := actual.Snapshot.InstrumentID
		want, found := expected[key]
		if !found || !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, key)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func compareOrders(
	ctx context.Context,
	tx pgx.Tx,
	_ engine.ShardID,
	expected map[string]engine.OrderSnapshot,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT order_id::text, orders.account_id, instrument_id, side,
		       order_type, time_in_force, status,
		       trim_scale(quantity)::text,
		       trim_scale(filled_quantity)::text,
		       trim_scale(average_fill_price)::text,
		       COALESCE(trim_scale(limit_price)::text, ''),
		       COALESCE(trim_scale(trigger_price)::text, ''),
		       triggered,
		       COALESCE(
				triggered_at,
				0
		       ),
		       reduce_only,
		       COALESCE(position_id::text, '00000000-0000-0000-0000-000000000000'),
		       COALESCE(bracket_id::text, '00000000-0000-0000-0000-000000000000'),
		       COALESCE(bracket_leg, ''),
		       bracket_leg_index, has_rested, has_slippage_band,
		       max_slippage_bps,
		       COALESCE(trim_scale(slippage_reference)::text, ''),
		       COALESCE(reject_reason, ''), version
		  FROM trading.orders AS orders`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual engine.OrderSnapshot
		var orderID, positionID, bracketID string
		var triggeredAt int64
		if scanErr := rows.Scan(
			&orderID,
			&actual.AccountID,
			&actual.InstrumentID,
			&actual.Side,
			&actual.Type,
			&actual.TimeInForce,
			&actual.Status,
			&actual.Quantity,
			&actual.FilledQuantity,
			&actual.AverageFillPrice,
			&actual.Price,
			&actual.TriggerPrice,
			&actual.Triggered,
			&triggeredAt,
			&actual.ReduceOnly,
			&positionID,
			&bracketID,
			&actual.BracketLeg,
			&actual.BracketLegIndex,
			&actual.HasRested,
			&actual.HasSlippageBand,
			&actual.MaxSlippageBPS,
			&actual.SlippageReference,
			&actual.RejectReason,
			&actual.Version,
		); scanErr != nil {
			return 0, scanErr
		}
		if actual.OrderID, err = engine.ParseID(orderID); err != nil {
			return 0, err
		}
		if actual.PositionID, err = engine.ParseID(positionID); err != nil {
			return 0, err
		}
		if actual.BracketID, err = engine.ParseID(bracketID); err != nil {
			return 0, err
		}
		actual.TriggeredAt = engine.LogicalTime(triggeredAt)
		want, found := expected[orderID]
		if !found || !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, orderID)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func compareFills(
	ctx context.Context,
	tx pgx.Tx,
	_ engine.ShardID,
	expected map[string]inputFill,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT fill_id::text, order_id::text, input_id::text,
		       fills.account_id, instrument_id, side,
		       trim_scale(price)::text, trim_scale(quantity)::text,
		       position_id::text, position_effect,
		       COALESCE(trim_scale(realized_pnl)::text, ''),
		       COALESCE(settlement_currency, ''), liquidity_side,
		       COALESCE(trim_scale(fee)::text, ''),
		       COALESCE(fee_currency, ''),
		       logical_time
		  FROM trading.fills AS fills`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual inputFill
		var fillID, orderID, inputID, positionID string
		var logicalTime int64
		if scanErr := rows.Scan(
			&fillID,
			&orderID,
			&inputID,
			&actual.Snapshot.AccountID,
			&actual.Snapshot.InstrumentID,
			&actual.Snapshot.Side,
			&actual.Snapshot.Price,
			&actual.Snapshot.Quantity,
			&positionID,
			&actual.Snapshot.PositionEffect,
			&actual.Snapshot.RealizedPnL,
			&actual.Snapshot.SettlementCurrency,
			&actual.Snapshot.LiquiditySide,
			&actual.Snapshot.Fee,
			&actual.Snapshot.FeeCurrency,
			&logicalTime,
		); scanErr != nil {
			return 0, scanErr
		}
		if actual.Snapshot.FillID, err = engine.ParseID(fillID); err != nil {
			return 0, err
		}
		if actual.Snapshot.OrderID, err = engine.ParseID(orderID); err != nil {
			return 0, err
		}
		if actual.InputID, err = engine.ParseID(inputID); err != nil {
			return 0, err
		}
		if actual.Snapshot.PositionID, err = engine.ParseID(positionID); err != nil {
			return 0, err
		}
		actual.Snapshot.LogicalTime = engine.LogicalTime(logicalTime)
		want, found := expected[fillID]
		if !found || !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, fillID)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func comparePositions(
	ctx context.Context,
	tx pgx.Tx,
	_ engine.ShardID,
	expected map[string]engine.PositionSnapshot,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT position_id::text, position.account_id, instrument_id, side,
		       status, trim_scale(signed_quantity)::text,
		       trim_scale(average_open_price)::text,
		       trim_scale(realized_pnl)::text, settlement_currency,
		       margin_mode, trim_scale(isolated_collateral)::text, version
		  FROM trading.positions AS position`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual engine.PositionSnapshot
		var positionID string
		if scanErr := rows.Scan(
			&positionID,
			&actual.AccountID,
			&actual.InstrumentID,
			&actual.Side,
			&actual.Status,
			&actual.SignedQuantity,
			&actual.AverageOpenPrice,
			&actual.RealizedPnL,
			&actual.SettlementCurrency,
			&actual.MarginMode,
			&actual.IsolatedCollateral,
			&actual.Version,
		); scanErr != nil {
			return 0, scanErr
		}
		if actual.PositionID, err = engine.ParseID(positionID); err != nil {
			return 0, err
		}
		want, found := expected[positionID]
		if !found || !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, positionID)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func compareCommands(
	ctx context.Context,
	tx pgx.Tx,
	shardID engine.ShardID,
	expected map[string]durableCommand,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT command.command_id::text, command.account_id,
		       command.account_sequence, command.command_type,
		       command.schema_version, command.canonical_payload,
		       command.status, COALESCE(command.result, 'null'::jsonb),
		       command.logical_time,
		       COALESCE(outbox.subject, ''),
		       COALESCE(outbox.schema_version, 0),
		       COALESCE(outbox.payload, 'null'::jsonb),
		       COALESCE(outbox.producer_class, ''),
		       COALESCE(outbox.engine_shard_id, -1),
		       COALESCE(outbox.engine_input_id::text, ''),
		       assignment.shard_id,
		       idempotency.state
		  FROM trading.commands AS command
		  LEFT JOIN engine.account_shards AS assignment USING (account_id)
		  LEFT JOIN messaging.outbox AS outbox
		    ON outbox.message_id = command.command_id
		  LEFT JOIN trading.idempotency_records AS idempotency
		    ON idempotency.command_id = command.command_id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual durableCommand
		var commandID string
		var logicalTime int64
		var assignmentShard *int64
		var idempotencyState *string
		if scanErr := rows.Scan(
			&commandID,
			&actual.AccountID,
			&actual.AccountSequence,
			&actual.CommandType,
			&actual.SchemaVersion,
			&actual.CanonicalAction,
			&actual.Status,
			&actual.Result,
			&logicalTime,
			&actual.OutboxSubject,
			&actual.OutboxSchema,
			&actual.OutboxPayload,
			&actual.OutboxProducer,
			&actual.OutboxShardID,
			&actual.OutboxInputID,
			&assignmentShard,
			&idempotencyState,
		); scanErr != nil {
			return 0, scanErr
		}
		if actual.CommandID, err = engine.ParseID(commandID); err != nil {
			return 0, err
		}
		actual.LogicalTime = engine.LogicalTime(logicalTime)
		actual.CanonicalAction, err = canonicalJSON(actual.CanonicalAction)
		if err != nil {
			return 0, err
		}
		actual.Result, err = canonicalJSON(actual.Result)
		if err != nil {
			return 0, err
		}
		actual.OutboxPayload, err = canonicalJSON(actual.OutboxPayload)
		if err != nil {
			return 0, err
		}
		want, found := expected[commandID]
		if !found {
			if !pendingCommandProjectionMatches(
				actual,
				shardID,
				assignmentShard,
				idempotencyState,
			) {
				mismatches++
			}
			continue
		}
		want.CanonicalAction, err = canonicalJSON(want.CanonicalAction)
		if err != nil {
			return 0, err
		}
		want.Result, err = canonicalJSON(want.Result)
		if err != nil {
			return 0, err
		}
		want.OutboxPayload, err = canonicalJSON(want.OutboxPayload)
		if err != nil {
			return 0, err
		}
		if want.AccountID == "" {
			want.AccountID = actual.AccountID
		}
		if !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, commandID)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func pendingCommandProjectionMatches(
	command durableCommand,
	shardID engine.ShardID,
	assignmentShard *int64,
	idempotencyState *string,
) bool {
	if command.Status != "pending" ||
		assignmentShard == nil ||
		*assignmentShard != int64(shardID) ||
		idempotencyState == nil ||
		*idempotencyState != string(IdempotencyInProgress) ||
		command.OutboxProducer != "api" ||
		command.OutboxSchema == 0 ||
		command.OutboxShardID != -1 ||
		command.OutboxInputID != "" {
		return false
	}
	input, action, err := engine.DecodeInputMessage(command.OutboxPayload)
	if err != nil {
		return false
	}
	expectedSubject := fmt.Sprintf(
		"engine.input.%d.command.v%d",
		input.ShardID,
		input.SchemaVersion,
	)
	canonicalInput, err := canonicalJSON(input.Payload.Bytes())
	if err != nil {
		return false
	}
	actionAccountID, scoped := engine.TradingActionAccountID(action)
	return input.InputID == command.CommandID &&
		input.Kind == engine.InputKindCommand &&
		engine.TradingActionAllowedForInputKind(input.Kind, action.Kind) &&
		input.ShardID == shardID &&
		input.SchemaVersion == command.SchemaVersion &&
		input.SourceSequence == command.AccountSequence &&
		input.LogicalTime == command.LogicalTime &&
		string(action.Kind) == command.CommandType &&
		(!scoped || actionAccountID == command.AccountID) &&
		command.OutboxSubject == expectedSubject &&
		command.OutboxSchema == input.SchemaVersion &&
		bytes.Equal(command.CanonicalAction, canonicalInput)
}

func compareFunding(
	ctx context.Context,
	tx pgx.Tx,
	_ engine.ShardID,
	expected map[string]inputFunding,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT funding_id::text, settlement_id::text, position_id::text,
		       input_id::text, funding.account_id, instrument_id,
		       trim_scale(signed_quantity)::text,
		       trim_scale(oracle_price)::text, trim_scale(rate)::text,
		       trim_scale(amount)::text, settlement_currency
		  FROM trading.funding_settlements AS funding`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual inputFunding
		var fundingID, settlementID, positionID, inputID string
		if scanErr := rows.Scan(
			&fundingID,
			&settlementID,
			&positionID,
			&inputID,
			&actual.Snapshot.AccountID,
			&actual.Snapshot.InstrumentID,
			&actual.Snapshot.SignedQuantity,
			&actual.Snapshot.OraclePrice,
			&actual.Snapshot.Rate,
			&actual.Snapshot.Amount,
			&actual.Snapshot.SettlementCurrency,
		); scanErr != nil {
			return 0, scanErr
		}
		if actual.Snapshot.FundingID, err = engine.ParseID(fundingID); err != nil {
			return 0, err
		}
		if actual.Snapshot.SettlementID, err = engine.ParseID(settlementID); err != nil {
			return 0, err
		}
		if actual.Snapshot.PositionID, err = engine.ParseID(positionID); err != nil {
			return 0, err
		}
		if actual.InputID, err = engine.ParseID(inputID); err != nil {
			return 0, err
		}
		want, found := expected[fundingID]
		if !found || !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, fundingID)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func compareLedger(
	ctx context.Context,
	tx pgx.Tx,
	_ engine.ShardID,
	expected map[string]engine.LedgerTransactionSnapshot,
) (uint64, error) {
	var transactionCount uint64
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		  FROM ledger.transactions`,
	).Scan(&transactionCount); err != nil {
		return 0, err
	}
	rows, err := tx.Query(ctx, `
		SELECT transaction.transaction_id::text, transaction.business_key,
		       transaction.input_id::text,
		       transaction.logical_time,
		       entry.entry_id::text, entry.account_id, entry.currency,
		       trim_scale(entry.amount)::text
		  FROM ledger.transactions AS transaction
		  JOIN ledger.entries AS entry
		    ON entry.transaction_id = transaction.transaction_id
		 ORDER BY transaction.transaction_id, entry.entry_id`)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	actual := make(map[string]engine.LedgerTransactionSnapshot)
	for rows.Next() {
		var transactionID, inputID, entryID string
		var businessKey, accountID, currency, amount string
		var logicalTime int64
		if scanErr := rows.Scan(
			&transactionID,
			&businessKey,
			&inputID,
			&logicalTime,
			&entryID,
			&accountID,
			&currency,
			&amount,
		); scanErr != nil {
			return 0, scanErr
		}
		transaction := actual[transactionID]
		if transaction.TransactionID.IsZero() {
			if transaction.TransactionID, err = engine.ParseID(transactionID); err != nil {
				return 0, err
			}
			if transaction.InputID, err = engine.ParseID(inputID); err != nil {
				return 0, err
			}
			transaction.BusinessKey = businessKey
			transaction.LogicalTime = engine.LogicalTime(logicalTime)
		}
		parsedEntryID, err := engine.ParseID(entryID)
		if err != nil {
			return 0, err
		}
		transaction.Entries = append(transaction.Entries, engine.LedgerEntrySnapshot{
			EntryID:   parsedEntryID,
			AccountID: accountID,
			Currency:  currency,
			Amount:    amount,
		})
		actual[transactionID] = transaction
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	var mismatches uint64
	if transactionCount > uint64(len(actual)) {
		mismatches += transactionCount - uint64(len(actual))
	}
	for key, got := range actual {
		want, found := expected[key]
		if !found || !projectionEqual(want, got) {
			mismatches++
		}
		delete(expected, key)
	}
	return mismatches + uint64(len(expected)), nil
}

func compareDomainEvents(
	ctx context.Context,
	tx pgx.Tx,
	shardID engine.ShardID,
	expected map[string]durableDomainEvent,
) (uint64, error) {
	rows, err := tx.Query(ctx, `
		SELECT outbox.message_id::text, outbox.subject,
		       outbox.schema_version, outbox.payload, outbox.producer_class,
		       COALESCE(outbox.engine_shard_id, -1),
		       COALESCE(outbox.engine_input_id::text, '')
		  FROM messaging.outbox AS outbox
		 WHERE outbox.subject LIKE 'domain.v1.%'
		   AND (
				outbox.engine_shard_id = $1
				OR (
					outbox.producer_class = 'engine'
					AND outbox.engine_shard_id IS NULL
				)
		   )`,
		int64(shardID),
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var mismatches uint64
	for rows.Next() {
		var actual durableDomainEvent
		var messageID string
		if scanErr := rows.Scan(
			&messageID,
			&actual.Subject,
			&actual.SchemaVersion,
			&actual.Payload,
			&actual.ProducerClass,
			&actual.EngineShardID,
			&actual.EngineInputID,
		); scanErr != nil {
			return 0, scanErr
		}
		if actual.MessageID, err = engine.ParseID(messageID); err != nil {
			return 0, err
		}
		actual.Payload, err = canonicalJSON(actual.Payload)
		if err != nil {
			return 0, err
		}
		want, found := expected[messageID]
		if found {
			want.Payload, err = canonicalJSON(want.Payload)
			if err != nil {
				return 0, err
			}
		}
		if !found || !projectionEqual(want, actual) {
			mismatches++
		}
		delete(expected, messageID)
	}
	return mismatches + uint64(len(expected)), rows.Err()
}

func projectionEqual(expected, actual any) bool {
	expectedJSON, expectedErr := json.Marshal(expected)
	actualJSON, actualErr := json.Marshal(actual)
	return expectedErr == nil && actualErr == nil && bytes.Equal(expectedJSON, actualJSON)
}
