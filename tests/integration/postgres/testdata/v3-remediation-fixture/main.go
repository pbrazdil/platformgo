package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing/fstest"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
	platformpostgres "github.com/upcomers-org/platformgo/internal/adapters/postgres"
	"github.com/upcomers-org/platformgo/internal/engine"
	"github.com/upcomers-org/platformgo/internal/testsupport/postgresfixture"
)

const (
	migrationTip = "20260726000800_phase3_balance_projection_hash_v3.up.sql"

	phaseSeedInvalid      = "seed-invalid"
	phaseRemediate        = "remediate"
	phaseRecoverReconcile = "recover-reconcile"

	accountID    = "urn:xb:account:pg19-v3-remediation"
	instrumentID = "BTC-PERP"
)

const shardID engine.ShardID = 64

var (
	fixtureNamespace = engine.IDFromSequence(engine.ID{}, uint64(shardID))
	orderID          = engine.IDFromSequence(fixtureNamespace, 100)
)

type options struct {
	phase                      string
	expectedStateHash          string
	expectedNextStreamSequence uint64
}

type protocolResult struct {
	StateHash          string `json:"stateHash"`
	NextStreamSequence uint64 `json:"nextStreamSequence"`
}

func main() {
	dsn := os.Getenv("PLATFORMGO_TEST_POSTGRES_DSN")
	result, err := run(context.Background(), os.Args[1:], dsn)
	if err != nil {
		message := err.Error()
		if dsn != "" {
			message = strings.ReplaceAll(
				message,
				dsn,
				"[REDACTED_POSTGRES_DSN]",
			)
		}
		_, _ = fmt.Fprintln(os.Stderr, message)
		os.Exit(1)
	}
	if err := json.NewEncoder(os.Stdout).Encode(result); err != nil {
		_, _ = fmt.Fprintln(os.Stderr, "encode fixture result")
		os.Exit(1)
	}
}

func run(
	ctx context.Context,
	arguments []string,
	dsn string,
) (protocolResult, error) {
	parsed, err := parseOptions(arguments)
	if err != nil {
		return protocolResult{}, err
	}
	if engine.CurrentDecisionHashVersion != 3 {
		return protocolResult{}, fmt.Errorf(
			"artifact decision-hash version is %d, want 3",
			engine.CurrentDecisionHashVersion,
		)
	}
	if dsn == "" {
		return protocolResult{}, errors.New(
			"PLATFORMGO_TEST_POSTGRES_DSN is required",
		)
	}
	config, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return protocolResult{}, errors.New("parse PostgreSQL test DSN")
	}
	config.MaxConns = 8
	pool, err := pgxpool.NewWithConfig(ctx, config)
	if err != nil {
		return protocolResult{}, errors.New("open PostgreSQL test pool")
	}
	defer pool.Close()
	if err := pool.Ping(ctx); err != nil {
		return protocolResult{}, fmt.Errorf("ping PostgreSQL: %w", err)
	}
	var serverVersionNumber int
	if err := pool.QueryRow(
		ctx,
		`SELECT current_setting('server_version_num')::integer`,
	).Scan(&serverVersionNumber); err != nil {
		return protocolResult{}, fmt.Errorf("read PostgreSQL version: %w", err)
	}
	if serverVersionNumber/10000 != 19 {
		return protocolResult{}, fmt.Errorf(
			"PostgreSQL major is %d, want 19",
			serverVersionNumber/10000,
		)
	}

	migrations, err := migrationsThroughTip()
	if err != nil {
		return protocolResult{}, err
	}
	migrator := platformpostgres.NewMigrator(pool, migrations)
	switch parsed.phase {
	case phaseSeedInvalid:
		return seedInvalid(ctx, pool, migrator)
	case phaseRemediate:
		return remediate(ctx, pool, migrator, parsed)
	case phaseRecoverReconcile:
		return recoverReconcile(ctx, pool, migrator, parsed)
	default:
		return protocolResult{}, fmt.Errorf(
			"unsupported fixture phase %q",
			parsed.phase,
		)
	}
}

