package postgres

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/upcomers-org/platformgo/internal/application"
	"github.com/upcomers-org/platformgo/internal/edge"
	"github.com/upcomers-org/platformgo/internal/engine"
)

// CompatibilityStore owns Phase 3 identity and query projections.
type CompatibilityStore struct {
	pool *pgxpool.Pool
}

// NewCompatibilityStore binds compatibility persistence to PostgreSQL.
func NewCompatibilityStore(pool *pgxpool.Pool) *CompatibilityStore {
	return &CompatibilityStore{pool: pool}
}

// UserByLogin loads one identity by its case-insensitive login.
func (store *CompatibilityStore) UserByLogin(
	ctx context.Context,
	login string,
) (application.IdentityRecord, error) {
	return store.loadIdentity(
		ctx,
		`SELECT user_id, login, COALESCE(email, ''), COALESCE(password_hash, '')
		   FROM identity.users
		  WHERE broker_subject IS NULL
		    AND normalized_login = $1`,
		strings.ToLower(strings.TrimSpace(login)),
	)
}

// BrokerUserByID loads one identity only inside the authenticated tenant.
func (store *CompatibilityStore) BrokerUserByID(
	ctx context.Context,
	brokerTenant string,
	userID string,
) (application.IdentityRecord, error) {
	return store.loadIdentity(
		ctx,
		`SELECT user_id, login, COALESCE(email, ''), COALESCE(password_hash, '')
		   FROM identity.users
		  WHERE broker_subject = $1
		    AND user_id = $2`,
		brokerTenant,
		userID,
	)
}

// UserByID loads one identity by its stable URN.
func (store *CompatibilityStore) UserByID(
	ctx context.Context,
	userID string,
) (application.IdentityRecord, error) {
	return store.loadIdentity(
		ctx,
		`SELECT user_id, login, COALESCE(email, ''), COALESCE(password_hash, '')
		   FROM identity.users
		  WHERE user_id = $1`,
		userID,
	)
}

func (store *CompatibilityStore) loadIdentity(
	ctx context.Context,
	query string,
	values ...string,
) (application.IdentityRecord, error) {
	if store == nil || store.pool == nil {
		return application.IdentityRecord{}, errors.New("identity store: PostgreSQL pool is required")
	}
	var record application.IdentityRecord
	arguments := make([]any, len(values))
	for index := range values {
		arguments[index] = values[index]
	}
	if err := store.pool.QueryRow(ctx, query, arguments...).Scan(
		&record.UserID,
		&record.Login,
		&record.Email,
		&record.PasswordHash,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return application.IdentityRecord{}, application.ErrIdentityNotFound
		}
		return application.IdentityRecord{}, fmt.Errorf("load identity: %w", err)
	}
	return record, nil
}

// BrokerUserAccounts returns only account claims owned by the same tenant.
func (store *CompatibilityStore) BrokerUserAccounts(
	ctx context.Context,
	brokerTenant string,
	userID string,
) ([]string, error) {
	return store.userAccounts(ctx, userID, &brokerTenant)
}

// UserAccounts returns stable account authorization claims in lexical order.
func (store *CompatibilityStore) UserAccounts(
	ctx context.Context,
	userID string,
) ([]string, error) {
	return store.userAccounts(ctx, userID, nil)
}

// AccountsByUser loads complete account summaries for one durable owner.
func (store *CompatibilityStore) AccountsByUser(
	ctx context.Context,
	userID string,
) ([]application.AccountRecord, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("identity store: PostgreSQL pool is required")
	}
	rows, err := store.pool.Query(ctx, `
		SELECT
			ownership.account_id,
			profile.login,
			ownership.user_id,
			profile.base_currency,
			account.margin_mode,
			account.oms_mode,
			profile.market_venue,
			profile.permitted_classes,
			account.status,
			profile.created_at
		  FROM identity.user_accounts AS ownership
		  LEFT JOIN trading.accounts AS account
		    ON account.account_id = ownership.account_id
		  LEFT JOIN identity.account_profiles AS profile
		    ON profile.account_id = ownership.account_id
		 WHERE ownership.user_id = $1
		 ORDER BY profile.login, ownership.account_id`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list account summaries: %w", err)
	}
	defer rows.Close()
	accounts := make([]application.AccountRecord, 0)
	for rows.Next() {
		var (
			record           application.AccountRecord
			login            *int64
			baseCurrency     *string
			marginMode       *string
			omsMode          *string
			marketVenue      *string
			permittedClasses []string
			status           *string
			createdAt        *time.Time
		)
		if err := rows.Scan(
			&record.AccountID,
			&login,
			&record.UserID,
			&baseCurrency,
			&marginMode,
			&omsMode,
			&marketVenue,
			&permittedClasses,
			&status,
			&createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan account summary: %w", err)
		}
		if login == nil ||
			baseCurrency == nil ||
			marginMode == nil ||
			omsMode == nil ||
			marketVenue == nil ||
			permittedClasses == nil ||
			status == nil ||
			createdAt == nil {
			return nil, fmt.Errorf(
				"account summary %q is incomplete",
				record.AccountID,
			)
		}
		record.Login = *login
		record.BaseCurrency = *baseCurrency
		record.MarginMode = *marginMode
		record.OmsMode = *omsMode
		record.MarketVenue = *marketVenue
		record.PermittedClasses = append(
			[]string(nil),
			permittedClasses...,
		)
		record.Status = *status
		record.CreatedAt = *createdAt
		accounts = append(accounts, record)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list account summaries: %w", err)
	}
	return accounts, nil
}

