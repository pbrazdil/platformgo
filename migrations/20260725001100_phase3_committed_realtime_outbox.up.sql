-- Phase 3 committed realtime publication outbox.
--
-- Lock/rewrite: additive schema/tables plus validation of the canonical user
-- ID shape required for an injective legacy user-channel mapping; no rewrite.
-- Transaction: engine decisions allocate channel sequence and insert immutable
-- publication identity in the same transaction as economic state and outbox.
-- Delivery: projector workers may update only bounded claim/retry/ack columns.
-- Failure/retry: a publish may repeat after an ambiguous failure, but its
-- event_id and per-channel sequence remain stable for client deduplication.

CREATE SCHEMA realtime;

ALTER TABLE identity.users
ADD CONSTRAINT users_realtime_channel_identity CHECK (
    user_id COLLATE "C" ~ '^urn:xb:user:[A-Za-z0-9._~-]{1,250}$'
) NOT VALID;

ALTER TABLE identity.users
VALIDATE CONSTRAINT users_realtime_channel_identity;

CREATE TABLE realtime.channel_sequences (
    channel text PRIMARY KEY
        CHECK (channel COLLATE "C" ~ '^user:[A-Za-z0-9._~-]{1,250}$'),
    last_sequence bigint NOT NULL CHECK (last_sequence > 0)
);

CREATE TABLE realtime.publications (
    channel text NOT NULL
        CHECK (channel COLLATE "C" ~ '^user:[A-Za-z0-9._~-]{1,250}$'),
    event_id uuid NOT NULL,
    sequence bigint NOT NULL CHECK (sequence > 0),
    schema_version integer NOT NULL CHECK (schema_version > 0),
    event_type text NOT NULL CHECK (event_type IN (
        'order.created',
        'order.updated',
        'order.cancelled',
        'order.triggered',
        'order.filled',
        'order.partially_filled',
        'position.opened',
        'position.updated',
        'position.closed',
        'position.liquidated',
        'position.take_profit.hit',
        'position.stop_loss.hit',
        'account.updated',
        'trade.created'
    )),
    account_id text NOT NULL REFERENCES identity.user_accounts(account_id),
    logical_time bigint NOT NULL,
    data jsonb NOT NULL CHECK (jsonb_typeof(data) = 'object'),
    attempts integer NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    retry_attempt_base integer NOT NULL DEFAULT 0
        CHECK (retry_attempt_base >= 0 AND retry_attempt_base <= attempts),
    next_attempt_at timestamptz NOT NULL DEFAULT '-infinity',
    claimed_at timestamptz,
    published_at timestamptz,
    quarantined_at timestamptz,
    failure_class text CHECK (
        failure_class IS NULL OR failure_class IN (
            'transient',
            'permanent',
            'retry_exhausted'
        )
    ),
    last_error text,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    PRIMARY KEY (channel, event_id),
    UNIQUE (channel, sequence),
    CHECK (published_at IS NULL OR claimed_at IS NOT NULL),
    CHECK (
        (
            failure_class IS NULL
            AND last_error IS NULL
            AND quarantined_at IS NULL
        )
        OR (
            published_at IS NULL
            AND failure_class = 'transient'
            AND last_error IS NOT NULL
            AND quarantined_at IS NULL
        )
        OR (
            published_at IS NULL
            AND failure_class IN ('permanent', 'retry_exhausted')
            AND last_error IS NOT NULL
            AND quarantined_at IS NOT NULL
        )
    )
);

CREATE INDEX realtime_publications_claim_idx
ON realtime.publications (next_attempt_at, channel, sequence)
WHERE published_at IS NULL;

CREATE INDEX realtime_publications_unpublished_predecessor_idx
ON realtime.publications (channel, sequence)
WHERE published_at IS NULL;

CREATE TABLE realtime.publication_requeues (
    request_id uuid PRIMARY KEY,
    channel text NOT NULL,
    event_id uuid NOT NULL,
    authenticated_actor text NOT NULL CHECK (authenticated_actor <> ''),
    claimed_actor text NOT NULL CHECK (claimed_actor <> ''),
    reason text NOT NULL CHECK (reason <> ''),
    previous_attempts integer NOT NULL CHECK (previous_attempts > 0),
    previous_failure_class text NOT NULL CHECK (
        previous_failure_class IN ('permanent', 'retry_exhausted')
    ),
    previous_error text NOT NULL CHECK (previous_error <> ''),
    previous_quarantined_at timestamptz NOT NULL,
    requested_at timestamptz NOT NULL,
    FOREIGN KEY (channel, event_id)
        REFERENCES realtime.publications(channel, event_id)
);

CREATE TRIGGER realtime_publication_requeues_are_immutable
BEFORE UPDATE OR DELETE ON realtime.publication_requeues
FOR EACH ROW EXECUTE FUNCTION engine.reject_immutable_change();

