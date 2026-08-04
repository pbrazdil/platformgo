package postgres_test

import (
	"context"
	"errors"
	"testing"
	"time"

	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

func TestOutboxRetriesUnknownOutcomeWithStableMessageID(t *testing.T) {
	pool := currentProvisionedStorePool(t, 7)

	messageID := engine.IDFromSequence(engine.ID{}, 41)
	if _, err := pool.Exec(context.Background(), `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload, next_attempt_at
		) VALUES ($1,'domain.v1.order.filled',1,$2,$3)`,
		messageID.String(),
		[]byte(`{"orderId":"order-1"}`),
		time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC),
	); err != nil {
		t.Fatalf("seed outbox: %v", err)
	}

	publisher := &unknownOutcomePublisher{}
	store := platformpostgres.NewMessagingStore(pool)
	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	_, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		now,
		10,
		time.Minute,
		time.Second,
	)
	if !errors.Is(err, errUnknownPublishOutcome) {
		t.Fatalf("first publish error = %v, want unknown outcome", err)
	}

	published, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		now.Add(time.Second),
		10,
		time.Minute,
		time.Second,
	)
	if err != nil {
		t.Fatalf("retry PublishOutboxBatch: %v", err)
	}
	if published != 1 {
		t.Fatalf("published = %d, want 1", published)
	}
	if len(publisher.ids) != 2 ||
		publisher.ids[0] != messageID.String() ||
		publisher.ids[1] != messageID.String() {
		t.Fatalf("publish IDs = %v, want stable %s twice", publisher.ids, messageID)
	}

	var attempts int
	var sequence *uint64
	if err := pool.QueryRow(context.Background(), `
		SELECT attempts, publish_sequence
		  FROM messaging.outbox
		 WHERE message_id = $1`,
		messageID.String(),
	).Scan(&attempts, &sequence); err != nil {
		t.Fatalf("read published outbox: %v", err)
	}
	if attempts != 2 || sequence == nil || *sequence != 71 {
		t.Fatalf("outbox attempts=%d sequence=%v, want 2 and 71", attempts, sequence)
	}
}

func TestOutboxDoesNotPublishLaterAccountCommandBeforeEarlierCommand(t *testing.T) {
	pool := currentProvisionedStorePool(t, 7)

	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	journal := platformpostgres.NewCommandJournal(pool)
	commandIDs := []engine.ID{
		engine.IDFromSequence(engine.ID{}, 201),
		engine.IDFromSequence(engine.ID{}, 202),
	}
	for index, commandID := range commandIDs {
		sequence := uint64(index + 1)
		request := validCommandRequest(
			t,
			commandID,
			"account-1",
			sequence,
			7,
			now,
		)
		request.RequestHash = [32]byte{byte(sequence)}
		if _, err := journal.Begin(context.Background(), request); err != nil {
			t.Fatalf("Begin sequence %d: %v", sequence, err)
		}
	}

	publisher := &failFirstPublisher{}
	store := platformpostgres.NewMessagingStore(pool)
	publishNow := time.Now().UTC().Add(time.Second)
	if published, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		publishNow,
		10,
		time.Minute,
		time.Second,
	); !errors.Is(err, errUnknownPublishOutcome) || published != 0 {
		t.Fatalf("first publish = %d, error %v", published, err)
	}
	if len(publisher.ids) != 1 || publisher.ids[0] != commandIDs[0] {
		t.Fatalf("first publish IDs = %v, want only %s", publisher.ids, commandIDs[0])
	}

	if published, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		publishNow.Add(time.Second),
		10,
		time.Minute,
		time.Second,
	); err != nil || published != 1 {
		t.Fatalf("sequence 1 retry = %d, error %v", published, err)
	}
	if len(publisher.ids) != 2 || publisher.ids[1] != commandIDs[0] {
		t.Fatalf("retry publish IDs = %v, want sequence 1 twice", publisher.ids)
	}

	if published, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		publishNow.Add(2*time.Second),
		10,
		time.Minute,
		time.Second,
	); err != nil || published != 1 {
		t.Fatalf("sequence 2 publish = %d, error %v", published, err)
	}
	if len(publisher.ids) != 3 || publisher.ids[2] != commandIDs[1] {
		t.Fatalf("ordered publish IDs = %v, want %v", publisher.ids, commandIDs)
	}
}