func parseOptions(arguments []string) (options, error) {
	var parsed options
	flags := flag.NewFlagSet("v3-remediation-fixture", flag.ContinueOnError)
	flags.SetOutput(io.Discard)
	flags.StringVar(&parsed.phase, "phase", "", "fixture phase")
	flags.StringVar(
		&parsed.expectedStateHash,
		"expected-state-hash",
		"",
		"expected predecessor state hash",
	)
	flags.Uint64Var(
		&parsed.expectedNextStreamSequence,
		"expected-next-stream-sequence",
		0,
		"expected predecessor next stream sequence",
	)
	if err := flags.Parse(arguments); err != nil {
		return options{}, fmt.Errorf("parse fixture flags: %w", err)
	}
	if flags.NArg() != 0 {
		return options{}, errors.New("positional arguments are not supported")
	}
	switch parsed.phase {
	case phaseSeedInvalid:
		if parsed.expectedStateHash != "" ||
			parsed.expectedNextStreamSequence != 0 {
			return options{}, errors.New(
				"seed phase does not accept expected state",
			)
		}
	case phaseRemediate, phaseRecoverReconcile:
		decoded, err := hex.DecodeString(parsed.expectedStateHash)
		if err != nil || len(decoded) != 32 {
			return options{}, errors.New(
				"expected state hash must be 32 canonical bytes",
			)
		}
		if parsed.expectedNextStreamSequence == 0 {
			return options{}, errors.New(
				"expected next stream sequence is required",
			)
		}
	default:
		return options{}, errors.New(
			"--phase must be seed-invalid, remediate, or recover-reconcile",
		)
	}
	return parsed, nil
}

func seedInvalid(
	ctx context.Context,
	pool *pgxpool.Pool,
	migrator *platformpostgres.Migrator,
) (protocolResult, error) {
	if err := postgresfixture.ResetDurableSchemas(ctx, pool); err != nil {
		return protocolResult{}, fmt.Errorf("reset disposable database: %w", err)
	}
	if err := ensureRuntimeRoles(ctx, pool); err != nil {
		return protocolResult{}, err
	}
	if err := migrator.MigrateAndProvision(ctx, shardID); err != nil {
		return protocolResult{}, fmt.Errorf("migrate v3 schema: %w", err)
	}
	if err := verifyExactTip(ctx, pool, migrator); err != nil {
		return protocolResult{}, err
	}

	store := platformpostgres.NewEngineStore(pool)
	ownership, err := store.AcquireShardOwnership(ctx, shardID)
	if err != nil {
		return protocolResult{}, fmt.Errorf("acquire seed ownership: %w", err)
	}
	state := engine.NewState(shardID)
	apply := func(spec inputSpec) (engine.Decision, error) {
		next, decision, applyErr := applyInput(
			ctx,
			pool,
			store,
			ownership,
			state,
			spec,
		)
		if applyErr == nil {
			state = next
		}
		return decision, applyErr
	}

	for _, spec := range seedInputSpecs()[:5] {
		decision, err := apply(spec)
		if err != nil {
			_ = ownership.Close(context.Background())
			return protocolResult{}, err
		}
		if decision.CommandResult.Status != engine.CommandStatusAccepted {
			_ = ownership.Close(context.Background())
			return protocolResult{}, fmt.Errorf(
				"seed sequence %d result is %+v, want accepted",
				spec.sequence,
				decision.CommandResult,
			)
		}
	}
	lowerMaximum, err := apply(seedInputSpecs()[5])
	if err != nil {
		_ = ownership.Close(context.Background())
		return protocolResult{}, err
	}
	if lowerMaximum.CommandResult.Status != engine.CommandStatusAccepted ||
		len(lowerMaximum.InstrumentChanges) != 1 ||
		lowerMaximum.InstrumentChanges[0].MaxLeverage != "5" {
		_ = ownership.Close(context.Background())
		return protocolResult{}, fmt.Errorf(
			"historical max reduction result=%+v changes=%+v, want accepted max 5",
			lowerMaximum.CommandResult,
			lowerMaximum.InstrumentChanges,
		)
	}
	refreshedBook, err := apply(seedInputSpecs()[6])
	if err != nil {
		_ = ownership.Close(context.Background())
		return protocolResult{}, err
	}
	if refreshedBook.CommandResult.Status != engine.CommandStatusAccepted ||
		len(refreshedBook.BookChanges) != 1 {
		_ = ownership.Close(context.Background())
		return protocolResult{}, fmt.Errorf(
			"revision-two book refresh decision = %+v",
			refreshedBook,
		)
	}
	restingOrder, err := apply(seedInputSpecs()[7])
	if err != nil {
		_ = ownership.Close(context.Background())
		return protocolResult{}, err
	}
	if restingOrder.CommandResult.Status != engine.CommandStatusAccepted ||
		len(restingOrder.OrderChanges) != 1 ||
		restingOrder.OrderChanges[0].Status != engine.OrderStatusWorking ||
		len(restingOrder.Fills) != 0 {
		_ = ownership.Close(context.Background())
		return protocolResult{}, fmt.Errorf(
			"resting order decision = %+v",
			restingOrder,
		)
	}
	if err := ownership.Close(ctx); err != nil {
		return protocolResult{}, fmt.Errorf("close seed ownership: %w", err)
	}

	if err := requireDurableState(ctx, pool, state, 8); err != nil {
		return protocolResult{}, err
	}
	return resultForState(state), nil
}

