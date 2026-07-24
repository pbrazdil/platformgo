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