CREATE FUNCTION realtime.allocate_channel_sequence(requested_channel text)
RETURNS bigint
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    allocated_sequence bigint;
BEGIN
    IF requested_channel COLLATE "C"
       !~ '^user:[A-Za-z0-9._~-]{1,250}$' THEN
        RAISE EXCEPTION 'invalid realtime channel'
            USING ERRCODE = '22023';
    END IF;
    INSERT INTO realtime.channel_sequences (channel, last_sequence)
    VALUES (requested_channel, 1)
    ON CONFLICT (channel) DO UPDATE SET
        last_sequence = realtime.channel_sequences.last_sequence + 1
    RETURNING last_sequence INTO allocated_sequence;
    RETURN allocated_sequence;
END;
$$;

CREATE FUNCTION engine.runtime_schema_migrations()
RETURNS TABLE (filename text, checksum bytea)
LANGUAGE sql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
    SELECT migration.filename, migration.checksum
      FROM engine.schema_migrations AS migration
     ORDER BY migration.filename
$$;

CREATE FUNCTION engine.require_runtime_schema_revision()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF current_setting(
           'platformgo.runtime_schema_revision',
           true
       ) IS DISTINCT FROM
           '20260725001100_phase3_committed_realtime_outbox' THEN
        RAISE EXCEPTION
            'engine runtime schema revision is missing or incompatible'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE TRIGGER input_receipts_require_runtime_schema_revision
BEFORE INSERT ON engine.input_receipts
FOR EACH ROW EXECUTE FUNCTION engine.require_runtime_schema_revision();

CREATE FUNCTION realtime.protect_publication_identity()
RETURNS trigger
LANGUAGE plpgsql
SET search_path = pg_catalog
AS $$
BEGIN
    IF NEW.channel IS DISTINCT FROM OLD.channel
       OR NEW.event_id IS DISTINCT FROM OLD.event_id
       OR NEW.sequence IS DISTINCT FROM OLD.sequence
       OR NEW.schema_version IS DISTINCT FROM OLD.schema_version
       OR NEW.event_type IS DISTINCT FROM OLD.event_type
       OR NEW.account_id IS DISTINCT FROM OLD.account_id
       OR NEW.logical_time IS DISTINCT FROM OLD.logical_time
       OR NEW.data IS DISTINCT FROM OLD.data
       OR NEW.created_at IS DISTINCT FROM OLD.created_at THEN
        RAISE EXCEPTION 'realtime publication identity is immutable'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.attempts < OLD.attempts
       OR NEW.attempts > OLD.attempts + 1 THEN
        RAISE EXCEPTION 'realtime publication attempts must be monotonic'
            USING ERRCODE = '55000';
    END IF;
    IF NEW.retry_attempt_base < OLD.retry_attempt_base
       OR NEW.retry_attempt_base > NEW.attempts THEN
        RAISE EXCEPTION 'realtime retry attempt base must be monotonic'
            USING ERRCODE = '55000';
    END IF;
    IF OLD.quarantined_at IS NOT NULL
       AND current_user <> (
           SELECT pg_get_userbyid(procedure.proowner)
             FROM pg_proc AS procedure
            WHERE procedure.oid =
                  'realtime.requeue_publication(uuid,text,uuid,text,text)'
                  ::regprocedure
       ) THEN
        RAISE EXCEPTION
            'quarantined realtime publication requires audited repair'
            USING ERRCODE = '42501';
    END IF;
    IF OLD.published_at IS NOT NULL
       AND (
           NEW.attempts IS DISTINCT FROM OLD.attempts
           OR NEW.retry_attempt_base IS DISTINCT FROM OLD.retry_attempt_base
           OR NEW.next_attempt_at IS DISTINCT FROM OLD.next_attempt_at
           OR NEW.claimed_at IS DISTINCT FROM OLD.claimed_at
           OR NEW.published_at IS DISTINCT FROM OLD.published_at
           OR NEW.quarantined_at IS DISTINCT FROM OLD.quarantined_at
           OR NEW.failure_class IS DISTINCT FROM OLD.failure_class
           OR NEW.last_error IS DISTINCT FROM OLD.last_error
       ) THEN
        RAISE EXCEPTION 'published realtime delivery state is immutable'
            USING ERRCODE = '55000';
    END IF;
    RETURN NEW;
END;
$$;

CREATE FUNCTION realtime.requeue_publication(
    requested_id uuid,
    requested_channel text,
    requested_event_id uuid,
    requested_actor text,
    requested_reason text
)
RETURNS void
LANGUAGE plpgsql
SECURITY DEFINER
SET search_path = pg_catalog
AS $$
DECLARE
    prior_attempts integer;
    prior_failure_class text;
    prior_error text;
    prior_quarantined_at timestamptz;
    prior_channel text;
    prior_event_id uuid;
    prior_authenticated_actor text;
    prior_claimed_actor text;
    prior_reason text;