func remediate(
	ctx context.Context,
	pool *pgxpool.Pool,
	migrator *platformpostgres.Migrator,
	parsed options,
) (protocolResult, error) {
	if err := verifyExactTip(ctx, pool, migrator); err != nil {
		return protocolResult{}, err
	}
	store := platformpostgres.NewEngineStore(pool)
	ownership, err := store.AcquireShardOwnership(ctx, shardID)
	if err != nil {
		return protocolResult{}, fmt.Errorf(
			"acquire remediation ownership: %w",
			err,
		)
	}
	state, err := store.RecoverTradingState(ctx, shardID)
	if err != nil {
		_ = ownership.Close(context.Background())
		return protocolResult{}, fmt.Errorf("recover seed state: %w", err)
	}
	if err := requireExpectedState(state, parsed); err != nil {
		_ = ownership.Close(context.Background())
		return protocolResult{}, err
	}
	apply := func(spec inputSpec) (engine.Decision, error) {
		next, decision, applyErr := applyInput(
			ctx,
			pool,
			store,
			ownership,
			state,
			spec,
		)
		if applyErr == nil {
			state = next
		}
		return decision, applyErr
	}

	rejected, err := apply(inputSpec{
		sequence:             9,
		kind:                 engine.InputKindConfiguration,
		configurationVersion: 1,
		instrumentVersion:    2,
		action:               configureRisk("5"),
	})
	if err != nil {
		_ = ownership.Close(context.Background())
		return protocolResult{}, err
	}
	if rejected.CommandResult.Status != engine.CommandStatusRejected ||
		rejected.CommandResult.Reason != engine.RejectionRiskConfigLocked ||
		len(rejected.RiskChanges) != 0 {
		_ = ownership.Close(context.Background())
		return protocolResult{}, fmt.Errorf(
			"locked correction result=%+v changes=%+v",
			rejected.CommandResult,
			rejected.RiskChanges,
		)
	}

	cancelled, err := apply(inputSpec{
		sequence:             10,
		accountSequence:      4,
		kind:                 engine.InputKindCommand,
		configurationVersion: 1,
		instrumentVersion:    2,
		action: engine.TradingAction{
			Kind: engine.TradingActionCancelOrder,
			CancelOrder: &engine.CancelOrder{
				AccountID: accountID,
				OrderID:   orderID,
			},
		},
	})
	if err != nil {
		_ = ownership.Close(context.Background())
		return protocolResult{}, err
	}
	if cancelled.CommandResult.Status != engine.CommandStatusAccepted ||
		len(cancelled.OrderChanges) != 1 ||
		cancelled.OrderChanges[0].Status != engine.OrderStatusCancelled {
		_ = ownership.Close(context.Background())
		return protocolResult{}, fmt.Errorf(
			"cancel decision = %+v",
			cancelled,
		)
	}

	accepted, err := apply(inputSpec{
		sequence:             11,
		kind:                 engine.InputKindConfiguration,
		configurationVersion: 1,
		instrumentVersion:    2,
		action:               configureRisk("5.00"),
	})
	if err != nil {
		_ = ownership.Close(context.Background())
		return protocolResult{}, err
	}
	if accepted.CommandResult.Status != engine.CommandStatusAccepted ||
		len(accepted.RiskChanges) != 1 ||
		accepted.RiskChanges[0].Leverage != "5" {
		_ = ownership.Close(context.Background())
		return protocolResult{}, fmt.Errorf(
			"accepted correction = %+v",
			accepted,
		)
	}
	if err := ownership.Close(ctx); err != nil {
		return protocolResult{}, fmt.Errorf(
			"close remediation ownership: %w",
			err,
		)
	}
	if err := requireDurableState(ctx, pool, state, 11); err != nil {
		return protocolResult{}, err
	}
	return resultForState(state), nil
}

