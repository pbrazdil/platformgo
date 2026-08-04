package postgres_test

import (
	"context"
	"crypto/sha256"
	"errors"
	"fmt"
	"slices"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
)

const predictionMarketCatalogMigration = "20260803000100_phase3_prediction_market_catalog.up.sql"

type predictionCatalogColumn struct {
	name       string
	typeName   string
	notNull    bool
	identity   string
	generated  string
	collation  string
	defaultSQL string
}

type predictionCatalogConstraint struct {
	name       string
	typeName   string
	definition string
	columns    string
	indexName  string
	referenced string
	refColumns string
	updateRule string
	deleteRule string
	deferrable bool
	deferred   bool
	validated  bool
}

type predictionCatalogIndex struct {
	name           string
	relation       string
	access         string
	unique         bool
	primary        bool
	valid          bool
	ready          bool
	live           bool
	keyCount       int
	attributeCount int
	keys           string
	options        string
	definition     string
}

var predictionCatalogRuntimeRoles = []string{
	"platformgo_api",
	"platformgo_engine",
	"platformgo_outbox",
	"platformgo_projector",
	"platformgo_realtime",
	"platformgo_realtime_repair",
}

var predictionCatalogRelations = []string{
	"trading.prediction_events",
	"trading.prediction_markets",
	"trading.prediction_legs",
}

func expectedPredictionCatalogColumns() map[string][]predictionCatalogColumn {
	return map[string][]predictionCatalogColumn{
		"trading.prediction_events": {
			{name: "event_id", typeName: "uuid", notNull: true},
			{name: "source_venue", typeName: "text", notNull: true, collation: "default"},
			{name: "event_key", typeName: "text", notNull: true, collation: "default"},
			{name: "title", typeName: "text", notNull: true, collation: "default"},
			{name: "series", typeName: "text", collation: "default"},
			{name: "status", typeName: "text", notNull: true, collation: "default"},
			{name: "created_at", typeName: "timestamp with time zone", notNull: true, defaultSQL: "clock_timestamp()"},
			{name: "updated_at", typeName: "timestamp with time zone", notNull: true, defaultSQL: "clock_timestamp()"},
		},
		"trading.prediction_markets": {
			{name: "market_id", typeName: "uuid", notNull: true},
			{name: "source_venue", typeName: "text", notNull: true, collation: "default"},
			{name: "market_key", typeName: "text", notNull: true, collation: "default"},
			{name: "question", typeName: "text", notNull: true, collation: "default"},
			{name: "resolution_time", typeName: "timestamp with time zone"},
			{name: "mutually_exclusive", typeName: "boolean", notNull: true},
			{name: "status", typeName: "text", notNull: true, collation: "default"},
			{name: "event_id", typeName: "uuid"},
			{name: "stage_label", typeName: "text", collation: "default"},
			{name: "stage_ordinal", typeName: "integer"},
			{name: "created_at", typeName: "timestamp with time zone", notNull: true, defaultSQL: "clock_timestamp()"},
			{name: "updated_at", typeName: "timestamp with time zone", notNull: true, defaultSQL: "clock_timestamp()"},
		},
		"trading.prediction_legs": {
			{name: "instrument_id", typeName: "text", notNull: true, collation: "default"},
			{name: "market_id", typeName: "uuid", notNull: true},
			{name: "display_name", typeName: "text", notNull: true, collation: "default"},
			{name: "outcome_index", typeName: "integer", notNull: true},
			{name: "outcome_label", typeName: "text", notNull: true, collation: "default"},
			{name: "enabled", typeName: "boolean", notNull: true},
		},
	}
}

func expectedPredictionCatalogConstraints() map[string][]predictionCatalogConstraint {
	check := func(name, columns, definition string) predictionCatalogConstraint {
		return predictionCatalogConstraint{
			name: name, typeName: "c", columns: columns, definition: definition,
			validated: true,
		}
	}
	primary := func(name, columns, indexName, definition string) predictionCatalogConstraint {
		return predictionCatalogConstraint{
			name: name, typeName: "p", columns: columns, indexName: indexName,
			definition: definition, validated: true,
		}
	}
	unique := func(name, columns, indexName, definition string) predictionCatalogConstraint {
		return predictionCatalogConstraint{
			name: name, typeName: "u", columns: columns, indexName: indexName,
			definition: definition, validated: true,
		}
	}
	foreign := func(name, columns, indexName, referenced, refColumns, definition string) predictionCatalogConstraint {
		return predictionCatalogConstraint{
			name: name, typeName: "f", columns: columns, referenced: referenced,
			indexName: indexName, refColumns: refColumns, updateRule: "a", deleteRule: "a",
			definition: definition, validated: true,
		}
	}
	return map[string][]predictionCatalogConstraint{
		"trading.prediction_events": {
			check("prediction_events_source_venue_check", "{2}", "CHECK ((source_venue = ANY (ARRAY['hyperliquid'::text, 'polymarket'::text, 'kalshi'::text])))"),
			check("prediction_events_event_key_check", "{3}", "CHECK ((event_key <> ''::text))"),
			check("prediction_events_title_check", "{4}", "CHECK ((title <> ''::text))"),
			check("prediction_events_status_check", "{6}", "CHECK ((status = ANY (ARRAY['open'::text, 'closed'::text, 'resolved'::text, 'settled'::text])))"),
			primary("prediction_events_pkey", "{1}", "trading.prediction_events_pkey", "PRIMARY KEY (event_id)"),
		},
		"trading.prediction_markets": {
			check("prediction_markets_source_venue_check", "{2}", "CHECK ((source_venue = ANY (ARRAY['hyperliquid'::text, 'polymarket'::text, 'kalshi'::text])))"),
			check("prediction_markets_market_key_check", "{3}", "CHECK ((market_key <> ''::text))"),
			check("prediction_markets_question_check", "{4}", "CHECK ((question <> ''::text))"),
			check("prediction_markets_status_check", "{7}", "CHECK ((status = ANY (ARRAY['open'::text, 'closed'::text, 'resolved'::text, 'settled'::text])))"),
			check("prediction_markets_stage_label_check", "{9}", "CHECK (((stage_label IS NULL) OR (stage_label <> ''::text)))"),
			check("prediction_markets_stage_ordinal_check", "{10}", "CHECK (((stage_ordinal IS NULL) OR (stage_ordinal >= 0)))"),
			primary("prediction_markets_pkey", "{1}", "trading.prediction_markets_pkey", "PRIMARY KEY (market_id)"),
			foreign("prediction_markets_event_fk", "{8}", "trading.prediction_events_pkey", "trading.prediction_events", "{1}", "FOREIGN KEY (event_id) REFERENCES trading.prediction_events(event_id)"),
		},
		"trading.prediction_legs": {
			check("prediction_legs_display_name_check", "{3}", "CHECK ((display_name <> ''::text))"),
			check("prediction_legs_outcome_index_check", "{4}", "CHECK ((outcome_index >= 0))"),
			check("prediction_legs_outcome_label_check", "{5}", "CHECK ((outcome_label <> ''::text))"),
			primary("prediction_legs_pkey", "{1}", "trading.prediction_legs_pkey", "PRIMARY KEY (instrument_id)"),
			foreign("prediction_legs_instrument_fk", "{1}", "trading.instruments_pkey", "trading.instruments", "{1}", "FOREIGN KEY (instrument_id) REFERENCES trading.instruments(instrument_id)"),
			foreign("prediction_legs_market_fk", "{2}", "trading.prediction_markets_pkey", "trading.prediction_markets", "{1}", "FOREIGN KEY (market_id) REFERENCES trading.prediction_markets(market_id)"),
			unique("prediction_legs_market_outcome_key", "{2,4}", "trading.prediction_legs_market_outcome_key", "UNIQUE (market_id, outcome_index)"),
		},
	}
}

