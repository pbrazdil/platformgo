-- Owner-approved pre-release baseline. No Phase 2 migration was released on
-- main, and the only database with the superseded development history was
-- explicitly disposable and recreated before this baseline was accepted.

DO $$
DECLARE
    required_role text;
BEGIN
    FOREACH required_role IN ARRAY ARRAY[
        'platformgo_api',
        'platformgo_engine',
        'platformgo_outbox',
        'platformgo_projector'
    ]
    LOOP
        IF NOT EXISTS (
            SELECT 1
              FROM pg_roles
             WHERE rolname = required_role
               AND NOT rolcanlogin
               AND NOT rolsuper
               AND NOT rolcreatedb
               AND NOT rolcreaterole
               AND NOT rolreplication
               AND NOT rolbypassrls
        ) THEN
            RAISE EXCEPTION
                'required pre-provisioned runtime role % is missing or unsafe',
                required_role
                USING ERRCODE = '42501';
        END IF;
    END LOOP;
END;
$$;

CREATE SCHEMA IF NOT EXISTS engine;
CREATE SCHEMA IF NOT EXISTS trading;
CREATE SCHEMA IF NOT EXISTS ledger;
CREATE SCHEMA IF NOT EXISTS messaging;
CREATE SCHEMA IF NOT EXISTS market;