func recoverReconcile(
	ctx context.Context,
	pool *pgxpool.Pool,
	migrator *platformpostgres.Migrator,
	parsed options,
) (protocolResult, error) {
	if err := verifyExactTip(ctx, pool, migrator); err != nil {
		return protocolResult{}, err
	}
	store := platformpostgres.NewEngineStore(pool)
	ownership, err := store.AcquireShardOwnership(ctx, shardID)
	if err != nil {
		return protocolResult{}, fmt.Errorf(
			"acquire recovery ownership: %w",
			err,
		)
	}
	state, err := store.RecoverTradingState(ctx, shardID)
	if err != nil {
		_ = ownership.Close(context.Background())
		return protocolResult{}, fmt.Errorf("recover corrected state: %w", err)
	}
	if err := requireExpectedState(state, parsed); err != nil {
		_ = ownership.Close(context.Background())
		return protocolResult{}, err
	}
	if err := ownership.Close(ctx); err != nil {
		return protocolResult{}, fmt.Errorf("close recovery ownership: %w", err)
	}
	report, err := store.ReconcileShard(ctx, shardID)
	if err != nil {
		return protocolResult{}, fmt.Errorf("reconcile corrected state: %w", err)
	}
	if !v3UpgradeBoundaryClean(report) {
		return protocolResult{}, fmt.Errorf(
			"v3 reconciliation does not match the pending-upgrade boundary: %+v",
			report,
		)
	}
	var riskAboveMaximum uint64
	if err := pool.QueryRow(ctx, `
		SELECT count(*)
		  FROM trading.risk_configs AS risk
		  JOIN trading.instruments AS instrument
		    ON instrument.instrument_id = risk.instrument_id
		 WHERE risk.leverage > instrument.max_leverage`,
	).Scan(&riskAboveMaximum); err != nil {
		return protocolResult{}, fmt.Errorf(
			"scan corrected risk authority: %w",
			err,
		)
	}
	if riskAboveMaximum != 0 {
		return protocolResult{}, fmt.Errorf(
			"corrected risk authority has %d row(s) above maximum",
			riskAboveMaximum,
		)
	}
	return resultForState(state), nil
}

type inputSpec struct {
	sequence             uint64
	accountSequence      uint64
	kind                 engine.InputKind
	configurationVersion uint64
	instrumentVersion    uint64
	action               engine.TradingAction
}

