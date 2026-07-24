ALTER TABLE engine.input_receipts
    ADD COLUMN business_input_hash bytea NOT NULL
        CHECK (octet_length(business_input_hash) = 32);

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

GRANT SELECT, INSERT ON engine.duplicate_delivery_receipts
    TO platformgo_engine;