func expectedPredictionCatalogIndexes() map[string]predictionCatalogIndex {
	return map[string]predictionCatalogIndex{
		"prediction_events_pkey": {
			name: "prediction_events_pkey", relation: "trading.prediction_events", access: "btree", unique: true, primary: true,
			valid: true, ready: true, live: true, keyCount: 1, attributeCount: 1, keys: "1", options: "0",
			definition: "CREATE UNIQUE INDEX prediction_events_pkey ON trading.prediction_events USING btree (event_id)",
		},
		"prediction_events_source_venue_event_key_idx": {
			name: "prediction_events_source_venue_event_key_idx", relation: "trading.prediction_events", access: "btree", unique: true,
			valid: true, ready: true, live: true, keyCount: 2, attributeCount: 2, keys: "2 3", options: "0 0",
			definition: "CREATE UNIQUE INDEX prediction_events_source_venue_event_key_idx ON trading.prediction_events USING btree (source_venue COLLATE \"C\", event_key COLLATE \"C\")",
		},
		"prediction_markets_pkey": {
			name: "prediction_markets_pkey", relation: "trading.prediction_markets", access: "btree", unique: true, primary: true,
			valid: true, ready: true, live: true, keyCount: 1, attributeCount: 1, keys: "1", options: "0",
			definition: "CREATE UNIQUE INDEX prediction_markets_pkey ON trading.prediction_markets USING btree (market_id)",
		},
		"prediction_markets_source_venue_market_key_idx": {
			name: "prediction_markets_source_venue_market_key_idx", relation: "trading.prediction_markets", access: "btree", unique: true,
			valid: true, ready: true, live: true, keyCount: 2, attributeCount: 2, keys: "2 3", options: "0 0",
			definition: "CREATE UNIQUE INDEX prediction_markets_source_venue_market_key_idx ON trading.prediction_markets USING btree (source_venue COLLATE \"C\", market_key COLLATE \"C\")",
		},
		"prediction_markets_catalog_order_idx": {
			name: "prediction_markets_catalog_order_idx", relation: "trading.prediction_markets", access: "btree",
			valid: true, ready: true, live: true, keyCount: 5, attributeCount: 5, keys: "10 11 2 3 1", options: "0 3 0 0 0",
			definition: "CREATE INDEX prediction_markets_catalog_order_idx ON trading.prediction_markets USING btree (stage_ordinal, created_at DESC, source_venue COLLATE \"C\", market_key COLLATE \"C\", market_id)",
		},
		"prediction_legs_pkey": {
			name: "prediction_legs_pkey", relation: "trading.prediction_legs", access: "btree", unique: true, primary: true,
			valid: true, ready: true, live: true, keyCount: 1, attributeCount: 1, keys: "1", options: "0",
			definition: "CREATE UNIQUE INDEX prediction_legs_pkey ON trading.prediction_legs USING btree (instrument_id)",
		},
		"prediction_legs_market_outcome_key": {
			name: "prediction_legs_market_outcome_key", relation: "trading.prediction_legs", access: "btree", unique: true,
			valid: true, ready: true, live: true, keyCount: 2, attributeCount: 2, keys: "2 4", options: "0 0",
			definition: "CREATE UNIQUE INDEX prediction_legs_market_outcome_key ON trading.prediction_legs USING btree (market_id, outcome_index)",
		},
		"prediction_legs_market_order_idx": {
			name: "prediction_legs_market_order_idx", relation: "trading.prediction_legs", access: "btree",
			valid: true, ready: true, live: true, keyCount: 3, attributeCount: 3, keys: "2 4 1", options: "0 0 0",
			definition: "CREATE INDEX prediction_legs_market_order_idx ON trading.prediction_legs USING btree (market_id, outcome_index, instrument_id COLLATE \"C\")",
		},
	}
}

func assertPredictionCatalogColumns(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	want := expectedPredictionCatalogColumns()
	for _, relation := range predictionCatalogRelations {
		rows, err := pool.Query(context.Background(), `
			SELECT
				attribute.attname,
				pg_catalog.format_type(attribute.atttypid, attribute.atttypmod),
				attribute.attnotnull,
				attribute.attidentity::text,
				attribute.attgenerated::text,
				CASE WHEN attribute.attcollation = 0 THEN '' ELSE coll.collname END,
				COALESCE(pg_catalog.pg_get_expr(default_value.adbin, default_value.adrelid), '')
			  FROM pg_catalog.pg_attribute AS attribute
			  LEFT JOIN pg_catalog.pg_attrdef AS default_value
				ON default_value.adrelid = attribute.attrelid
				AND default_value.adnum = attribute.attnum
			  LEFT JOIN pg_catalog.pg_collation AS coll
				ON coll.oid = attribute.attcollation
			 WHERE attribute.attrelid = $1::pg_catalog.regclass
			   AND attribute.attnum > 0
			   AND NOT attribute.attisdropped
			 ORDER BY attribute.attnum`, relation)
		if err != nil {
			t.Fatalf("query exact columns for %s: %v", relation, err)
		}
		var got []predictionCatalogColumn
		for rows.Next() {
			var column predictionCatalogColumn
			if err := rows.Scan(
				&column.name,
				&column.typeName,
				&column.notNull,
				&column.identity,
				&column.generated,
				&column.collation,
				&column.defaultSQL,
			); err != nil {
				rows.Close()
				t.Fatalf("scan exact columns for %s: %v", relation, err)
			}
			got = append(got, column)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("iterate exact columns for %s: %v", relation, err)
		}
		rows.Close()
		if !slices.Equal(got, want[relation]) {
			t.Fatalf("exact columns for %s = %#v, want %#v", relation, got, want[relation])
		}
	}
}

func assertPredictionCatalogConstraints(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	want := expectedPredictionCatalogConstraints()
	for _, relation := range predictionCatalogRelations {
		rows, err := pool.Query(context.Background(), `
			SELECT
				constraint_row.conname,
				constraint_row.contype::text,
				pg_catalog.pg_get_constraintdef(constraint_row.oid),
				constraint_row.conkey::text,
				CASE WHEN constraint_row.conindid = 0 THEN '' ELSE constraint_row.conindid::pg_catalog.regclass::text END,
				CASE WHEN constraint_row.confrelid = 0 THEN '' ELSE constraint_row.confrelid::pg_catalog.regclass::text END,
				COALESCE(constraint_row.confkey::text, ''),
				COALESCE(NULLIF(constraint_row.confupdtype::text, ' '), ''),
				COALESCE(NULLIF(constraint_row.confdeltype::text, ' '), ''),
				constraint_row.condeferrable,
				constraint_row.condeferred,
				constraint_row.convalidated
			  FROM pg_catalog.pg_constraint AS constraint_row
			 WHERE constraint_row.conrelid = $1::pg_catalog.regclass
			   AND constraint_row.contype IN ('c', 'p', 'u', 'f')
			 ORDER BY constraint_row.conname`, relation)
		if err != nil {
			t.Fatalf("query exact constraints for %s: %v", relation, err)
		}
		got := make(map[string]predictionCatalogConstraint)
		for rows.Next() {
			var constraint predictionCatalogConstraint
			if err := rows.Scan(
				&constraint.name,
				&constraint.typeName,
				&constraint.definition,
				&constraint.columns,
				&constraint.indexName,
				&constraint.referenced,
				&constraint.refColumns,
				&constraint.updateRule,
				&constraint.deleteRule,
				&constraint.deferrable,
				&constraint.deferred,
				&constraint.validated,
			); err != nil {
				rows.Close()
				t.Fatalf("scan exact constraints for %s: %v", relation, err)
			}
			got[constraint.name] = constraint
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatalf("iterate exact constraints for %s: %v", relation, err)
		}
		rows.Close()
		if len(got) != len(want[relation]) {
			t.Fatalf("constraint count for %s = %d, want %d (%v)", relation, len(got), len(want[relation]), got)
		}
		for _, expected := range want[relation] {
			actual, ok := got[expected.name]
			if !ok {
				t.Fatalf("constraint %s on %s is missing", expected.name, relation)
			}
			if actual != expected {
				t.Fatalf("constraint %s on %s = %#v, want %#v", expected.name, relation, actual, expected)
			}
		}
	}
}

