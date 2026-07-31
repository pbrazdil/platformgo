package postgres_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

type postgresExplainPlan struct {
	NodeType     string                `json:"Node Type"`
	RelationName string                `json:"Relation Name"`
	IndexName    string                `json:"Index Name"`
	Plans        []postgresExplainPlan `json:"Plans"`
}

func TestRealtimeClaimQueryUsesHotPathIndexes(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	resetDurableSchemas(t, pool)
	migrator := newCurrentTestMigrator(
		t,
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("migrate realtime query-plan database: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		INSERT INTO trading.accounts (account_id, oms_mode)
		VALUES ('urn:xb:account:realtime-plan', 'NETTING');
		INSERT INTO identity.users (user_id, login, normalized_login)
		VALUES (
			'urn:xb:user:realtime-plan',
			'realtime-plan',
			'realtime-plan'
		);
		INSERT INTO identity.user_accounts (user_id, account_id)
		VALUES (
			'urn:xb:user:realtime-plan',
			'urn:xb:account:realtime-plan'
		);
		INSERT INTO realtime.publications (
			channel,
			event_id,
			sequence,
			schema_version,
			event_type,
			account_id,
			logical_time,
			data,
			attempts,
			next_attempt_at,
			claimed_at,
			published_at
		)
		SELECT
			'user:plan-' || channel_number,
			md5(channel_number::text || ':' || sequence_number::text)::uuid,
			sequence_number,
			1,
			'account.updated',
			'urn:xb:account:realtime-plan',
			channel_number * 1000 + sequence_number,
			'{}'::jsonb,
			CASE WHEN sequence_number < 100 THEN 1 ELSE 0 END,
			'-infinity'::timestamptz,
			CASE
				WHEN sequence_number < 100
				THEN '2026-07-25T00:00:00Z'::timestamptz
			END,
			CASE
				WHEN sequence_number < 100
				THEN '2026-07-25T00:00:01Z'::timestamptz
			END
		  FROM generate_series(1, 200) AS channels(channel_number)
		 CROSS JOIN generate_series(1, 100) AS sequences(sequence_number);
		ANALYZE realtime.publications`,
	); err != nil {
		t.Fatalf("seed representative realtime history: %v", err)
	}

	var rawPlan []byte
	if err := pool.QueryRow(ctx, `
		EXPLAIN (FORMAT JSON, COSTS OFF)
		SELECT publication.channel,
		       publication.event_id::text,
		       publication.event_type,
		       publication.account_id,
		       publication.logical_time,
		       publication.data,
		       publication.schema_version,
		       publication.sequence,
		       publication.attempts,
		       publication.retry_attempt_base
		  FROM realtime.publications AS publication
		 WHERE publication.published_at IS NULL
		   AND publication.quarantined_at IS NULL
		   AND publication.next_attempt_at <= $1::timestamptz
		   AND (
		       publication.claimed_at IS NULL
		       OR publication.claimed_at <= $2::timestamptz
		   )
		   AND NOT EXISTS (
		       SELECT 1
		         FROM realtime.publications AS predecessor
		        WHERE predecessor.channel = publication.channel
		          AND predecessor.sequence < publication.sequence
		          AND predecessor.published_at IS NULL
		   )
		 ORDER BY publication.next_attempt_at,
		          publication.channel,
		          publication.sequence
		 FOR UPDATE OF publication SKIP LOCKED
		 LIMIT $3`,
		"2026-07-26T00:00:00Z",
		"2026-07-25T23:59:30Z",
		1,
	).Scan(&rawPlan); err != nil {
		t.Fatalf("explain realtime claim query: %v", err)
	}
	var explained []struct {
		Plan postgresExplainPlan `json:"Plan"`
	}
	if err := json.Unmarshal(rawPlan, &explained); err != nil {
		t.Fatalf("decode realtime claim plan: %v", err)
	}
	if len(explained) != 1 {
		t.Fatalf("realtime claim plans = %d, want 1", len(explained))
	}
	indexes := make(map[string]bool)
	var publicationSequentialScan bool
	walkPostgresPlan(explained[0].Plan, func(plan postgresExplainPlan) {
		if plan.IndexName != "" {
			indexes[plan.IndexName] = true
		}
		if plan.NodeType == "Seq Scan" && plan.RelationName == "publications" {
			publicationSequentialScan = true
		}
	})
	for _, required := range []string{
		"realtime_publications_claim_idx",
		"realtime_publications_unpublished_predecessor_idx",
	} {
		if !indexes[required] {
			t.Fatalf(
				"realtime claim plan indexes = %v, missing %s",
				indexes,
				required,
			)
		}
	}
	if publicationSequentialScan {
		t.Fatal("realtime claim plan sequentially scans publications")
	}
}

func walkPostgresPlan(
	plan postgresExplainPlan,
	visit func(postgresExplainPlan),
) {
	visit(plan)
	for _, child := range plan.Plans {
		walkPostgresPlan(child, visit)
	}
}