func (store *CompatibilityStore) userAccounts(
	ctx context.Context,
	userID string,
	brokerTenant *string,
) ([]string, error) {
	var tenant any
	if brokerTenant != nil {
		tenant = *brokerTenant
	}
	rows, err := store.pool.Query(ctx, `
		SELECT account_id
		  FROM identity.user_accounts
		 WHERE user_id = $1
		   AND (
				$2::text IS NULL
				OR broker_subject = $2
		   )
		 ORDER BY account_id`,
		userID,
		tenant,
	)
	if err != nil {
		return nil, fmt.Errorf("list identity accounts: %w", err)
	}
	defer rows.Close()
	accounts := make([]string, 0)
	for rows.Next() {
		var accountID string
		if err := rows.Scan(&accountID); err != nil {
			return nil, fmt.Errorf("scan identity account: %w", err)
		}
		accounts = append(accounts, accountID)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list identity accounts: %w", err)
	}
	return accounts, nil
}

// CreateBrokerUser converges case-insensitively on normalized email.
func (store *CompatibilityStore) CreateBrokerUser(
	ctx context.Context,
	brokerTenant string,
	userID string,
	login string,
	email string,
) (application.IdentityRecord, bool, error) {
	var record application.IdentityRecord
	var created bool
	if err := store.pool.QueryRow(ctx, `
		SELECT user_id, login, email, created
		  FROM identity.create_broker_user($1,$2,$3,$4)`,
		brokerTenant,
		userID,
		login,
		email,
	).Scan(
		&record.UserID,
		&record.Login,
		&record.Email,
		&created,
	); err != nil {
		return application.IdentityRecord{}, false, fmt.Errorf("create broker user: %w", err)
	}
	return record, created, nil
}

// CreateSession persists only the hash of an opaque refresh credential.
func (store *CompatibilityStore) CreateSession(
	ctx context.Context,
	sessionID engine.ID,
	userID string,
	refreshHash [sha256.Size]byte,
	expiresAt time.Time,
) error {
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO identity.sessions (
			session_id, user_id, refresh_hash, expires_at
		) VALUES ($1,$2,$3,$4)`,
		sessionID.String(),
		userID,
		refreshHash[:],
		expiresAt,
	); err != nil {
		return fmt.Errorf("create identity session: %w", err)
	}
	return nil
}

// ClaimClientRateLimit uses shared PostgreSQL authority across API replicas.
func (store *CompatibilityStore) ClaimClientRateLimit(
	ctx context.Context,
	principal string,
) (application.ClientRateLimitResult, error) {
	var result application.ClientRateLimitResult
	var retryAfter string
	if err := store.pool.QueryRow(ctx, `
		SELECT allowed, retry_after_seconds::text
		  FROM identity.claim_client_rate_limit($1)`,
		principal,
	).Scan(&result.Allowed, &retryAfter); err != nil {
		return application.ClientRateLimitResult{},
			fmt.Errorf("claim client rate limit: %w", err)
	}
	parsedRetryAfter, err := strconv.ParseUint(retryAfter, 10, 64)
	if err != nil {
		return application.ClientRateLimitResult{},
			fmt.Errorf("claim client rate limit: retry after: %w", err)
	}
	result.RetryAfter = parsedRetryAfter
	return result, nil
}

// ReplayUserAPIKey resolves a committed response before new entropy or rate
// capacity is consumed.
func (store *CompatibilityStore) ReplayUserAPIKey(
	ctx context.Context,
	principal string,
	idempotencyHash [sha256.Size]byte,
	requestHash [sha256.Size]byte,
) (application.UserAPIKeyReplayResult, error) {
	var result application.UserAPIKeyReplayResult
	if err := store.pool.QueryRow(ctx, `
		SELECT
			found,
			response_status,
			replay_key_id,
			response_nonce,
			response_ciphertext
		  FROM identity.replay_user_api_key($1,$2,$3)`,
		principal,
		idempotencyHash[:],
		requestHash[:],
	).Scan(
		&result.Found,
		&result.ResponseStatus,
		&result.ReplayKeyID,
		&result.ReplayNonce,
		&result.ReplayCiphertext,
	); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.Code == "P0003" {
			return application.UserAPIKeyReplayResult{},
				edge.ErrIdempotencyConflict
		}
		return application.UserAPIKeyReplayResult{},
			fmt.Errorf("replay user API key: %w", err)
	}
	return result, nil
}

// VerifyAPIKeyPolicy fails closed when source deployment overrides have not
// been reconciled into the PostgreSQL-authoritative policy.
func (store *CompatibilityStore) VerifyAPIKeyPolicy(
	ctx context.Context,
	maxActive *int64,
	rateMax *uint64,
	rateWindow *uint64,
	idempotencyTTL *uint64,
) error {
	var matches bool
	if err := store.pool.QueryRow(ctx, `
		SELECT identity.verify_api_key_policy($1,$2,$3,$4)`,
		nullableInt64(maxActive),
		nullableUint64Decimal(rateMax),
		nullableUint64Decimal(rateWindow),
		nullableUint64Decimal(idempotencyTTL),
	).Scan(&matches); err != nil {
		return fmt.Errorf("verify API-key policy: %w", err)
	}
	if !matches {
		return errors.New(
			"API-key policy differs from configured source deployment values",
		)
	}
	return nil
}

// PurgeExpiredAPIKeyReplays deletes one bounded database-time batch.
func (store *CompatibilityStore) PurgeExpiredAPIKeyReplays(
	ctx context.Context,
	batchLimit int,
) (int64, error) {
	var deleted int64
	if err := store.pool.QueryRow(ctx, `
		SELECT identity.purge_expired_api_key_replays($1)`,
		batchLimit,
	).Scan(&deleted); err != nil {
		return 0, fmt.Errorf("purge expired API-key replays: %w", err)
	}
	return deleted, nil
}

// APIKeyReplayCoverage reports the live encrypted replay backlog by key ID.
type APIKeyReplayCoverage struct {
	KeyID           string
	LiveCount       int64
	OldestExpiresAt string
}

// APIKeyReplayCoverage loads the least-privilege readiness and rotation view.
func (store *CompatibilityStore) APIKeyReplayCoverage(
	ctx context.Context,
) ([]APIKeyReplayCoverage, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT replay_key_id, live_count, oldest_expires_at
		  FROM identity.api_key_replay_coverage()`)
	if err != nil {
		return nil, fmt.Errorf("load API-key replay coverage: %w", err)
	}
	defer rows.Close()
	coverage := make([]APIKeyReplayCoverage, 0)
	for rows.Next() {
		var item APIKeyReplayCoverage
		if scanErr := rows.Scan(
			&item.KeyID,
			&item.LiveCount,
			&item.OldestExpiresAt,
		); scanErr != nil {
			return nil, fmt.Errorf(
				"load API-key replay coverage: %w",
				scanErr,
			)
		}
		coverage = append(coverage, item)
	}
	if rowsErr := rows.Err(); rowsErr != nil {
		return nil, fmt.Errorf("load API-key replay coverage: %w", rowsErr)
	}
	return coverage, nil
}