func TestOutboxBlocksMissingPredecessorAndRejectsCorruptCommandBinding(t *testing.T) {
	tests := []struct {
		name   string
		mutate func(context.Context, *pgxpool.Pool, []engine.ID) error
	}{
		{
			name: "missing predecessor outbox",
			mutate: func(
				ctx context.Context,
				pool *pgxpool.Pool,
				ids []engine.ID,
			) error {
				_, err := pool.Exec(
					ctx,
					"DELETE FROM messaging.outbox WHERE message_id = $1",
					ids[0].String(),
				)
				return err
			},
		},
		{
			name: "corrupt current envelope",
			mutate: func(
				ctx context.Context,
				pool *pgxpool.Pool,
				ids []engine.ID,
			) error {
				_, err := pool.Exec(ctx, `
					UPDATE messaging.outbox
					   SET payload = jsonb_set(payload, '{sourceSequence}', '2')
					 WHERE message_id = $1`,
					ids[0].String(),
				)
				return err
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			ctx := context.Background()
			pool := currentProvisionedStorePool(t, 7)
			now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
			ids := []engine.ID{
				engine.IDFromSequence(engine.ID{}, 211),
				engine.IDFromSequence(engine.ID{}, 212),
			}
			for index, id := range ids {
				request := validCommandRequest(
					t,
					id,
					"account-claim",
					uint64(index+1),
					7,
					now.Add(time.Duration(index)*time.Second),
				)
				request.RequestHash = [32]byte{byte(index + 1)}
				if _, err := platformpostgres.NewCommandJournal(pool).Begin(
					ctx,
					request,
				); err != nil {
					t.Fatalf("Begin %d: %v", index+1, err)
				}
			}
			if err := test.mutate(ctx, pool, ids); err != nil {
				t.Fatalf("mutate: %v", err)
			}
			publisher := &claimRequiringPublisher{}
			published, err := platformpostgres.NewMessagingStore(pool).
				PublishOutboxBatch(
					ctx,
					publisher,
					time.Now().UTC().Add(time.Second),
					10,
					time.Minute,
					time.Second,
				)
			if test.name == "missing predecessor outbox" {
				if err != nil || published != 0 || len(publisher.messages) != 0 {
					t.Fatalf(
						"missing predecessor publish = %d messages %d error %v",
						published,
						len(publisher.messages),
						err,
					)
				}
				return
			}
			if !errors.Is(err, errMissingCommandClaim) ||
				published != 0 ||
				len(publisher.messages) != 1 {
				t.Fatalf(
					"corrupt binding publish = %d messages %d error %v",
					published,
					len(publisher.messages),
					err,
				)
			}
		})
	}
}