func seedInputSpecs() []inputSpec {
	return []inputSpec{
		{
			sequence:             1,
			kind:                 engine.InputKindConfiguration,
			configurationVersion: 1,
			instrumentVersion:    1,
			action:               configureInstrument(1, "10"),
		},
		{
			sequence:             2,
			accountSequence:      1,
			kind:                 engine.InputKindCommand,
			configurationVersion: 1,
			instrumentVersion:    1,
			action: engine.TradingAction{
				Kind: engine.TradingActionConfigureAccount,
				ConfigureAccount: &engine.ConfigureAccount{
					AccountID: accountID,
					OmsMode:   engine.OmsModeNetting,
				},
			},
		},
		{
			sequence:             3,
			accountSequence:      2,
			kind:                 engine.InputKindCommand,
			configurationVersion: 1,
			instrumentVersion:    1,
			action: engine.TradingAction{
				Kind: engine.TradingActionAdjustBalance,
				AdjustBalance: &engine.AdjustBalance{
					AccountID:     accountID,
					Currency:      "USDC",
					CurrencyScale: 2,
					Operation:     engine.BalanceOperationDeposit,
					Amount:        "1000",
				},
			},
		},
		{
			sequence:             4,
			kind:                 engine.InputKindConfiguration,
			configurationVersion: 1,
			instrumentVersion:    1,
			action:               configureRisk("10"),
		},
		{
			sequence:             5,
			kind:                 engine.InputKindMarket,
			configurationVersion: 1,
			instrumentVersion:    1,
			action: engine.TradingAction{
				Kind: engine.TradingActionUpdateBook,
				UpdateBook: &engine.UpdateBook{
					InstrumentID: instrumentID,
					MarkPrice:    "100",
					Bids: []engine.BookLevel{{
						Price: "99", Quantity: "10",
					}},
					Asks: []engine.BookLevel{{
						Price: "100", Quantity: "10",
					}},
				},
			},
		},
		{
			sequence:             6,
			kind:                 engine.InputKindConfiguration,
			configurationVersion: 1,
			instrumentVersion:    2,
			action:               configureInstrument(2, "5"),
		},
		{
			sequence:             7,
			kind:                 engine.InputKindMarket,
			configurationVersion: 1,
			instrumentVersion:    2,
			action: engine.TradingAction{
				Kind: engine.TradingActionUpdateBook,
				UpdateBook: &engine.UpdateBook{
					InstrumentID: instrumentID,
					MarkPrice:    "100",
					Bids: []engine.BookLevel{{
						Price: "99", Quantity: "10",
					}},
					Asks: []engine.BookLevel{{
						Price: "100", Quantity: "10",
					}},
				},
			},
		},
		{
			sequence:             8,
			accountSequence:      3,
			kind:                 engine.InputKindCommand,
			configurationVersion: 1,
			instrumentVersion:    2,
			action: engine.TradingAction{
				Kind: engine.TradingActionSubmitOrder,
				SubmitOrder: &engine.SubmitOrder{
					OrderID:      orderID,
					AccountID:    accountID,
					InstrumentID: instrumentID,
					Side:         engine.SideBuy,
					Type:         engine.OrderTypeLimit,
					TimeInForce:  engine.TimeInForceGTC,
					Quantity:     "1",
					Price:        "90",
				},
			},
		},
	}
}

func configureInstrument(
	revision uint64,
	maximumLeverage string,
) engine.TradingAction {
	return engine.TradingAction{
		Kind: engine.TradingActionConfigureInstrument,
		ConfigureInstrument: &engine.ConfigureInstrument{
			InstrumentID:            instrumentID,
			Revision:                revision,
			PriceScale:              2,
			QuantityScale:           3,
			SettlementCurrency:      "USDC",
			SettlementCurrencyScale: 2,
			InitialMarginRate:       "0.1",
			MaintenanceMarginRate:   "0.05",
			MaxLeverage:             maximumLeverage,
			MakerFeeRate:            "0",
			TakerFeeRate:            "0",
		},
	}
}

func configureRisk(leverage string) engine.TradingAction {
	return engine.TradingAction{
		Kind: engine.TradingActionConfigureRisk,
		ConfigureRisk: &engine.ConfigureRisk{
			AccountID:    accountID,
			InstrumentID: instrumentID,
			MarginMode:   engine.MarginModeCross,
			Leverage:     leverage,
		},
	}
}