// CreateUserAPIKey atomically claims optional idempotency, enforces the
// PostgreSQL-authoritative owner cap, stores only the secret hash, and appends
// the corresponding audit fact.
func (store *CompatibilityStore) CreateUserAPIKey(
	ctx context.Context,
	creation application.UserAPIKeyCreation,
) (application.UserAPIKeyCreationResult, error) {
	var result application.UserAPIKeyCreationResult
	var retryAfter string
	if err := store.pool.QueryRow(ctx, `
		SELECT
			outcome,
			response_status,
			retry_after_seconds::text,
			replay_key_id,
			response_nonce,
			response_ciphertext
		  FROM identity.create_user_api_key(
			$1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13
		)`,
		creation.OwnerUserID,
		creation.APIKeyID.String(),
		creation.Name,
		creation.KeyHash[:],
		creation.Prefix,
		creation.Scopes,
		creation.AuditEventID.String(),
		creation.RequestID,
		creation.IdempotencyHash[:],
		creation.RequestHash[:],
		creation.ReplayKeyID,
		creation.ReplayNonce,
		creation.ReplayCiphertext,
	).Scan(
		&result.Outcome,
		&result.ResponseStatus,
		&retryAfter,
		&result.ReplayKeyID,
		&result.ReplayNonce,
		&result.ReplayCiphertext,
	); err != nil {
		var postgresError *pgconn.PgError
		switch {
		case errors.As(err, &postgresError) &&
			postgresError.Code == "P0002":
			return application.UserAPIKeyCreationResult{},
				application.ErrIdentityNotFound
		default:
			return application.UserAPIKeyCreationResult{},
				fmt.Errorf("create user API key: %w", err)
		}
	}
	parsedRetryAfter, err := strconv.ParseUint(retryAfter, 10, 64)
	if err != nil {
		return application.UserAPIKeyCreationResult{},
			fmt.Errorf("create user API key: retry after: %w", err)
	}
	result.RetryAfter = parsedRetryAfter
	return result, nil
}

func nullableInt64(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullableUint64Decimal(value *uint64) any {
	if value == nil {
		return nil
	}
	return strconv.FormatUint(*value, 10)
}

// BrokerEcho atomically stores or replays the exact principal-scoped response.
func (store *CompatibilityStore) BrokerEcho(
	ctx context.Context,
	principal string,
	idempotencyKey string,
	requestHash [sha256.Size]byte,
	resultID string,
	expiresAt time.Time,
) (string, error) {
	var stored string
	if err := store.pool.QueryRow(ctx, `
		SELECT identity.claim_broker_echo($1,$2,$3,$4,$5)`,
		principal,
		idempotencyKey,
		requestHash[:],
		resultID,
		expiresAt,
	).Scan(&stored); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.Code == "23505" {
			return "", edge.ErrIdempotencyConflict
		}
		return "", fmt.Errorf("broker echo: %w", err)
	}
	return stored, nil
}

// ReplayBrokerAccount resolves an existing durable mutation without requiring
// the command workers to be currently ready.
func (store *CompatibilityStore) ReplayBrokerAccount(
	ctx context.Context,
	principal string,
	brokerTenant string,
	idempotencyKey string,
	requestHash [sha256.Size]byte,
) (edge.BrokerAccountAdmission, bool, error) {
	replay, found, err := NewCommandJournal(store.pool).Replay(
		ctx,
		"broker-account\x1f"+principal,
		idempotencyKey,
		requestHash,
	)
	if err != nil || !found {
		return edge.BrokerAccountAdmission{}, found, err
	}
	admission, err := store.waitBrokerAccountCompletion(
		ctx,
		brokerTenant,
		replay,
	)
	return admission, true, err
}