func assertPredictionCatalogIndexes(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	want := expectedPredictionCatalogIndexes()
	rows, err := pool.Query(context.Background(), `
		SELECT
			index_class.relname,
			index_row.indrelid::pg_catalog.regclass::text,
			access_method.amname,
			index_row.indisunique,
			index_row.indisprimary,
			index_row.indisvalid,
			index_row.indisready,
			index_row.indislive,
			index_row.indnkeyatts,
			index_row.indnatts,
			index_row.indkey::text,
			index_row.indoption::text,
			pg_catalog.pg_get_indexdef(index_row.indexrelid)
		  FROM pg_catalog.pg_index AS index_row
		  JOIN pg_catalog.pg_class AS index_class ON index_class.oid = index_row.indexrelid
		  JOIN pg_catalog.pg_am AS access_method ON access_method.oid = index_class.relam
		 WHERE index_row.indrelid IN (
				'trading.prediction_events'::pg_catalog.regclass,
				'trading.prediction_markets'::pg_catalog.regclass,
				'trading.prediction_legs'::pg_catalog.regclass
			)
		 ORDER BY index_class.relname`)
	if err != nil {
		t.Fatalf("query exact prediction indexes: %v", err)
	}
	got := make(map[string]predictionCatalogIndex)
	for rows.Next() {
		var index predictionCatalogIndex
		if err := rows.Scan(
			&index.name,
			&index.relation,
			&index.access,
			&index.unique,
			&index.primary,
			&index.valid,
			&index.ready,
			&index.live,
			&index.keyCount,
			&index.attributeCount,
			&index.keys,
			&index.options,
			&index.definition,
		); err != nil {
			rows.Close()
			t.Fatalf("scan exact prediction indexes: %v", err)
		}
		got[index.name] = index
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate exact prediction indexes: %v", err)
	}
	rows.Close()
	if len(got) != len(want) {
		t.Fatalf("prediction index count = %d, want %d (%v)", len(got), len(want), got)
	}
	for name, expected := range want {
		actual, ok := got[name]
		if !ok {
			t.Fatalf("prediction index %s is missing", name)
		}
		if actual != expected {
			t.Fatalf("prediction index %s = %#v, want %#v", name, actual, expected)
		}
	}
}

func assertPredictionCatalogPhysicalShape(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	for _, relation := range predictionCatalogRelations {
		var (
			owner, expectedOwner, access                                        string
			relkind, persistence                                                string
			rls, forceRLS, partition, rules, triggers, subclass                 bool
			toastRelation, toastOwner, toastKind, toastPersistence, toastAccess string
			toastPartition, toastRules, toastTriggers, toastSubclass            bool
		)
		if err := pool.QueryRow(ctx, `
			SELECT
				relation.relowner::pg_catalog.regrole::text,
				authority.relowner::pg_catalog.regrole::text,
				access_method.amname,
				relation.relkind::text,
				relation.relpersistence::text,
				relation.relrowsecurity,
				relation.relforcerowsecurity,
				relation.relispartition,
				relation.relhasrules,
				relation.relhastriggers,
				relation.relhassubclass,
				COALESCE(toast.oid::pg_catalog.regclass::text, ''),
				COALESCE(toast.relowner::pg_catalog.regrole::text, ''),
				COALESCE(toast.relkind::text, ''),
				COALESCE(toast.relpersistence::text, ''),
				COALESCE(toast_access_method.amname, ''),
				COALESCE(toast.relispartition, false),
				COALESCE(toast.relhasrules, false),
				COALESCE(toast.relhastriggers, false),
				COALESCE(toast.relhassubclass, false)
			  FROM pg_catalog.pg_class AS relation
			  JOIN pg_catalog.pg_class AS authority
				ON authority.oid = 'engine.schema_migrations'::pg_catalog.regclass
			  JOIN pg_catalog.pg_am AS access_method ON access_method.oid = relation.relam
			  LEFT JOIN pg_catalog.pg_class AS toast ON toast.oid = relation.reltoastrelid
			  LEFT JOIN pg_catalog.pg_am AS toast_access_method ON toast_access_method.oid = toast.relam
			 WHERE relation.oid = $1::pg_catalog.regclass`, relation).Scan(
			&owner,
			&expectedOwner,
			&access,
			&relkind,
			&persistence,
			&rls,
			&forceRLS,
			&partition,
			&rules,
			&triggers,
			&subclass,
			&toastRelation,
			&toastOwner,
			&toastKind,
			&toastPersistence,
			&toastAccess,
			&toastPartition,
			&toastRules,
			&toastTriggers,
			&toastSubclass,
		); err != nil {
			t.Fatalf("inspect exact physical shape for %s: %v", relation, err)
		}
		if owner != expectedOwner || access != "heap" || relkind != "r" || persistence != "p" ||
			rls || forceRLS || partition || rules || subclass || toastRelation == "" || toastOwner != owner ||
			toastKind != "t" || toastPersistence != "p" || toastAccess != "heap" || toastPartition || toastRules || toastSubclass {
			t.Fatalf("physical shape for %s = owner=%q/%q access=%q kind=%q persistence=%q rls=%t force=%t partition=%t rules=%t triggers=%t subclass=%t toast=%q owner=%q kind=%q persistence=%q access=%q partition=%t rules=%t triggers=%t subclass=%t", relation, owner, expectedOwner, access, relkind, persistence, rls, forceRLS, partition, rules, triggers, subclass, toastRelation, toastOwner, toastKind, toastPersistence, toastAccess, toastPartition, toastRules, toastTriggers, toastSubclass)
		}
	}
	var userTriggers, relationRules int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::integer
		  FROM pg_catalog.pg_trigger
		 WHERE tgrelid IN (
				'trading.prediction_events'::pg_catalog.regclass,
				'trading.prediction_markets'::pg_catalog.regclass,
				'trading.prediction_legs'::pg_catalog.regclass
			)
		   AND NOT tgisinternal`).Scan(&userTriggers); err != nil {
		t.Fatalf("inspect prediction user triggers: %v", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::integer
		  FROM pg_catalog.pg_rewrite
		 WHERE ev_class IN (
				'trading.prediction_events'::pg_catalog.regclass,
				'trading.prediction_markets'::pg_catalog.regclass,
				'trading.prediction_legs'::pg_catalog.regclass
			)`).Scan(&relationRules); err != nil {
		t.Fatalf("inspect prediction relation rules: %v", err)
	}
	if userTriggers != 0 || relationRules != 0 {
		t.Fatalf("prediction user triggers/rules = %d/%d, want 0/0", userTriggers, relationRules)
	}

	rows, err := pool.Query(ctx, `
		SELECT index_class.relname,
			   index_class.relowner::pg_catalog.regrole::text,
			   authority.relowner::pg_catalog.regrole::text,
			   index_class.relkind::text,
			   index_class.relpersistence::text,
			   index_class.relhasrules,
			   index_class.relhassubclass
		  FROM pg_catalog.pg_index AS index_row
		  JOIN pg_catalog.pg_class AS index_class ON index_class.oid = index_row.indexrelid
		  JOIN pg_catalog.pg_class AS authority ON authority.oid = 'engine.schema_migrations'::pg_catalog.regclass
		 WHERE index_row.indrelid IN (
				'trading.prediction_events'::pg_catalog.regclass,
				'trading.prediction_markets'::pg_catalog.regclass,
				'trading.prediction_legs'::pg_catalog.regclass
			)
		 ORDER BY index_class.relname`)
	if err != nil {
		t.Fatalf("inspect prediction index physical shape: %v", err)
	}
	indexCount := 0
	for rows.Next() {
		indexCount++
		var name, owner, expectedOwner, kind, persistence string
		var rules, subclass bool
		if err := rows.Scan(&name, &owner, &expectedOwner, &kind, &persistence, &rules, &subclass); err != nil {
			rows.Close()
			t.Fatalf("scan prediction index physical shape: %v", err)
		}
		if owner != expectedOwner || kind != "i" || persistence != "p" || rules || subclass {
			t.Fatalf("prediction index %s shape = owner=%q/%q kind=%q persistence=%q rules=%t subclass=%t", name, owner, expectedOwner, kind, persistence, rules, subclass)
		}
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate prediction index physical shape: %v", err)
	}
	rows.Close()
	if indexCount != len(expectedPredictionCatalogIndexes()) {
		t.Fatalf("prediction index physical-shape count = %d, want %d", indexCount, len(expectedPredictionCatalogIndexes()))
	}
}