CREATE TABLE IF NOT EXISTS engine.schema_migrations (
    filename text PRIMARY KEY,
    checksum bytea NOT NULL CHECK (octet_length(checksum) = 32),
    applied_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE OR REPLACE FUNCTION engine.reject_immutable_change()
RETURNS trigger
LANGUAGE plpgsql
AS $$
BEGIN
    RAISE EXCEPTION 'immutable relation %.% cannot be %',
        TG_TABLE_SCHEMA, TG_TABLE_NAME, lower(TG_OP)
        USING ERRCODE = '55000';
END;
$$;

CREATE TABLE engine.shard_checkpoints (
    shard_id bigint PRIMARY KEY CHECK (shard_id >= 0),
    next_stream_sequence bigint NOT NULL CHECK (next_stream_sequence > 0),
    ready boolean NOT NULL,
    state_hash bytea NOT NULL CHECK (octet_length(state_hash) = 32),
    state_snapshot jsonb NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE engine.input_receipts (
    shard_id bigint NOT NULL CHECK (shard_id >= 0),
    input_id uuid NOT NULL,
    stream_sequence bigint NOT NULL CHECK (stream_sequence > 0),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    input_hash_version integer NOT NULL CHECK (input_hash_version > 0),
    input_hash bytea NOT NULL CHECK (octet_length(input_hash) = 32),
    decision_hash_version integer NOT NULL CHECK (decision_hash_version > 0),
    decision_hash bytea NOT NULL CHECK (octet_length(decision_hash) = 32),
    resulting_state_hash bytea NOT NULL CHECK (octet_length(resulting_state_hash) = 32),
    envelope jsonb NOT NULL,
    decision jsonb NOT NULL,
    committed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    business_input_hash bytea NOT NULL
        CHECK (octet_length(business_input_hash) = 32),
    business_input_hash_version integer NOT NULL
        CHECK (business_input_hash_version > 0),
    PRIMARY KEY (shard_id, input_id),
    UNIQUE (shard_id, stream_sequence)
);

CREATE TRIGGER input_receipts_are_immutable
BEFORE UPDATE OR DELETE ON engine.input_receipts
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TABLE engine.shard_faults (
    shard_id bigint NOT NULL CHECK (shard_id >= 0),
    resulting_state_hash bytea NOT NULL
        CHECK (octet_length(resulting_state_hash) = 32),
    input_id uuid NOT NULL,
    stream_sequence bigint NOT NULL CHECK (stream_sequence > 0),
    error_kind text NOT NULL CHECK (error_kind <> ''),
    error_detail text NOT NULL,
    envelope jsonb NOT NULL,
    supplied_action bytea NOT NULL,
    committed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (shard_id, resulting_state_hash)
);

CREATE TRIGGER shard_faults_are_immutable
BEFORE UPDATE OR DELETE ON engine.shard_faults
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TABLE engine.duplicate_delivery_receipts (
    shard_id bigint NOT NULL CHECK (shard_id >= 0),
    stream_sequence bigint NOT NULL CHECK (stream_sequence > 0),
    input_id uuid NOT NULL,
    input_hash bytea NOT NULL CHECK (octet_length(input_hash) = 32),
    original_decision_hash bytea NOT NULL
        CHECK (octet_length(original_decision_hash) = 32),
    decision_hash bytea NOT NULL CHECK (octet_length(decision_hash) = 32),
    resulting_state_hash bytea NOT NULL
        CHECK (octet_length(resulting_state_hash) = 32),
    envelope jsonb NOT NULL,
    decision jsonb NOT NULL,
    committed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (shard_id, stream_sequence)
);

CREATE INDEX duplicate_delivery_input_idx
ON engine.duplicate_delivery_receipts (shard_id, input_id, stream_sequence);

CREATE TRIGGER duplicate_delivery_receipts_are_immutable
BEFORE UPDATE OR DELETE ON engine.duplicate_delivery_receipts
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TABLE engine.account_shards (
    account_id text PRIMARY KEY CHECK (account_id <> ''),
    shard_id bigint NOT NULL CHECK (shard_id >= 0),
    assigned_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TRIGGER account_shards_are_immutable
BEFORE UPDATE OR DELETE ON engine.account_shards
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TABLE trading.idempotency_records (
    scope text NOT NULL CHECK (scope <> ''),
    idempotency_key text NOT NULL CHECK (idempotency_key <> ''),
    request_hash bytea NOT NULL CHECK (octet_length(request_hash) = 32),
    command_id uuid NOT NULL,
    state text NOT NULL CHECK (state IN ('in_progress', 'completed')),
    response_status integer,
    response_headers jsonb,
    response_body bytea,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    expires_at timestamptz NOT NULL,
    PRIMARY KEY (scope, idempotency_key),
    UNIQUE (command_id),
    CHECK (
        (state = 'in_progress' AND response_status IS NULL AND response_headers IS NULL AND response_body IS NULL)
        OR
        (state = 'completed' AND response_status IS NOT NULL AND response_headers IS NOT NULL AND response_body IS NOT NULL)
    )
);

CREATE TABLE trading.commands (
    command_id uuid PRIMARY KEY,
    account_id text NOT NULL CHECK (account_id <> ''),
    account_sequence bigint NOT NULL CHECK (account_sequence > 0),
    command_type text NOT NULL CHECK (command_type <> ''),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    canonical_payload jsonb NOT NULL,
    status text NOT NULL CHECK (status IN ('pending', 'accepted', 'rejected', 'completed')),
    result jsonb,
    logical_time timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz,
    UNIQUE (account_id, account_sequence),
    CHECK (
        (status = 'pending' AND completed_at IS NULL)
        OR
        (status <> 'pending' AND completed_at IS NOT NULL)
    )
);

CREATE TABLE trading.instruments (
    instrument_id text PRIMARY KEY CHECK (instrument_id <> ''),
    revision bigint NOT NULL CHECK (revision > 0),
    price_scale smallint NOT NULL CHECK (price_scale BETWEEN 0 AND 18),
    quantity_scale smallint NOT NULL CHECK (quantity_scale BETWEEN 0 AND 18),
    settlement_currency text NOT NULL CHECK (settlement_currency <> ''),
    settlement_currency_scale smallint NOT NULL CHECK (settlement_currency_scale BETWEEN 0 AND 18),
    initial_margin_rate numeric(38,18) NOT NULL CHECK (initial_margin_rate >= 0),
    maintenance_margin_rate numeric(38,18) NOT NULL CHECK (maintenance_margin_rate >= 0),
    max_leverage numeric(38,18) NOT NULL CHECK (max_leverage > 0),
    maker_fee_rate numeric(38,18) NOT NULL CHECK (maker_fee_rate BETWEEN -1 AND 1),
    taker_fee_rate numeric(38,18) NOT NULL CHECK (taker_fee_rate BETWEEN -1 AND 1),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0)
);

CREATE TABLE trading.accounts (
    account_id text PRIMARY KEY CHECK (account_id <> ''),
    oms_mode text NOT NULL CHECK (oms_mode IN ('NETTING', 'HEDGING')),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0)
);

CREATE TABLE trading.risk_configs (
    account_id text NOT NULL REFERENCES trading.accounts(account_id),
    instrument_id text NOT NULL REFERENCES trading.instruments(instrument_id),
    margin_mode text NOT NULL CHECK (margin_mode IN ('CROSS', 'ISOLATED')),
    leverage numeric(38,18) NOT NULL CHECK (leverage > 0),
    version bigint NOT NULL DEFAULT 1 CHECK (version > 0),
    PRIMARY KEY (account_id, instrument_id)
);

CREATE TABLE trading.orders (
    order_id uuid PRIMARY KEY,
    account_id text NOT NULL REFERENCES trading.accounts(account_id),
    instrument_id text NOT NULL REFERENCES trading.instruments(instrument_id),
    side text NOT NULL CHECK (side IN ('BUY', 'SELL')),
    order_type text NOT NULL CHECK (order_type <> ''),
    time_in_force text NOT NULL CHECK (time_in_force <> ''),
    status text NOT NULL CHECK (status <> ''),
    quantity numeric(38,18) NOT NULL CHECK (quantity > 0),
    filled_quantity numeric(38,18) NOT NULL CHECK (filled_quantity >= 0 AND filled_quantity <= quantity),
    average_fill_price numeric(38,18) NOT NULL CHECK (average_fill_price >= 0),
    limit_price numeric(38,18),
    trigger_price numeric(38,18),
    triggered boolean NOT NULL,
    triggered_at timestamptz,
    reduce_only boolean NOT NULL,
    position_id uuid,
    bracket_id uuid,
    bracket_leg text,
    bracket_leg_index integer CHECK (bracket_leg_index IS NULL OR bracket_leg_index >= 0),
    has_rested boolean NOT NULL,
    reject_reason text,
    version bigint NOT NULL CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    has_slippage_band boolean NOT NULL DEFAULT false,
    max_slippage_bps integer NOT NULL DEFAULT 0
        CHECK (max_slippage_bps >= 0),
    slippage_reference numeric(38,18),
    CHECK ((triggered AND triggered_at IS NOT NULL) OR (NOT triggered)),
    CHECK (limit_price IS NULL OR limit_price > 0),
    CHECK (trigger_price IS NULL OR trigger_price > 0)
);

CREATE INDEX orders_account_status_idx
ON trading.orders (account_id, status, order_id);

CREATE TABLE trading.fills (
    fill_id uuid PRIMARY KEY,
    order_id uuid NOT NULL REFERENCES trading.orders(order_id),
    input_id uuid NOT NULL,
    account_id text NOT NULL REFERENCES trading.accounts(account_id),
    instrument_id text NOT NULL REFERENCES trading.instruments(instrument_id),
    side text NOT NULL CHECK (side IN ('BUY', 'SELL')),
    price numeric(38,18) NOT NULL CHECK (price > 0),
    quantity numeric(38,18) NOT NULL CHECK (quantity > 0),
    position_id uuid NOT NULL,
    position_effect text NOT NULL CHECK (position_effect <> ''),
    realized_pnl numeric(38,18),
    settlement_currency text CHECK (settlement_currency <> ''),
    liquidity_side text NOT NULL CHECK (liquidity_side IN ('MAKER', 'TAKER')),
    fee numeric(38,18),
    fee_currency text CHECK (fee_currency <> ''),
    logical_time timestamptz NOT NULL,
    UNIQUE (input_id, fill_id),
    CONSTRAINT fills_realized_pnl_pair_check CHECK (
        (realized_pnl IS NULL AND settlement_currency IS NULL)
        OR
        (realized_pnl IS NOT NULL AND settlement_currency IS NOT NULL)
    ),
    CONSTRAINT fills_fee_pair_check CHECK (
        (fee IS NULL AND fee_currency IS NULL)
        OR
        (fee IS NOT NULL AND fee_currency IS NOT NULL)
    )
);

CREATE TRIGGER fills_are_immutable
BEFORE UPDATE OR DELETE ON trading.fills
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TABLE trading.positions (
    position_id uuid PRIMARY KEY,
    account_id text NOT NULL REFERENCES trading.accounts(account_id),
    instrument_id text NOT NULL REFERENCES trading.instruments(instrument_id),
    side text NOT NULL CHECK (side IN ('LONG', 'SHORT', 'FLAT')),
    status text NOT NULL CHECK (status <> ''),
    signed_quantity numeric(38,18) NOT NULL,
    average_open_price numeric(38,18) NOT NULL CHECK (average_open_price >= 0),
    realized_pnl numeric(38,18) NOT NULL,
    settlement_currency text NOT NULL CHECK (settlement_currency <> ''),
    margin_mode text NOT NULL CHECK (margin_mode IN ('CROSS', 'ISOLATED')),
    isolated_collateral numeric(38,18) NOT NULL CHECK (isolated_collateral >= 0),
    version bigint NOT NULL CHECK (version > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    UNIQUE (account_id, instrument_id, position_id)
);

CREATE TABLE trading.funding_settlements (
    funding_id uuid PRIMARY KEY,
    settlement_id uuid NOT NULL,
    position_id uuid NOT NULL,
    input_id uuid NOT NULL,
    account_id text NOT NULL REFERENCES trading.accounts(account_id),
    instrument_id text NOT NULL REFERENCES trading.instruments(instrument_id),
    signed_quantity numeric(38,18) NOT NULL,
    oracle_price numeric(38,18) NOT NULL CHECK (oracle_price > 0),
    rate numeric(38,18) NOT NULL,
    amount numeric(38,18) NOT NULL,
    settlement_currency text NOT NULL CHECK (settlement_currency <> ''),
    UNIQUE (settlement_id, position_id)
);

CREATE TRIGGER funding_settlements_are_immutable
BEFORE UPDATE OR DELETE ON trading.funding_settlements
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TABLE ledger.transactions (
    transaction_id uuid PRIMARY KEY,
    business_key text NOT NULL UNIQUE CHECK (business_key <> ''),
    input_id uuid NOT NULL,
    logical_time timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE ledger.entries (
    entry_id uuid PRIMARY KEY,
    transaction_id uuid NOT NULL REFERENCES ledger.transactions(transaction_id),
    account_id text NOT NULL CHECK (account_id <> ''),
    currency text NOT NULL CHECK (currency <> ''),
    amount numeric(38,18) NOT NULL CHECK (amount <> 0)
);

CREATE INDEX ledger_entries_account_currency_idx
ON ledger.entries (account_id, currency, transaction_id);

CREATE OR REPLACE FUNCTION ledger.assert_transaction_balanced()
RETURNS trigger
LANGUAGE plpgsql
AS $$
DECLARE
    affected_transaction uuid;
BEGIN
    affected_transaction := COALESCE(NEW.transaction_id, OLD.transaction_id);
    IF EXISTS (
        SELECT currency
        FROM ledger.entries
        WHERE transaction_id = affected_transaction
        GROUP BY currency
        HAVING sum(amount) <> 0
    ) THEN
        RAISE EXCEPTION 'ledger transaction % is not balanced', affected_transaction
            USING ERRCODE = '23514';
    END IF;
    RETURN NULL;
END;
$$;

CREATE CONSTRAINT TRIGGER ledger_transaction_must_balance
AFTER INSERT OR UPDATE OR DELETE ON ledger.entries
DEFERRABLE INITIALLY DEFERRED
FOR EACH ROW EXECUTE FUNCTION ledger.assert_transaction_balanced();

CREATE TRIGGER ledger_transactions_are_immutable
BEFORE UPDATE OR DELETE ON ledger.transactions
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TRIGGER ledger_entries_are_immutable
BEFORE UPDATE OR DELETE ON ledger.entries
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE TABLE ledger.balances (
    account_id text NOT NULL,
    currency text NOT NULL CHECK (currency <> ''),
    total numeric(38,18) NOT NULL,
    used numeric(38,18) NOT NULL CHECK (used >= 0),
    free numeric(38,18) NOT NULL,
    equity numeric(38,18) NOT NULL,
    ledger_sequence bigint NOT NULL CHECK (ledger_sequence >= 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (account_id, currency),
    CHECK (free = equity - used)
);

CREATE TABLE market.books (
    instrument_id text PRIMARY KEY
        REFERENCES trading.instruments(instrument_id),
    mark_price numeric(38,18) NOT NULL CHECK (mark_price > 0),
    bids jsonb NOT NULL,
    asks jsonb NOT NULL,
    stream_sequence bigint NOT NULL CHECK (stream_sequence > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);

CREATE TABLE messaging.outbox (
    message_id uuid PRIMARY KEY,
    subject text NOT NULL CHECK (subject <> ''),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    payload jsonb NOT NULL,
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    next_attempt_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    claimed_at timestamptz,
    published_at timestamptz,
    publish_sequence bigint CHECK (publish_sequence IS NULL OR publish_sequence > 0),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    CHECK (
        (published_at IS NULL AND publish_sequence IS NULL)
        OR
        (published_at IS NOT NULL AND publish_sequence IS NOT NULL)
    )
);

CREATE INDEX outbox_pending_idx
ON messaging.outbox (next_attempt_at, created_at, message_id)
WHERE published_at IS NULL;

CREATE TABLE messaging.inbox (
    consumer text NOT NULL CHECK (consumer <> ''),
    message_id uuid NOT NULL,
    processed_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (consumer, message_id)
);

CREATE TRIGGER inbox_is_immutable
BEFORE UPDATE OR DELETE ON messaging.inbox
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

REVOKE ALL ON SCHEMA engine, trading, ledger, market, messaging FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA engine, trading, ledger, market, messaging
    FROM PUBLIC;

GRANT USAGE ON SCHEMA engine, trading, ledger, market, messaging
    TO platformgo_api;
GRANT SELECT, INSERT ON engine.account_shards TO platformgo_api;
GRANT SELECT, INSERT ON trading.idempotency_records TO platformgo_api;
GRANT UPDATE (
    state,
    response_status,
    response_headers,
    response_body
) ON trading.idempotency_records TO platformgo_api;
GRANT SELECT, INSERT ON trading.commands TO platformgo_api;
GRANT SELECT, INSERT ON messaging.outbox TO platformgo_api;
GRANT SELECT ON
    trading.instruments,
    trading.accounts,
    trading.risk_configs,
    trading.orders,
    trading.fills,
    trading.positions,
    trading.funding_settlements,
    ledger.balances,
    market.books
TO platformgo_api;

GRANT USAGE ON SCHEMA engine, trading, ledger, market, messaging
    TO platformgo_engine;
GRANT SELECT, INSERT, UPDATE ON engine.shard_checkpoints
    TO platformgo_engine;
GRANT SELECT, INSERT ON
    engine.input_receipts,
    engine.shard_faults,
    engine.duplicate_delivery_receipts
TO platformgo_engine;
GRANT SELECT ON engine.account_shards TO platformgo_engine;
GRANT SELECT ON trading.commands TO platformgo_engine;
GRANT UPDATE (
    status,
    result,
    completed_at
) ON trading.commands TO platformgo_engine;
GRANT SELECT, INSERT, UPDATE ON
    trading.instruments,
    trading.accounts,
    trading.risk_configs,
    trading.orders,
    trading.positions
TO platformgo_engine;
GRANT SELECT, INSERT ON trading.fills, trading.funding_settlements
    TO platformgo_engine;
GRANT SELECT, INSERT ON ledger.transactions, ledger.entries
    TO platformgo_engine;
GRANT SELECT, INSERT, UPDATE ON ledger.balances, market.books
    TO platformgo_engine;
GRANT SELECT, INSERT ON messaging.outbox TO platformgo_engine;

GRANT USAGE ON SCHEMA messaging TO platformgo_outbox;
GRANT SELECT ON messaging.outbox TO platformgo_outbox;
GRANT UPDATE (
    attempts,
    next_attempt_at,
    claimed_at,
    published_at,
    publish_sequence,
    last_error
) ON messaging.outbox TO platformgo_outbox;

GRANT USAGE ON SCHEMA messaging TO platformgo_projector;
GRANT SELECT, INSERT ON messaging.inbox TO platformgo_projector;
