ALTER TABLE trading.accounts
    DROP CONSTRAINT accounts_oms_mode_check,
    ADD CONSTRAINT accounts_oms_mode_check
        CHECK (oms_mode IN ('NETTING', 'HEDGING'));

ALTER TABLE trading.risk_configs
    DROP CONSTRAINT risk_configs_margin_mode_check,
    ADD CONSTRAINT risk_configs_margin_mode_check
        CHECK (margin_mode IN ('CROSS', 'ISOLATED'));

ALTER TABLE trading.orders
    DROP CONSTRAINT orders_side_check,
    ADD CONSTRAINT orders_side_check
        CHECK (side IN ('BUY', 'SELL')),
    ADD COLUMN has_slippage_band boolean NOT NULL DEFAULT false,
    ADD COLUMN max_slippage_bps integer NOT NULL DEFAULT 0
        CHECK (max_slippage_bps >= 0),
    ADD COLUMN slippage_reference numeric(38,18);

ALTER TABLE trading.fills
    DROP CONSTRAINT fills_side_check,
    ADD CONSTRAINT fills_side_check
        CHECK (side IN ('BUY', 'SELL')),
    DROP CONSTRAINT fills_liquidity_side_check,
    ADD CONSTRAINT fills_liquidity_side_check
        CHECK (liquidity_side IN ('MAKER', 'TAKER'));

ALTER TABLE trading.positions
    DROP CONSTRAINT positions_side_check,
    ADD CONSTRAINT positions_side_check
        CHECK (side IN ('LONG', 'SHORT', 'FLAT')),
    DROP CONSTRAINT positions_margin_mode_check,
    ADD CONSTRAINT positions_margin_mode_check
        CHECK (margin_mode IN ('CROSS', 'ISOLATED'));

CREATE SCHEMA IF NOT EXISTS market;

CREATE TABLE market.books (
    instrument_id text PRIMARY KEY
        REFERENCES trading.instruments(instrument_id),
    mark_price numeric(38,18) NOT NULL CHECK (mark_price > 0),
    bids jsonb NOT NULL,
    asks jsonb NOT NULL,
    stream_sequence bigint NOT NULL CHECK (stream_sequence > 0),
    updated_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