func predictionCatalogRawACL(t *testing.T, pool *pgxpool.Pool, relation string) []string {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT
			CASE WHEN privilege.grantee = 0 THEN 'public' ELSE pg_catalog.pg_get_userbyid(privilege.grantee) END,
			privilege.privilege_type,
			privilege.is_grantable,
			CASE WHEN privilege.grantor = 0 THEN 'public' ELSE pg_catalog.pg_get_userbyid(privilege.grantor) END
		  FROM pg_catalog.pg_class AS relation
		  CROSS JOIN LATERAL pg_catalog.aclexplode(
				COALESCE(relation.relacl, pg_catalog.acldefault('r', relation.relowner))
			) AS privilege
		 WHERE relation.oid = $1::pg_catalog.regclass
		 ORDER BY 1, 2, 3, 4`, relation)
	if err != nil {
		t.Fatalf("query raw ACL for %s: %v", relation, err)
	}
	var got []string
	for rows.Next() {
		var grantee, privilege, grantor string
		var grantable bool
		if err := rows.Scan(&grantee, &privilege, &grantable, &grantor); err != nil {
			rows.Close()
			t.Fatalf("scan raw ACL for %s: %v", relation, err)
		}
		got = append(got, fmt.Sprintf("%s|%s|%t|%s", grantee, privilege, grantable, grantor))
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		t.Fatalf("iterate raw ACL for %s: %v", relation, err)
	}
	rows.Close()
	return got
}

func assertPredictionCatalogACL(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx := context.Background()
	var owner string
	if err := pool.QueryRow(ctx, `
		SELECT relation.relowner::pg_catalog.regrole::text
		  FROM pg_catalog.pg_class AS relation
		 WHERE relation.oid = 'engine.schema_migrations'::pg_catalog.regclass`).Scan(&owner); err != nil {
		t.Fatalf("read prediction catalog owner: %v", err)
	}
	ownerPrivileges := []string{"DELETE", "INSERT", "MAINTAIN", "REFERENCES", "SELECT", "TRIGGER", "TRUNCATE", "UPDATE"}
	for _, relation := range predictionCatalogRelations {
		want := make([]string, 0, len(ownerPrivileges)+1)
		for _, privilege := range ownerPrivileges {
			want = append(want, fmt.Sprintf("%s|%s|false|%s", owner, privilege, owner))
		}
		want = append(want, fmt.Sprintf("platformgo_api|SELECT|false|%s", owner))
		slices.Sort(want)
		if got := predictionCatalogRawACL(t, pool, relation); !slices.Equal(got, want) {
			t.Fatalf("raw ACL for %s = %v, want exact %v", relation, got, want)
		}
		var columnACLs int
		if err := pool.QueryRow(ctx, `
			SELECT count(*)::integer
			  FROM pg_catalog.pg_attribute
			 WHERE attrelid = $1::pg_catalog.regclass
			   AND attnum > 0
			   AND NOT attisdropped
			   AND attacl IS NOT NULL`, relation).Scan(&columnACLs); err != nil {
			t.Fatalf("inspect column ACL for %s: %v", relation, err)
		}
		if columnACLs != 0 {
			t.Fatalf("column ACL rows for %s = %d, want 0", relation, columnACLs)
		}

		for _, role := range append([]string{"public"}, predictionCatalogRuntimeRoles...) {
			for _, privilege := range ownerPrivileges {
				var got bool
				if err := pool.QueryRow(ctx, `SELECT has_table_privilege($1, $2, $3)`, role, relation, privilege).Scan(&got); err != nil {
					t.Fatalf("inspect %s %s on %s: %v", role, privilege, relation, err)
				}
				want := role == "platformgo_api" && privilege == "SELECT"
				if got != want {
					t.Fatalf("%s %s on %s = %t, want %t", role, privilege, relation, got, want)
				}
			}
		}
	}
	var membershipEdges int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)::integer
		  FROM pg_catalog.pg_auth_members AS membership
		  JOIN pg_catalog.pg_roles AS member_role ON member_role.oid = membership.member
		  JOIN pg_catalog.pg_roles AS parent_role ON parent_role.oid = membership.roleid
		 WHERE member_role.rolname = ANY($1::text[])
		    OR parent_role.rolname = ANY($1::text[])`, predictionCatalogRuntimeRoles).Scan(&membershipEdges); err != nil {
		t.Fatalf("inspect runtime-role membership edges: %v", err)
	}
	if membershipEdges != 0 {
		t.Fatalf("runtime-role membership edges = %d, want 0", membershipEdges)
	}
}