// CreateBrokerAccount atomically admits an ordered configure-account command
// with the exact response and tenant-scoped identity provisioning intent.
func (store *CompatibilityStore) CreateBrokerAccount(
	ctx context.Context,
	principal string,
	brokerTenant string,
	idempotencyKey string,
	requestHash [sha256.Size]byte,
	result edge.BrokerAccountResult,
	expiresAt time.Time,
	requireRuntimeReady bool,
) (edge.BrokerAccountAdmission, error) {
	body, err := json.Marshal(result)
	if err != nil {
		return edge.BrokerAccountAdmission{}, fmt.Errorf(
			"create broker account: encode response: %w",
			err,
		)
	}
	body = append(body, '\n')
	accountUUID, err := engine.ParseID(
		strings.TrimPrefix(result.ID, "urn:xb:account:"),
	)
	if err != nil {
		return edge.BrokerAccountAdmission{}, fmt.Errorf(
			"create broker account: parse account ID: %w",
			err,
		)
	}
	createdAt, err := time.Parse(time.RFC3339Nano, result.CreatedAt)
	if err != nil {
		return edge.BrokerAccountAdmission{}, fmt.Errorf(
			"create broker account: parse createdAt: %w",
			err,
		)
	}
	journal := NewCommandJournal(store.pool)
	configurationVersion, err := journal.ConfigurationVersion(ctx)
	if err != nil {
		return edge.BrokerAccountAdmission{}, err
	}
	sequence, err := journal.NextAccountSequence(ctx, result.ID)
	if err != nil {
		return edge.BrokerAccountAdmission{}, err
	}
	action := engine.TradingAction{
		Kind: engine.TradingActionConfigureAccount,
		ConfigureAccount: &engine.ConfigureAccount{
			AccountID: result.ID,
			OmsMode:   engine.OmsModeNetting,
		},
	}
	payload, err := engine.EncodeTradingAction(action)
	if err != nil {
		return edge.BrokerAccountAdmission{}, fmt.Errorf(
			"create broker account: encode action: %w",
			err,
		)
	}
	commandID := engine.IDFromSequence(accountUUID, 1)
	var shardID uint32
	if queryErr := store.pool.QueryRow(ctx, `
		SELECT shard_id
		  FROM engine.deployment_shard
		 WHERE singleton`).Scan(&shardID); queryErr != nil {
		return edge.BrokerAccountAdmission{}, fmt.Errorf(
			"create broker account: read deployment shard: %w",
			queryErr,
		)
	}
	input := engine.InputEnvelope{
		InputID:              commandID,
		SchemaVersion:        engine.CurrentSchemaVersion,
		ShardID:              engine.ShardID(shardID),
		Kind:                 engine.InputKindCommand,
		SourceID:             principal,
		SourceSequence:       sequence,
		LogicalTime:          engine.NewLogicalTime(createdAt),
		ConfigurationVersion: configurationVersion,
		InstrumentVersion:    1,
		Payload:              payload,
	}
	outboxPayload, err := engine.EncodeInputMessage(input)
	if err != nil {
		return edge.BrokerAccountAdmission{}, fmt.Errorf(
			"create broker account: encode command: %w",
			err,
		)
	}
	begin, err := journal.Begin(ctx, application.BeginCommandRequest{
		Scope:            "broker-account\x1f" + principal,
		IdempotencyKey:   idempotencyKey,
		RequestHash:      requestHash,
		CommandID:        commandID,
		AccountID:        result.ID,
		AccountSequence:  sequence,
		CommandType:      string(engine.TradingActionConfigureAccount),
		SchemaVersion:    engine.CurrentSchemaVersion,
		CanonicalPayload: payload.Bytes(),
		OutboxSubject: fmt.Sprintf(
			"engine.input.%d.command.v%d",
			input.ShardID,
			engine.CurrentSchemaVersion,
		),
		OutboxPayload: outboxPayload,
		LogicalTime:   createdAt,
		ExpiresAt:     expiresAt,
		Response: application.StoredResponse{
			Status:  201,
			Headers: []byte(`{"content-type":["application/json"]}`),
			Body:    body,
		},
		AccountProvisioning: &application.AccountProvisioningIntent{
			BrokerTenant: brokerTenant,
			UserID:       result.UserID,
			Login:        result.Login,
			BaseCurrency: result.BaseCurrency,
			MarketVenue:  result.MarketVenue,
			PermittedClasses: append(
				[]string(nil),
				result.PermittedClasses...,
			),
			CreatedAt: createdAt,
		},
		RequireRuntimeReady: requireRuntimeReady,
	})
	if errors.Is(err, application.ErrIdempotencyConflict) {
		return edge.BrokerAccountAdmission{}, edge.ErrIdempotencyConflict
	}
	if err != nil {
		return edge.BrokerAccountAdmission{}, err
	}
	return store.waitBrokerAccountCompletion(ctx, brokerTenant, begin)
}

func (store *CompatibilityStore) waitBrokerAccountCompletion(
	ctx context.Context,
	brokerTenant string,
	replay application.BeginCommandResult,
) (edge.BrokerAccountAdmission, error) {
	response := replay.Response
	var account edge.BrokerAccountResult
	if response.Status != 201 ||
		json.Unmarshal(response.Body, &account) != nil ||
		account.ID == "" {
		return edge.BrokerAccountAdmission{}, errors.New(
			"create broker account: invalid stored response",
		)
	}
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		var commandStatus string
		var idempotencyState string
		var graphComplete bool
		err := store.pool.QueryRow(ctx, `
			SELECT
				command.status,
				idempotency.state,
				EXISTS (
					SELECT 1
					  FROM trading.accounts AS trading_account
					  JOIN identity.user_accounts AS ownership
					    ON ownership.account_id = trading_account.account_id
					   AND ownership.user_id = $3
					   AND ownership.broker_subject = $2
					  JOIN identity.account_profiles AS profile
					    ON profile.account_id = trading_account.account_id
					 WHERE trading_account.account_id = $4
				)
			  FROM trading.commands AS command
			  JOIN trading.idempotency_records AS idempotency
			    ON idempotency.command_id = command.command_id
			 WHERE command.command_id = $1`,
			replay.CommandID.String(),
			brokerTenant,
			account.UserID,
			account.ID,
		).Scan(&commandStatus, &idempotencyState, &graphComplete)
		if err != nil {
			return edge.BrokerAccountAdmission{}, fmt.Errorf(
				"create broker account: inspect engine completion: %w",
				err,
			)
		}
		switch {
		case commandStatus == "accepted" &&
			idempotencyState == "completed" &&
			graphComplete:
			return edge.BrokerAccountAdmission{
				BrokerAccountResult: account,
				Response: edge.StoredResponse{
					Status:  response.Status,
					Headers: append([]byte(nil), response.Headers...),
					Body:    append([]byte(nil), response.Body...),
				},
			}, nil
		case commandStatus != "pending":
			return edge.BrokerAccountAdmission{}, fmt.Errorf(
				"create broker account: engine completed with status %q",
				commandStatus,
			)
		}
		select {
		case <-ctx.Done():
			return edge.BrokerAccountAdmission{}, fmt.Errorf(
				"create broker account: wait for engine completion: %w",
				ctx.Err(),
			)
		case <-ticker.C:
		}
	}
}

