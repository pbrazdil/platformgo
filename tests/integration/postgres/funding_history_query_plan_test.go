package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jackc/pgx/v5/pgxpool"
)

func TestFundingHistoryQueriesUseKeysetIndexes(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	if err := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	).Migrate(ctx); err != nil {
		t.Fatalf("migrate funding query-plan database: %v", err)
	}
	tx, err := pool.Begin(ctx)
	if err != nil {
		t.Fatalf("begin funding query-plan seed: %v", err)
	}
	defer func() { _ = tx.Rollback(context.Background()) }()
	if _, err := tx.Exec(ctx, `
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260730000400_phase3_broker_funding_acl',
				true
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				true
			);
		INSERT INTO engine.deployment_shard (shard_id) VALUES (41);
		INSERT INTO trading.instruments (
			instrument_id, revision, price_scale, quantity_scale,
			settlement_currency, settlement_currency_scale,
			initial_margin_rate, maintenance_margin_rate, max_leverage,
			maker_fee_rate, taker_fee_rate
		) VALUES (
			'BTC-PERP', 1, 2, 3, 'USDC', 2,
			0.1, 0.05, 10, 0, 0
		);
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('account-plan', 'NETTING');
		INSERT INTO engine.account_shards (account_id, shard_id)
		VALUES ('account-plan', 41);
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		)
		SELECT
			41,
			format(
				'00000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			sequence_number,
			1,
			1,
			decode(repeat('01', 32), 'hex'),
			4,
			decode(repeat('02', 32), 'hex'),
			decode(repeat('03', 32), 'hex'),
			jsonb_build_object(
				'LogicalTime',
				1784901600000000000 + sequence_number
			),
			jsonb_build_object(
				'DecisionHashVersion',
				4,
				'CommandResult',
				jsonb_build_object('Status', 'accepted'),
				'FundingChanges',
				jsonb_build_array(
					jsonb_build_object(
						'FundingID',
						(
							SELECT jsonb_agg(
								get_byte(
									uuid_send(
										format(
											'10000000-0000-0000-0000-%s',
											lpad(
												to_hex(sequence_number),
												12,
												'0'
											)
										)::uuid
									),
									octet
								)
								ORDER BY octet
							)
							  FROM generate_series(0, 15) AS bytes(octet)
						),
						'SettlementID',
						(
							SELECT jsonb_agg(
								get_byte(
									uuid_send(
										format(
											'20000000-0000-0000-0000-%s',
											lpad(
												to_hex(sequence_number),
												12,
												'0'
											)
										)::uuid
									),
									octet
								)
								ORDER BY octet
							)
							  FROM generate_series(0, 15) AS bytes(octet)
						),
						'PositionID',
						(
							SELECT jsonb_agg(
								get_byte(
									uuid_send(
										'30000000-0000-0000-0000-000000000001'
											::uuid
									),
									octet
								)
								ORDER BY octet
							)
							  FROM generate_series(0, 15) AS bytes(octet)
						),
						'AccountID',
						'account-plan',
						'InstrumentID',
						'BTC-PERP',
						'SignedQuantity',
						'1',
						'OraclePrice',
						'100',
						'Rate',
						'0.01',
						'Amount',
						'-1',
						'SettlementCurrency',
						'USDC'
					)
				)
			),
			decode(repeat('04', 32), 'hex'),
			1
		  FROM generate_series(1, 10000) AS sequence(sequence_number);
		INSERT INTO trading.funding_settlements (
			funding_id, settlement_id, position_id, input_id,
			account_id, instrument_id, signed_quantity, oracle_price,
			rate, amount, settlement_currency
		)
		SELECT
			format(
				'10000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			format(
				'20000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'30000000-0000-0000-0000-000000000001'::uuid,
			format(
				'00000000-0000-0000-0000-%s',
				lpad(to_hex(sequence_number), 12, '0')
			)::uuid,
			'account-plan',
			'BTC-PERP',
			1,
			100,
			0.01,
			-1,
			'USDC'
		  FROM generate_series(1, 10000) AS sequence(sequence_number);
		INSERT INTO trading.funding_history_projection (
			funding_id, account_id, instrument_id, position_id, logical_time
		)
		SELECT
			funding.funding_id,
			funding.account_id,
			funding.instrument_id,
			funding.position_id,
			(receipt.envelope ->> 'LogicalTime')::bigint
		  FROM trading.funding_settlements AS funding
		  JOIN engine.input_receipts AS receipt
		    ON receipt.input_id = funding.input_id
		   AND receipt.shard_id = 41;
		INSERT INTO trading.funding_instrument_provenance (
			funding_id, instrument_id, revision, price_scale, quantity_scale
		)
		SELECT
			funding.funding_id,
			instrument.instrument_id,
			instrument.revision,
			instrument.price_scale,
			instrument.quantity_scale
		  FROM trading.funding_settlements AS funding
		  JOIN trading.instruments AS instrument
		    ON instrument.instrument_id = funding.instrument_id;
		ANALYZE trading.funding_settlements;
		ANALYZE trading.funding_history_projection`); err != nil {
		t.Fatalf("seed representative funding history: %v", err)
	}
	if err := tx.Commit(ctx); err != nil {
		t.Fatalf("commit funding query-plan seed: %v", err)
	}

	assertFundingPlanUsesIndex(
		t,
		pool,
		`SELECT funding.funding_id
		   FROM trading.funding_history_projection AS history
		   JOIN trading.funding_settlements AS funding
		     ON funding.funding_id = history.funding_id
		  WHERE history.account_id = 'account-plan'
		    AND (history.logical_time, history.funding_id) <
		        (1784901600000010001, 'ffffffff-ffff-ffff-ffff-ffffffffffff')
		  ORDER BY history.logical_time DESC, history.funding_id DESC
		  LIMIT 51`,
		"funding_history_projection_account_idx",
	)
	assertFundingPlanUsesIndex(
		t,
		pool,
		`SELECT funding.funding_id
		   FROM trading.funding_history_projection AS history
		   JOIN trading.funding_settlements AS funding
		     ON funding.funding_id = history.funding_id
		  WHERE history.account_id = 'account-plan'
		    AND (history.logical_time, history.funding_id) >
		        (1784901600000009950, '00000000-0000-0000-0000-000000000000')
		  ORDER BY history.logical_time ASC, history.funding_id ASC
		  LIMIT 51`,
		"funding_history_projection_account_idx",
	)
	assertFundingPlanUsesIndex(
		t,
		pool,
		`SELECT funding.funding_id, profile.login
		   FROM trading.funding_history_projection AS history
		   JOIN trading.funding_settlements AS funding
		     ON funding.funding_id = history.funding_id
		   LEFT JOIN identity.account_profiles AS profile
		     ON profile.account_id = history.account_id
		  WHERE history.instrument_id = 'BTC-PERP'
		    AND (history.logical_time, history.funding_id) <
		        (1784901600000010001, 'ffffffff-ffff-ffff-ffff-ffffffffffff')
		  ORDER BY history.logical_time DESC, history.funding_id DESC
		  LIMIT 51`,
		"funding_history_projection_instrument_idx",
	)
	assertFundingPlanUsesIndex(
		t,
		pool,
		`SELECT sum(funding.amount)
		   FROM trading.funding_history_projection AS history
		   JOIN trading.funding_settlements AS funding
		     ON funding.funding_id = history.funding_id
		  WHERE history.account_id = 'account-plan'
		    AND history.position_id =
		        '30000000-0000-0000-0000-000000000001'
		    AND history.logical_time >= 1784901600000009950`,
		"funding_history_projection_account_position_idx",
	)
}