// The current-tip harness applies the additive catalog from a clean schema.
// This test keeps the schema, relationship, ordering-index, and least-
// privilege boundary executable without introducing a population writer.
func TestPostgresPredictionMarketCatalogMigrationSchemaAndACL(t *testing.T) {
	ctx := context.Background()
	pool := predictionMarketCatalogCurrentPool(t)

	assertPredictionCatalogColumns(t, pool)
	assertPredictionCatalogConstraints(t, pool)
	assertPredictionCatalogIndexes(t, pool)
	assertPredictionCatalogPhysicalShape(t, pool)
	assertPredictionCatalogACL(t, pool)

	for _, relation := range []string{
		"trading.prediction_events",
		"trading.prediction_markets",
		"trading.prediction_legs",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, "SELECT to_regclass($1) IS NOT NULL", relation).
			Scan(&exists); err != nil {
			t.Fatalf("inspect %s: %v", relation, err)
		}
		if !exists {
			t.Fatalf("relation %s does not exist", relation)
		}
	}

	for _, expected := range []struct {
		relation   string
		constraint string
	}{
		{"trading.prediction_events", "prediction_events_pkey"},
		{"trading.prediction_markets", "prediction_markets_pkey"},
		{"trading.prediction_markets", "prediction_markets_event_fk"},
		{"trading.prediction_legs", "prediction_legs_pkey"},
		{"trading.prediction_legs", "prediction_legs_instrument_fk"},
		{"trading.prediction_legs", "prediction_legs_market_fk"},
		{"trading.prediction_legs", "prediction_legs_market_outcome_key"},
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_constraint
				 WHERE conrelid = $1::regclass
				   AND conname = $2
			)`, expected.relation, expected.constraint).Scan(&exists); err != nil {
			t.Fatalf("inspect constraint %s: %v", expected.constraint, err)
		}
		if !exists {
			t.Fatalf("constraint %s on %s is missing", expected.constraint, expected.relation)
		}
	}

	for _, indexName := range []string{
		"prediction_events_source_venue_event_key_idx",
		"prediction_markets_source_venue_market_key_idx",
		"prediction_markets_catalog_order_idx",
		"prediction_legs_market_order_idx",
	} {
		var exists bool
		if err := pool.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_indexes
				 WHERE schemaname = 'trading'
				   AND indexname = $1
			)`, indexName).Scan(&exists); err != nil {
			t.Fatalf("inspect index %s: %v", indexName, err)
		}
		if !exists {
			t.Fatalf("index %s is missing", indexName)
		}
	}

	for _, relation := range []string{
		"trading.prediction_events",
		"trading.prediction_markets",
		"trading.prediction_legs",
	} {
		var (
			ownerMatches bool
			validShape   bool
			aclRows      int
			ownerACLRows int
			apiACLRows   int
			invalidACL   int
			columnACLs   int
		)
		if err := pool.QueryRow(ctx, `
			SELECT
				relation.relowner = authority.relowner,
				relation.relkind = 'r'
					AND relation.relpersistence = 'p'
					AND NOT relation.relrowsecurity
					AND NOT relation.relforcerowsecurity
					AND NOT relation.relispartition
			FROM pg_catalog.pg_class AS relation
			CROSS JOIN LATERAL (
				SELECT relowner
				  FROM pg_catalog.pg_class
				 WHERE oid = 'engine.schema_migrations'::pg_catalog.regclass
			) AS authority
			WHERE relation.oid = $1::pg_catalog.regclass`, relation).Scan(
			&ownerMatches,
			&validShape,
		); err != nil {
			t.Fatalf("inspect raw owner/shape for %s: %v", relation, err)
		}
		if !ownerMatches || !validShape {
			t.Fatalf("raw owner/shape for %s is invalid: owner=%t shape=%t", relation, ownerMatches, validShape)
		}
		if err := pool.QueryRow(ctx, `
			SELECT
				count(*)::integer,
				count(*) FILTER (WHERE privilege.grantee = relation.relowner)::integer,
				count(*) FILTER (
					WHERE privilege.grantee = 'platformgo_api'::pg_catalog.regrole
				)::integer,
				count(*) FILTER (
					WHERE NOT (
						(privilege.grantee = relation.relowner
						 AND privilege.grantor = relation.relowner
						 AND NOT privilege.is_grantable
						 AND privilege.privilege_type IN (
							'INSERT', 'SELECT', 'UPDATE', 'DELETE',
							'TRUNCATE', 'REFERENCES', 'TRIGGER', 'MAINTAIN'
						 ))
						OR (privilege.grantee = 'platformgo_api'::pg_catalog.regrole
						 AND privilege.grantor = relation.relowner
						 AND NOT privilege.is_grantable
						 AND privilege.privilege_type = 'SELECT')
					)
				)::integer
			FROM pg_catalog.pg_class AS relation
			CROSS JOIN LATERAL pg_catalog.aclexplode(
				COALESCE(
					relation.relacl,
					pg_catalog.acldefault('r', relation.relowner)
				)
			) AS privilege
			WHERE relation.oid = $1::pg_catalog.regclass`, relation).Scan(
			&aclRows,
			&ownerACLRows,
			&apiACLRows,
			&invalidACL,
		); err != nil {
			t.Fatalf("inspect raw ACL for %s: %v", relation, err)
		}
		if aclRows != 9 || ownerACLRows != 8 || apiACLRows != 1 || invalidACL != 0 {
			t.Fatalf(
				"raw ACL for %s = rows %d owner %d api %d invalid %d, want 9/8/1/0",
				relation, aclRows, ownerACLRows, apiACLRows, invalidACL,
			)
		}
		if err := pool.QueryRow(ctx, `
			SELECT count(*)::integer
			  FROM pg_catalog.pg_attribute
			 WHERE attrelid = $1::pg_catalog.regclass
			   AND attnum > 0
			   AND NOT attisdropped
			   AND attacl IS NOT NULL`, relation).Scan(&columnACLs); err != nil {
			t.Fatalf("inspect column ACL for %s: %v", relation, err)
		}
		if columnACLs != 0 {
			t.Fatalf("raw column ACL rows for %s = %d, want 0", relation, columnACLs)
		}

		for _, role := range []string{"public", "platformgo_engine"} {
			var selectPrivilege, insertPrivilege, updatePrivilege, deletePrivilege bool
			if err := pool.QueryRow(ctx, `
				SELECT
					has_table_privilege($1, $2, 'SELECT'),
					has_table_privilege($1, $2, 'INSERT'),
					has_table_privilege($1, $2, 'UPDATE'),
					has_table_privilege($1, $2, 'DELETE')`, role, relation).Scan(
				&selectPrivilege,
				&insertPrivilege,
				&updatePrivilege,
				&deletePrivilege,
			); err != nil {
				t.Fatalf("inspect %s ACL on %s: %v", role, relation, err)
			}
			if selectPrivilege || insertPrivilege || updatePrivilege || deletePrivilege {
				t.Fatalf(
					"unexpected %s ACL on %s: select=%t insert=%t update=%t delete=%t",
					role,
					relation,
					selectPrivilege,
					insertPrivilege,
					updatePrivilege,
					deletePrivilege,
				)
			}
		}
		var apiSelect, apiInsert, apiUpdate, apiDelete bool
		if err := pool.QueryRow(ctx, `
			SELECT
				has_table_privilege('platformgo_api', $1, 'SELECT'),
				has_table_privilege('platformgo_api', $1, 'INSERT'),
				has_table_privilege('platformgo_api', $1, 'UPDATE'),
				has_table_privilege('platformgo_api', $1, 'DELETE')`, relation).Scan(
			&apiSelect,
			&apiInsert,
			&apiUpdate,
			&apiDelete,
		); err != nil {
			t.Fatalf("inspect platformgo_api ACL on %s: %v", relation, err)
		}
		if !apiSelect || apiInsert || apiUpdate || apiDelete {
			t.Fatalf(
				"unexpected platformgo_api ACL on %s: select=%t insert=%t update=%t delete=%t",
				relation,
				apiSelect,
				apiInsert,
				apiUpdate,
				apiDelete,
			)
		}
	}

	var beforeCount int
	if err := pool.QueryRow(ctx,
		"SELECT count(*) FROM engine.schema_migrations").Scan(&beforeCount); err != nil {
		t.Fatalf("read migration count before rerun: %v", err)
	}
	if err := newExactCurrentTestMigrator(t, pool, currentMigrationFS()).Migrate(ctx); err != nil {
		t.Fatalf("rerun current migrations: %v", err)
	}
	var afterCount int
	var tip string
	if err := pool.QueryRow(ctx, `
		SELECT count(*), max(filename) FROM engine.schema_migrations`).Scan(
		&afterCount,
		&tip,
	); err != nil {
		t.Fatalf("read migration count after rerun: %v", err)
	}
	if afterCount != beforeCount || tip != predictionMarketCatalogMigration {
		t.Fatalf(
			"rerun migration history = count %d tip %q, want count %d tip %q",
			afterCount,
			tip,
			beforeCount,
			predictionMarketCatalogMigration,
		)
	}
}