// Instruments returns the public exact catalog projection.
func (store *CompatibilityStore) Instruments(
	ctx context.Context,
) ([]edge.InstrumentView, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT
			instrument_id,
			settlement_currency,
			trim_scale(1 / power(10::numeric, price_scale))::text,
			trim_scale(1 / power(10::numeric, quantity_scale))::text,
			trim_scale(max_leverage)::text,
			trim_scale(maker_fee_rate)::text,
			trim_scale(taker_fee_rate)::text
		  FROM trading.instruments
		 ORDER BY instrument_id`)
	if err != nil {
		return nil, fmt.Errorf("list instruments: %w", err)
	}
	defer rows.Close()
	values := make([]edge.InstrumentView, 0)
	for rows.Next() {
		var value edge.InstrumentView
		if err := rows.Scan(
			&value.Symbol,
			&value.SettlementAsset,
			&value.PriceIncrement,
			&value.SizeIncrement,
			&value.MaxLeverage,
			&value.MakerFee,
			&value.TakerFee,
		); err != nil {
			return nil, fmt.Errorf("scan instrument: %w", err)
		}
		value.DisplayName = value.Symbol
		value.Enabled = true
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list instruments: %w", err)
	}
	return values, nil
}

// Orders returns the account's durable order projection.
func (store *CompatibilityStore) Orders(
	ctx context.Context,
	accountID string,
) ([]edge.OrderView, error) {
	rows, queryErr := store.pool.Query(ctx, `
		SELECT
			orders.order_id::text,
			COALESCE(intent.intent_id, ''),
			instrument_id,
			side,
			order_type,
			trim_scale(quantity)::text,
			status,
			trim_scale(filled_quantity)::text,
			trim_scale(limit_price)::text,
			trim_scale(trigger_price)::text,
			time_in_force,
			reduce_only
		  FROM trading.orders AS orders
		  LEFT JOIN trading.order_intents AS intent
		    ON intent.order_id = orders.order_id
		 WHERE orders.account_id = $1
		UNION ALL
		SELECT
			intent.order_id::text,
			intent.intent_id,
			command.canonical_payload->'submitOrder'->>'instrumentId',
			command.canonical_payload->'submitOrder'->>'side',
			command.canonical_payload->'submitOrder'->>'type',
			command.canonical_payload->'submitOrder'->>'quantity',
			'pending',
			'0',
			command.canonical_payload->'submitOrder'->>'price',
			command.canonical_payload->'submitOrder'->>'triggerPrice',
			command.canonical_payload->'submitOrder'->>'timeInForce',
			COALESCE(
				(command.canonical_payload->'submitOrder'->>'reduceOnly')::boolean,
				false
			)
		  FROM trading.order_intents AS intent
		  JOIN trading.commands AS command
		    ON command.command_id = intent.command_id
		  LEFT JOIN trading.orders AS orders
		    ON orders.order_id = intent.order_id
		 WHERE intent.account_id = $1
		   AND command.status = 'pending'
		   AND orders.order_id IS NULL
		 ORDER BY 1`,
		accountID,
	)
	if queryErr != nil {
		return nil, fmt.Errorf("list orders: %w", queryErr)
	}
	defer rows.Close()
	values := make([]edge.OrderView, 0)
	for rows.Next() {
		var value edge.OrderView
		if err := rows.Scan(
			&value.OrderID,
			&value.IntentID,
			&value.Symbol,
			&value.Side,
			&value.Type,
			&value.Quantity,
			&value.Status,
			&value.FilledQuantity,
			&value.LimitPrice,
			&value.TriggerPrice,
			&value.TimeInForce,
			&value.ReduceOnly,
		); err != nil {
			return nil, fmt.Errorf("scan order: %w", err)
		}
		value.OrderID = "urn:xb:order:" + value.OrderID
		value.AccountID = accountID
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list orders: %w", err)
	}
	return values, nil
}

// Positions returns the account's durable position projection.
func (store *CompatibilityStore) Positions(
	ctx context.Context,
	accountID string,
) ([]edge.PositionView, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT
			position_id::text,
			instrument_id,
			side,
			trim_scale(abs(signed_quantity))::text,
			status
		  FROM trading.positions
		 WHERE account_id = $1
		 ORDER BY position_id`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list positions: %w", err)
	}
	defer rows.Close()
	values := make([]edge.PositionView, 0)
	for rows.Next() {
		var value edge.PositionView
		if err := rows.Scan(
			&value.PositionID,
			&value.Symbol,
			&value.Side,
			&value.Quantity,
			&value.Status,
		); err != nil {
			return nil, fmt.Errorf("scan position: %w", err)
		}
		value.AccountID = accountID
		values = append(values, value)
	}
	return values, rows.Err()
}