BEGIN
    IF requested_actor = '' OR requested_reason = '' THEN
        RAISE EXCEPTION 'realtime requeue actor and reason are required'
            USING ERRCODE = '22023';
    END IF;
    PERFORM pg_advisory_xact_lock(
        hashtextextended(requested_id::text, 578721)
    );
    SELECT
        requeue.channel,
        requeue.event_id,
        requeue.authenticated_actor,
        requeue.claimed_actor,
        requeue.reason
      INTO
        prior_channel,
        prior_event_id,
        prior_authenticated_actor,
        prior_claimed_actor,
        prior_reason
      FROM realtime.publication_requeues AS requeue
     WHERE requeue.request_id = requested_id;
    IF FOUND THEN
        IF prior_channel = requested_channel
           AND prior_event_id = requested_event_id
           AND prior_authenticated_actor = session_user
           AND prior_claimed_actor = requested_actor
           AND prior_reason = requested_reason THEN
            RETURN;
        END IF;
        RAISE EXCEPTION 'realtime requeue request identity conflict'
            USING ERRCODE = '23505';
    END IF;
    SELECT publication.attempts,
           publication.failure_class,
           publication.last_error,
           publication.quarantined_at
      INTO prior_attempts,
           prior_failure_class,
           prior_error,
           prior_quarantined_at
      FROM realtime.publications AS publication
     WHERE publication.channel = requested_channel
       AND publication.event_id = requested_event_id
       AND publication.published_at IS NULL
       AND publication.quarantined_at IS NOT NULL
     FOR UPDATE;
    IF NOT FOUND THEN
        RAISE EXCEPTION 'quarantined realtime publication not found'
            USING ERRCODE = 'P0002';
    END IF;
    INSERT INTO realtime.publication_requeues (
        request_id,
        channel,
        event_id,
        authenticated_actor,
        claimed_actor,
        reason,
        previous_attempts,
        previous_failure_class,
        previous_error,
        previous_quarantined_at,
        requested_at
    ) VALUES (
        requested_id,
        requested_channel,
        requested_event_id,
        session_user,
        requested_actor,
        requested_reason,
        prior_attempts,
        prior_failure_class,
        prior_error,
        prior_quarantined_at,
        clock_timestamp()
    );
    UPDATE realtime.publications
       SET retry_attempt_base = attempts,
           next_attempt_at = clock_timestamp(),
           claimed_at = NULL,
           quarantined_at = NULL,
           failure_class = NULL,
           last_error = NULL
     WHERE channel = requested_channel
       AND event_id = requested_event_id;
END;
$$;

CREATE TRIGGER realtime_publication_identity_is_immutable
BEFORE UPDATE ON realtime.publications
FOR EACH ROW EXECUTE FUNCTION realtime.protect_publication_identity();

REVOKE ALL ON SCHEMA realtime FROM PUBLIC;
REVOKE ALL ON ALL TABLES IN SCHEMA realtime FROM PUBLIC;
REVOKE ALL ON FUNCTION realtime.allocate_channel_sequence(text) FROM PUBLIC;
REVOKE ALL ON FUNCTION realtime.protect_publication_identity() FROM PUBLIC;
REVOKE ALL ON FUNCTION realtime.requeue_publication(
    uuid,
    text,
    uuid,
    text,
    text
) FROM PUBLIC;
REVOKE ALL ON FUNCTION engine.runtime_schema_migrations() FROM PUBLIC;
REVOKE ALL ON FUNCTION engine.require_runtime_schema_revision() FROM PUBLIC;

GRANT SELECT ON identity.user_accounts TO platformgo_engine;
GRANT SELECT ON trading.order_intents TO platformgo_engine;
GRANT USAGE ON SCHEMA realtime TO platformgo_engine;
GRANT SELECT, INSERT ON realtime.publications TO platformgo_engine;
GRANT SELECT ON realtime.channel_sequences TO platformgo_engine;
GRANT EXECUTE ON FUNCTION realtime.allocate_channel_sequence(text)
TO platformgo_engine;

GRANT USAGE ON SCHEMA realtime TO platformgo_realtime;
GRANT SELECT ON realtime.publications TO platformgo_realtime;
GRANT UPDATE (
    attempts,
    next_attempt_at,
    claimed_at,
    published_at,
    quarantined_at,
    failure_class,
    last_error
) ON realtime.publications TO platformgo_realtime;

GRANT USAGE ON SCHEMA realtime TO platformgo_realtime_repair;
GRANT SELECT ON realtime.publications TO platformgo_realtime_repair;
GRANT SELECT ON realtime.publication_requeues TO platformgo_realtime_repair;
GRANT EXECUTE ON FUNCTION realtime.requeue_publication(
    uuid,
    text,
    uuid,
    text,
    text
) TO platformgo_realtime_repair;

GRANT USAGE ON SCHEMA engine TO
    platformgo_api,
    platformgo_engine,
    platformgo_outbox,
    platformgo_projector,
    platformgo_realtime,
    platformgo_realtime_repair;
GRANT EXECUTE ON FUNCTION engine.runtime_schema_migrations() TO
    platformgo_api,
    platformgo_engine,
    platformgo_outbox,
    platformgo_projector,
    platformgo_realtime,
    platformgo_realtime_repair;