// An enabled event trigger is an unsafe catalog authority. The migration
// must reject it before creating any prediction relation, preserve tip 43,
// and become retryable after the operator removes that hostile authority.
func TestPostgresPredictionMarketCatalogMigrationRollsBackOnEventTrigger(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newPredictionMarketTip43Fixture(t, true)
	pool := fixture.admin
	fixture.demote(t)
	if _, err := pool.Exec(ctx, `
		CREATE FUNCTION public.prediction_catalog_event_trigger()
		RETURNS event_trigger
		LANGUAGE plpgsql
		AS $$ BEGIN RETURN; END $$;
		CREATE EVENT TRIGGER prediction_catalog_event_trigger
			ON ddl_command_start
			EXECUTE FUNCTION public.prediction_catalog_event_trigger()`); err != nil {
		t.Fatalf("install hostile event trigger: %v", err)
	}
	defer func() {
		_, _ = pool.Exec(ctx, `
			DROP EVENT TRIGGER IF EXISTS prediction_catalog_event_trigger;
			DROP FUNCTION IF EXISTS public.prediction_catalog_event_trigger()`)
	}()

	err := platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("hostile event-trigger migration error = %v, want SQLSTATE 55000", err)
	}
	var (
		tip          string
		marketExists bool
	)
	if err := pool.QueryRow(ctx, `
		SELECT
			max(filename),
			to_regclass('trading.prediction_markets') IS NOT NULL
		  FROM engine.schema_migrations`).Scan(&tip, &marketExists); err != nil {
		t.Fatalf("inspect rolled-back prediction catalog migration: %v", err)
	}
	if tip != runtimeAuthorityACLMigration || marketExists {
		t.Fatalf("rolled-back prediction migration state = tip %q markets=%t", tip, marketExists)
	}

	if _, err := pool.Exec(ctx, `
		DROP EVENT TRIGGER prediction_catalog_event_trigger;
		DROP FUNCTION public.prediction_catalog_event_trigger()`); err != nil {
		t.Fatalf("remove hostile event trigger: %v", err)
	}
	if _, err := pool.Exec(ctx, `
		ALTER DEFAULT PRIVILEGES IN SCHEMA trading
			GRANT SELECT ON TABLES TO platformgo_api`); err != nil {
		t.Fatalf("install hostile default privilege: %v", err)
	}
	err = platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	).Migrate(ctx)
	if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
		t.Fatalf("hostile default-ACL migration error = %v, want SQLSTATE 55000", err)
	}
	if err := pool.QueryRow(ctx, `
		SELECT max(filename) FROM engine.schema_migrations`).Scan(&tip); err != nil {
		t.Fatalf("inspect default-ACL rollback tip: %v", err)
	}
	if tip != runtimeAuthorityACLMigration {
		t.Fatalf("default-ACL rollback tip = %q, want %q", tip, runtimeAuthorityACLMigration)
	}
	if _, err := pool.Exec(ctx, `
		ALTER DEFAULT PRIVILEGES IN SCHEMA trading
			REVOKE SELECT ON TABLES FROM platformgo_api`); err != nil {
		t.Fatalf("remove hostile default privilege: %v", err)
	}
	if err := platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	).Migrate(ctx); err != nil {
		t.Fatalf("retry prediction catalog migration: %v", err)
	}
	var retryTip string
	if err := pool.QueryRow(ctx,
		"SELECT max(filename) FROM engine.schema_migrations").Scan(&retryTip); err != nil {
		t.Fatalf("inspect retried prediction migration: %v", err)
	}
	if retryTip != predictionMarketCatalogMigration {
		t.Fatalf("retried prediction migration tip = %q, want %q", retryTip, predictionMarketCatalogMigration)
	}
}

func TestPostgresPredictionMarketCatalogMigrationPreservesTip43DataAndFilenode(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newPredictionMarketTip43Fixture(t, true)
	if _, err := fixture.owner.Exec(ctx, `
		INSERT INTO trading.instruments (
			instrument_id,
			revision,
			price_scale,
			quantity_scale,
			settlement_currency,
			settlement_currency_scale,
			initial_margin_rate,
			maintenance_margin_rate,
			max_leverage,
			maker_fee_rate,
			taker_fee_rate
		) VALUES (
			'BTC-PERP',
			1,
			2,
			3,
			'USD',
			2,
			'0.100000000000000000',
			'0.050000000000000000',
			'10.000000000000000000',
			'0.001000000000000000',
			'0.002000000000000000'
		)`); err != nil {
		t.Fatalf("seed tip-43 instrument: %v", err)
	}
	var (
		beforeFilenode uint64
		beforeCount    int
		beforeID       string
		beforeRevision int64
	)
	if err := fixture.admin.QueryRow(ctx, `
		SELECT relfilenode::bigint
		  FROM pg_catalog.pg_class
		 WHERE oid = 'trading.instruments'::pg_catalog.regclass`).Scan(&beforeFilenode); err != nil {
		t.Fatalf("inspect tip-43 instruments filenode: %v", err)
	}
	if err := fixture.admin.QueryRow(ctx, `
		SELECT count(*)::integer, max(instrument_id), max(revision)
		  FROM trading.instruments`).Scan(&beforeCount, &beforeID, &beforeRevision); err != nil {
		t.Fatalf("inspect tip-43 instrument row: %v", err)
	}

	migratePredictionMarketCatalogAsDemotedExactOwner(t, fixture)

	var (
		afterFilenode uint64
		afterCount    int
		afterID       string
		afterRevision int64
	)
	if err := fixture.admin.QueryRow(ctx, `
		SELECT relfilenode::bigint
		  FROM pg_catalog.pg_class
		 WHERE oid = 'trading.instruments'::pg_catalog.regclass`).Scan(&afterFilenode); err != nil {
		t.Fatalf("inspect tip-44 instruments filenode: %v", err)
	}
	if err := fixture.admin.QueryRow(ctx, `
		SELECT count(*)::integer, max(instrument_id), max(revision)
		  FROM trading.instruments`).Scan(&afterCount, &afterID, &afterRevision); err != nil {
		t.Fatalf("inspect tip-44 instrument row: %v", err)
	}
	if afterFilenode != beforeFilenode || afterCount != beforeCount ||
		afterID != beforeID || afterRevision != beforeRevision {
		t.Fatalf(
			"tip-43 instruments changed across migration: filenode %d/%d rows %d/%d id %q/%q revision %d/%d",
			afterFilenode, beforeFilenode, afterCount, beforeCount,
			afterID, beforeID, afterRevision, beforeRevision,
		)
	}
}