// Balances returns exact authoritative ledger balance projections.
func (store *CompatibilityStore) Balances(
	ctx context.Context,
	accountID string,
) ([]edge.BalanceView, error) {
	rows, err := store.pool.Query(ctx, `
		SELECT
			currency,
			trim_scale(total)::text,
			trim_scale(used)::text,
			trim_scale(free)::text,
			trim_scale(equity)::text
		  FROM ledger.balances
		 WHERE account_id = $1
		 ORDER BY currency`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list balances: %w", err)
	}
	defer rows.Close()
	values := make([]edge.BalanceView, 0)
	for rows.Next() {
		var value edge.BalanceView
		if err := rows.Scan(
			&value.Currency,
			&value.Total,
			&value.Locked,
			&value.Free,
			&value.Equity,
		); err != nil {
			return nil, fmt.Errorf("scan balance: %w", err)
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// LatestFillExecution returns the newest immutable execution-time projection
// proven by the first native fill-history source port.
func (store *CompatibilityStore) LatestFillExecution(
	ctx context.Context,
	accountID string,
) (edge.FillExecutionView, error) {
	var (
		view        edge.FillExecutionView
		logicalTime int64
	)
	if err := store.pool.QueryRow(ctx, `
		SELECT
			fill.fill_id::text,
			fill.order_id::text,
			fill.side,
			fill.position_effect,
			fill.logical_time
		  FROM trading.fills AS fill
		 WHERE fill.account_id = $1
		 ORDER BY fill.logical_time DESC, fill.fill_id DESC
		 LIMIT 1`,
		accountID,
	).Scan(
		&view.FillID,
		&view.OrderID,
		&view.Side,
		&view.TradeType,
		&logicalTime,
	); err != nil {
		return edge.FillExecutionView{}, fmt.Errorf("read latest fill execution: %w", err)
	}
	if err := validateFillTradeType(view.TradeType); err != nil {
		return edge.FillExecutionView{}, fmt.Errorf(
			"read latest fill execution: %w",
			err,
		)
	}
	view.OrderID = "urn:xb:order:" + view.OrderID
	view.FilledAt = time.Unix(0, logicalTime).UTC().Format(time.RFC3339Nano)
	return view, nil
}

// FillExecutionFilter is the source-proven subset of fill-history filters.
// TradeID names the external contract field that filters the durable fill ID.
type FillExecutionFilter struct {
	Side      string
	TradeID   string
	Limit     int
	Cursor    string
	Direction string
}

type fillHistoryCursor struct {
	logicalTime int64
	fillID      string
}

type fillHistoryRow struct {
	view        edge.FillExecutionView
	logicalTime int64
}

// FilterFillExecutions returns one newest-first immutable execution page
// matching the source-proven filters. It remains internal until the complete
// HTTP fills contract is implemented.
func (store *CompatibilityStore) FilterFillExecutions(
	ctx context.Context,
	accountID string,
	filter FillExecutionFilter,
) (edge.FillExecutionPage, error) {
	side := strings.ToUpper(strings.TrimSpace(filter.Side))
	if side != "" && side != "BUY" && side != "SELL" {
		return edge.FillExecutionPage{}, edge.ErrInvalidRequest
	}
	tradeID := strings.TrimSpace(filter.TradeID)
	if tradeID != "" {
		if _, err := engine.ParseID(tradeID); err != nil {
			return edge.FillExecutionPage{}, edge.ErrInvalidRequest
		}
	}
	limit := filter.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	cursor, err := decodeFillHistoryCursor(filter.Cursor)
	if err != nil {
		return edge.FillExecutionPage{}, err
	}
	forward := cursor == nil ||
		(filter.Direction != "prev" && filter.Direction != "backward")
	var requestedSide any
	if side != "" {
		requestedSide = side
	}
	var requestedTradeID any
	if tradeID != "" {
		requestedTradeID = tradeID
	}
	query := `
		WITH page AS (
			SELECT
				fill.fill_id,
				fill.order_id,
				fill.side,
				fill.position_effect,
				fill.logical_time
			  FROM trading.fills AS fill
			 WHERE fill.account_id = $1
			   AND ($2::text IS NULL OR fill.side = $2)
			   AND ($3::uuid IS NULL OR fill.fill_id = $3)
			 ORDER BY fill.logical_time DESC, fill.fill_id DESC
			 LIMIT $4
		),
		filtered_total AS (
			SELECT count(*) AS total
			  FROM trading.fills AS counted
			 WHERE counted.account_id = $1
			   AND ($2::text IS NULL OR counted.side = $2)
			   AND ($3::uuid IS NULL OR counted.fill_id = $3)
		)
		SELECT
			page.fill_id::text,
			page.order_id::text,
			page.side,
			page.position_effect,
			page.logical_time,
			filtered_total.total
		  FROM filtered_total
		  LEFT JOIN page ON true
		 ORDER BY page.logical_time DESC NULLS LAST,
		          page.fill_id DESC NULLS LAST`
	args := []any{
		accountID,
		requestedSide,
		requestedTradeID,
		limit + 1,
	}
	if cursor != nil {
		query = `
			WITH page AS (
				SELECT
					fill.fill_id,
					fill.order_id,
					fill.side,
					fill.position_effect,
					fill.logical_time
				  FROM trading.fills AS fill
				 WHERE fill.account_id = $1
				   AND ($2::text IS NULL OR fill.side = $2)
				   AND ($3::uuid IS NULL OR fill.fill_id = $3)
				   AND (fill.logical_time, fill.fill_id) < ($4, $5)
				 ORDER BY fill.logical_time DESC, fill.fill_id DESC
				 LIMIT $6
			),
			filtered_total AS (
				SELECT count(*) AS total
				  FROM trading.fills AS counted
				 WHERE counted.account_id = $1
				   AND ($2::text IS NULL OR counted.side = $2)
				   AND ($3::uuid IS NULL OR counted.fill_id = $3)
			)
			SELECT
				page.fill_id::text,
				page.order_id::text,
				page.side,
				page.position_effect,
				page.logical_time,
				filtered_total.total
			  FROM filtered_total
			  LEFT JOIN page ON true
			 ORDER BY page.logical_time DESC NULLS LAST,
			          page.fill_id DESC NULLS LAST`
		if !forward {
			query = `
				WITH page AS (
					SELECT
						fill.fill_id,
						fill.order_id,
						fill.side,
						fill.position_effect,
						fill.logical_time
					  FROM trading.fills AS fill
					 WHERE fill.account_id = $1
					   AND ($2::text IS NULL OR fill.side = $2)
					   AND ($3::uuid IS NULL OR fill.fill_id = $3)
					   AND (fill.logical_time, fill.fill_id) > ($4, $5)
					 ORDER BY fill.logical_time ASC, fill.fill_id ASC
					 LIMIT $6
				),
				filtered_total AS (
					SELECT count(*) AS total
					  FROM trading.fills AS counted
					 WHERE counted.account_id = $1
					   AND ($2::text IS NULL OR counted.side = $2)
					   AND ($3::uuid IS NULL OR counted.fill_id = $3)
				)
				SELECT
					page.fill_id::text,
					page.order_id::text,
					page.side,
					page.position_effect,
					page.logical_time,
					filtered_total.total
				  FROM filtered_total
				  LEFT JOIN page ON true
				 ORDER BY page.logical_time ASC NULLS LAST,
				          page.fill_id ASC NULLS LAST`
		}
		args = []any{
			accountID,
			requestedSide,
			requestedTradeID,
			cursor.logicalTime,
			cursor.fillID,
			limit + 1,
		}
	}
	rows, err := store.pool.Query(ctx, query, args...)
	if err != nil {
		return edge.FillExecutionPage{}, fmt.Errorf("filter fill executions: %w", err)
	}
	defer rows.Close()
	history := make([]fillHistoryRow, 0, limit+1)
	var total int64
	for rows.Next() {
		var row fillHistoryRow
		var (
			fillID         *string
			orderID        *string
			side           *string
			positionEffect *string
			logicalTime    *int64
		)
		if err := rows.Scan(
			&fillID,
			&orderID,
			&side,
			&positionEffect,
			&logicalTime,
			&total,
		); err != nil {
			return edge.FillExecutionPage{}, fmt.Errorf(
				"scan filtered fill execution: %w",
				err,
			)
		}
		if fillID == nil {
			continue
		}
		if orderID == nil ||
			side == nil ||
			positionEffect == nil ||
			logicalTime == nil {
			return edge.FillExecutionPage{}, fmt.Errorf(
				"scan filtered fill execution: incomplete durable fill",
			)
		}
		row.view.FillID = *fillID
		row.view.OrderID = *orderID
		row.view.Side = *side
		row.view.TradeType = *positionEffect
		row.logicalTime = *logicalTime
		if err := validateFillTradeType(row.view.TradeType); err != nil {
			return edge.FillExecutionPage{}, fmt.Errorf(
				"scan filtered fill execution: %w",
				err,
			)
		}
		row.view.OrderID = "urn:xb:order:" + row.view.OrderID
		row.view.FilledAt = time.Unix(0, row.logicalTime).
			UTC().
			Format(time.RFC3339Nano)
		history = append(history, row)
	}
	if err := rows.Err(); err != nil {
		return edge.FillExecutionPage{}, fmt.Errorf("filter fill executions: %w", err)
	}
	hasMore := len(history) > limit
	if hasMore {
		history = history[:limit]
	}
	if !forward && cursor != nil {
		for left, right := 0, len(history)-1; left < right; left, right = left+1, right-1 {
			history[left], history[right] = history[right], history[left]
		}
	}
	page := edge.FillExecutionPage{
		Items: make([]edge.FillExecutionView, len(history)),
		Total: total,
	}
	for index := range history {
		page.Items[index] = history[index].view
	}
	if len(history) != 0 {
		newest := encodeFillHistoryCursor(history[0])
		oldest := encodeFillHistoryCursor(history[len(history)-1])
		if forward {
			if hasMore {
				page.NextCursor = &oldest
			}
			if cursor != nil {
				page.PrevCursor = &newest
			}
		} else {
			page.NextCursor = &oldest
			if hasMore {
				page.PrevCursor = &newest
			}
		}
	}
	return page, nil
}

func encodeFillHistoryCursor(row fillHistoryRow) string {
	raw := strconv.FormatInt(row.logicalTime, 10) + ":" + row.view.FillID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeFillHistoryCursor(encoded string) (*fillHistoryCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, edge.ErrInvalidRequest
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return nil, edge.ErrInvalidRequest
	}
	nanoseconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, edge.ErrInvalidRequest
	}
	if _, err := engine.ParseID(parts[1]); err != nil {
		return nil, edge.ErrInvalidRequest
	}
	return &fillHistoryCursor{
		logicalTime: nanoseconds,
		fillID:      parts[1],
	}, nil
}

func validateFillTradeType(tradeType string) error {
	switch engine.PositionEffect(tradeType) {
	case engine.PositionEffectOpen,
		engine.PositionEffectIncrease,
		engine.PositionEffectReduce,
		engine.PositionEffectFlip,
		engine.PositionEffectClose:
		return nil
	default:
		return fmt.Errorf("unsupported durable fill position effect %q", tradeType)
	}
}

// Funding returns one exact, newest-first account funding page.
func (store *CompatibilityStore) Funding(
	ctx context.Context,
	accountID string,
	params edge.PageParams,
) (edge.FundingPage, error) {
	return store.fundingPage(ctx, accountID, "", false, params)
}

// FundingBySymbol returns a fleet funding page with account login identity.
func (store *CompatibilityStore) FundingBySymbol(
	ctx context.Context,
	symbol string,
	params edge.PageParams,
) (edge.FundingPage, error) {
	return store.fundingPage(ctx, "", symbol, true, params)
}

// FundingPaidByPosition sums exact funding for one position and optional cycle.
func (store *CompatibilityStore) FundingPaidByPosition(
	ctx context.Context,
	accountID string,
	positionID string,
	since *time.Time,
) (string, error) {
	if _, err := engine.ParseID(positionID); err != nil {
		return "", fmt.Errorf("position funding total: invalid position ID: %w", err)
	}
	var requestedSince any
	if since != nil {
		requestedSince = since.UnixNano()
	}
	var total string
	if err := store.pool.QueryRow(ctx, `
		SELECT trim_scale(
			trading.account_position_funding_total(
				$1,
				$2,
				$3::bigint
			)
		)::text`,
		accountID,
		positionID,
		requestedSince,
	).Scan(&total); err != nil {
		return "", fmt.Errorf("position funding total: %w", err)
	}
	return total, nil
}

type fundingHistoryCursor struct {
	logicalTime int64
	fundingID   string
}

type fundingHistoryRow struct {
	view        edge.FundingView
	logicalTime int64
}

func (store *CompatibilityStore) fundingPage(
	ctx context.Context,
	accountID string,
	symbol string,
	includeAccountLogin bool,
	params edge.PageParams,
) (edge.FundingPage, error) {
	limit := params.Limit
	if limit == 0 {
		limit = 50
	}
	if limit < 1 {
		limit = 1
	}
	if limit > 200 {
		limit = 200
	}
	cursor, err := decodeFundingHistoryCursor(params.Cursor)
	if err != nil {
		return edge.FundingPage{}, err
	}
	forward := params.Direction != "prev" && params.Direction != "backward"
	var (
		requestedAccount any
		requestedSymbol  any
		cursorTime       any
		cursorID         any
	)
	if accountID != "" {
		requestedAccount = accountID
	}
	if symbol != "" {
		requestedSymbol = symbol
	}
	if cursor != nil {
		cursorTime = cursor.logicalTime
		cursorID = cursor.fundingID
	}
	query := `
		SELECT
			funding_id::text,
			instrument_id,
			position_id::text,
			trim_scale(signed_quantity)::text,
			trim_scale(oracle_price)::text,
			trim_scale(funding_rate)::text,
			trim_scale(funding_amount)::text,
			settlement_currency,
			funding_logical_time,
			account_login
		  FROM trading.read_account_funding_history(
			$1::text,
			$2::bigint,
			$3::uuid,
			$4,
			$5,
			$6
		  )`
	filter := requestedAccount
	if includeAccountLogin {
		query = `
		SELECT
			funding_id::text,
			instrument_id,
			position_id::text,
			trim_scale(signed_quantity)::text,
			trim_scale(oracle_price)::text,
			trim_scale(funding_rate)::text,
			trim_scale(funding_amount)::text,
			settlement_currency,
			funding_logical_time,
			account_login
		  FROM trading.read_symbol_funding_history(
			$1::text,
			$2::bigint,
			$3::uuid,
			$4,
			$5,
			$6
		  )`
		filter = requestedSymbol
	}
	rows, queryErr := store.pool.Query(ctx,
		query,
		filter,
		cursorTime,
		cursorID,
		cursor != nil,
		limit+1,
		forward,
	)
	if queryErr != nil {
		return edge.FundingPage{}, fmt.Errorf("list funding: %w", queryErr)
	}
	defer rows.Close()
	history := make([]fundingHistoryRow, 0, limit+1)
	for rows.Next() {
		var row fundingHistoryRow
		var rawPositionID string
		if err := rows.Scan(
			&row.view.FundingID,
			&row.view.Symbol,
			&rawPositionID,
			&row.view.PositionSignedQuantity,
			&row.view.OraclePrice,
			&row.view.FundingRate,
			&row.view.FundingAmount,
			&row.view.Currency,
			&row.logicalTime,
			&row.view.AccountLogin,
		); err != nil {
			return edge.FundingPage{}, fmt.Errorf("scan funding: %w", err)
		}
		row.view.PositionID = hex.EncodeToString([]byte(rawPositionID))
		row.view.FundingTime = time.Unix(0, row.logicalTime).
			UTC().
			Format(time.RFC3339Nano)
		if !includeAccountLogin {
			row.view.AccountLogin = nil
		}
		history = append(history, row)
	}
	if err := rows.Err(); err != nil {
		return edge.FundingPage{}, fmt.Errorf("list funding: %w", err)
	}
	hasMore := len(history) > limit
	if hasMore {
		history = history[:limit]
	}
	if !forward && cursor != nil {
		for left, right := 0, len(history)-1; left < right; left, right = left+1, right-1 {
			history[left], history[right] = history[right], history[left]
		}
	}
	page := edge.FundingPage{
		Items: make([]edge.FundingView, len(history)),
	}
	for index := range history {
		page.Items[index] = history[index].view
	}
	if len(history) != 0 {
		newest := encodeFundingHistoryCursor(history[0])
		oldest := encodeFundingHistoryCursor(history[len(history)-1])
		if forward {
			if hasMore {
				page.NextCursor = &oldest
			}
			if cursor != nil {
				page.PrevCursor = &newest
			}
		} else {
			page.NextCursor = &oldest
			if hasMore {
				page.PrevCursor = &newest
			}
		}
	}
	if cursor == nil {
		var total int64
		countQuery := `SELECT trading.account_funding_history_count($1::text)`
		countFilter := requestedAccount
		if includeAccountLogin {
			countQuery = `SELECT trading.symbol_funding_history_count($1::text)`
			countFilter = requestedSymbol
		}
		if err := store.pool.QueryRow(
			ctx,
			countQuery,
			countFilter,
		).Scan(&total); err != nil {
			return edge.FundingPage{}, fmt.Errorf("count funding: %w", err)
		}
		page.Total = &total
	}
	return page, nil
}

func encodeFundingHistoryCursor(row fundingHistoryRow) string {
	raw := strconv.FormatInt(row.logicalTime, 10) + ":" + row.view.FundingID
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

func decodeFundingHistoryCursor(encoded string) (*fundingHistoryCursor, error) {
	if encoded == "" {
		return nil, nil
	}
	raw, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return nil, edge.ErrInvalidRequest
	}
	parts := strings.SplitN(string(raw), ":", 2)
	if len(parts) != 2 {
		return nil, edge.ErrInvalidRequest
	}
	nanoseconds, err := strconv.ParseInt(parts[0], 10, 64)
	if err != nil {
		return nil, edge.ErrInvalidRequest
	}
	if _, err := engine.ParseID(parts[1]); err != nil {
		return nil, edge.ErrInvalidRequest
	}
	return &fundingHistoryCursor{
		logicalTime: nanoseconds,
		fundingID:   parts[1],
	}, nil
}