func applyInput(
	ctx context.Context,
	pool *pgxpool.Pool,
	store *platformpostgres.EngineStore,
	ownership *platformpostgres.ShardOwnership,
	state engine.State,
	spec inputSpec,
) (engine.State, engine.Decision, error) {
	payload, err := engine.EncodeTradingAction(spec.action)
	if err != nil {
		return state, engine.Decision{}, fmt.Errorf(
			"encode sequence %d action: %w",
			spec.sequence,
			err,
		)
	}
	sourceSequence := spec.sequence
	streamSequence := spec.sequence
	if spec.kind == engine.InputKindCommand {
		if spec.accountSequence == 0 {
			return state, engine.Decision{}, fmt.Errorf(
				"command sequence %d has no account sequence",
				spec.sequence,
			)
		}
		sourceSequence = spec.accountSequence
		streamSequence = 0
	}
	input := engine.InputEnvelope{
		InputID:              engine.IDFromSequence(fixtureNamespace, spec.sequence),
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              shardID,
		Kind:                 spec.kind,
		SourceID:             "postgres19-v3-remediation",
		SourceSequence:       sourceSequence,
		StreamSequence:       streamSequence,
		MarketSequence:       spec.sequence,
		LogicalTime:          engine.NewLogicalTime(fixtureTime(spec.sequence)),
		ConfigurationVersion: spec.configurationVersion,
		InstrumentVersion:    spec.instrumentVersion,
		Payload:              payload,
	}
	var commandRequest *platformpostgres.BeginCommandRequest
	if spec.kind == engine.InputKindCommand {
		request, err := admitPendingCommand(ctx, pool, input, spec.action)
		if err != nil {
			return state, engine.Decision{}, err
		}
		commandRequest = &request
		input.StreamSequence = spec.sequence
	}
	next, decision, duplicate, err := store.ApplyTrading(
		ctx,
		state,
		input,
		spec.action,
		platformpostgres.ApplyOptions{Ownership: ownership},
	)
	if err != nil {
		return state, engine.Decision{}, fmt.Errorf(
			"apply v3 sequence %d: %w",
			spec.sequence,
			err,
		)
	}
	if duplicate {
		return state, engine.Decision{}, fmt.Errorf(
			"v3 sequence %d was unexpectedly duplicate",
			spec.sequence,
		)
	}
	if decision.DecisionHashVersion != 3 {
		return state, engine.Decision{}, fmt.Errorf(
			"v3 sequence %d decision version = %d",
			spec.sequence,
			decision.DecisionHashVersion,
		)
	}
	if commandRequest != nil {
		if err := requireCommandReplay(ctx, pool, *commandRequest); err != nil {
			return state, engine.Decision{}, err
		}
	}
	return next, decision, nil
}

func fixtureTime(sequence uint64) time.Time {
	return time.Date(
		2026,
		time.July,
		26,
		21,
		0,
		int(sequence),
		123456789,
		time.UTC,
	)
}

func admitPendingCommand(
	ctx context.Context,
	pool *pgxpool.Pool,
	input engine.InputEnvelope,
	action engine.TradingAction,
) (platformpostgres.BeginCommandRequest, error) {
	actionAccountID, scoped := engine.TradingActionAccountID(action)
	if !scoped || actionAccountID == "" {
		return platformpostgres.BeginCommandRequest{}, fmt.Errorf(
			"command sequence %d has no account scope",
			input.StreamSequence,
		)
	}
	outboxPayload, err := engine.EncodeInputMessage(input)
	if err != nil {
		return platformpostgres.BeginCommandRequest{}, fmt.Errorf(
			"encode command outbox: %w",
			err,
		)
	}
	request := platformpostgres.BeginCommandRequest{
		Scope:            "v3-remediation:" + actionAccountID,
		IdempotencyKey:   input.InputID.String(),
		RequestHash:      sha256.Sum256(outboxPayload),
		CommandID:        input.InputID,
		AccountID:        actionAccountID,
		AccountSequence:  input.SourceSequence,
		CommandType:      string(action.Kind),
		SchemaVersion:    input.SchemaVersion,
		CanonicalPayload: input.Payload.Bytes(),
		OutboxSubject: fmt.Sprintf(
			"engine.input.%d.command.v%d",
			input.ShardID,
			input.SchemaVersion,
		),
		OutboxPayload: outboxPayload,
		LogicalTime:   time.Unix(0, input.LogicalTime.UnixNano()).UTC(),
		ExpiresAt: time.Unix(0, input.LogicalTime.UnixNano()).
			UTC().
			Add(24 * time.Hour),
		Response: platformpostgres.StoredResponse{
			Status:  202,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body: []byte(fmt.Sprintf(
				`{"commandId":%q,"status":"accepted"}`,
				input.InputID.String(),
			)),
		},
	}
	if action.SubmitOrder != nil {
		request.OrderID = action.SubmitOrder.OrderID
		request.IntentID = input.InputID.String()
	}
	result, err := platformpostgres.NewCommandJournal(pool).Begin(ctx, request)
	if err != nil {
		return platformpostgres.BeginCommandRequest{}, fmt.Errorf(
			"admit command sequence %d: %w",
			input.SourceSequence,
			err,
		)
	}
	if !result.Created ||
		result.CommandID != input.InputID ||
		result.State != platformpostgres.IdempotencyInProgress ||
		!storedResponseEqual(result.Response, request.Response) {
		return platformpostgres.BeginCommandRequest{}, fmt.Errorf(
			"admitted command sequence %d = %+v",
			input.SourceSequence,
			result,
		)
	}
	return request, nil
}