func TestOutboxRuntimeRoleExecutesProductionClaimAndRepublish(t *testing.T) {
	adminPool := currentProvisionedStorePool(t, 7)

	now := time.Date(2026, time.July, 24, 12, 0, 0, 0, time.UTC)
	commandID := engine.IDFromSequence(engine.ID{}, 301)
	request := validCommandRequest(t, commandID, "role-account", 1, 7, now)
	request.RequestHash = [32]byte{1}
	if _, err := platformpostgres.NewCommandJournal(adminPool).Begin(
		context.Background(),
		request,
	); err != nil {
		t.Fatalf("Begin: %v", err)
	}
	eventID := engine.IDFromSequence(engine.ID{}, 302)
	inputID := engine.IDFromSequence(engine.ID{}, 303)
	engineSeed, err := adminPool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire engine seed connection: %v", err)
	}
	defer engineSeed.Release()
	if _, err := engineSeed.Exec(context.Background(), `
		SELECT
			set_config(
				'platformgo.runtime_schema_revision',
				'20260730000400_phase3_broker_funding_acl',
				false
			),
			set_config(
				'platformgo.engine_decision_hash_version',
				'4',
				false
			)`); err != nil {
		t.Fatalf("bind engine seed schema revision: %v", err)
	}
	if _, err := engineSeed.Exec(context.Background(), `
		INSERT INTO engine.input_receipts (
			shard_id, input_id, stream_sequence, schema_version,
			input_hash_version, input_hash, decision_hash_version,
			decision_hash, resulting_state_hash, envelope, decision,
			business_input_hash, business_input_hash_version
		) VALUES (
			7, $1, 1, 1,
			1, decode(repeat('11', 32), 'hex'), 4,
			decode(repeat('12', 32), 'hex'),
			decode(repeat('13', 32), 'hex'), '{}',
			'{"DecisionHashVersion":4}',
			decode(repeat('14', 32), 'hex'), 1
		)`,
		inputID.String(),
	); err != nil {
		t.Fatalf("seed engine receipt: %v", err)
	}
	if _, err := adminPool.Exec(context.Background(), `
		INSERT INTO messaging.outbox (
			message_id, subject, schema_version, payload,
			producer_class, engine_shard_id, engine_input_id
		) VALUES (
			$2::uuid, 'domain.v1.order.filled', 1,
			jsonb_build_object(
				'messageId', ($2::uuid)::text,
				'correlationId', ($1::uuid)::text
			),
			'engine', 7, $1::uuid
		)`,
		inputID.String(),
		eventID.String(),
	); err != nil {
		t.Fatalf("seed engine event: %v", err)
	}

	rolePool := outboxRolePool(t, adminPool)
	var currentRole string
	if err := rolePool.QueryRow(
		context.Background(),
		"SELECT current_role",
	).Scan(&currentRole); err != nil {
		t.Fatalf("read current role: %v", err)
	}
	if currentRole != "platformgo_outbox" {
		t.Fatalf("current role = %q, want platformgo_outbox", currentRole)
	}

	publisher := &recordingPublisher{}
	store := platformpostgres.NewMessagingStore(rolePool)
	published, err := store.PublishOutboxBatch(
		context.Background(),
		publisher,
		time.Now().UTC().Add(time.Second),
		10,
		time.Minute,
		time.Second,
	)
	if err != nil || published != 2 {
		t.Fatalf("PublishOutboxBatch = %d, error %v", published, err)
	}
	var orderedCommands int
	var engineEvents int
	for _, message := range publisher.messages {
		if message.HasOrderedCommandClaim() {
			orderedCommands++
		}
		if message.HasEngineEventClaim() {
			engineEvents++
		}
	}
	if orderedCommands != 1 || engineEvents != 1 {
		t.Fatalf(
			"publication claims = ordered %d engine %d, want 1 and 1",
			orderedCommands,
			engineEvents,
		)
	}

	sequence, err := store.RepublishOutbox(
		context.Background(),
		publisher,
		commandID,
	)
	if err != nil {
		t.Fatalf("RepublishOutbox: %v", err)
	}
	if sequence != 103 || len(publisher.messages) != 3 ||
		!publisher.messages[2].HasOrderedCommandClaim() {
		t.Fatalf(
			"republish sequence=%d messages=%#v, want 103 and ordered claim",
			sequence,
			publisher.messages,
		)
	}

	assertOutboxRoleReadDenied(
		t,
		rolePool,
		"SELECT result FROM trading.commands LIMIT 1",
	)
	assertOutboxRoleReadDenied(
		t,
		rolePool,
		"SELECT input_hash FROM engine.input_receipts LIMIT 1",
	)
}