func TestPostgresPredictionMarketCatalogMigrationGlobalFenceRejectsAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newPredictionMarketTip43Fixture(t, true)
	fixture.demote(t)
	const globalFenceKey int64 = 88288443778895

	fenceConn, err := fixture.admin.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire global-fence connection: %v", err)
	}
	defer fenceConn.Release()
	if _, err := fenceConn.Exec(ctx,
		`SELECT pg_catalog.pg_advisory_lock($1::bigint)`, globalFenceKey); err != nil {
		t.Fatalf("hold global maintenance fence: %v", err)
	}
	defer func() {
		_, _ = fenceConn.Exec(context.Background(),
			`SELECT pg_catalog.pg_advisory_unlock($1::bigint)`, globalFenceKey)
	}()

	var beforeCount int
	if err := fixture.admin.QueryRow(ctx,
		`SELECT count(*)::integer FROM engine.schema_migrations`).Scan(&beforeCount); err != nil {
		t.Fatalf("inspect fenced tip-43 history: %v", err)
	}
	err = platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf("fenced migration error = %v, want SQLSTATE 55P03", err)
	}
	var (
		afterCount  int
		marketExist bool
	)
	if err := fixture.admin.QueryRow(ctx, `
		SELECT
			count(*)::integer,
			to_regclass('trading.prediction_markets') IS NOT NULL
		  FROM engine.schema_migrations`).Scan(&afterCount, &marketExist); err != nil {
		t.Fatalf("inspect fenced rollback: %v", err)
	}
	if afterCount != beforeCount || marketExist {
		t.Fatalf("fenced migration changed tip-43 state: count %d/%d markets=%t", afterCount, beforeCount, marketExist)
	}
	if _, err := fenceConn.Exec(ctx,
		`SELECT pg_catalog.pg_advisory_unlock($1::bigint)`, globalFenceKey); err != nil {
		t.Fatalf("release global maintenance fence for retry: %v", err)
	}

	migrator := platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	)
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after global fence release: %v", err)
	}
	if err := migrator.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retried migration after global fence release: %v", err)
	}
}

func TestPostgresPredictionMarketCatalogMigrationShardContentionRejectsAndRetries(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newPredictionMarketTip43Fixture(t, true)
	fixture.demote(t)
	const shardNamespace int64 = 1346850639
	const shardID int64 = 7

	fenceConn, err := fixture.admin.Acquire(ctx)
	if err != nil {
		t.Fatalf("acquire shard-fence connection: %v", err)
	}
	defer fenceConn.Release()
	if _, err := fenceConn.Exec(ctx,
		`SELECT pg_catalog.pg_advisory_lock($1, $2)`, shardNamespace, shardID); err != nil {
		t.Fatalf("hold shard writer fence: %v", err)
	}
	defer func() {
		_, _ = fenceConn.Exec(context.Background(),
			`SELECT pg_catalog.pg_advisory_unlock($1, $2)`, shardNamespace, shardID)
	}()

	var beforeCount int
	if err := fixture.admin.QueryRow(ctx,
		`SELECT count(*)::integer FROM engine.schema_migrations`).Scan(&beforeCount); err != nil {
		t.Fatalf("inspect shard-contended tip-43 history: %v", err)
	}
	err = platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf("shard-contended migration error = %v, want SQLSTATE 55P03", err)
	}
	var (
		afterCount  int
		marketExist bool
	)
	if err := fixture.admin.QueryRow(ctx, `
		SELECT count(*)::integer,
		       to_regclass('trading.prediction_markets') IS NOT NULL
		  FROM engine.schema_migrations`).Scan(&afterCount, &marketExist); err != nil {
		t.Fatalf("inspect shard-contended rollback: %v", err)
	}
	if afterCount != beforeCount || marketExist {
		t.Fatalf("shard-contended migration changed tip-43 state: count %d/%d markets=%t", afterCount, beforeCount, marketExist)
	}
	if _, err := fenceConn.Exec(ctx,
		`SELECT pg_catalog.pg_advisory_unlock($1, $2)`, shardNamespace, shardID); err != nil {
		t.Fatalf("release shard writer fence for retry: %v", err)
	}

	migrator := platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	)
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after shard-fence release: %v", err)
	}
	if err := migrator.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retried migration after shard-fence release: %v", err)
	}
}

func TestPostgresPredictionMarketCatalogMigrationBootstrapFunctionContention(
	t *testing.T,
) {
	ctx := context.Background()
	fixture := newPredictionMarketTip43Fixture(t, true)
	fixture.demote(t)
	guardTx, err := fixture.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin bootstrap-function guard: %v", err)
	}
	t.Cleanup(func() { _ = guardTx.Rollback(context.Background()) })
	if _, err := guardTx.Exec(ctx, `
		SELECT pg_catalog.pg_get_object_address(
			'function',
			ARRAY['identity', 'bootstrap_first_admin'],
			ARRAY['text', 'bytea', 'text', 'uuid', 'text', 'bytea']
		)`); err != nil {
		t.Fatalf("hold bootstrap-function object lock: %v", err)
	}
	dropTx, err := fixture.admin.Begin(ctx)
	if err != nil {
		t.Fatalf("begin bootstrap-function drop guard: %v", err)
	}
	t.Cleanup(func() { _ = dropTx.Rollback(context.Background()) })
	var dropPID int32
	if err := dropTx.QueryRow(ctx, `SELECT pg_backend_pid()`).Scan(&dropPID); err != nil {
		t.Fatalf("read bootstrap-function drop backend PID: %v", err)
	}
	dropResult := make(chan error, 1)
	go func() {
		_, dropErr := dropTx.Exec(ctx, `
			DROP FUNCTION identity.bootstrap_first_admin(
				text, bytea, text, uuid, text, bytea
			)`)
		dropResult <- dropErr
	}()
	const bootstrapFunction = "identity.bootstrap_first_admin(text,bytea,text,uuid,text,bytea)"
	deadline := time.Now().Add(3 * time.Second)
	for {
		var waiting bool
		if err := fixture.admin.QueryRow(ctx, `
			SELECT EXISTS (
				SELECT 1
				  FROM pg_catalog.pg_locks
				 WHERE pid = $1
				   AND locktype = 'object'
				   AND classid = 'pg_catalog.pg_proc'::pg_catalog.regclass
				   AND objid = $2::pg_catalog.regprocedure
				   AND mode = 'AccessExclusiveLock'
				   AND NOT granted
			)`, dropPID, bootstrapFunction).Scan(&waiting); err != nil {
			t.Fatalf("inspect bootstrap-function drop wait: %v", err)
		}
		if waiting {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("bootstrap-function drop did not reach object lock wait")
		}
		select {
		case dropErr := <-dropResult:
			t.Fatalf("bootstrap-function drop completed before guard release: %v", dropErr)
		case <-time.After(10 * time.Millisecond):
		}
	}

	var beforeCount int
	if err := fixture.admin.QueryRow(ctx,
		`SELECT count(*)::integer FROM engine.schema_migrations`).Scan(&beforeCount); err != nil {
		t.Fatalf("inspect function-contended tip-43 history: %v", err)
	}
	err = platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	).Migrate(ctx)
	var postgresError *pgconn.PgError
	if !errors.As(err, &postgresError) || postgresError.Code != "55P03" {
		t.Fatalf("function-contended migration error = %v, want SQLSTATE 55P03", err)
	}
	var (
		afterCount  int
		marketExist bool
	)
	if err := fixture.admin.QueryRow(ctx, `
		SELECT count(*)::integer,
		       to_regclass('trading.prediction_markets') IS NOT NULL
		  FROM engine.schema_migrations`).Scan(&afterCount, &marketExist); err != nil {
		t.Fatalf("inspect function-contended rollback: %v", err)
	}
	if afterCount != beforeCount || marketExist {
		t.Fatalf("function-contended migration changed tip-43 state: count %d/%d markets=%t", afterCount, beforeCount, marketExist)
	}
	if err := guardTx.Rollback(ctx); err != nil {
		t.Fatalf("release bootstrap-function object lock for retry: %v", err)
	}
	if dropErr := <-dropResult; dropErr != nil {
		t.Fatalf("bootstrap-function drop did not continue after guard release: %v", dropErr)
	}
	if err := dropTx.Rollback(ctx); err != nil {
		t.Fatalf("restore bootstrap function after contention: %v", err)
	}

	migrator := platformpostgres.NewMigrator(
		fixture.owner,
		migrationFilesThrough(t, predictionMarketCatalogMigration),
	)
	if err := migrator.Migrate(ctx); err != nil {
		t.Fatalf("retry migration after function-object release: %v", err)
	}
	if err := migrator.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify retried migration after function-object release: %v", err)
	}
}