func requireCommandReplay(
	ctx context.Context,
	pool *pgxpool.Pool,
	request platformpostgres.BeginCommandRequest,
) error {
	journal := platformpostgres.NewCommandJournal(pool)
	replay, err := journal.Begin(ctx, request)
	if err != nil {
		return fmt.Errorf("replay terminal command %s: %w", request.CommandID, err)
	}
	if replay.Created ||
		replay.CommandID != request.CommandID ||
		replay.State != platformpostgres.IdempotencyCompleted ||
		!storedResponseEqual(replay.Response, request.Response) {
		return fmt.Errorf("terminal command %s replay = %+v", request.CommandID, replay)
	}
	conflict := request
	conflict.RequestHash[0] ^= 0xff
	if _, err := journal.Begin(ctx, conflict); !errors.Is(
		err,
		platformpostgres.ErrIdempotencyConflict,
	) {
		return fmt.Errorf(
			"terminal command %s conflict error = %v",
			request.CommandID,
			err,
		)
	}
	return nil
}

func storedResponseEqual(left, right platformpostgres.StoredResponse) bool {
	return left.Status == right.Status &&
		bytes.Equal(left.Headers, right.Headers) &&
		bytes.Equal(left.Body, right.Body)
}

func requireDurableState(
	ctx context.Context,
	pool *pgxpool.Pool,
	state engine.State,
	wantReceipts uint64,
) error {
	var (
		receiptCount    uint64
		invalidReceipts uint64
		duplicateCount  uint64
		nextSequence    uint64
		ready           bool
		checkpointHash  string
		faultCount      uint64
	)
	if err := pool.QueryRow(ctx, `
		SELECT
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
			(SELECT next_stream_sequence
			   FROM engine.shard_checkpoints
			  WHERE shard_id = $1),
			(SELECT ready
			   FROM engine.shard_checkpoints
			  WHERE shard_id = $1),
			(SELECT encode(state_hash, 'hex')
			   FROM engine.shard_checkpoints
			  WHERE shard_id = $1),
			(SELECT count(*)
			   FROM engine.shard_faults
			  WHERE shard_id = $1)`,
		int64(shardID),
	).Scan(
		&receiptCount,
		&invalidReceipts,
		&duplicateCount,
		&nextSequence,
		&ready,
		&checkpointHash,
		&faultCount,
	); err != nil {
		return fmt.Errorf("inspect durable v3 state: %w", err)
	}
	if receiptCount != wantReceipts ||
		invalidReceipts != 0 ||
		duplicateCount != 0 ||
		nextSequence != state.NextStreamSequence() ||
		!ready ||
		checkpointHash != state.Hash().String() ||
		faultCount != 0 {
		return fmt.Errorf(
			"durable v3 state receipts=%d invalid=%d duplicates=%d next=%d ready=%t hash=%s faults=%d",
			receiptCount,
			invalidReceipts,
			duplicateCount,
			nextSequence,
			ready,
			checkpointHash,
			faultCount,
		)
	}
	return nil
}

