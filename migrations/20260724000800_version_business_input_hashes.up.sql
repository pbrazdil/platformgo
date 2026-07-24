ALTER TABLE engine.input_receipts
    ADD COLUMN business_input_hash_version integer NOT NULL DEFAULT 1
        CHECK (business_input_hash_version > 0);

ALTER TABLE engine.input_receipts
    ALTER COLUMN business_input_hash_version DROP DEFAULT;