func assertFundingPlanUsesIndex(
	t *testing.T,
	pool *pgxpool.Pool,
	query string,
	requiredIndex string,
) {
	t.Helper()
	var rawPlan []byte
	if err := pool.QueryRow(
		context.Background(),
		"EXPLAIN (FORMAT JSON, COSTS OFF) "+query,
	).Scan(&rawPlan); err != nil {
		t.Fatalf("explain funding query: %v", err)
	}
	var explained []struct {
		Plan postgresExplainPlan `json:"Plan"`
	}
	if err := json.Unmarshal(rawPlan, &explained); err != nil {
		t.Fatalf("decode funding plan: %v", err)
	}
	if len(explained) != 1 {
		t.Fatalf("funding plans = %d, want 1", len(explained))
	}
	var (
		indexFound    bool
		projectionSeq bool
	)
	walkPostgresPlan(explained[0].Plan, func(plan postgresExplainPlan) {
		indexFound = indexFound || plan.IndexName == requiredIndex
		projectionSeq = projectionSeq ||
			(plan.NodeType == "Seq Scan" &&
				plan.RelationName == "funding_history_projection")
	})
	if !indexFound || projectionSeq {
		t.Fatalf(
			"funding plan required index %q found=%t projection-seq=%t: %s",
			requiredIndex,
			indexFound,
			projectionSeq,
			rawPlan,
		)
	}
}