func requireExpectedState(state engine.State, parsed options) error {
	if !state.Ready() ||
		state.Hash().String() != parsed.expectedStateHash ||
		state.NextStreamSequence() != parsed.expectedNextStreamSequence {
		return fmt.Errorf(
			"recovered state ready=%t hash=%s next=%d, want true/%s/%d",
			state.Ready(),
			state.Hash(),
			state.NextStreamSequence(),
			parsed.expectedStateHash,
			parsed.expectedNextStreamSequence,
		)
	}
	return nil
}

func resultForState(state engine.State) protocolResult {
	return protocolResult{
		StateHash:          state.Hash().String(),
		NextStreamSequence: state.NextStreamSequence(),
	}
}

func v3UpgradeBoundaryClean(report platformpostgres.ReconciliationReport) bool {
	return report.Ready &&
		report.ReceiptCount == 11 &&
		report.DuplicateDeliveryCount == 0 &&
		report.NextStreamSequence == 12 &&
		report.DeliveryMismatchCount == 0 &&
		report.LedgerMismatchCount == 0 &&
		report.UnbalancedGroupCount == 0 &&
		report.OrderFillMismatchCount == 0 &&
		report.PositionMismatchCount == 0 &&
		report.CommandMismatchCount == 0 &&
		report.ProtectionMismatchCount == 0 &&
		report.FundingMismatchCount == 0 &&
		report.ConfigurationMismatchCount == 0 &&
		report.MarketMismatchCount == 0 &&
		report.MessagingMismatchCount == 0 &&
		report.RealtimeMismatchCount == 0 &&
		report.RealtimeQuarantinedCount == 0 &&
		report.PendingOutboxMessages == 6
}

func verifyExactTip(
	ctx context.Context,
	pool *pgxpool.Pool,
	migrator *platformpostgres.Migrator,
) error {
	if err := migrator.VerifyCurrent(ctx); err != nil {
		return fmt.Errorf("verify exact v3 schema: %w", err)
	}
	var tip string
	var leverageColumnExists bool
	if err := pool.QueryRow(ctx, `
		SELECT
			max(filename),
			EXISTS (
				SELECT 1
				  FROM pg_attribute
				 WHERE attrelid = 'trading.fills'::regclass
				   AND attname = 'effective_leverage'
				   AND NOT attisdropped
			)
		  FROM engine.schema_migrations`,
	).Scan(&tip, &leverageColumnExists); err != nil {
		return fmt.Errorf("inspect v3 schema tip: %w", err)
	}
	if tip != migrationTip || leverageColumnExists {
		return fmt.Errorf(
			"v3 schema tip=%q effective_leverage=%t",
			tip,
			leverageColumnExists,
		)
	}
	return nil
}

func migrationsThroughTip() (fstest.MapFS, error) {
	entries, err := os.ReadDir("migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}
	files := fstest.MapFS{}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() ||
			filepath.Ext(name) != ".sql" ||
			name > migrationTip {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("migrations", name))
		if err != nil {
			return nil, fmt.Errorf("read migration %s: %w", name, err)
		}
		files[name] = &fstest.MapFile{Data: raw}
	}
	if _, found := files[migrationTip]; !found {
		return nil, fmt.Errorf("pinned migration %s is missing", migrationTip)
	}
	return files, nil
}

func ensureRuntimeRoles(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `
		DO $$
		DECLARE required_role text;
		BEGIN
			FOREACH required_role IN ARRAY ARRAY[
				'platformgo_api',
				'platformgo_engine',
				'platformgo_outbox',
				'platformgo_projector',
				'platformgo_realtime',
				'platformgo_realtime_repair'
			]
			LOOP
				IF NOT EXISTS (
					SELECT 1 FROM pg_roles WHERE rolname = required_role
				) THEN
					EXECUTE format(
						'CREATE ROLE %I NOLOGIN NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS',
						required_role
					);
				END IF;
			END LOOP;
		END
		$$`); err != nil {
		return fmt.Errorf("ensure runtime roles: %w", err)
	}
	return nil
}
