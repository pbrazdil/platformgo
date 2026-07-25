package compatibility_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	platformruntime "github.com/upcomers-org/platformgo/internal/runtime"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
	"github.com/upcomers-org/platformgo/migrations"
)

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/infra/e2e_lifecycle.rs:97
// test: outbox_runner_drains_then_exits_on_shutdown
func TestOutboxWorkerDrainsThenExitsOnShutdown(t *testing.T) {
	runOutboxWorkerProof(
		t,
		[]string{"outbox-publisher"},
		false,
	)
}

// Ported from:
// platform: 50141367492be46ebf5623f6191a14b94af2f2bd
// source: apps/app/tests/it/jobs/e2e_worker.rs:10
// test: worker_runs_outbox_publisher
func TestWorkerRunsOutboxPublisherWithHealth(t *testing.T) {
	runOutboxWorkerProof(t, []string{"outbox-publisher"}, true)
}

func runOutboxWorkerProof(
	t *testing.T,
	handlers []string,
	verifyWorkerHealth bool,
) {
	t.Helper()
	databaseURL := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	natsURL := os.Getenv("PLATFORMGO_TEST_NATS_URL")
	if databaseURL == "" || natsURL == "" {
		t.Skip("Phase 3 worker dependencies are not configured")
	}
	ctx := context.Background()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if err := postgresfixture.ResetDurableSchemas(ctx, pool); err != nil {
		t.Fatal(err)
	}
	if err := platformpostgres.NewMigrator(
		pool,
		migrations.Files,
	).MigrateAndProvision(ctx, 7); err != nil {
		t.Fatal(err)
	}
	outboxDatabaseURL := provisionRuntimeLogin(
		t,
		ctx,
		pool,
		databaseURL,
		"platformgo_outbox",
	)
	connection, err := nats.Connect(natsURL)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	js, err := jetstream.New(connection)
	if err != nil {
		t.Fatal(err)
	}
	_ = js.DeleteStream(ctx, platformnats.OpsStream)
	if _, err := pool.Exec(ctx, `
		INSERT INTO messaging.outbox (
			message_id,
			subject,
			schema_version,
			payload
		) VALUES (
			'019f94e0-7572-4df4-a2b9-08f2acbe6cf4',
			'ops.v1.phase3-worker',
			1,
			'{"kind":"phase3-worker-proof"}'
		)`); err != nil {
		t.Fatal(err)
	}

	workerContext, cancel := context.WithCancel(ctx)
	healthAddress := unusedAddress(t)
	healthURL := "http://" + healthAddress
	workerResult := make(chan error, 1)
	workerFinished := false
	defer func() {
		cancel()
		if !workerFinished {
			select {
			case <-workerResult:
			case <-time.After(10 * time.Second):
				t.Errorf("worker cleanup timed out")
			}
		}
	}()
	go func() {
		workerResult <- platformruntime.RunWorkers(
			workerContext,
			platformruntime.Config{
				DatabaseURL: outboxDatabaseURL,
				NATSURL:     natsURL,
				NATSStreamLimits: platformnats.StreamLimits{
					Replicas: 1, MaxMessages: 1_000_000, MaxBytes: 2 << 30,
					MaxMessageBytes: 1 << 20, MaxAge: 30 * 24 * time.Hour,
					DuplicateWindow: 24 * time.Hour,
				},
				HealthAddress: healthAddress,
				ShardID:       7,
			},
			handlers,
		)
	}()
	readinessStore := platformpostgres.NewCompatibilityStore(pool)
	readinessDeadline := time.NewTimer(5 * time.Second)
	defer readinessDeadline.Stop()
	for {
		ready := false
		response, requestErr := http.Get(healthURL + "/readyz")
		if requestErr == nil {
			ready = response.StatusCode == http.StatusOK
			_ = response.Body.Close()
		}
		if ready {
			break
		}
		select {
		case <-readinessDeadline.C:
			cancel()
			t.Fatal("workers did not publish post-initialization readiness")
		case <-time.After(10 * time.Millisecond):
		}
	}
	if verifyWorkerHealth {
		response, requestErr := http.Get(healthURL + "/healthz")
		if requestErr != nil {
			cancel()
			t.Fatal(requestErr)
		}
		liveBody, readErr := io.ReadAll(response.Body)
		_ = response.Body.Close()
		if readErr != nil ||
			response.StatusCode != http.StatusOK ||
			string(liveBody) != "ok" {
			cancel()
			t.Fatalf(
				"worker health status=%d body=%q error=%v",
				response.StatusCode,
				liveBody,
				readErr,
			)
		}
	}
	stream, err := js.Stream(ctx, platformnats.OpsStream)
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	consumer, err := stream.CreateOrUpdateConsumer(ctx, jetstream.ConsumerConfig{
		Name:          "phase3-worker-proof",
		Durable:       "phase3-worker-proof",
		DeliverPolicy: jetstream.DeliverAllPolicy,
		AckPolicy:     jetstream.AckExplicitPolicy,
		FilterSubject: "ops.v1.phase3-worker",
	})
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	messageResult := make(chan jetstream.Msg, 1)
	messageError := make(chan error, 1)
	go func() {
		batch, fetchErr := consumer.Fetch(1, jetstream.FetchMaxWait(10*time.Second))
		if fetchErr != nil {
			messageError <- fetchErr
			return
		}
		for message := range batch.Messages() {
			messageResult <- message
			return
		}
		if batchErr := batch.Error(); batchErr != nil {
			messageError <- batchErr
			return
		}
		messageError <- errors.New("JetStream consumer returned an empty batch")
	}()
	var message jetstream.Msg
	select {
	case message = <-messageResult:
	case receiveErr := <-messageError:
		cancel()
		t.Fatalf("outbox publication: %v", receiveErr)
	case workerErr := <-workerResult:
		workerFinished = true
		cancel()
		t.Fatalf("outbox worker exited before publication: %v", workerErr)
	case <-time.After(12 * time.Second):
		cancel()
		t.Fatal("outbox publication timed out")
	}
	if got := message.Headers().Get(nats.MsgIdHdr); got != "019f94e0-7572-4df4-a2b9-08f2acbe6cf4" {
		cancel()
		t.Fatalf("JetStream Nats-Msg-Id = %q", got)
	}
	var payload map[string]string
	if err := json.Unmarshal(message.Data(), &payload); err != nil ||
		payload["kind"] != "phase3-worker-proof" {
		cancel()
		t.Fatalf("outbox payload = %s, error = %v", message.Data(), err)
	}
	if err := message.Ack(); err != nil {
		cancel()
		t.Fatalf("acknowledge durable JetStream delivery: %v", err)
	}
	var drainTransaction pgx.Tx
	defer func() {
		if drainTransaction != nil {
			_ = drainTransaction.Rollback(context.Background())
		}
	}()
	if verifyWorkerHealth {
		drainTransaction, err = pool.Begin(ctx)
		if err != nil {
			cancel()
			t.Fatal(err)
		}
		if _, err := drainTransaction.Exec(
			ctx,
			"SELECT pg_advisory_xact_lock_shared($1, $2)",
			int64(0x50474144),
			int64(0),
		); err != nil {
			_ = drainTransaction.Rollback(ctx)
			cancel()
			t.Fatal(err)
		}
	}
	// Cancel as soon as delivery is observed. The in-flight batch must still
	// finish its durable published_at update before RunWorkers returns.
	cancel()
	drainDeadline := time.NewTimer(5 * time.Second)
	defer drainDeadline.Stop()
	for {
		drained := false
		if verifyWorkerHealth {
			response, requestErr := http.Get(healthURL + "/readyz")
			if requestErr != nil {
				drained = true
			} else {
				drained = response.StatusCode != http.StatusOK
				_ = response.Body.Close()
			}
		} else {
			drained = readinessStore.RuntimeCommandReady(ctx, 7) != nil
		}
		if drained {
			break
		}
		select {
		case <-drainDeadline.C:
			t.Fatal("worker drain retained command readiness")
		case <-time.After(time.Millisecond):
		}
	}
	if verifyWorkerHealth {
		select {
		case workerErr := <-workerResult:
			workerFinished = true
			t.Fatalf(
				"worker completed before blocked readiness drain: %v",
				workerErr,
			)
		default:
		}
		if err := drainTransaction.Rollback(context.Background()); err != nil {
			t.Fatal(err)
		}
		drainTransaction = nil
	}
	select {
	case workerErr := <-workerResult:
		workerFinished = true
		if workerErr != nil {
			t.Fatal(workerErr)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("outbox worker did not drain and exit after cancellation")
	}
	publishDeadline := time.NewTimer(5 * time.Second)
	defer publishDeadline.Stop()
	publishPoll := time.NewTicker(10 * time.Millisecond)
	defer publishPoll.Stop()
	for {
		var published bool
		if err := pool.QueryRow(ctx, `
			SELECT published_at IS NOT NULL
			  FROM messaging.outbox
			 WHERE message_id = '019f94e0-7572-4df4-a2b9-08f2acbe6cf4'`,
		).Scan(&published); err != nil {
			t.Fatal(err)
		}
		if published {
			break
		}
		select {
		case <-publishDeadline.C:
			t.Fatal("outbox row was delivered but not marked published")
		case <-publishPoll.C:
		}
	}
}
