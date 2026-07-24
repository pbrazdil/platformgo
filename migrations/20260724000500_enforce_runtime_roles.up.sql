DO $$
BEGIN
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_api') THEN
        CREATE ROLE platformgo_api NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_engine') THEN
        CREATE ROLE platformgo_engine NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_outbox') THEN
        CREATE ROLE platformgo_outbox NOLOGIN;
    END IF;
    IF NOT EXISTS (SELECT 1 FROM pg_roles WHERE rolname = 'platformgo_projector') THEN
        CREATE ROLE platformgo_projector NOLOGIN;
    END IF;
END;
$$;

REVOKE ALL ON SCHEMA engine, trading, ledger, market, messaging FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA engine, trading, ledger, market, messaging
    FROM PUBLIC;

GRANT USAGE ON SCHEMA trading, ledger, market, messaging TO platformgo_api;
GRANT SELECT, INSERT, UPDATE ON trading.idempotency_records TO platformgo_api;
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
GRANT SELECT, INSERT ON engine.input_receipts, engine.shard_faults
    TO platformgo_engine;
GRANT SELECT, INSERT, UPDATE ON
    trading.commands,
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
GRANT SELECT, UPDATE ON messaging.outbox TO platformgo_outbox;

GRANT USAGE ON SCHEMA messaging TO platformgo_projector;
GRANT SELECT, INSERT ON messaging.inbox TO platformgo_projector;
