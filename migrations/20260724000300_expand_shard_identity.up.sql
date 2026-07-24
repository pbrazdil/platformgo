ALTER TABLE engine.shard_checkpoints
    ALTER COLUMN shard_id TYPE bigint;

ALTER TABLE engine.input_receipts
    ALTER COLUMN shard_id TYPE bigint;
