package postgres_test

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
)

const (
	// Pin the final decision-hash v3 artifact so this upgrade test cannot
	// silently start exercising current writer behavior.
	v3RemediationArtifactRevision = "2021865d18393bcadf8ed4eae8632295caba2238"
	v3RemediationShardID          = engine.ShardID(64)

	v3RemediationSeedStateHash          = "a1afddda50731e887a75ca77e8c31bdb1a9b884a27b846f3683bd47041fdb991"
	v3RemediationFinalStateHash         = "87560ea6a2598b77acc84904af44b4d38441b6d3e5ebb81db15e0ffd265cd245"
	v3RemediationPostDuplicateStateHash = "6a4963ed19a43822ef0eff4c09b0354a7cb14cac258ac53da3642b41a5f71ef1"
)

func TestFillEffectiveLeverageV3RemediationUpgrade(t *testing.T) {
	ctx := context.Background()
	pool := postgresPool(t)
	repositoryRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatalf("resolve repository root: %v", err)
	}
	fixture := buildV3RemediationFixture(t, repositoryRoot)

	seed := runV3RemediationFixture(
		t,
		fixture,
		"seed-invalid",
		"",
		0,
	)
	requireV3RemediationState(t, seed, v3RemediationSeedStateHash, 9)

	beforeRejectedCutover := inspectV3RemediationDatabase(t, pool)
	requireInvalidV3RemediationState(t, beforeRejectedCutover, seed)

	current := platformpostgres.NewMigrator(
		pool,
		migrationFilesThrough(t, fillEffectiveLeverageTargetTip),
	)
	cutoverErr := current.Migrate(ctx)
	requireFillEffectiveLeverageSQLState(t, cutoverErr, "55000")
	if !strings.Contains(
		cutoverErr.Error(),
		"preexisting risk leverage exceeds instrument maximum",
	) {
		t.Fatalf("rejected v4 cutover error = %v", cutoverErr)
	}

	afterRejectedCutover := inspectV3RemediationDatabase(t, pool)
	if afterRejectedCutover != beforeRejectedCutover {
		t.Fatalf(
			"rejected v4 cutover changed the v3 database:\n before %+v\n after  %+v",
			beforeRejectedCutover,
			afterRejectedCutover,
		)
	}

	remediated := runV3RemediationFixture(
		t,
		fixture,
		"remediate",
		seed.StateHash,
		seed.NextStreamSequence,
	)
	requireV3RemediationState(t, remediated, v3RemediationFinalStateHash, 12)

	recovered := runV3RemediationFixture(
		t,
		fixture,
		"recover-reconcile",
		remediated.StateHash,
		remediated.NextStreamSequence,
	)
	requireV3RemediationState(t, recovered, v3RemediationFinalStateHash, 12)
	if recovered != remediated {
		t.Fatalf(
			"pinned v3 recovery = %+v, want remediation %+v",
			recovered,
			remediated,
		)
	}
	requireNoV3EngineOwner(t, pool)

	beforeSuccessfulCutover := inspectV3RemediationDatabase(t, pool)
	requireCorrectedV3RemediationState(
		t,
		beforeSuccessfulCutover,
		recovered,
	)
	var riskAboveMaximum int
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.risk_configs AS risk
		  JOIN trading.instruments AS instrument
		    ON instrument.instrument_id = risk.instrument_id
		 WHERE risk.leverage > instrument.max_leverage`,
	).Scan(&riskAboveMaximum); err != nil {
		t.Fatalf("rescan corrected v3 risk authority: %v", err)
	}
	if riskAboveMaximum != 0 {
		t.Fatalf(
			"corrected v3 risk authority still has %d row(s) above instrument maximum",
			riskAboveMaximum,
		)
	}

	if err := current.Migrate(ctx); err != nil {
		t.Fatalf("apply v4 cutover after normal v3 remediation: %v", err)
	}
	if err := current.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify v4 cutover after normal v3 remediation: %v", err)
	}

	afterSuccessfulCutover := inspectV3RemediationDatabase(t, pool)
	if afterSuccessfulCutover.durable() != beforeSuccessfulCutover.durable() {
		t.Fatalf(
			"v4 cutover changed corrected durable history:\n before %+v\n after  %+v",
			beforeSuccessfulCutover.durable(),
			afterSuccessfulCutover.durable(),
		)
	}
	if afterSuccessfulCutover.MigrationTip !=
		fillEffectiveLeverageTargetTip ||
		afterSuccessfulCutover.MigrationCount !=
			beforeSuccessfulCutover.MigrationCount+2 ||
		!afterSuccessfulCutover.EffectiveLeverageColumnExists ||
		afterSuccessfulCutover.EffectiveLeverageColumnType !=
			"numeric(38,18)" ||
		afterSuccessfulCutover.EffectiveLeverageColumnNotNull ||
		afterSuccessfulCutover.EffectiveLeverageColumnHasDefault ||
		!afterSuccessfulCutover.EffectiveLeverageConstraintValid {
		t.Fatalf(
			"successful v4 cutover schema = %+v",
			afterSuccessfulCutover,
		)
	}

	currentTip := platformpostgres.NewMigrator(
		pool,
		os.DirFS(filepath.Join("..", "..", "..", "migrations")),
	)
	if err := currentTip.Migrate(ctx); err != nil {
		t.Fatalf("advance remediated v3 database to current schema: %v", err)
	}
	if err := currentTip.VerifyCurrent(ctx); err != nil {
		t.Fatalf("verify current schema after v3 remediation: %v", err)
	}
	afterCurrentCutover := inspectV3RemediationDatabase(t, pool)
	if afterCurrentCutover.durable() != afterSuccessfulCutover.durable() {
		t.Fatalf(
			"current schema cutover changed remediated durable history:\n before %+v\n after  %+v",
			afterSuccessfulCutover.durable(),
			afterCurrentCutover.durable(),
		)
	}

	currentStore := platformpostgres.NewEngineStore(pool)
	currentRecovered, err := currentStore.RecoverTradingState(
		ctx,
		v3RemediationShardID,
	)
	if err != nil {
		t.Fatalf("current artifact recover corrected v3 history: %v", err)
	}
	if !currentRecovered.Ready() ||
		currentRecovered.Hash().String() != recovered.StateHash ||
		currentRecovered.NextStreamSequence() != recovered.NextStreamSequence {
		t.Fatalf(
			"current recovery ready=%t hash=%s next=%d, want true/%s/%d",
			currentRecovered.Ready(),
			currentRecovered.Hash(),
			currentRecovered.NextStreamSequence(),
			recovered.StateHash,
			recovered.NextStreamSequence,
		)
	}

	ownership, err := currentStore.AcquireShardOwnership(
		ctx,
		v3RemediationShardID,
	)
	if err != nil {
		t.Fatalf("acquire current artifact ownership: %v", err)
	}
	publisher := &v3RemediationOutboxPublisher{
		pool:             pool,
		store:            currentStore,
		ownership:        ownership,
		state:            currentRecovered,
		publishSequences: make(map[string]uint64, 6),
	}
	messagingStore := platformpostgres.NewMessagingStore(pool)
	publishedTotal := 0
	publishNow := time.Date(2099, 1, 1, 0, 0, 0, 0, time.UTC)
	for {
		published, publishErr := messagingStore.PublishOutboxBatch(
			ctx,
			publisher,
			publishNow,
			64,
			time.Minute,
			time.Minute,
		)
		if publishErr != nil {
			_ = ownership.Close(context.Background())
			t.Fatalf("publish pending v3 outbox through current artifact: %v", publishErr)
		}
		publishedTotal += published
		if published == 0 {
			break
		}
	}
	if publishedTotal != 6 ||
		publisher.commandDeliveries != 4 ||
		publisher.eventDeliveries != 2 {
		_ = ownership.Close(context.Background())
		t.Fatalf(
			"published v3 outbox total/commands/events = %d/%d/%d, want 6/4/2",
			publishedTotal,
			publisher.commandDeliveries,
			publisher.eventDeliveries,
		)
	}
	postDuplicateState := publisher.state
	if err := ownership.Close(ctx); err != nil {
		t.Fatalf("close current artifact ownership: %v", err)
	}
	requireV3RemediationOutboxPublished(
		t,
		pool,
		publisher.publishSequences,
		publishNow,
	)
	requireV3RemediationDuplicateReceipts(t, pool)

	afterDuplicateDelivery := inspectV3RemediationDatabase(t, pool)
	requirePostDuplicateV3RemediationState(
		t,
		afterDuplicateDelivery,
		afterCurrentCutover,
		postDuplicateState,
	)

	freshStore := platformpostgres.NewEngineStore(pool)
	freshRecovered, err := freshStore.RecoverTradingState(
		ctx,
		v3RemediationShardID,
	)
	if err != nil {
		t.Fatalf("fresh current artifact recover duplicate deliveries: %v", err)
	}
	if !freshRecovered.Ready() ||
		freshRecovered.Hash() != postDuplicateState.Hash() ||
		freshRecovered.NextStreamSequence() !=
			postDuplicateState.NextStreamSequence() {
		t.Fatalf(
			"fresh current recovery ready=%t hash=%s next=%d, want true/%s/%d",
			freshRecovered.Ready(),
			freshRecovered.Hash(),
			freshRecovered.NextStreamSequence(),
			postDuplicateState.Hash(),
			postDuplicateState.NextStreamSequence(),
		)
	}
	report, err := freshStore.ReconcileShard(ctx, v3RemediationShardID)
	if err != nil {
		t.Fatalf("current artifact reconcile duplicate deliveries: %v", err)
	}
	requireCurrentReconciliationClean(t, report, 11, 4, 16)
}

type v3RemediationOutboxPublisher struct {
	pool              *pgxpool.Pool
	store             *platformpostgres.EngineStore
	ownership         *platformpostgres.ShardOwnership
	state             engine.State
	commandDeliveries int
	eventDeliveries   int
	publishSequences  map[string]uint64
}

func (publisher *v3RemediationOutboxPublisher) Publish(
	ctx context.Context,
	message platformpostgres.OutboxMessage,
) (uint64, error) {
	commandClaim := message.HasOrderedCommandClaim()
	eventClaim := message.HasEngineEventClaim()
	if commandClaim == eventClaim {
		return 0, fmt.Errorf(
			"outbox message %s has command/event claims %t/%t",
			message.MessageID,
			commandClaim,
			eventClaim,
		)
	}
	if eventClaim {
		publisher.eventDeliveries++
		sequence := 1_000_000 + uint64(publisher.eventDeliveries)
		if err := publisher.recordPublication(message.MessageID, sequence); err != nil {
			return 0, err
		}
		return sequence, nil
	}

	input, action, err := engine.DecodeInputMessage(message.Payload)
	if err != nil {
		return 0, fmt.Errorf(
			"decode pending v3 command %s: %w",
			message.MessageID,
			err,
		)
	}
	if input.InputID != message.MessageID ||
		input.ShardID != v3RemediationShardID ||
		input.StreamSequence != 0 ||
		input.SourceSequence != uint64(publisher.commandDeliveries+1) {
		return 0, fmt.Errorf(
			"pending v3 command envelope id/shard/stream/source = %s/%d/%d/%d",
			input.InputID,
			input.ShardID,
			input.StreamSequence,
			input.SourceSequence,
		)
	}

	var originalHashBytes []byte
	var originalHashVersion uint32
	var originalStreamSequence uint64
	if err := publisher.pool.QueryRow(ctx, `
		SELECT decision_hash, decision_hash_version, stream_sequence
		  FROM engine.input_receipts
		 WHERE shard_id = $1 AND input_id = $2`,
		int64(v3RemediationShardID),
		input.InputID.String(),
	).Scan(
		&originalHashBytes,
		&originalHashVersion,
		&originalStreamSequence,
	); err != nil {
		return 0, fmt.Errorf(
			"load original v3 receipt for command %s: %w",
			input.InputID,
			err,
		)
	}
	var originalHash engine.Hash
	if len(originalHashBytes) != len(originalHash) ||
		originalHashVersion != 3 ||
		originalStreamSequence >= publisher.state.NextStreamSequence() {
		return 0, fmt.Errorf(
			"original command %s hash bytes/version/stream = %d/%d/%d",
			input.InputID,
			len(originalHashBytes),
			originalHashVersion,
			originalStreamSequence,
		)
	}
	copy(originalHash[:], originalHashBytes)

	previous := publisher.state
	input.StreamSequence = previous.NextStreamSequence()
	next, decision, duplicate, err := publisher.store.ApplyTrading(
		ctx,
		previous,
		input,
		action,
		platformpostgres.ApplyOptions{Ownership: publisher.ownership},
	)
	if err != nil {
		return 0, fmt.Errorf(
			"apply pending v3 command %s as current duplicate: %w",
			input.InputID,
			err,
		)
	}
	if !duplicate ||
		decision.DecisionHashVersion != engine.CurrentDecisionHashVersion ||
		decision.DuplicateOfDecisionHash != originalHash ||
		decision.PreviousStateHash != previous.Hash() ||
		decision.NextStateHash != next.Hash() ||
		next.NextStreamSequence() != previous.NextStreamSequence()+1 ||
		!next.Ready() ||
		decision.DecisionHash.IsZero() ||
		hasV3RemediationEconomicEffects(decision) {
		return 0, fmt.Errorf(
			"current duplicate decision for %s = duplicate %t decision %+v next ready/hash/sequence %t/%s/%d",
			input.InputID,
			duplicate,
			decision,
			next.Ready(),
			next.Hash(),
			next.NextStreamSequence(),
		)
	}
	publisher.state = next
	publisher.commandDeliveries++
	if err := publisher.recordPublication(
		message.MessageID,
		input.StreamSequence,
	); err != nil {
		return 0, err
	}
	return input.StreamSequence, nil
}

func (publisher *v3RemediationOutboxPublisher) recordPublication(
	messageID engine.ID,
	sequence uint64,
) error {
	key := messageID.String()
	if _, exists := publisher.publishSequences[key]; exists {
		return fmt.Errorf("outbox message %s was published more than once", messageID)
	}
	publisher.publishSequences[key] = sequence
	return nil
}

func hasV3RemediationEconomicEffects(decision engine.Decision) bool {
	return len(decision.InstrumentChanges) != 0 ||
		len(decision.AccountChanges) != 0 ||
		len(decision.RiskChanges) != 0 ||
		len(decision.BalanceChanges) != 0 ||
		len(decision.LedgerChanges) != 0 ||
		len(decision.FundingChanges) != 0 ||
		len(decision.BookChanges) != 0 ||
		len(decision.OrderChanges) != 0 ||
		len(decision.Fills) != 0 ||
		len(decision.PositionChanges) != 0 ||
		len(decision.Events) != 0
}

func requireV3RemediationOutboxPublished(
	t *testing.T,
	pool *pgxpool.Pool,
	expectedSequences map[string]uint64,
	publishedAt time.Time,
) {
	t.Helper()
	if len(expectedSequences) != 6 {
		t.Fatalf(
			"recorded v3 outbox publication count = %d, want 6",
			len(expectedSequences),
		)
	}
	rows, err := pool.Query(context.Background(), `
		SELECT message_id::text, attempts, claimed_at, published_at,
		       publish_sequence, last_error
		  FROM messaging.outbox
		 ORDER BY message_id`)
	if err != nil {
		t.Fatalf("read published v3 outbox: %v", err)
	}
	defer rows.Close()
	seen := make(map[string]struct{}, 6)
	for rows.Next() {
		var messageID string
		var attempts uint32
		var claimedAt *time.Time
		var actualPublishedAt *time.Time
		var publishSequence *uint64
		var lastError *string
		if err := rows.Scan(
			&messageID,
			&attempts,
			&claimedAt,
			&actualPublishedAt,
			&publishSequence,
			&lastError,
		); err != nil {
			t.Fatalf("scan published v3 outbox: %v", err)
		}
		expectedSequence, expected := expectedSequences[messageID]
		if !expected ||
			attempts != 1 ||
			claimedAt != nil ||
			actualPublishedAt == nil ||
			!actualPublishedAt.Equal(publishedAt) ||
			publishSequence == nil ||
			*publishSequence != expectedSequence ||
			lastError != nil {
			t.Fatalf(
				"published v3 outbox %s attempts/claimed/published/sequence/error = %d/%v/%v/%v/%v, want 1/nil/%s/%d/nil",
				messageID,
				attempts,
				claimedAt,
				actualPublishedAt,
				publishSequence,
				lastError,
				publishedAt.Format(time.RFC3339Nano),
				expectedSequence,
			)
		}
		seen[messageID] = struct{}{}
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate published v3 outbox: %v", err)
	}
	if len(seen) != len(expectedSequences) {
		t.Fatalf(
			"published v3 outbox row count = %d, want %d",
			len(seen),
			len(expectedSequences),
		)
	}
}

func requireV3RemediationDuplicateReceipts(
	t *testing.T,
	pool *pgxpool.Pool,
) {
	t.Helper()
	rows, err := pool.Query(context.Background(), `
		SELECT duplicate.input_id::text,
		       duplicate.stream_sequence,
		       duplicate.original_decision_hash,
		       duplicate.decision_hash,
		       duplicate.resulting_state_hash,
		       duplicate.envelope,
		       duplicate.decision,
		       original.decision_hash,
		       original.decision_hash_version,
		       original.stream_sequence
		  FROM engine.duplicate_delivery_receipts AS duplicate
		  JOIN engine.input_receipts AS original
		    ON original.shard_id = duplicate.shard_id
		   AND original.input_id = duplicate.input_id
		 WHERE duplicate.shard_id = $1
		 ORDER BY duplicate.stream_sequence`,
		int64(v3RemediationShardID),
	)
	if err != nil {
		t.Fatalf("read v3 remediation duplicate receipts: %v", err)
	}
	defer rows.Close()
	expectedOriginalSequences := [...]uint64{2, 3, 8, 10}
	count := 0
	for rows.Next() {
		var inputIDText string
		var streamSequence uint64
		var originalHashBytes []byte
		var decisionHashBytes []byte
		var stateHashBytes []byte
		var envelopeJSON []byte
		var decisionJSON []byte
		var receiptHashBytes []byte
		var receiptHashVersion uint32
		var receiptStreamSequence uint64
		if err := rows.Scan(
			&inputIDText,
			&streamSequence,
			&originalHashBytes,
			&decisionHashBytes,
			&stateHashBytes,
			&envelopeJSON,
			&decisionJSON,
			&receiptHashBytes,
			&receiptHashVersion,
			&receiptStreamSequence,
		); err != nil {
			t.Fatalf("scan v3 remediation duplicate receipt: %v", err)
		}
		if count >= len(expectedOriginalSequences) {
			t.Fatalf("unexpected extra duplicate receipt at stream %d", streamSequence)
		}
		inputID, err := engine.ParseID(inputIDText)
		if err != nil {
			t.Fatalf("parse duplicate input ID %q: %v", inputIDText, err)
		}
		originalHash := requireV3RemediationHash(
			t,
			"duplicate original decision",
			originalHashBytes,
		)
		receiptHash := requireV3RemediationHash(
			t,
			"business receipt decision",
			receiptHashBytes,
		)
		decisionHash := requireV3RemediationHash(
			t,
			"duplicate decision",
			decisionHashBytes,
		)
		stateHash := requireV3RemediationHash(
			t,
			"duplicate resulting state",
			stateHashBytes,
		)
		var decision engine.Decision
		if err := json.Unmarshal(decisionJSON, &decision); err != nil {
			t.Fatalf("decode duplicate decision %s: %v", inputID, err)
		}
		var envelope struct {
			InputID        string
			ShardID        uint32
			SourceSequence uint64
			StreamSequence uint64
		}
		if err := json.Unmarshal(envelopeJSON, &envelope); err != nil {
			t.Fatalf("decode duplicate envelope %s: %v", inputID, err)
		}
		if streamSequence != uint64(12+count) ||
			receiptStreamSequence != expectedOriginalSequences[count] ||
			receiptHashVersion != 3 ||
			originalHash != receiptHash ||
			decision.InputID != inputID ||
			decision.StreamSequence != streamSequence ||
			decision.DecisionHashVersion != 4 ||
			decision.DuplicateOfDecisionHash != originalHash ||
			decision.DecisionHash != decisionHash ||
			decision.NextStateHash != stateHash ||
			hasV3RemediationEconomicEffects(decision) ||
			envelope.InputID != inputIDText ||
			envelope.ShardID != uint32(v3RemediationShardID) ||
			envelope.SourceSequence != uint64(count+1) ||
			envelope.StreamSequence != streamSequence {
			t.Fatalf(
				"duplicate receipt %d input/stream/original/version/decision/envelope = %s/%d/%d/%d/%+v/%+v",
				count,
				inputID,
				streamSequence,
				receiptStreamSequence,
				receiptHashVersion,
				decision,
				envelope,
			)
		}
		count++
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("iterate v3 remediation duplicate receipts: %v", err)
	}
	if count != len(expectedOriginalSequences) {
		t.Fatalf(
			"v3 remediation duplicate receipt count = %d, want %d",
			count,
			len(expectedOriginalSequences),
		)
	}
}

func requireV3RemediationHash(
	t *testing.T,
	label string,
	encoded []byte,
) engine.Hash {
	t.Helper()
	var hash engine.Hash
	if len(encoded) != len(hash) {
		t.Fatalf("%s hash length = %d, want %d", label, len(encoded), len(hash))
	}
	copy(hash[:], encoded)
	return hash
}

type v3RemediationResult struct {
	StateHash          string `json:"stateHash"`
	NextStreamSequence uint64 `json:"nextStreamSequence"`
}

func buildV3RemediationFixture(t *testing.T, repositoryRoot string) string {
	t.Helper()
	object := v3RemediationArtifactRevision + "^{commit}"
	if output, err := exec.Command(
		"git",
		"-C",
		repositoryRoot,
		"cat-file",
		"-e",
		object,
	).CombinedOutput(); err != nil {
		t.Fatalf(
			"pinned v3 artifact %s is unavailable; fetch that exact object before running the PostgreSQL integration suite: %s",
			v3RemediationArtifactRevision,
			redactV3RemediationOutput(string(output)),
		)
	}

	archiveRoot := t.TempDir()
	archiveCommand := exec.Command(
		"git",
		"-C",
		repositoryRoot,
		"archive",
		"--format=tar",
		v3RemediationArtifactRevision,
		"--",
		"go.mod",
		"go.sum",
		"contracts",
		"internal",
		"migrations",
	)
	archivePipe, err := archiveCommand.StdoutPipe()
	if err != nil {
		t.Fatalf("open pinned v3 archive: %v", err)
	}
	var archiveStderr bytes.Buffer
	archiveCommand.Stderr = &archiveStderr
	if err := archiveCommand.Start(); err != nil {
		t.Fatalf("start pinned v3 archive: %v", err)
	}
	if err := extractV3RemediationArchive(archiveRoot, archivePipe); err != nil {
		_ = archiveCommand.Process.Kill()
		_ = archiveCommand.Wait()
		t.Fatalf("extract pinned v3 archive: %v", err)
	}
	if err := archiveCommand.Wait(); err != nil {
		t.Fatalf(
			"archive pinned v3 artifact: %v: %s",
			err,
			redactV3RemediationOutput(archiveStderr.String()),
		)
	}

	fixtureSource, err := os.ReadFile(filepath.Join(
		repositoryRoot,
		"tests",
		"integration",
		"postgres",
		"testdata",
		"v3-remediation-fixture",
		"main.go",
	))
	if err != nil {
		t.Fatalf("read v3 remediation fixture source: %v", err)
	}
	fixtureDirectory := filepath.Join(
		archiveRoot,
		"cmd",
		"v3-remediation-fixture",
	)
	if err := os.MkdirAll(fixtureDirectory, 0o755); err != nil {
		t.Fatalf("create archived fixture directory: %v", err)
	}
	if err := os.WriteFile(
		filepath.Join(fixtureDirectory, "main.go"),
		fixtureSource,
		0o600,
	); err != nil {
		t.Fatalf("install archived fixture source: %v", err)
	}

	binary := filepath.Join(archiveRoot, "platformgo-v3-remediation")
	build := exec.Command(
		"go",
		"build",
		"-buildvcs=false",
		"-trimpath",
		"-o",
		binary,
		"./cmd/v3-remediation-fixture",
	)
	build.Dir = archiveRoot
	build.Env = v3RemediationBuildEnvironment()
	output, err := build.CombinedOutput()
	if err != nil {
		t.Fatalf(
			"build pinned v3 remediation fixture: %v: %s",
			err,
			redactV3RemediationOutput(string(output)),
		)
	}
	return binary
}

func extractV3RemediationArchive(root string, source io.Reader) error {
	const maximumArchiveBytes = int64(16 << 20)
	reader := tar.NewReader(source)
	var extractedBytes int64
	for {
		header, err := reader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		clean := filepath.Clean(filepath.FromSlash(header.Name))
		if clean == "." ||
			filepath.IsAbs(clean) ||
			clean == ".." ||
			strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
			return fmt.Errorf("unsafe archive path %q", header.Name)
		}
		target := filepath.Join(root, clean)
		relative, err := filepath.Rel(root, target)
		if err != nil ||
			relative == ".." ||
			strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return fmt.Errorf("archive path escapes root: %q", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeXHeader, tar.TypeXGlobalHeader:
			continue
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 ||
				extractedBytes > maximumArchiveBytes-header.Size {
				return fmt.Errorf("pinned v3 archive exceeds %d bytes", maximumArchiveBytes)
			}
			extractedBytes += header.Size
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			file, err := os.OpenFile(
				target,
				os.O_CREATE|os.O_EXCL|os.O_WRONLY,
				header.FileInfo().Mode().Perm(),
			)
			if err != nil {
				return err
			}
			_, copyErr := io.CopyN(file, reader, header.Size)
			closeErr := file.Close()
			if copyErr != nil {
				return copyErr
			}
			if closeErr != nil {
				return closeErr
			}
		default:
			return fmt.Errorf(
				"unsupported archive entry %q type %d",
				header.Name,
				header.Typeflag,
			)
		}
	}
}

func runV3RemediationFixture(
	t *testing.T,
	binary string,
	phase string,
	expectedStateHash string,
	expectedNextStreamSequence uint64,
) v3RemediationResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	arguments := []string{"--phase", phase}
	if expectedStateHash != "" {
		arguments = append(
			arguments,
			"--expected-state-hash",
			expectedStateHash,
			"--expected-next-stream-sequence",
			fmt.Sprint(expectedNextStreamSequence),
		)
	}
	command := exec.CommandContext(ctx, binary, arguments...)
	command.Dir = filepath.Dir(binary)
	command.Env = v3RemediationRuntimeEnvironment()
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf(
			"run pinned v3 fixture phase %s: %v: %s",
			phase,
			err,
			redactV3RemediationOutput(stderr.String()),
		)
	}
	var result v3RemediationResult
	decoder := json.NewDecoder(&stdout)
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&result); err != nil {
		t.Fatalf(
			"decode pinned v3 fixture phase %s: %v: %s",
			phase,
			err,
			redactV3RemediationOutput(stdout.String()),
		)
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		t.Fatalf(
			"pinned v3 fixture phase %s emitted trailing output: %s",
			phase,
			redactV3RemediationOutput(stdout.String()),
		)
	}
	return result
}

func v3RemediationBuildEnvironment() []string {
	environment := make([]string, 0, len(os.Environ())+2)
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(name, "PLATFORMGO_TEST_") ||
			name == "GOWORK" ||
			name == "GOFLAGS" {
			continue
		}
		environment = append(environment, entry)
	}
	return append(environment, "GOWORK=off", "GOFLAGS=-mod=readonly")
}

func v3RemediationRuntimeEnvironment() []string {
	environment := []string{
		"PATH=" + os.Getenv("PATH"),
		"LC_ALL=C",
		"TZ=UTC",
		"PLATFORMGO_TEST_POSTGRES_DSN=" +
			os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN"),
		"PLATFORMGO_TEST_POSTGRES_RESET_AUTHORIZED=" +
			os.Getenv("PLATFORMGO_TEST_POSTGRES_RESET_AUTHORIZED"),
	}
	if temporaryDirectory := os.Getenv("TMPDIR"); temporaryDirectory != "" {
		environment = append(environment, "TMPDIR="+temporaryDirectory)
	}
	return environment
}

func redactV3RemediationOutput(output string) string {
	dsn := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	if dsn != "" {
		output = strings.ReplaceAll(output, dsn, "[REDACTED_POSTGRES_DSN]")
	}
	return strings.TrimSpace(output)
}

func requireV3RemediationState(
	t *testing.T,
	result v3RemediationResult,
	stateHash string,
	nextStreamSequence uint64,
) {
	t.Helper()
	if result.StateHash != stateHash ||
		result.NextStreamSequence != nextStreamSequence ||
		len(result.StateHash) != 64 {
		t.Fatalf(
			"pinned v3 state hash/next = %s/%d, want %s/%d",
			result.StateHash,
			result.NextStreamSequence,
			stateHash,
			nextStreamSequence,
		)
	}
}

func requireCurrentReconciliationClean(
	t *testing.T,
	report platformpostgres.ReconciliationReport,
	receiptCount uint64,
	duplicateDeliveryCount uint64,
	nextStreamSequence uint64,
) {
	t.Helper()
	if !report.Ready ||
		report.ReceiptCount != receiptCount ||
		report.DuplicateDeliveryCount != duplicateDeliveryCount ||
		report.NextStreamSequence != nextStreamSequence ||
		report.DeliveryMismatchCount != 0 ||
		report.LedgerMismatchCount != 0 ||
		report.UnbalancedGroupCount != 0 ||
		report.OrderFillMismatchCount != 0 ||
		report.PositionMismatchCount != 0 ||
		report.CommandMismatchCount != 0 ||
		report.ProtectionMismatchCount != 0 ||
		report.FundingMismatchCount != 0 ||
		report.ConfigurationMismatchCount != 0 ||
		report.MarketMismatchCount != 0 ||
		report.MessagingMismatchCount != 0 ||
		report.RealtimeMismatchCount != 0 ||
		report.RealtimeQuarantinedCount != 0 ||
		report.PendingOutboxMessages != 0 {
		t.Fatalf("current reconciliation = %+v", report)
	}
}

type v3RemediationDatabaseSnapshot struct {
	MigrationTip                      string
	MigrationCount                    int
	EffectiveLeverageColumnExists     bool
	EffectiveLeverageColumnType       string
	EffectiveLeverageColumnNotNull    bool
	EffectiveLeverageColumnHasDefault bool
	EffectiveLeverageConstraintValid  bool
	CommandCount                      int
	CompletedIdempotencyCount         int
	InProgressIdempotencyCount        int
	ReplayResponseCount               int
	OutboxCount                       int
	PublishedOutboxCount              int
	PendingOutboxCount                int
	ReceiptCount                      int
	InvalidReceiptCount               int
	DuplicateDeliveryCount            int
	CheckpointHash                    string
	CheckpointNextStreamSequence      uint64
	CheckpointReady                   bool
	RiskLeverage                      string
	RiskVersion                       uint64
	InstrumentMaximumLeverage         string
	InstrumentVersion                 uint64
	OrderStatus                       string
	OrderVersion                      uint64
	ActiveOrderCount                  int
	BalanceTotal                      string
	BalanceUsed                       string
	BalanceFree                       string
	BalanceEquity                     string
	BalanceLedgerSequence             uint64
	LedgerTransactionCount            int
	LedgerEntryCount                  int
	FillCount                         int
	PositionCount                     int
	FaultCount                        int
}

type v3RemediationDurableSnapshot struct {
	CommandCount                 int
	CompletedIdempotencyCount    int
	InProgressIdempotencyCount   int
	ReplayResponseCount          int
	OutboxCount                  int
	PublishedOutboxCount         int
	PendingOutboxCount           int
	ReceiptCount                 int
	InvalidReceiptCount          int
	DuplicateDeliveryCount       int
	CheckpointHash               string
	CheckpointNextStreamSequence uint64
	CheckpointReady              bool
	RiskLeverage                 string
	RiskVersion                  uint64
	InstrumentMaximumLeverage    string
	InstrumentVersion            uint64
	OrderStatus                  string
	OrderVersion                 uint64
	ActiveOrderCount             int
	BalanceTotal                 string
	BalanceUsed                  string
	BalanceFree                  string
	BalanceEquity                string
	BalanceLedgerSequence        uint64
	LedgerTransactionCount       int
	LedgerEntryCount             int
	FillCount                    int
	PositionCount                int
	FaultCount                   int
}

func (snapshot v3RemediationDatabaseSnapshot) durable() v3RemediationDurableSnapshot {
	return v3RemediationDurableSnapshot{
		CommandCount:                 snapshot.CommandCount,
		CompletedIdempotencyCount:    snapshot.CompletedIdempotencyCount,
		InProgressIdempotencyCount:   snapshot.InProgressIdempotencyCount,
		ReplayResponseCount:          snapshot.ReplayResponseCount,
		OutboxCount:                  snapshot.OutboxCount,
		PublishedOutboxCount:         snapshot.PublishedOutboxCount,
		PendingOutboxCount:           snapshot.PendingOutboxCount,
		ReceiptCount:                 snapshot.ReceiptCount,
		InvalidReceiptCount:          snapshot.InvalidReceiptCount,
		DuplicateDeliveryCount:       snapshot.DuplicateDeliveryCount,
		CheckpointHash:               snapshot.CheckpointHash,
		CheckpointNextStreamSequence: snapshot.CheckpointNextStreamSequence,
		CheckpointReady:              snapshot.CheckpointReady,
		RiskLeverage:                 snapshot.RiskLeverage,
		RiskVersion:                  snapshot.RiskVersion,
		InstrumentMaximumLeverage:    snapshot.InstrumentMaximumLeverage,
		InstrumentVersion:            snapshot.InstrumentVersion,
		OrderStatus:                  snapshot.OrderStatus,
		OrderVersion:                 snapshot.OrderVersion,
		ActiveOrderCount:             snapshot.ActiveOrderCount,
		BalanceTotal:                 snapshot.BalanceTotal,
		BalanceUsed:                  snapshot.BalanceUsed,
		BalanceFree:                  snapshot.BalanceFree,
		BalanceEquity:                snapshot.BalanceEquity,
		BalanceLedgerSequence:        snapshot.BalanceLedgerSequence,
		LedgerTransactionCount:       snapshot.LedgerTransactionCount,
		LedgerEntryCount:             snapshot.LedgerEntryCount,
		FillCount:                    snapshot.FillCount,
		PositionCount:                snapshot.PositionCount,
		FaultCount:                   snapshot.FaultCount,
	}
}

func inspectV3RemediationDatabase(
	t *testing.T,
	pool *pgxpool.Pool,
) v3RemediationDatabaseSnapshot {
	t.Helper()
	var snapshot v3RemediationDatabaseSnapshot
	if err := pool.QueryRow(context.Background(), `
		SELECT
			COALESCE((SELECT max(filename) FROM engine.schema_migrations), ''),
			(SELECT count(*) FROM engine.schema_migrations),
			EXISTS (
				SELECT 1
				  FROM pg_attribute
				 WHERE attrelid = 'trading.fills'::regclass
				   AND attname = 'effective_leverage'
				   AND NOT attisdropped
			),
			COALESCE((
				SELECT format_type(atttypid, atttypmod)
				  FROM pg_attribute
				 WHERE attrelid = 'trading.fills'::regclass
				   AND attname = 'effective_leverage'
				   AND NOT attisdropped
			), ''),
			COALESCE((
				SELECT attnotnull
				  FROM pg_attribute
				 WHERE attrelid = 'trading.fills'::regclass
				   AND attname = 'effective_leverage'
				   AND NOT attisdropped
			), false),
			COALESCE((
				SELECT atthasdef
				  FROM pg_attribute
				 WHERE attrelid = 'trading.fills'::regclass
				   AND attname = 'effective_leverage'
				   AND NOT attisdropped
			), false),
			COALESCE((
				SELECT convalidated
				  FROM pg_constraint
				 WHERE conrelid = 'trading.fills'::regclass
				   AND conname = 'fills_effective_leverage_positive'
			), false),
			(SELECT count(*)
			   FROM trading.commands
			  WHERE account_id =
			        'urn:xb:account:pg19-v3-remediation'),
			(SELECT count(*)
			   FROM trading.idempotency_records
			  WHERE scope =
			        'v3-remediation:urn:xb:account:pg19-v3-remediation'
			    AND state = 'completed'),
			(SELECT count(*)
			   FROM trading.idempotency_records
			  WHERE scope =
			        'v3-remediation:urn:xb:account:pg19-v3-remediation'
			    AND state = 'in_progress'),
			(SELECT count(*)
			   FROM trading.idempotency_records
			  WHERE scope =
			        'v3-remediation:urn:xb:account:pg19-v3-remediation'
			    AND response_status = 202
			    AND response_headers IS NOT NULL
			    AND response_body IS NOT NULL),
			(SELECT count(*) FROM messaging.outbox),
			(SELECT count(*)
			   FROM messaging.outbox
			  WHERE published_at IS NOT NULL),
			(SELECT count(*)
			   FROM messaging.outbox
			  WHERE published_at IS NULL),
			(SELECT count(*)
			   FROM engine.input_receipts
			  WHERE shard_id = $1),
				(SELECT count(*)
				   FROM engine.input_receipts AS receipt
				  WHERE receipt.shard_id = $1
				    AND (
						receipt.decision_hash_version <> 3
						OR receipt.decision ->> 'DecisionHashVersion'
						   IS DISTINCT FROM '3'
						OR receipt.decision -> 'DecisionHash' IS DISTINCT FROM (
							SELECT jsonb_agg(
								get_byte(receipt.decision_hash, byte_index)
								ORDER BY byte_index
							)
							  FROM generate_series(
								0,
								octet_length(receipt.decision_hash) - 1
							  ) AS bytes(byte_index)
						)
						OR receipt.decision -> 'NextStateHash' IS DISTINCT FROM (
							SELECT jsonb_agg(
								get_byte(receipt.resulting_state_hash, byte_index)
								ORDER BY byte_index
							)
							  FROM generate_series(
								0,
								octet_length(receipt.resulting_state_hash) - 1
							  ) AS bytes(byte_index)
						)
				    )),
			(SELECT count(*)
			   FROM engine.duplicate_delivery_receipts
			  WHERE shard_id = $1),
			COALESCE((
				SELECT encode(state_hash, 'hex')
				  FROM engine.shard_checkpoints
				 WHERE shard_id = $1
			), ''),
			COALESCE((
				SELECT next_stream_sequence
				  FROM engine.shard_checkpoints
				 WHERE shard_id = $1
			), 0),
			COALESCE((
				SELECT ready
				  FROM engine.shard_checkpoints
				 WHERE shard_id = $1
			), false),
			COALESCE((
				SELECT trim_scale(leverage)::text
				  FROM trading.risk_configs
				 WHERE account_id =
				       'urn:xb:account:pg19-v3-remediation'
				   AND instrument_id = 'BTC-PERP'
			), ''),
			COALESCE((
				SELECT version
				  FROM trading.risk_configs
				 WHERE account_id =
				       'urn:xb:account:pg19-v3-remediation'
				   AND instrument_id = 'BTC-PERP'
			), 0),
			COALESCE((
				SELECT trim_scale(max_leverage)::text
				  FROM trading.instruments
				 WHERE instrument_id = 'BTC-PERP'
			), ''),
			COALESCE((
				SELECT version
				  FROM trading.instruments
				 WHERE instrument_id = 'BTC-PERP'
			), 0),
			COALESCE((
				SELECT status
				  FROM trading.orders
				 WHERE account_id =
				       'urn:xb:account:pg19-v3-remediation'
			), ''),
			COALESCE((
				SELECT version
				  FROM trading.orders
				 WHERE account_id =
				       'urn:xb:account:pg19-v3-remediation'
			), 0),
			(SELECT count(*)
			   FROM trading.orders
			  WHERE account_id =
			        'urn:xb:account:pg19-v3-remediation'
			    AND status IN ('held', 'working', 'partially_filled')),
			COALESCE((
				SELECT trim_scale(total)::text
				  FROM ledger.balances
				 WHERE account_id =
				       'urn:xb:account:pg19-v3-remediation'
				   AND currency = 'USDC'
			), ''),
			COALESCE((
				SELECT trim_scale(used)::text
				  FROM ledger.balances
				 WHERE account_id =
				       'urn:xb:account:pg19-v3-remediation'
				   AND currency = 'USDC'
			), ''),
			COALESCE((
				SELECT trim_scale(free)::text
				  FROM ledger.balances
				 WHERE account_id =
				       'urn:xb:account:pg19-v3-remediation'
				   AND currency = 'USDC'
			), ''),
			COALESCE((
				SELECT trim_scale(equity)::text
				  FROM ledger.balances
				 WHERE account_id =
				       'urn:xb:account:pg19-v3-remediation'
				   AND currency = 'USDC'
			), ''),
			COALESCE((
				SELECT ledger_sequence
				  FROM ledger.balances
				 WHERE account_id =
				       'urn:xb:account:pg19-v3-remediation'
				   AND currency = 'USDC'
			), 0),
			(SELECT count(*) FROM ledger.transactions),
			(SELECT count(*) FROM ledger.entries),
			(SELECT count(*) FROM trading.fills),
			(SELECT count(*) FROM trading.positions),
			(SELECT count(*) FROM engine.shard_faults
			  WHERE shard_id = $1)`,
		int64(v3RemediationShardID),
	).Scan(
		&snapshot.MigrationTip,
		&snapshot.MigrationCount,
		&snapshot.EffectiveLeverageColumnExists,
		&snapshot.EffectiveLeverageColumnType,
		&snapshot.EffectiveLeverageColumnNotNull,
		&snapshot.EffectiveLeverageColumnHasDefault,
		&snapshot.EffectiveLeverageConstraintValid,
		&snapshot.CommandCount,
		&snapshot.CompletedIdempotencyCount,
		&snapshot.InProgressIdempotencyCount,
		&snapshot.ReplayResponseCount,
		&snapshot.OutboxCount,
		&snapshot.PublishedOutboxCount,
		&snapshot.PendingOutboxCount,
		&snapshot.ReceiptCount,
		&snapshot.InvalidReceiptCount,
		&snapshot.DuplicateDeliveryCount,
		&snapshot.CheckpointHash,
		&snapshot.CheckpointNextStreamSequence,
		&snapshot.CheckpointReady,
		&snapshot.RiskLeverage,
		&snapshot.RiskVersion,
		&snapshot.InstrumentMaximumLeverage,
		&snapshot.InstrumentVersion,
		&snapshot.OrderStatus,
		&snapshot.OrderVersion,
		&snapshot.ActiveOrderCount,
		&snapshot.BalanceTotal,
		&snapshot.BalanceUsed,
		&snapshot.BalanceFree,
		&snapshot.BalanceEquity,
		&snapshot.BalanceLedgerSequence,
		&snapshot.LedgerTransactionCount,
		&snapshot.LedgerEntryCount,
		&snapshot.FillCount,
		&snapshot.PositionCount,
		&snapshot.FaultCount,
	); err != nil {
		t.Fatalf("inspect v3 remediation database: %v", err)
	}
	return snapshot
}

func requireInvalidV3RemediationState(
	t *testing.T,
	snapshot v3RemediationDatabaseSnapshot,
	result v3RemediationResult,
) {
	t.Helper()
	if snapshot.MigrationTip != fillEffectiveLeveragePreviousTip ||
		snapshot.EffectiveLeverageColumnExists ||
		snapshot.CommandCount != 3 ||
		snapshot.CompletedIdempotencyCount != 3 ||
		snapshot.InProgressIdempotencyCount != 0 ||
		snapshot.ReplayResponseCount != 3 ||
		snapshot.OutboxCount != 4 ||
		snapshot.PublishedOutboxCount != 0 ||
		snapshot.PendingOutboxCount != 4 ||
		snapshot.ReceiptCount != 8 ||
		snapshot.InvalidReceiptCount != 0 ||
		snapshot.DuplicateDeliveryCount != 0 ||
		snapshot.CheckpointHash != result.StateHash ||
		snapshot.CheckpointNextStreamSequence != 9 ||
		!snapshot.CheckpointReady ||
		snapshot.RiskLeverage != "10" ||
		snapshot.RiskVersion != 1 ||
		snapshot.InstrumentMaximumLeverage != "5" ||
		snapshot.InstrumentVersion != 2 ||
		snapshot.OrderStatus != "working" ||
		snapshot.OrderVersion != 1 ||
		snapshot.ActiveOrderCount != 1 ||
		snapshot.BalanceTotal != "1000" ||
		snapshot.BalanceUsed != "1" ||
		snapshot.BalanceFree != "999" ||
		snapshot.BalanceEquity != "1000" ||
		snapshot.BalanceLedgerSequence != 8 ||
		snapshot.LedgerTransactionCount != 1 ||
		snapshot.LedgerEntryCount != 2 ||
		snapshot.FillCount != 0 ||
		snapshot.PositionCount != 0 ||
		snapshot.FaultCount != 0 {
		t.Fatalf("invalid v3 remediation fixture state = %+v", snapshot)
	}
}

func requireCorrectedV3RemediationState(
	t *testing.T,
	snapshot v3RemediationDatabaseSnapshot,
	result v3RemediationResult,
) {
	t.Helper()
	if snapshot.MigrationTip != fillEffectiveLeveragePreviousTip ||
		snapshot.EffectiveLeverageColumnExists ||
		snapshot.CommandCount != 4 ||
		snapshot.CompletedIdempotencyCount != 4 ||
		snapshot.InProgressIdempotencyCount != 0 ||
		snapshot.ReplayResponseCount != 4 ||
		snapshot.OutboxCount != 6 ||
		snapshot.PublishedOutboxCount != 0 ||
		snapshot.PendingOutboxCount != 6 ||
		snapshot.ReceiptCount != 11 ||
		snapshot.InvalidReceiptCount != 0 ||
		snapshot.DuplicateDeliveryCount != 0 ||
		snapshot.CheckpointHash != result.StateHash ||
		snapshot.CheckpointNextStreamSequence != 12 ||
		!snapshot.CheckpointReady ||
		snapshot.RiskLeverage != "5" ||
		snapshot.RiskVersion != 2 ||
		snapshot.InstrumentMaximumLeverage != "5" ||
		snapshot.InstrumentVersion != 2 ||
		snapshot.OrderStatus != "cancelled" ||
		snapshot.OrderVersion != 2 ||
		snapshot.ActiveOrderCount != 0 ||
		snapshot.BalanceTotal != "1000" ||
		snapshot.BalanceUsed != "0" ||
		snapshot.BalanceFree != "1000" ||
		snapshot.BalanceEquity != "1000" ||
		snapshot.BalanceLedgerSequence != 10 ||
		snapshot.LedgerTransactionCount != 1 ||
		snapshot.LedgerEntryCount != 2 ||
		snapshot.FillCount != 0 ||
		snapshot.PositionCount != 0 ||
		snapshot.FaultCount != 0 {
		t.Fatalf("corrected v3 remediation fixture state = %+v", snapshot)
	}
}

func requirePostDuplicateV3RemediationState(
	t *testing.T,
	snapshot v3RemediationDatabaseSnapshot,
	before v3RemediationDatabaseSnapshot,
	state engine.State,
) {
	t.Helper()
	expected := before
	expected.PublishedOutboxCount = 6
	expected.PendingOutboxCount = 0
	expected.DuplicateDeliveryCount = 4
	expected.CheckpointHash = state.Hash().String()
	expected.CheckpointNextStreamSequence = 16
	if !state.Ready() ||
		state.Hash().String() != v3RemediationPostDuplicateStateHash ||
		state.NextStreamSequence() != 16 ||
		snapshot != expected {
		t.Fatalf(
			"post-duplicate durable state ready/hash/next=%t/%s/%d:\n got  %+v\n want %+v",
			state.Ready(),
			state.Hash(),
			state.NextStreamSequence(),
			snapshot,
			expected,
		)
	}
}

func requireNoV3EngineOwner(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	connection, err := pool.Acquire(context.Background())
	if err != nil {
		t.Fatalf("acquire v3 owner-drain probe: %v", err)
	}
	defer connection.Release()
	var acquired bool
	if err := connection.QueryRow(context.Background(), `
		SELECT pg_try_advisory_lock(1346850639, $1)`,
		int64(v3RemediationShardID),
	).Scan(&acquired); err != nil {
		t.Fatalf("probe drained v3 engine owner: %v", err)
	}
	if !acquired {
		t.Fatal("pinned v3 engine ownership remains active after fixture exit")
	}
	var released bool
	if err := connection.QueryRow(context.Background(), `
		SELECT pg_advisory_unlock(1346850639, $1)`,
		int64(v3RemediationShardID),
	).Scan(&released); err != nil {
		t.Fatalf("release v3 owner-drain probe: %v", err)
	}
	if !released {
		t.Fatal("v3 owner-drain probe lock was not released")
	}
}
