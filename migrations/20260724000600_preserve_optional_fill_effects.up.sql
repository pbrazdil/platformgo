ALTER TABLE trading.fills
    ALTER COLUMN realized_pnl DROP NOT NULL,
    ALTER COLUMN settlement_currency DROP NOT NULL,
    ALTER COLUMN fee DROP NOT NULL,
    ALTER COLUMN fee_currency DROP NOT NULL,
    ADD CONSTRAINT fills_realized_pnl_pair_check CHECK (
        (realized_pnl IS NULL AND settlement_currency IS NULL)
        OR
        (realized_pnl IS NOT NULL AND settlement_currency IS NOT NULL)
    ),
    ADD CONSTRAINT fills_fee_pair_check CHECK (
        (fee IS NULL AND fee_currency IS NULL)
        OR
        (fee IS NOT NULL AND fee_currency IS NOT NULL)
    );
