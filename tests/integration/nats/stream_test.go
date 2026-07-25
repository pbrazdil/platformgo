package nats_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/nats-io/nats.go/jetstream"
	platformnats "github.com/upcomers-org/platformgo/internal/adapters/nats"
	"github.com/upcomers-org/platformgo/internal/engine"
)

// resetEngineShardStream isolates tests from durable state left by an earlier
// run against the same explicitly configured disposable JetStream server.
func resetEngineShardStream(
	t *testing.T,
	ctx context.Context,
	js jetstream.JetStream,
	shardID engine.ShardID,
) {
	t.Helper()
	err := js.DeleteStream(
		ctx,
		fmt.Sprintf("%s_%d", platformnats.EngineInputsStream, shardID),
	)
	if err != nil && !errors.Is(err, jetstream.ErrStreamNotFound) {
		t.Fatalf("reset engine shard stream %d: %v", shardID, err)
	}
}
