package postgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/upcomers-org/platformgo/internal/engine"
)

// ErrReconciliationMismatch means durable state violates a checked invariant.
var ErrReconciliationMismatch = errors.New("durable reconciliation mismatch")

// ReconciliationReport is the measured durable shard summary.
type ReconciliationReport struct {
	ShardID                    engine.ShardID
	ReceiptCount               uint64
	DuplicateDeliveryCount     uint64
	DeliveryMismatchCount      uint64
	NextStreamSequence         uint64
	Ready                      bool
	LedgerMismatchCount        uint64
	UnbalancedGroupCount       uint64
	OrderFillMismatchCount     uint64
	PositionMismatchCount      uint64
	CommandMismatchCount       uint64
	ProtectionMismatchCount    uint64
	FundingMismatchCount       uint64
	ConfigurationMismatchCount uint64
	MarketMismatchCount        uint64
	MessagingMismatchCount     uint64
	PendingOutboxMessages      uint64
}

// ReconcileShard replays and hash-verifies the shard, then checks contiguous
// receipts, balanced ledger groups, and ledger-to-balance projection equality.
func (store *EngineStore) ReconcileShard(
	ctx context.Context,
	shardID engine.ShardID,
) (ReconciliationReport, error) {
	if store == nil || store.pool == nil {
		return ReconciliationReport{}, errors.New(
			"reconcile shard: PostgreSQL pool is required",
		)
	}
	ownership, ownershipErr := store.AcquireShardOwnership(ctx, shardID)
	if ownershipErr != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d ownership: %w",
			shardID,
			ownershipErr,
		)
	}
	defer func() {
		_ = ownership.Close(context.WithoutCancel(ctx))
	}()
	tx, token, releaseOwnership, beginErr := store.beginEngineTx(ctx, ownership)
	if beginErr != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d: begin transaction: %w",
			shardID,
			beginErr,
		)
	}
	defer releaseOwnership()
	defer func() {
		_ = tx.Rollback(context.WithoutCancel(ctx))
	}()
	if writerErr := acquireShardWriter(ctx, tx, shardID); writerErr != nil {
		return ReconciliationReport{}, writerErr
	}
	if ownershipErr := verifyOwnershipEpoch(
		ctx,
		tx,
		shardID,
		token,
	); ownershipErr != nil {
		return ReconciliationReport{}, ownershipErr
	}
	recovered, recoverErr := recoverTradingState(ctx, tx, shardID)
	if recoverErr != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"%w: replay shard %d: %w",
			ErrReconciliationMismatch,
			shardID,
			recoverErr,
		)
	}
	report := ReconciliationReport{
		ShardID:            shardID,
		NextStreamSequence: recovered.NextStreamSequence(),
		Ready:              recovered.Ready(),
	}
	var minimumSequence *uint64
	var maximumSequence *uint64
	var totalDeliveryCount uint64
	if queryErr := tx.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.input_receipts
		 WHERE shard_id = $1`,
		int64(shardID),
	).Scan(&report.ReceiptCount); queryErr != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d business receipts: %w",
			shardID,
			queryErr,
		)
	}
	if queryErr := tx.QueryRow(ctx, `
		WITH fill_totals AS (
			SELECT
				order_id,
				sum(quantity) AS filled_quantity,
				sum(price * quantity) AS fill_notional,
				min(account_id) AS account_id,
				max(account_id) AS maximum_account_id,
				min(instrument_id) AS instrument_id,
				max(instrument_id) AS maximum_instrument_id
			  FROM trading.fills
			 WHERE EXISTS (
				SELECT 1
				  FROM engine.account_shards AS assignment
				 WHERE assignment.account_id = fills.account_id
				   AND assignment.shard_id = $1
			 )
			 GROUP BY order_id
		)
		SELECT count(*)
		  FROM trading.orders AS orders
		  LEFT JOIN fill_totals
		    ON fill_totals.order_id = orders.order_id
		 WHERE EXISTS (
			SELECT 1
			  FROM engine.account_shards AS assignment
			 WHERE assignment.account_id = orders.account_id
			   AND assignment.shard_id = $1
		 )
		   AND (
				orders.filled_quantity <> COALESCE(fill_totals.filled_quantity, 0)
		    OR (
				COALESCE(fill_totals.filled_quantity, 0) = 0
				AND orders.average_fill_price <> 0
			)
		    OR fill_totals.account_id IS DISTINCT FROM fill_totals.maximum_account_id
		    OR fill_totals.instrument_id IS DISTINCT FROM fill_totals.maximum_instrument_id
		    OR (
				fill_totals.filled_quantity IS NOT NULL
				AND (
					orders.account_id <> fill_totals.account_id
					OR orders.instrument_id <> fill_totals.instrument_id
				)
			)
		    OR (orders.status = 'filled') <> (orders.filled_quantity = orders.quantity)
		   )`,
		int64(shardID),
	).Scan(&report.OrderFillMismatchCount); queryErr != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d orders and fills: %w",
			shardID,
			queryErr,
		)
	}
	if queryErr := tx.QueryRow(ctx, `
		WITH fill_totals AS (
			SELECT
				position_id,
				min(account_id) AS account_id,
				max(account_id) AS maximum_account_id,
				min(instrument_id) AS instrument_id,
				max(instrument_id) AS maximum_instrument_id,
				sum(CASE side WHEN 'BUY' THEN quantity ELSE -quantity END)
					AS signed_quantity,
				sum(COALESCE(realized_pnl, 0)) AS realized_pnl
			  FROM trading.fills
			 WHERE EXISTS (
				SELECT 1
				  FROM engine.account_shards AS assignment
				 WHERE assignment.account_id = fills.account_id
				   AND assignment.shard_id = $1
			 )
			 GROUP BY position_id
		),
		positions AS (
			SELECT position.*
			  FROM trading.positions AS position
			 WHERE EXISTS (
				SELECT 1
				  FROM engine.account_shards AS assignment
				 WHERE assignment.account_id = position.account_id
				   AND assignment.shard_id = $1
			 )
		)
		SELECT count(*)
		  FROM fill_totals
		  FULL OUTER JOIN positions
		    ON positions.position_id = fill_totals.position_id
		 WHERE fill_totals.position_id IS NULL
		    OR positions.position_id IS NULL
		    OR fill_totals.account_id IS DISTINCT FROM fill_totals.maximum_account_id
		    OR fill_totals.instrument_id IS DISTINCT FROM fill_totals.maximum_instrument_id
		    OR positions.account_id <> fill_totals.account_id
		    OR positions.instrument_id <> fill_totals.instrument_id
		    OR positions.signed_quantity <> fill_totals.signed_quantity
		    OR positions.realized_pnl <> fill_totals.realized_pnl
		    OR (
				fill_totals.signed_quantity <> 0
				AND positions.side <> CASE
					WHEN fill_totals.signed_quantity > 0 THEN 'LONG'
					ELSE 'SHORT'
				END
			)
		    OR positions.status <> CASE
				WHEN fill_totals.signed_quantity = 0 THEN 'closed'
				ELSE 'open'
			END`,
		int64(shardID),
	).Scan(&report.PositionMismatchCount); queryErr != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d positions: %w",
			shardID,
			queryErr,
		)
	}
	positionProjectionMismatches, err := compareRecoveredPositions(
		ctx,
		tx,
		recovered,
		shardID,
	)
	if err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d position projection: %w",
			shardID,
			err,
		)
	}
	report.PositionMismatchCount += positionProjectionMismatches
	projectionMismatches, err := compareDurableProjections(ctx, tx, shardID)
	if err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d exact projections: %w",
			shardID,
			err,
		)
	}
	report.ConfigurationMismatchCount += projectionMismatches.configuration
	report.LedgerMismatchCount += projectionMismatches.balance
	report.OrderFillMismatchCount += projectionMismatches.orderFill
	report.PositionMismatchCount += projectionMismatches.position
	report.CommandMismatchCount += projectionMismatches.command
	report.FundingMismatchCount += projectionMismatches.funding
	report.MarketMismatchCount += projectionMismatches.market
	report.LedgerMismatchCount += projectionMismatches.ledger
	report.MessagingMismatchCount += projectionMismatches.messaging
	var commandInvariantMismatches uint64
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.commands AS command
		  LEFT JOIN engine.input_receipts AS receipt
		    ON receipt.input_id = command.command_id
		   AND receipt.shard_id = $1
		 WHERE EXISTS (
			SELECT 1
			  FROM engine.account_shards AS assignment
			 WHERE assignment.account_id = command.account_id
			   AND assignment.shard_id = $1
		 )
		   AND (
				(
					command.status = 'pending'
					AND receipt.input_id IS NOT NULL
				)
				OR (
					command.status <> 'pending'
					AND (
						receipt.input_id IS NULL
						OR command.result IS NULL
						OR command.result ->> 'Status' IS DISTINCT FROM command.status
						OR receipt.decision #>> '{CommandResult,Status}'
							IS DISTINCT FROM command.status
					)
				)
			)`,
		int64(shardID),
	).Scan(&commandInvariantMismatches); err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d commands: %w",
			shardID,
			err,
		)
	}
	report.CommandMismatchCount += commandInvariantMismatches
	var fundingCountMismatches uint64
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.orders AS protection
		  LEFT JOIN trading.positions AS position
		    ON position.position_id = protection.position_id
		 WHERE protection.bracket_leg IS NOT NULL
		   AND EXISTS (
			SELECT 1
			  FROM engine.account_shards AS assignment
			 WHERE assignment.account_id = protection.account_id
			   AND assignment.shard_id = $1
		   )
		   AND protection.status IN ('held', 'working', 'partially_filled')
		   AND (
				protection.position_id IS NULL
				OR position.position_id IS NULL
				OR position.status <> 'open'
				OR position.side = 'FLAT'
				OR position.signed_quantity = 0
				OR NOT protection.reduce_only
		   )`,
		int64(shardID),
	).Scan(&report.ProtectionMismatchCount); err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d position protection: %w",
			shardID,
			err,
		)
	}
	if err := tx.QueryRow(ctx, `
		WITH expected_counts AS (
			SELECT
				input_id,
				CASE
					WHEN jsonb_typeof(decision -> 'FundingChanges') = 'array'
					THEN jsonb_array_length(decision -> 'FundingChanges')
					ELSE 0
				END AS effect_count
			  FROM engine.input_receipts
			 WHERE shard_id = $1
		),
		expected AS (
			SELECT input_id, effect_count
			  FROM expected_counts
			 WHERE effect_count > 0
		),
		actual AS (
			SELECT input_id, count(*) AS effect_count
			  FROM trading.funding_settlements AS funding
			 WHERE EXISTS (
				SELECT 1
				  FROM engine.account_shards AS assignment
				 WHERE assignment.account_id = funding.account_id
				   AND assignment.shard_id = $1
			 )
			 GROUP BY input_id
		)
		SELECT count(*)
		  FROM expected
		  FULL OUTER JOIN actual USING (input_id)
		 WHERE expected.effect_count IS DISTINCT FROM actual.effect_count`,
		int64(shardID),
	).Scan(&fundingCountMismatches); err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d funding effects: %w",
			shardID,
			err,
		)
	}
	report.FundingMismatchCount += fundingCountMismatches
	var balanceFoldMismatches uint64
	if err := tx.QueryRow(ctx, `
		SELECT count(*), min(stream_sequence), max(stream_sequence)
		  FROM (
			SELECT stream_sequence
			  FROM engine.input_receipts
			 WHERE shard_id = $1
			UNION ALL
			SELECT stream_sequence
			  FROM engine.duplicate_delivery_receipts
			 WHERE shard_id = $1
		  ) AS deliveries`,
		int64(shardID),
	).Scan(
		&totalDeliveryCount,
		&minimumSequence,
		&maximumSequence,
	); err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d delivery range: %w",
			shardID,
			err,
		)
	}
	if totalDeliveryCount > 0 &&
		(minimumSequence == nil ||
			maximumSequence == nil ||
			*minimumSequence != 1 ||
			*maximumSequence != totalDeliveryCount) {
		report.DeliveryMismatchCount++
	}
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.duplicate_delivery_receipts
		 WHERE shard_id = $1`,
		int64(shardID),
	).Scan(&report.DuplicateDeliveryCount); err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d duplicate deliveries: %w",
			shardID,
			err,
		)
	}
	if totalDeliveryCount+1 != report.NextStreamSequence {
		report.DeliveryMismatchCount++
	}

	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		  FROM (
			SELECT entry.transaction_id, entry.currency
			  FROM ledger.entries AS entry
			  JOIN ledger.transactions AS transaction
			    ON transaction.transaction_id = entry.transaction_id
			  JOIN engine.input_receipts AS receipt
			    ON receipt.input_id = transaction.input_id
			   AND receipt.shard_id = $1
			 GROUP BY entry.transaction_id, entry.currency
			HAVING sum(amount) <> 0
		  ) AS unbalanced`,
		int64(shardID),
	).Scan(&report.UnbalancedGroupCount); err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d ledger balance: %w",
			shardID,
			err,
		)
	}
	if err := tx.QueryRow(ctx, `
		WITH ledger_totals AS (
			SELECT entry.account_id, entry.currency, sum(entry.amount) AS total
			  FROM ledger.entries AS entry
			 WHERE EXISTS (
				SELECT 1
				  FROM engine.account_shards AS assignment
				 WHERE assignment.account_id = entry.account_id
				   AND assignment.shard_id = $1
			 )
			 GROUP BY entry.account_id, entry.currency
		),
		shard_balances AS (
			SELECT balance.*
			  FROM ledger.balances AS balance
			 WHERE EXISTS (
				SELECT 1
				  FROM engine.account_shards AS assignment
				 WHERE assignment.account_id = balance.account_id
				   AND assignment.shard_id = $1
			 )
		),
		mismatches AS (
			SELECT
				COALESCE(ledger_totals.account_id, balances.account_id) AS account_id,
				COALESCE(ledger_totals.currency, balances.currency) AS currency
			  FROM ledger_totals
			  FULL OUTER JOIN shard_balances AS balances
			    ON balances.account_id = ledger_totals.account_id
			   AND balances.currency = ledger_totals.currency
			 WHERE COALESCE(ledger_totals.total, 0) <> COALESCE(balances.total, 0)
		)
		SELECT count(*) FROM mismatches`,
		int64(shardID),
	).Scan(&balanceFoldMismatches); err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d balance projection: %w",
			shardID,
			err,
		)
	}
	report.LedgerMismatchCount += balanceFoldMismatches
	if err := tx.QueryRow(ctx, `
		SELECT count(*)
		  FROM messaging.outbox AS outbox
		 WHERE outbox.published_at IS NULL
		   AND (
				EXISTS (
					SELECT 1
					  FROM trading.commands AS command
					  JOIN engine.account_shards AS assignment
					    ON assignment.account_id = command.account_id
					 WHERE command.command_id = outbox.message_id
					   AND assignment.shard_id = $1
				)
				OR EXISTS (
					SELECT 1
					  FROM engine.input_receipts AS receipt
					 WHERE receipt.shard_id = $1
					   AND receipt.input_id::text =
					       outbox.payload ->> 'correlationId'
				)
		   )`,
		int64(shardID),
	).Scan(&report.PendingOutboxMessages); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d outbox backlog: %w",
			shardID,
			err,
		)
	}
	if report.DeliveryMismatchCount != 0 ||
		report.UnbalancedGroupCount != 0 ||
		report.LedgerMismatchCount != 0 ||
		report.OrderFillMismatchCount != 0 ||
		report.PositionMismatchCount != 0 ||
		report.CommandMismatchCount != 0 ||
		report.ProtectionMismatchCount != 0 ||
		report.FundingMismatchCount != 0 ||
		report.ConfigurationMismatchCount != 0 ||
		report.MarketMismatchCount != 0 ||
		report.MessagingMismatchCount != 0 {
		mismatch := fmt.Errorf(
			"%w: shard %d has delivery=%d ledger=%d balance=%d order_fill=%d position=%d command=%d protection=%d funding=%d configuration=%d market=%d messaging=%d mismatches",
			ErrReconciliationMismatch,
			shardID,
			report.DeliveryMismatchCount,
			report.UnbalancedGroupCount,
			report.LedgerMismatchCount,
			report.OrderFillMismatchCount,
			report.PositionMismatchCount,
			report.CommandMismatchCount,
			report.ProtectionMismatchCount,
			report.FundingMismatchCount,
			report.ConfigurationMismatchCount,
			report.MarketMismatchCount,
			report.MessagingMismatchCount,
		)
		if recovered.Ready() {
			payload, payloadErr := engine.EncodeTradingAction(engine.TradingAction{})
			if payloadErr != nil {
				return report, errors.Join(mismatch, payloadErr)
			}
			namespace := engine.IDFromSequence(engine.ID{}, uint64(shardID))
			input := engine.InputEnvelope{
				InputID:        engine.IDFromSequence(namespace, recovered.NextStreamSequence()),
				SchemaVersion:  engine.CurrentSchemaVersion,
				ShardID:        shardID,
				Kind:           engine.InputKindControl,
				SourceID:       "reconciliation",
				SourceSequence: recovered.NextStreamSequence(),
				StreamSequence: recovered.NextStreamSequence(),
				LogicalTime:    0,
				Payload:        payload,
			}
			halted, haltErr := engine.FailClosed(
				recovered,
				input,
				engine.ErrDurableInputConflict,
				mismatch.Error(),
			)
			if err := persistEngineFault(
				ctx,
				tx,
				input,
				engine.TradingAction{},
				halted,
				haltErr,
			); err != nil {
				return report, errors.Join(mismatch, err)
			}
			if err := persistCheckpoint(ctx, tx, halted); err != nil {
				return report, errors.Join(mismatch, err)
			}
			report.Ready = false
		}
		if err := tx.Commit(ctx); err != nil {
			return report, errors.Join(
				mismatch,
				fmt.Errorf("commit reconciliation halt: %w", err),
			)
		}
		return report, mismatch
	}
	if err := tx.Commit(ctx); err != nil {
		return report, fmt.Errorf("reconcile shard %d commit: %w", shardID, err)
	}
	return report, nil
}

func compareRecoveredPositions(
	ctx context.Context,
	tx pgx.Tx,
	recovered engine.State,
	shardID engine.ShardID,
) (uint64, error) {
	expected := make(map[string]engine.PositionSnapshot)
	for _, position := range recovered.Positions() {
		expected[position.PositionID.String()] = position
	}
	rows, err := tx.Query(ctx, `
		SELECT
			position_id::text,
			account_id,
			instrument_id,
			side,
			status,
			trim_scale(signed_quantity)::text,
			trim_scale(average_open_price)::text,
			trim_scale(realized_pnl)::text,
			settlement_currency,
			margin_mode,
			trim_scale(isolated_collateral)::text,
			version
		  FROM trading.positions
		 WHERE EXISTS (
			SELECT 1
			  FROM engine.account_shards AS assignment
			 WHERE assignment.account_id = trading.positions.account_id
			   AND assignment.shard_id = $1
		 )
		 ORDER BY position_id`,
		int64(shardID),
	)
	if err != nil {
		return 0, err
	}
	defer rows.Close()

	var mismatches uint64
	for rows.Next() {
		var actual engine.PositionSnapshot
		var positionID string
		if err := rows.Scan(
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
		); err != nil {
			return 0, err
		}
		expectedPosition, found := expected[positionID]
		if !found {
			mismatches++
			continue
		}
		delete(expected, positionID)
		actual.PositionID = expectedPosition.PositionID
		if actual != expectedPosition {
			mismatches++
		}
	}
	if err := rows.Err(); err != nil {
		return 0, err
	}
	return mismatches + uint64(len(expected)), nil
}