func TestInboxClaimAndConsumerEffectCommitTogether(t *testing.T) {
	pool := currentStorePool(t)
	if _, err := pool.Exec(
		context.Background(),
		"CREATE TABLE messaging.projection_probe (message_id uuid PRIMARY KEY)",
	); err != nil {
		t.Fatalf("create projection probe: %v", err)
	}

	store := platformpostgres.NewMessagingStore(pool)
	messageID := engine.IDFromSequence(engine.ID{}, 51)
	fail := true
	effect := func(ctx context.Context, tx pgx.Tx) error {
		if fail {
			return errors.New("projection failed")
		}
		_, err := tx.Exec(
			ctx,
			"INSERT INTO messaging.projection_probe (message_id) VALUES ($1)",
			messageID.String(),
		)
		return err
	}
	if _, err := store.ApplyInbox(
		context.Background(),
		"projector",
		messageID,
		effect,
	); err == nil {
		t.Fatal("failed effect unexpectedly committed")
	}
	fail = false
	duplicate, err := store.ApplyInbox(
		context.Background(),
		"projector",
		messageID,
		effect,
	)
	if err != nil || duplicate {
		t.Fatalf("successful ApplyInbox = duplicate %t, error %v", duplicate, err)
	}
	duplicate, err = store.ApplyInbox(
		context.Background(),
		"projector",
		messageID,
		effect,
	)
	if err != nil || !duplicate {
		t.Fatalf("duplicate ApplyInbox = duplicate %t, error %v", duplicate, err)
	}

	var inboxRows int
	var projectionRows int
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM messaging.inbox",
	).Scan(&inboxRows); err != nil {
		t.Fatalf("count inbox: %v", err)
	}
	if err := pool.QueryRow(
		context.Background(),
		"SELECT count(*) FROM messaging.projection_probe",
	).Scan(&projectionRows); err != nil {
		t.Fatalf("count projection: %v", err)
	}
	if inboxRows != 1 || projectionRows != 1 {
		t.Fatalf(
			"inbox rows=%d projection rows=%d, want 1 and 1",
			inboxRows,
			projectionRows,
		)
	}
}

var errUnknownPublishOutcome = errors.New("publish acknowledgment lost")
var errMissingCommandClaim = errors.New("ordered command claim is missing")

type unknownOutcomePublisher struct {
	ids []string
}

func (publisher *unknownOutcomePublisher) Publish(
	_ context.Context,
	message platformpostgres.OutboxMessage,
) (uint64, error) {
	publisher.ids = append(publisher.ids, message.MessageID.String())
	if len(publisher.ids) == 1 {
		return 0, errUnknownPublishOutcome
	}
	return 71, nil
}

type failFirstPublisher struct {
	ids []engine.ID
}

func (publisher *failFirstPublisher) Publish(
	_ context.Context,
	message platformpostgres.OutboxMessage,
) (uint64, error) {
	publisher.ids = append(publisher.ids, message.MessageID)
	if len(publisher.ids) == 1 {
		return 0, errUnknownPublishOutcome
	}
	return uint64(80 + len(publisher.ids)), nil
}

type recordingPublisher struct {
	messages []platformpostgres.OutboxMessage
}

type claimRequiringPublisher struct {
	messages []platformpostgres.OutboxMessage
}

func (publisher *claimRequiringPublisher) Publish(
	_ context.Context,
	message platformpostgres.OutboxMessage,
) (uint64, error) {
	publisher.messages = append(publisher.messages, message)
	if !message.HasOrderedCommandClaim() {
		return 0, errMissingCommandClaim
	}
	return 100, nil
}

func (publisher *recordingPublisher) Publish(
	_ context.Context,
	message platformpostgres.OutboxMessage,
) (uint64, error) {
	publisher.messages = append(publisher.messages, message)
	return uint64(100 + len(publisher.messages)), nil
}

func outboxRolePool(t *testing.T, admin *pgxpool.Pool) *pgxpool.Pool {
	t.Helper()

	config := admin.Config().Copy()
	config.AfterConnect = func(ctx context.Context, conn *pgx.Conn) error {
		_, err := conn.Exec(ctx, "SET ROLE platformgo_outbox")
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		t.Fatalf("open outbox-role PostgreSQL pool: %v", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		t.Fatalf("ping outbox-role PostgreSQL pool: %v", err)
	}
	t.Cleanup(pool.Close)
	return pool
}

func assertOutboxRoleReadDenied(
	t *testing.T,
	pool *pgxpool.Pool,
	statement string,
) {
	t.Helper()

	var value any
	if err := pool.QueryRow(context.Background(), statement).Scan(&value); err == nil {
		t.Fatalf("outbox role unexpectedly executed %q", statement)
	}
}