func TestPostgresPredictionMarketCatalogMigrationRejectsDivergentTip43Manifest(
	t *testing.T,
) {
	ctx := context.Background()
	testCases := []struct {
		name   string
		mutate func(*testing.T, *predictionMarketTip43Fixture)
		repair func(*testing.T, *predictionMarketTip43Fixture)
	}{
		{
			name: "wrong checksum",
			mutate: func(t *testing.T, fixture *predictionMarketTip43Fixture) {
				t.Helper()
				if _, err := fixture.admin.Exec(ctx, `
					UPDATE engine.schema_migrations
					   SET checksum = decode(repeat('00', 32), 'hex')
					 WHERE filename = '20260731000200_phase3_runtime_authority_acl.up.sql'`); err != nil {
					t.Fatalf("install wrong tip-43 checksum: %v", err)
				}
			},
			repair: func(t *testing.T, fixture *predictionMarketTip43Fixture) {
				t.Helper()
				checksum := migrationFilesThrough(t, runtimeAuthorityACLMigration)[runtimeAuthorityACLMigration].Data
				expected := sha256.Sum256(checksum)
				if _, err := fixture.admin.Exec(ctx, `
					UPDATE engine.schema_migrations
					   SET checksum = $1
					 WHERE filename = '20260731000200_phase3_runtime_authority_acl.up.sql'`, expected[:]); err != nil {
					t.Fatalf("repair tip-43 checksum: %v", err)
				}
			},
		},
		{
			name: "extra row",
			mutate: func(t *testing.T, fixture *predictionMarketTip43Fixture) {
				t.Helper()
				if _, err := fixture.admin.Exec(ctx, `
					INSERT INTO engine.schema_migrations (filename, checksum)
					VALUES ('20260731000250_adversarial_extra.up.sql', decode(repeat('11', 32), 'hex'))`); err != nil {
					t.Fatalf("install extra tip-43 journal row: %v", err)
				}
			},
			repair: func(t *testing.T, fixture *predictionMarketTip43Fixture) {
				t.Helper()
				if _, err := fixture.admin.Exec(ctx, `
					DELETE FROM engine.schema_migrations
					 WHERE filename = '20260731000250_adversarial_extra.up.sql'`); err != nil {
					t.Fatalf("remove extra tip-43 journal row: %v", err)
				}
			},
		},
		{
			name: "missing row",
			mutate: func(t *testing.T, fixture *predictionMarketTip43Fixture) {
				t.Helper()
				if _, err := fixture.admin.Exec(ctx, `
					DELETE FROM engine.schema_migrations
					 WHERE filename = '20260731000200_phase3_runtime_authority_acl.up.sql'`); err != nil {
					t.Fatalf("remove tip-43 journal row: %v", err)
				}
			},
			repair: func(t *testing.T, fixture *predictionMarketTip43Fixture) {
				t.Helper()
				checksum := migrationFilesThrough(t, runtimeAuthorityACLMigration)[runtimeAuthorityACLMigration].Data
				expected := sha256.Sum256(checksum)
				if _, err := fixture.admin.Exec(ctx, `
					INSERT INTO engine.schema_migrations (filename, checksum)
					VALUES ('20260731000200_phase3_runtime_authority_acl.up.sql', $1)`, expected[:]); err != nil {
					t.Fatalf("restore missing tip-43 journal row: %v", err)
				}
			},
		},
	}

	for _, testCase := range testCases {
		t.Run(testCase.name, func(t *testing.T) {
			fixture := newPredictionMarketTip43Fixture(t, true)
			testCase.mutate(t, fixture)
			fixture.demote(t)
			var beforeCount int
			if err := fixture.admin.QueryRow(ctx,
				`SELECT count(*)::integer FROM engine.schema_migrations`).Scan(&beforeCount); err != nil {
				t.Fatalf("inspect divergent tip-43 history: %v", err)
			}
			file := migrationFilesThrough(t, predictionMarketCatalogMigration)[predictionMarketCatalogMigration]
			tx, err := fixture.owner.Begin(ctx)
			if err != nil {
				t.Fatalf("begin direct migration-body execution: %v", err)
			}
			_, err = tx.Exec(ctx, string(file.Data))
			if rollbackErr := tx.Rollback(ctx); rollbackErr != nil && err == nil {
				err = rollbackErr
			}
			var postgresError *pgconn.PgError
			if !errors.As(err, &postgresError) || postgresError.Code != "55000" {
				t.Fatalf("%s manifest error = %v, want SQLSTATE 55000", testCase.name, err)
			}
			var afterCount int
			var marketExist bool
			if err := fixture.admin.QueryRow(ctx, `
				SELECT count(*)::integer,
				       to_regclass('trading.prediction_markets') IS NOT NULL
				  FROM engine.schema_migrations`).Scan(&afterCount, &marketExist); err != nil {
				t.Fatalf("inspect %s manifest rollback: %v", testCase.name, err)
			}
			if afterCount != beforeCount || marketExist {
				t.Fatalf("%s manifest changed tip-43 state: count %d/%d markets=%t", testCase.name, afterCount, beforeCount, marketExist)
			}
			testCase.repair(t, fixture)
			migrator := platformpostgres.NewMigrator(
				fixture.owner,
				migrationFilesThrough(t, predictionMarketCatalogMigration),
			)
			if err := migrator.Migrate(ctx); err != nil {
				t.Fatalf("retry after %s manifest repair: %v", testCase.name, err)
			}
			if err := migrator.VerifyCurrent(ctx); err != nil {
				t.Fatalf("verify retry after %s manifest repair: %v", testCase.name, err)
			}
		})
	}
}
