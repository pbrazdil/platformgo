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
	ShardID                engine.ShardID
	ReceiptCount           uint64
	DuplicateDeliveryCount uint64
	NextStreamSequence     uint64
	Ready                  bool
	LedgerMismatchCount    uint64
	UnbalancedGroupCount   uint64
	PendingOutboxMessages  uint64
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
	recovered, err := store.RecoverTradingState(ctx, shardID)
	if err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"%w: replay shard %d: %w",
			ErrReconciliationMismatch,
			shardID,
			err,
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
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM engine.input_receipts
		 WHERE shard_id = $1`,
		int64(shardID),
	).Scan(&report.ReceiptCount); err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d business receipts: %w",
			shardID,
			err,
		)
	}
	if err := store.pool.QueryRow(ctx, `
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
		return report, fmt.Errorf(
			"%w: shard %d receipts are not contiguous",
			ErrReconciliationMismatch,
			shardID,
		)
	}
	if err := store.pool.QueryRow(ctx, `
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
		return report, fmt.Errorf(
			"%w: shard %d durable delivery count does not match next sequence",
			ErrReconciliationMismatch,
			shardID,
		)
	}

	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM (
			SELECT transaction_id, currency
			  FROM ledger.entries
			 GROUP BY transaction_id, currency
			HAVING sum(amount) <> 0
		  ) AS unbalanced`,
	).Scan(&report.UnbalancedGroupCount); err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d ledger balance: %w",
			shardID,
			err,
		)
	}
	if err := store.pool.QueryRow(ctx, `
		WITH ledger_totals AS (
			SELECT account_id, currency, sum(amount) AS total
			  FROM ledger.entries
			 WHERE account_id <> $1
			 GROUP BY account_id, currency
		),
		mismatches AS (
			SELECT
				COALESCE(ledger_totals.account_id, balances.account_id) AS account_id,
				COALESCE(ledger_totals.currency, balances.currency) AS currency
			  FROM ledger_totals
			  FULL OUTER JOIN ledger.balances AS balances
			    ON balances.account_id = ledger_totals.account_id
			   AND balances.currency = ledger_totals.currency
			 WHERE COALESCE(ledger_totals.total, 0) <> COALESCE(balances.total, 0)
		)
		SELECT count(*) FROM mismatches`,
		engine.SystemClearingAccount,
	).Scan(&report.LedgerMismatchCount); err != nil {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d balance projection: %w",
			shardID,
			err,
		)
	}
	if err := store.pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM messaging.outbox
		 WHERE published_at IS NULL`,
	).Scan(&report.PendingOutboxMessages); err != nil &&
		!errors.Is(err, pgx.ErrNoRows) {
		return ReconciliationReport{}, fmt.Errorf(
			"reconcile shard %d outbox backlog: %w",
			shardID,
			err,
		)
	}
	if report.UnbalancedGroupCount != 0 || report.LedgerMismatchCount != 0 {
		return report, fmt.Errorf(
			"%w: shard %d has %d unbalanced ledger groups and %d balance mismatches",
			ErrReconciliationMismatch,
			shardID,
			report.UnbalancedGroupCount,
			report.LedgerMismatchCount,
		)
	}
	return report, nil
}
