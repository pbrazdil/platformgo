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
	economic "github.com/upcomers-org/platformgo/internal/decimal/economic"
	"github.com/upcomers-org/platformgo/internal/domain"
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

// BrokerAccount returns one complete account only when the durable ownership
// anchor belongs to brokerTenant in the same PostgreSQL statement.
func (store *CompatibilityStore) BrokerAccount(
	ctx context.Context,
	brokerTenant string,
	accountID string,
) (edge.MyAccountView, error) {
	if store == nil || store.pool == nil {
		return edge.MyAccountView{}, errors.New(
			"broker account: PostgreSQL pool is required",
		)
	}
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
	err := store.pool.QueryRow(ctx, `
		WITH ownership AS MATERIALIZED (
			SELECT account_id, user_id
			  FROM identity.user_accounts
			 WHERE account_id = $2
			   AND broker_subject = $1
		)
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
		  FROM ownership
		  LEFT JOIN identity.account_profiles AS profile
		    ON profile.account_id = ownership.account_id
		   AND profile.broker_subject = $1
		  LEFT JOIN trading.accounts AS account
		    ON account.account_id = ownership.account_id`,
		brokerTenant,
		accountID,
	).Scan(
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
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return edge.MyAccountView{}, edge.ErrNotFound
	}
	if err != nil {
		return edge.MyAccountView{}, fmt.Errorf("read broker account: %w", err)
	}
	if login == nil ||
		baseCurrency == nil ||
		marginMode == nil ||
		omsMode == nil ||
		marketVenue == nil ||
		permittedClasses == nil ||
		status == nil ||
		createdAt == nil {
		return edge.MyAccountView{}, fmt.Errorf(
			"broker account %q is incomplete",
			record.AccountID,
		)
	}
	record.Login = *login
	record.BaseCurrency = *baseCurrency
	record.MarginMode = *marginMode
	record.OmsMode = *omsMode
	record.MarketVenue = *marketVenue
	record.PermittedClasses = append([]string(nil), permittedClasses...)
	record.Status = *status
	record.CreatedAt = *createdAt
	account, err := application.AccountSummary(record)
	if err != nil {
		return edge.MyAccountView{}, fmt.Errorf(
			"read broker account %q: %w",
			record.AccountID,
			err,
		)
	}
	return account, nil
}

const brokerAccountsUnfilteredQuery = `
	WITH ownership AS MATERIALIZED (
		SELECT account_id, user_id
		  FROM identity.user_accounts
		 WHERE broker_subject = $1
	)
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
	  FROM ownership
	  LEFT JOIN LATERAL (
		SELECT
			login, base_currency, market_venue, permitted_classes, created_at
		  FROM identity.account_profiles
		 WHERE account_id = ownership.account_id
		   AND broker_subject = $1
		 OFFSET 0
	  ) AS profile ON true
	  LEFT JOIN LATERAL (
		SELECT margin_mode, oms_mode, status
		  FROM trading.accounts
		 WHERE account_id = ownership.account_id
		 OFFSET 0
	  ) AS account ON true
	 ORDER BY profile.login, ownership.account_id COLLATE "C"`

const brokerAccountsFilteredQuery = `
	WITH ownership AS MATERIALIZED (
		SELECT account_id, user_id
		  FROM identity.user_accounts
		 WHERE broker_subject = $1
		   AND user_id = $2
		   AND EXISTS (
				SELECT 1
				  FROM identity.users
				 WHERE user_id = $2
				   AND broker_subject = $1
		   )
	)
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
	  FROM ownership
	  LEFT JOIN LATERAL (
		SELECT
			login, base_currency, market_venue, permitted_classes, created_at
		  FROM identity.account_profiles
		 WHERE account_id = ownership.account_id
		   AND broker_subject = $1
		 OFFSET 0
	  ) AS profile ON true
	  LEFT JOIN LATERAL (
		SELECT margin_mode, oms_mode, status
		  FROM trading.accounts
		 WHERE account_id = ownership.account_id
		 OFFSET 0
	  ) AS account ON true
	 ORDER BY profile.login, ownership.account_id COLLATE "C"`

// BrokerAccounts returns one completely validated tenant list from one
// PostgreSQL snapshot. The fixed filtered query uses a one-time tenant/user
// existence check before its user-key ownership lookup, so a foreign filter
// cannot scan that foreign user's account range. Both templates use unnamed
// extended-protocol execution so PostgreSQL plans for the concrete tenant
// instead of eventually reusing a tenant-agnostic generic plan.
func (store *CompatibilityStore) BrokerAccounts(
	ctx context.Context,
	brokerTenant string,
	userID *string,
) ([]edge.MyAccountView, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New(
			"broker accounts: PostgreSQL pool is required",
		)
	}
	var (
		rows pgx.Rows
		err  error
	)
	if userID == nil {
		rows, err = store.pool.Query(
			ctx,
			brokerAccountsUnfilteredQuery,
			pgx.QueryExecModeExec,
			brokerTenant,
		)
	} else {
		rows, err = store.pool.Query(
			ctx,
			brokerAccountsFilteredQuery,
			pgx.QueryExecModeExec,
			brokerTenant,
			*userID,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("list broker accounts: %w", err)
	}
	defer rows.Close()

	accounts, err := collectBrokerAccountRows(rows)
	if err != nil {
		return nil, fmt.Errorf("list broker accounts: %w", err)
	}
	return accounts, nil
}

type brokerAccountRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func collectBrokerAccountRows(rows brokerAccountRows) ([]edge.MyAccountView, error) {
	accounts := make([]edge.MyAccountView, 0)
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
			return nil, fmt.Errorf("scan row: %w", err)
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
				"broker account %q is incomplete",
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
		account, err := application.BrokerAccountListSummary(record)
		if err != nil {
			return nil, fmt.Errorf(
				"account %q: %w",
				record.AccountID,
				err,
			)
		}
		accounts = append(accounts, account)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("row stream: %w", err)
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

// PurgeExpiredBrokerEchoReplays deletes one bounded database-time batch.
func (store *CompatibilityStore) PurgeExpiredBrokerEchoReplays(
	ctx context.Context,
	batchLimit int,
) (int64, error) {
	var deleted int64
	if err := store.pool.QueryRow(ctx, `
		SELECT identity.purge_expired_broker_echo_replays($1)`,
		batchLimit,
	).Scan(&deleted); err != nil {
		return 0, fmt.Errorf("purge expired broker-echo replays: %w", err)
	}
	return deleted, nil
}

// BrokerEchoReplayCoverage is the aggregate bounded-retention authority.
type BrokerEchoReplayCoverage struct {
	MaxTotalRows               int
	MaxRowsPerPrincipal        int
	PurgeBatchSize             int
	MaxBatchesPerCycle         int
	CleanupIntervalSeconds     int
	CleanupCycleTimeoutSeconds int
	ExpiredReadinessSLOSeconds int
	MaxRetryAfterSeconds       int
	TotalRows                  int64
	LiveRows                   int64
	InvalidLiveRows            int64
	OverlongLiveRows           int64
	ExpiredRows                int64
	MaximumPrincipalRows       int64
	OldestLiveExpiresAt        string
	OldestExpiredAt            string
	OldestExpiredAgeSeconds    int64
}

// BrokerEchoReplayCoverage loads aggregate policy and backlog evidence.
func (store *CompatibilityStore) BrokerEchoReplayCoverage(
	ctx context.Context,
) (BrokerEchoReplayCoverage, error) {
	var coverage BrokerEchoReplayCoverage
	if err := store.pool.QueryRow(ctx, `
		SELECT
			max_total_rows,
			max_rows_per_principal,
			purge_batch_size,
			max_batches_per_cycle,
			cleanup_interval_seconds,
			cleanup_cycle_timeout_seconds,
			expired_readiness_slo_seconds,
			max_retry_after_seconds,
			total_rows,
			live_rows,
			invalid_live_rows,
			overlong_live_rows,
			expired_rows,
			maximum_principal_rows,
			oldest_live_expires_at,
			oldest_expired_at,
			oldest_expired_age_seconds
		  FROM identity.broker_echo_replay_coverage()`,
	).Scan(
		&coverage.MaxTotalRows,
		&coverage.MaxRowsPerPrincipal,
		&coverage.PurgeBatchSize,
		&coverage.MaxBatchesPerCycle,
		&coverage.CleanupIntervalSeconds,
		&coverage.CleanupCycleTimeoutSeconds,
		&coverage.ExpiredReadinessSLOSeconds,
		&coverage.MaxRetryAfterSeconds,
		&coverage.TotalRows,
		&coverage.LiveRows,
		&coverage.InvalidLiveRows,
		&coverage.OverlongLiveRows,
		&coverage.ExpiredRows,
		&coverage.MaximumPrincipalRows,
		&coverage.OldestLiveExpiresAt,
		&coverage.OldestExpiredAt,
		&coverage.OldestExpiredAgeSeconds,
	); err != nil {
		return BrokerEchoReplayCoverage{},
			fmt.Errorf("load broker-echo replay coverage: %w", err)
	}
	return coverage, nil
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
	idempotencyHash [sha256.Size]byte,
	requestHash [sha256.Size]byte,
	response edge.StoredResponse,
) (edge.StoredResponse, error) {
	responseHeaders, err := canonicalJSON(response.Headers)
	if err != nil {
		return edge.StoredResponse{}, fmt.Errorf(
			"broker echo response headers: %w",
			err,
		)
	}
	var stored edge.StoredResponse
	var (
		outcome        string
		retryAfterText string
		capacityScope  string
	)
	if err := store.pool.QueryRow(ctx, `
		SELECT
			outcome,
			retry_after_seconds::text,
			capacity_scope,
			response_status,
			response_headers,
			response_body
		  FROM identity.claim_broker_echo_response($1,$2,$3,$4,$5,$6)`,
		principal,
		idempotencyHash[:],
		requestHash[:],
		response.Status,
		responseHeaders,
		response.Body,
	).Scan(
		&outcome,
		&retryAfterText,
		&capacityScope,
		&stored.Status,
		&stored.Headers,
		&stored.Body,
	); err != nil {
		var postgresError *pgconn.PgError
		if errors.As(err, &postgresError) &&
			postgresError.Code == "23505" {
			return edge.StoredResponse{}, edge.ErrIdempotencyConflict
		}
		return edge.StoredResponse{}, fmt.Errorf("broker echo: %w", err)
	}
	switch outcome {
	case "stored":
		return stored, nil
	case "capacity_limited":
		retryAfter, parseErr := strconv.ParseUint(retryAfterText, 10, 64)
		if parseErr != nil {
			return edge.StoredResponse{}, fmt.Errorf(
				"broker echo: invalid capacity retry: %q: %w",
				retryAfterText,
				parseErr,
			)
		}
		if retryAfter == 0 {
			return edge.StoredResponse{}, errors.New(
				"broker echo: invalid zero capacity retry",
			)
		}
		return edge.StoredResponse{}, edge.RateLimitError{
			RetryAfterSeconds: retryAfter,
			CapacityScope:     capacityScope,
		}
	default:
		return edge.StoredResponse{}, fmt.Errorf(
			"broker echo: invalid durable outcome %q",
			outcome,
		)
	}
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

// PredictionMarkets returns one deterministic snapshot of the public
// prediction-market catalog. The flat statement deliberately joins enabled
// legs and authoritative instruments so the caller never observes a market or
// leg assembled from different PostgreSQL snapshots.
func (store *CompatibilityStore) PredictionMarkets(
	ctx context.Context,
) ([]edge.PredictionMarketView, error) {
	if store == nil || store.pool == nil {
		return nil, errors.New("list prediction markets: PostgreSQL pool is required")
	}
	rows, err := store.pool.Query(ctx, `
		SELECT
			market.market_id::text,
			market.source_venue,
			market.market_key,
			market.question,
			market.resolution_time,
			market.mutually_exclusive,
			market.status,
			market.event_id::text,
			market.stage_label,
			market.stage_ordinal,
			event.source_venue,
			event.event_key,
			event.title,
			event.status,
			event.series,
			leg.instrument_id,
			leg.display_name,
			leg.outcome_index,
			leg.outcome_label,
			instrument.price_scale,
			instrument.quantity_scale,
			trim_scale(1 / power(10::numeric, instrument.price_scale))::text,
			trim_scale(1 / power(10::numeric, instrument.quantity_scale))::text
		  FROM trading.prediction_markets AS market
		  JOIN trading.prediction_legs AS leg
		    ON leg.market_id = market.market_id
		   AND leg.enabled
		  LEFT JOIN trading.prediction_events AS event
		    ON event.event_id = market.event_id
		  LEFT JOIN trading.instruments AS instrument
		    ON instrument.instrument_id = leg.instrument_id
		 ORDER BY
			market.stage_ordinal ASC NULLS LAST,
			market.created_at DESC,
			market.source_venue COLLATE "C",
			market.market_key COLLATE "C",
			market.market_id::text COLLATE "C",
			leg.outcome_index,
			leg.instrument_id COLLATE "C"`)
	if err != nil {
		return nil, fmt.Errorf("list prediction markets: %w", err)
	}
	defer rows.Close()

	markets := make([]edge.PredictionMarketView, 0)
	var (
		currentMarketID string
		currentMarket   *edge.PredictionMarketView
	)
	for rows.Next() {
		var (
			marketID, sourceVenue, marketKey, question string
			resolutionTime                             *time.Time
			mutuallyExclusive                          bool
			status                                     string
			eventID, stageLabel                        *string
			stageOrdinal                               *int
			eventSourceVenue                           *string
			eventKey, eventTitle, eventStatus          *string
			eventSeries                                *string
			instrumentID, displayName, outcomeLabel    *string
			outcomeIndex                               *int
			priceScale, quantityScale                  *int16
			priceIncrement, sizeIncrement              *string
		)
		if err := rows.Scan(
			&marketID,
			&sourceVenue,
			&marketKey,
			&question,
			&resolutionTime,
			&mutuallyExclusive,
			&status,
			&eventID,
			&stageLabel,
			&stageOrdinal,
			&eventSourceVenue,
			&eventKey,
			&eventTitle,
			&eventStatus,
			&eventSeries,
			&instrumentID,
			&displayName,
			&outcomeIndex,
			&outcomeLabel,
			&priceScale,
			&quantityScale,
			&priceIncrement,
			&sizeIncrement,
		); err != nil {
			return nil, fmt.Errorf("scan prediction market: %w", err)
		}

		if strings.TrimSpace(marketID) == "" {
			return nil, errors.New("scan prediction market: market ID is empty")
		}
		for _, value := range []struct {
			name string
			text string
		}{
			{name: "source venue", text: sourceVenue},
			{name: "market key", text: marketKey},
			{name: "question", text: question},
			{name: "status", text: status},
		} {
			if strings.TrimSpace(value.text) == "" {
				return nil, fmt.Errorf(
					"scan prediction market %q: %s is empty",
					marketID,
					value.name,
				)
			}
		}
		if err := validatePredictionVenue(sourceVenue, "market", marketID); err != nil {
			return nil, err
		}
		if err := validatePredictionStatus(status, "market", marketID); err != nil {
			return nil, err
		}
		if stageLabel != nil && strings.TrimSpace(*stageLabel) == "" {
			return nil, fmt.Errorf(
				"scan prediction market %q: stage label is empty",
				marketID,
			)
		}
		if stageOrdinal != nil && *stageOrdinal < 0 {
			return nil, fmt.Errorf(
				"scan prediction market %q: stage ordinal is negative",
				marketID,
			)
		}
		if instrumentID == nil || strings.TrimSpace(*instrumentID) == "" {
			return nil, fmt.Errorf(
				"scan prediction market %q: enabled leg instrument reference is missing",
				marketID,
			)
		}
		if displayName == nil || strings.TrimSpace(*displayName) == "" {
			return nil, fmt.Errorf(
				"scan prediction market %q leg %q: display name is missing",
				marketID,
				*instrumentID,
			)
		}
		if outcomeIndex == nil || *outcomeIndex < 0 {
			return nil, fmt.Errorf(
				"scan prediction market %q leg %q: outcome index is invalid",
				marketID,
				*instrumentID,
			)
		}
		if outcomeLabel == nil || strings.TrimSpace(*outcomeLabel) == "" {
			return nil, fmt.Errorf(
				"scan prediction market %q leg %q: outcome label is missing",
				marketID,
				*instrumentID,
			)
		}
		if priceScale == nil || *priceScale < 0 || *priceScale > int16(economic.MaxScale) {
			return nil, fmt.Errorf(
				"scan prediction market %q leg %q: price scale is invalid",
				marketID,
				*instrumentID,
			)
		}
		if quantityScale == nil || *quantityScale < 0 || *quantityScale > int16(economic.MaxScale) {
			return nil, fmt.Errorf(
				"scan prediction market %q leg %q: quantity scale is invalid",
				marketID,
				*instrumentID,
			)
		}
		if err := validatePredictionIncrement(priceIncrement, "price", marketID, *instrumentID); err != nil {
			return nil, err
		}
		if err := validatePredictionIncrement(sizeIncrement, "size", marketID, *instrumentID); err != nil {
			return nil, err
		}
		if eventID != nil {
			if strings.TrimSpace(*eventID) == "" ||
				eventSourceVenue == nil ||
				eventKey == nil || strings.TrimSpace(*eventKey) == "" ||
				eventTitle == nil || strings.TrimSpace(*eventTitle) == "" ||
				eventStatus == nil || strings.TrimSpace(*eventStatus) == "" {
				return nil, fmt.Errorf(
					"scan prediction market %q: event reference %q is missing authoritative metadata",
					marketID,
					*eventID,
				)
			}
			if err := validatePredictionVenue(*eventSourceVenue, "event", *eventID); err != nil {
				return nil, err
			}
			if err := validatePredictionStatus(*eventStatus, "event", *eventID); err != nil {
				return nil, err
			}
		}

		if currentMarket == nil || currentMarketID != marketID {
			view := edge.PredictionMarketView{
				SourceVenue:       sourceVenue,
				MarketKey:         marketKey,
				Question:          question,
				MutuallyExclusive: mutuallyExclusive,
				Status:            status,
				StageLabel:        stageLabel,
				StageOrdinal:      stageOrdinal,
				Legs:              make([]edge.PredictionLegView, 0, 1),
			}
			if resolutionTime != nil {
				formatted, err := formatPredictionRFC3339(*resolutionTime)
				if err != nil {
					return nil, fmt.Errorf(
						"scan prediction market %q resolution time: %w",
						marketID,
						err,
					)
				}
				view.ResolutionTime = &formatted
			}
			if eventID != nil {
				view.Event = &edge.PredictionEventView{
					EventKey: *eventKey,
					Title:    *eventTitle,
					Series:   eventSeries,
					Status:   *eventStatus,
				}
			}
			markets = append(markets, view)
			currentMarket = &markets[len(markets)-1]
			currentMarketID = marketID
		}
		currentMarket.Legs = append(currentMarket.Legs, edge.PredictionLegView{
			Symbol:         *instrumentID,
			DisplayName:    *displayName,
			OutcomeIndex:   *outcomeIndex,
			OutcomeLabel:   *outcomeLabel,
			PriceIncrement: *priceIncrement,
			SizeIncrement:  *sizeIncrement,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("list prediction markets: %w", err)
	}
	return markets, nil
}

// formatPredictionRFC3339 mirrors chrono 0.4.45 DateTime::to_rfc3339:
// UTC is rendered with an explicit +00:00 offset and AutoSi chooses the
// shortest fractional precision that preserves the timestamp (seconds,
// milliseconds, microseconds, or nanoseconds). Chrono's NaiveDate range is
// narrower than Go's time.Time range, so values outside it fail closed.
func formatPredictionRFC3339(value time.Time) (string, error) {
	value = value.UTC()
	const (
		chronoMinYear = -262143
		chronoMaxYear = 262142
	)
	year := value.Year()
	if year < chronoMinYear || year > chronoMaxYear {
		return "", fmt.Errorf(
			"year %d is outside Chrono representable range [%d,%d]",
			year,
			chronoMinYear,
			chronoMaxYear,
		)
	}
	yearText := fmt.Sprintf("%04d", year)
	if year < 0 || year > 9999 {
		yearText = fmt.Sprintf("%+05d", year)
	}
	fraction := ""
	switch nanos := value.Nanosecond(); {
	case nanos == 0:
	case nanos%1_000_000 == 0:
		fraction = fmt.Sprintf(".%03d", nanos/1_000_000)
	case nanos%1_000 == 0:
		fraction = fmt.Sprintf(".%06d", nanos/1_000)
	default:
		fraction = fmt.Sprintf(".%09d", nanos)
	}
	return fmt.Sprintf(
		"%s-%02d-%02dT%02d:%02d:%02d%s+00:00",
		yearText,
		value.Month(),
		value.Day(),
		value.Hour(),
		value.Minute(),
		value.Second(),
		fraction,
	), nil
}

func validatePredictionIncrement(
	value *string,
	kind string,
	marketID string,
	instrumentID string,
) error {
	if value == nil || strings.TrimSpace(*value) == "" {
		return fmt.Errorf(
			"scan prediction market %q leg %q: %s increment is missing",
			marketID,
			instrumentID,
			kind,
		)
	}
	parsed, err := economic.Parse(*value)
	if err != nil || parsed.Sign() <= 0 {
		return fmt.Errorf(
			"scan prediction market %q leg %q: %s increment %q is invalid",
			marketID,
			instrumentID,
			kind,
			*value,
		)
	}
	return nil
}

func validatePredictionVenue(value, kind, identifier string) error {
	if value != "hyperliquid" && value != "polymarket" && value != "kalshi" {
		return fmt.Errorf(
			"scan prediction %s %q: source venue %q is invalid",
			kind,
			identifier,
			value,
		)
	}
	return nil
}

func validatePredictionStatus(value, kind, identifier string) error {
	switch value {
	case "open", "closed", "resolved", "settled":
		return nil
	default:
		return fmt.Errorf(
			"scan prediction %s %q: status %q is invalid",
			kind,
			identifier,
			value,
		)
	}
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
			balance.currency,
			scale.scale,
			trim_scale(balance.total)::text,
			trim_scale(balance.used)::text,
			trim_scale(balance.free)::text,
			trim_scale(balance.equity)::text
		  FROM ledger.balances AS balance
		  LEFT JOIN trading.currency_scales AS scale
		    ON scale.currency = balance.currency
		 WHERE balance.account_id = $1
		 ORDER BY balance.currency`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list balances: %w", err)
	}
	defer rows.Close()
	values := make([]edge.BalanceView, 0)
	for rows.Next() {
		var value edge.BalanceView
		var registeredScale *int16
		if err := rows.Scan(
			&value.Currency,
			&registeredScale,
			&value.Total,
			&value.Locked,
			&value.Free,
			&value.Equity,
		); err != nil {
			return nil, fmt.Errorf("scan balance: %w", err)
		}
		if registeredScale == nil ||
			*registeredScale < 0 ||
			*registeredScale > 18 {
			return nil, fmt.Errorf(
				"list balances: currency scale authority is unavailable",
			)
		}
		currency, err := domain.NewCurrency(
			value.Currency,
			uint8(*registeredScale),
		)
		if err != nil {
			return nil, fmt.Errorf(
				"list balances: invalid authoritative currency: %w",
				err,
			)
		}
		for _, field := range []struct {
			name   string
			target *string
		}{
			{name: "total", target: &value.Total},
			{name: "locked", target: &value.Locked},
			{name: "free", target: &value.Free},
			{name: "equity", target: &value.Equity},
		} {
			money, err := domain.NewMoney(*field.target, currency)
			if err != nil {
				return nil, fmt.Errorf(
					"list balances: invalid authoritative %s: %w",
					field.name,
					err,
				)
			}
			*field.target = money.Decimal().String()
		}
		values = append(values, value)
	}
	return values, rows.Err()
}

// BrokerBalances returns the complete exact balance projection only when both
// durable account authorities belong to brokerTenant in the same statement.
func (store *CompatibilityStore) BrokerBalances(
	ctx context.Context,
	brokerTenant string,
	accountID string,
) ([]edge.BalanceView, error) {
	rows, err := store.pool.Query(ctx, `
		WITH authority AS MATERIALIZED (
			SELECT EXISTS (
				SELECT 1
				  FROM identity.user_accounts AS ownership
				  JOIN identity.account_profiles AS profile
				    ON profile.account_id = ownership.account_id
				   AND profile.broker_subject = $1
				 WHERE ownership.account_id = $2
				   AND ownership.broker_subject = $1
			) AS authorized
		)
		SELECT
			authority.authorized,
			balance.currency,
			scale.scale,
			trim_scale(balance.total)::text,
			trim_scale(balance.used)::text,
			trim_scale(balance.free)::text,
			trim_scale(balance.equity)::text
		  FROM authority
		  LEFT JOIN ledger.balances AS balance
		    ON authority.authorized
		   AND balance.account_id = $2
		  LEFT JOIN trading.currency_scales AS scale
		    ON scale.currency = balance.currency
		 ORDER BY balance.currency COLLATE pg_catalog."C" NULLS LAST`,
		brokerTenant,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list broker balances: %w", err)
	}
	defer rows.Close()
	values, err := collectBrokerBalanceRows(rows)
	if err != nil {
		return nil, fmt.Errorf("list broker balances: %w", err)
	}
	return values, nil
}

type brokerBalanceRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func collectBrokerBalanceRows(rows brokerBalanceRows) ([]edge.BalanceView, error) {
	values := make([]edge.BalanceView, 0)
	sawSentinel := false
	authorized := false
	for rows.Next() {
		var (
			rowAuthorized   bool
			currency        *string
			registeredScale *int16
			total           *string
			used            *string
			free            *string
			equity          *string
		)
		if err := rows.Scan(
			&rowAuthorized,
			&currency,
			&registeredScale,
			&total,
			&used,
			&free,
			&equity,
		); err != nil {
			return nil, fmt.Errorf("scan balance: %w", err)
		}
		if sawSentinel && authorized != rowAuthorized {
			return nil, errors.New("inconsistent broker balance authority sentinel")
		}
		sawSentinel = true
		authorized = rowAuthorized
		payloadPresent := currency != nil ||
			registeredScale != nil ||
			total != nil ||
			used != nil ||
			free != nil ||
			equity != nil
		if !authorized {
			if payloadPresent {
				return nil, errors.New("unauthorized broker balance payload")
			}
			continue
		}
		if !payloadPresent {
			continue
		}
		if currency == nil ||
			registeredScale == nil ||
			total == nil ||
			used == nil ||
			free == nil ||
			equity == nil ||
			*registeredScale < 0 ||
			*registeredScale > 18 {
			return nil, errors.New("broker balance row is incomplete")
		}
		registeredCurrency, err := domain.NewCurrency(
			*currency,
			uint8(*registeredScale),
		)
		if err != nil {
			return nil, fmt.Errorf(
				"invalid authoritative currency: %w",
				err,
			)
		}
		value := edge.BalanceView{Currency: registeredCurrency.Code()}
		for _, field := range []struct {
			name   string
			source string
			target *string
		}{
			{name: "total", source: *total, target: &value.Total},
			{name: "locked", source: *used, target: &value.Locked},
			{name: "free", source: *free, target: &value.Free},
			{name: "equity", source: *equity, target: &value.Equity},
		} {
			money, err := domain.NewMoney(field.source, registeredCurrency)
			if err != nil {
				return nil, fmt.Errorf(
					"invalid authoritative %s: %w",
					field.name,
					err,
				)
			}
			*field.target = money.Decimal().String()
		}
		values = append(values, value)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if !sawSentinel {
		return nil, errors.New("broker balance authority sentinel is missing")
	}
	if !authorized {
		return nil, edge.ErrForbidden
	}
	return values, nil
}

// LatestFillExecution returns the newest immutable execution-time projection
// proven by the first native fill-history source port.
func (store *CompatibilityStore) LatestFillExecution(
	ctx context.Context,
	accountID string,
) (edge.FillExecutionView, error) {
	var (
		view                 edge.FillExecutionView
		logicalTime          int64
		orderAccountID       *string
		bracketLeg           *string
		intentID             *string
		intentAccountID      *string
		intentCommandAccount *string
		registeredScale      *int16
	)
	if err := store.pool.QueryRow(ctx, `
		SELECT
			fill.fill_id::text,
			fill.order_id::text,
			fill.position_id::text,
			fill.side,
			fill.position_effect,
			trim_scale(fill.realized_pnl)::text,
			fill.settlement_currency,
			currency_scale.scale,
			trim_scale(fill.effective_leverage)::text,
			fill.logical_time,
			orders.account_id,
			orders.bracket_leg,
			intent.intent_id,
			intent.account_id,
			intent_command.account_id
		  FROM trading.fills AS fill
		  LEFT JOIN trading.orders AS orders
		    ON orders.order_id = fill.order_id
		  LEFT JOIN trading.order_intents AS intent
		    ON intent.order_id = fill.order_id
		  LEFT JOIN trading.commands AS intent_command
		    ON intent_command.command_id = intent.command_id
		  LEFT JOIN trading.currency_scales AS currency_scale
		    ON currency_scale.currency = fill.settlement_currency
		 WHERE fill.account_id = $1
		 ORDER BY fill.logical_time DESC, fill.fill_id DESC
		 LIMIT 1`,
		accountID,
	).Scan(
		&view.FillID,
		&view.OrderID,
		&view.PositionID,
		&view.Side,
		&view.TradeType,
		&view.RealizedPnL,
		&view.SettlementCurrency,
		&registeredScale,
		&view.Leverage,
		&logicalTime,
		&orderAccountID,
		&bracketLeg,
		&intentID,
		&intentAccountID,
		&intentCommandAccount,
	); err != nil {
		return edge.FillExecutionView{}, fmt.Errorf("read latest fill execution: %w", err)
	}
	if err := validateFillTradeType(view.TradeType); err != nil {
		return edge.FillExecutionView{}, fmt.Errorf(
			"read latest fill execution: %w",
			err,
		)
	}
	if err := validateFillLeverage(&view); err != nil {
		return edge.FillExecutionView{}, fmt.Errorf(
			"read latest fill execution: %w",
			err,
		)
	}
	if (view.RealizedPnL == nil) != (view.SettlementCurrency == nil) {
		return edge.FillExecutionView{}, fmt.Errorf(
			"read latest fill execution: incomplete realized PnL",
		)
	}
	if err := validateFillRealizedMoney(&view, registeredScale); err != nil {
		return edge.FillExecutionView{}, fmt.Errorf(
			"read latest fill execution: %w",
			err,
		)
	}
	reason, err := fillExecutionReason(
		accountID,
		orderAccountID,
		bracketLeg,
		intentID,
		intentAccountID,
		intentCommandAccount,
	)
	if err != nil {
		return edge.FillExecutionView{}, fmt.Errorf(
			"read latest fill execution: %w",
			err,
		)
	}
	view.Reason = reason
	view.OrderID = "urn:xb:order:" + view.OrderID
	view.FilledAt = time.Unix(0, logicalTime).UTC().Format(time.RFC3339Nano)
	return view, nil
}

// FillExecutionFilter remains an alias for callers of the pre-HTTP store API.
type FillExecutionFilter = edge.FillExecutionFilter

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
	return store.filterFillExecutions(ctx, accountID, nil, filter)
}

// BrokerFills returns the accepted fill page only when both durable account
// authorities belong to brokerTenant in the same statement as the page read.
func (store *CompatibilityStore) BrokerFills(
	ctx context.Context,
	brokerTenant string,
	accountID string,
	filter FillExecutionFilter,
) (edge.FillExecutionPage, error) {
	return store.filterFillExecutions(
		ctx,
		accountID,
		&brokerTenant,
		filter,
	)
}

func (store *CompatibilityStore) filterFillExecutions(
	ctx context.Context,
	accountID string,
	brokerTenant *string,
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
		WITH authority AS (
			SELECT true AS authorized
			 WHERE $2::text IS NULL
			UNION ALL
			SELECT true
			  FROM identity.user_accounts AS ownership
			  JOIN identity.account_profiles AS profile
			    ON profile.account_id = ownership.account_id
			   AND profile.broker_subject = $2
			 WHERE $2::text IS NOT NULL
			   AND ownership.account_id = $1
			   AND ownership.broker_subject = $2
		),
		page AS (
			SELECT
				fill.fill_id,
				fill.order_id,
				fill.position_id,
				fill.side,
				fill.position_effect,
				fill.realized_pnl,
				fill.settlement_currency,
				fill.effective_leverage,
				fill.logical_time
			  FROM authority
			  JOIN trading.fills AS fill
			    ON fill.account_id = $1
			 WHERE fill.account_id = $1
			   AND ($3::text IS NULL OR fill.side = $3)
			   AND ($4::uuid IS NULL OR fill.fill_id = $4)
			 ORDER BY fill.logical_time DESC, fill.fill_id DESC
			 LIMIT $5
		),
		filtered_total AS (
			SELECT CASE
				WHEN EXISTS (SELECT 1 FROM authority)
				THEN (
					SELECT count(*)
					  FROM trading.fills AS counted
					 WHERE counted.account_id = $1
					   AND ($3::text IS NULL OR counted.side = $3)
					   AND ($4::uuid IS NULL OR counted.fill_id = $4)
				)
				ELSE -1
			END AS total
		)
		SELECT
			page.fill_id::text,
			page.order_id::text,
			page.position_id::text,
			page.side,
			page.position_effect,
			trim_scale(page.realized_pnl)::text,
			page.settlement_currency,
			currency_scale.scale,
			trim_scale(page.effective_leverage)::text,
			page.logical_time,
			orders.account_id,
			orders.bracket_leg,
			intent.intent_id,
			intent.account_id,
			intent_command.account_id,
			filtered_total.total
		  FROM filtered_total
		  LEFT JOIN page ON true
		  LEFT JOIN trading.orders AS orders
		    ON orders.order_id = page.order_id
		  LEFT JOIN trading.order_intents AS intent
		    ON intent.order_id = page.order_id
		  LEFT JOIN trading.commands AS intent_command
		    ON intent_command.command_id = intent.command_id
		  LEFT JOIN trading.currency_scales AS currency_scale
		    ON currency_scale.currency = page.settlement_currency
		 ORDER BY page.logical_time DESC NULLS LAST,
		          page.fill_id DESC NULLS LAST`
	args := []any{
		accountID,
		brokerTenant,
		requestedSide,
		requestedTradeID,
		limit + 1,
	}
	if cursor != nil {
		query = `
			WITH authority AS (
				SELECT true AS authorized
				 WHERE $2::text IS NULL
				UNION ALL
				SELECT true
				  FROM identity.user_accounts AS ownership
				  JOIN identity.account_profiles AS profile
				    ON profile.account_id = ownership.account_id
				   AND profile.broker_subject = $2
				 WHERE $2::text IS NOT NULL
				   AND ownership.account_id = $1
				   AND ownership.broker_subject = $2
			),
			page AS (
				SELECT
					fill.fill_id,
					fill.order_id,
					fill.position_id,
					fill.side,
					fill.position_effect,
					fill.realized_pnl,
					fill.settlement_currency,
					fill.effective_leverage,
					fill.logical_time
				  FROM authority
				  JOIN trading.fills AS fill
				    ON fill.account_id = $1
				 WHERE fill.account_id = $1
				   AND ($3::text IS NULL OR fill.side = $3)
				   AND ($4::uuid IS NULL OR fill.fill_id = $4)
				   AND (fill.logical_time, fill.fill_id) < ($5, $6)
				 ORDER BY fill.logical_time DESC, fill.fill_id DESC
				 LIMIT $7
			),
			filtered_total AS (
				SELECT CASE
					WHEN EXISTS (SELECT 1 FROM authority)
					THEN (
						SELECT count(*)
						  FROM trading.fills AS counted
						 WHERE counted.account_id = $1
						   AND ($3::text IS NULL OR counted.side = $3)
						   AND ($4::uuid IS NULL OR counted.fill_id = $4)
					)
					ELSE -1
				END AS total
			)
			SELECT
				page.fill_id::text,
				page.order_id::text,
				page.position_id::text,
				page.side,
				page.position_effect,
				trim_scale(page.realized_pnl)::text,
				page.settlement_currency,
				currency_scale.scale,
				trim_scale(page.effective_leverage)::text,
				page.logical_time,
				orders.account_id,
				orders.bracket_leg,
				intent.intent_id,
				intent.account_id,
				intent_command.account_id,
				filtered_total.total
			  FROM filtered_total
			  LEFT JOIN page ON true
			  LEFT JOIN trading.orders AS orders
			    ON orders.order_id = page.order_id
			  LEFT JOIN trading.order_intents AS intent
			    ON intent.order_id = page.order_id
			  LEFT JOIN trading.commands AS intent_command
			    ON intent_command.command_id = intent.command_id
			  LEFT JOIN trading.currency_scales AS currency_scale
			    ON currency_scale.currency = page.settlement_currency
			 ORDER BY page.logical_time DESC NULLS LAST,
			          page.fill_id DESC NULLS LAST`
		if !forward {
			query = `
				WITH authority AS (
					SELECT true AS authorized
					 WHERE $2::text IS NULL
					UNION ALL
					SELECT true
					  FROM identity.user_accounts AS ownership
					  JOIN identity.account_profiles AS profile
					    ON profile.account_id = ownership.account_id
					   AND profile.broker_subject = $2
					 WHERE $2::text IS NOT NULL
					   AND ownership.account_id = $1
					   AND ownership.broker_subject = $2
				),
				page AS (
					SELECT
						fill.fill_id,
						fill.order_id,
						fill.position_id,
						fill.side,
						fill.position_effect,
						fill.realized_pnl,
						fill.settlement_currency,
						fill.effective_leverage,
						fill.logical_time
					  FROM authority
					  JOIN trading.fills AS fill
					    ON fill.account_id = $1
					 WHERE fill.account_id = $1
					   AND ($3::text IS NULL OR fill.side = $3)
					   AND ($4::uuid IS NULL OR fill.fill_id = $4)
					   AND (fill.logical_time, fill.fill_id) > ($5, $6)
					 ORDER BY fill.logical_time ASC, fill.fill_id ASC
					 LIMIT $7
				),
				filtered_total AS (
					SELECT CASE
						WHEN EXISTS (SELECT 1 FROM authority)
						THEN (
							SELECT count(*)
							  FROM trading.fills AS counted
							 WHERE counted.account_id = $1
							   AND ($3::text IS NULL OR counted.side = $3)
							   AND ($4::uuid IS NULL OR counted.fill_id = $4)
						)
						ELSE -1
					END AS total
				)
				SELECT
					page.fill_id::text,
					page.order_id::text,
					page.position_id::text,
					page.side,
					page.position_effect,
					trim_scale(page.realized_pnl)::text,
					page.settlement_currency,
					currency_scale.scale,
					trim_scale(page.effective_leverage)::text,
					page.logical_time,
					orders.account_id,
					orders.bracket_leg,
					intent.intent_id,
					intent.account_id,
					intent_command.account_id,
					filtered_total.total
				  FROM filtered_total
				  LEFT JOIN page ON true
				  LEFT JOIN trading.orders AS orders
				    ON orders.order_id = page.order_id
				  LEFT JOIN trading.order_intents AS intent
				    ON intent.order_id = page.order_id
				  LEFT JOIN trading.commands AS intent_command
				    ON intent_command.command_id = intent.command_id
				  LEFT JOIN trading.currency_scales AS currency_scale
				    ON currency_scale.currency = page.settlement_currency
				 ORDER BY page.logical_time ASC NULLS LAST,
				          page.fill_id ASC NULLS LAST`
		}
		args = []any{
			accountID,
			brokerTenant,
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
	return collectFillExecutionRows(
		rows,
		accountID,
		brokerTenant != nil,
		limit,
		cursor,
		forward,
	)
}

type fillExecutionRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func collectFillExecutionRows(
	rows fillExecutionRows,
	accountID string,
	brokerRead bool,
	limit int,
	cursor *fillHistoryCursor,
	forward bool,
) (edge.FillExecutionPage, error) {
	history := make([]fillHistoryRow, 0, limit+1)
	var total int64
	for rows.Next() {
		var row fillHistoryRow
		var (
			fillID          *string
			orderID         *string
			positionID      *string
			side            *string
			positionEffect  *string
			realizedPnL     *string
			settlement      *string
			registeredScale *int16
			leverage        *string
			logicalTime     *int64
			orderAccount    *string
			bracketLeg      *string
			intentID        *string
			intentAccount   *string
			commandAccount  *string
		)
		if scanErr := rows.Scan(
			&fillID,
			&orderID,
			&positionID,
			&side,
			&positionEffect,
			&realizedPnL,
			&settlement,
			&registeredScale,
			&leverage,
			&logicalTime,
			&orderAccount,
			&bracketLeg,
			&intentID,
			&intentAccount,
			&commandAccount,
			&total,
		); scanErr != nil {
			return edge.FillExecutionPage{}, fmt.Errorf(
				"scan filtered fill execution: %w",
				scanErr,
			)
		}
		if fillID == nil {
			continue
		}
		if orderID == nil ||
			positionID == nil ||
			side == nil ||
			positionEffect == nil ||
			logicalTime == nil {
			return edge.FillExecutionPage{}, fmt.Errorf(
				"scan filtered fill execution: incomplete durable fill",
			)
		}
		row.view.FillID = *fillID
		row.view.OrderID = *orderID
		row.view.PositionID = *positionID
		row.view.Side = *side
		row.view.TradeType = *positionEffect
		row.view.RealizedPnL = realizedPnL
		row.view.SettlementCurrency = settlement
		row.view.Leverage = leverage
		row.logicalTime = *logicalTime
		if leverageErr := validateFillLeverage(
			&row.view,
		); leverageErr != nil {
			return edge.FillExecutionPage{}, fmt.Errorf(
				"scan filtered fill execution: %w",
				leverageErr,
			)
		}
		if (row.view.RealizedPnL == nil) !=
			(row.view.SettlementCurrency == nil) {
			return edge.FillExecutionPage{}, fmt.Errorf(
				"scan filtered fill execution: incomplete realized PnL",
			)
		}
		if moneyErr := validateFillRealizedMoney(
			&row.view,
			registeredScale,
		); moneyErr != nil {
			return edge.FillExecutionPage{}, fmt.Errorf(
				"scan filtered fill execution: %w",
				moneyErr,
			)
		}
		if tradeTypeErr := validateFillTradeType(
			row.view.TradeType,
		); tradeTypeErr != nil {
			return edge.FillExecutionPage{}, fmt.Errorf(
				"scan filtered fill execution: %w",
				tradeTypeErr,
			)
		}
		reason, reasonErr := fillExecutionReason(
			accountID,
			orderAccount,
			bracketLeg,
			intentID,
			intentAccount,
			commandAccount,
		)
		if reasonErr != nil {
			return edge.FillExecutionPage{}, fmt.Errorf(
				"scan filtered fill execution: %w",
				reasonErr,
			)
		}
		row.view.Reason = reason
		row.view.OrderID = "urn:xb:order:" + row.view.OrderID
		row.view.FilledAt = time.Unix(0, row.logicalTime).
			UTC().
			Format(time.RFC3339Nano)
		history = append(history, row)
	}
	if err := rows.Err(); err != nil {
		return edge.FillExecutionPage{}, fmt.Errorf("filter fill executions: %w", err)
	}
	if brokerRead && total == -1 {
		return edge.FillExecutionPage{}, edge.ErrForbidden
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

// Fills returns the accepted account-scoped HTTP fill projection.
func (store *CompatibilityStore) Fills(
	ctx context.Context,
	accountID string,
	filter edge.FillExecutionFilter,
) (edge.FillExecutionPage, error) {
	return store.FilterFillExecutions(ctx, accountID, filter)
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

func fillExecutionReason(
	fillAccountID string,
	orderAccountID *string,
	bracketLeg *string,
	intentID *string,
	intentAccountID *string,
	intentCommandAccountID *string,
) (string, error) {
	if orderAccountID == nil || *orderAccountID != fillAccountID {
		return "", fmt.Errorf(
			"fill/order account authority mismatch",
		)
	}
	hasIntentAuthority := intentID != nil ||
		intentAccountID != nil ||
		intentCommandAccountID != nil
	if hasIntentAuthority {
		if intentID == nil ||
			intentAccountID == nil ||
			intentCommandAccountID == nil {
			return "", fmt.Errorf(
				"incomplete fill intent authority",
			)
		}
		if *intentAccountID != fillAccountID ||
			*intentCommandAccountID != fillAccountID {
			return "", fmt.Errorf(
				"fill intent account authority mismatch",
			)
		}
	}

	switch {
	case bracketLeg == nil:
	case *bracketLeg == string(engine.BracketLegEntry):
	case *bracketLeg == string(engine.BracketLegStopLoss):
		return "stop_loss", nil
	case *bracketLeg == string(engine.BracketLegTakeProfit):
		return "take_profit", nil
	default:
		return "", fmt.Errorf(
			"unknown durable bracket leg %q",
			*bracketLeg,
		)
	}
	if intentID != nil {
		switch {
		case strings.HasPrefix(*intentID, "stopout:"):
			return "liquidation", nil
		case strings.HasPrefix(*intentID, "flatten:"):
			return "flatten", nil
		}
	}
	return "manual", nil
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

func validateFillLeverage(view *edge.FillExecutionView) error {
	if view.Leverage == nil {
		return nil
	}
	leverage, err := domain.NewRatio(*view.Leverage)
	if err != nil || leverage.Decimal().Sign() <= 0 {
		return errors.New("invalid effective leverage")
	}
	canonical := leverage.Decimal().String()
	view.Leverage = &canonical
	return nil
}

func validateFillRealizedMoney(
	view *edge.FillExecutionView,
	registeredScale *int16,
) error {
	if view.RealizedPnL == nil && view.SettlementCurrency == nil {
		return nil
	}
	if view.RealizedPnL == nil ||
		view.SettlementCurrency == nil ||
		registeredScale == nil ||
		*registeredScale < 0 ||
		*registeredScale > 18 {
		return errors.New("realized PnL currency authority is unavailable")
	}
	currency, err := domain.NewCurrency(
		*view.SettlementCurrency,
		uint8(*registeredScale),
	)
	if err != nil {
		return fmt.Errorf("invalid realized PnL currency: %w", err)
	}
	money, err := domain.NewMoney(*view.RealizedPnL, currency)
	if err != nil {
		return fmt.Errorf("invalid realized PnL: %w", err)
	}
	canonical := money.Decimal().String()
	view.RealizedPnL = &canonical
	return nil
}

// Funding returns one exact, newest-first account funding page.
func (store *CompatibilityStore) Funding(
	ctx context.Context,
	accountID string,
	params edge.PageParams,
) (edge.FundingPage, error) {
	return store.fundingPage(ctx, accountID, "", false, params)
}

// BrokerFunding returns one complete funding page only when both durable
// account authorities belong to brokerTenant. Authority, page rows, and the
// optional first-page total are read by one PostgreSQL statement.
func (store *CompatibilityStore) BrokerFunding(
	ctx context.Context,
	brokerTenant string,
	accountID string,
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
	forward := cursor == nil ||
		(params.Direction != "prev" && params.Direction != "backward")
	var cursorTime any
	var cursorID any
	if cursor != nil {
		cursorTime = cursor.logicalTime
		cursorID = cursor.fundingID
	}

	rows, err := store.pool.Query(ctx, `
		WITH authority AS MATERIALIZED (
			SELECT
				ownership.account_id,
				profile.login AS account_login
			  FROM identity.user_accounts AS ownership
			  JOIN identity.account_profiles AS profile
			    ON profile.account_id = ownership.account_id
			   AND profile.broker_subject = $1
			 WHERE ownership.account_id = $2
			   AND ownership.broker_subject = $1
		),
		page AS MATERIALIZED (
			SELECT
				authority.account_login,
				funding.funding_id,
				funding.instrument_id,
				funding.instrument_revision,
				funding.price_scale,
				funding.quantity_scale,
				funding.position_id,
				funding.signed_quantity,
				funding.oracle_price,
				funding.funding_rate,
				funding.funding_amount,
				funding.settlement_currency,
				funding.funding_logical_time,
				funding.ordinality AS page_ordinal
			  FROM authority
			  CROSS JOIN LATERAL trading.read_broker_account_funding_history(
				authority.account_id,
				$3::bigint,
				$4::uuid,
				$5,
				$6,
				$7
			  ) WITH ORDINALITY AS funding
		),
		total AS MATERIALIZED (
			SELECT trading.account_funding_history_count(
				authority.account_id
			) AS value
			  FROM authority
			 WHERE NOT $5
		),
		sentinel AS (
			SELECT EXISTS (SELECT 1 FROM authority) AS authorized
		)
		SELECT
			sentinel.authorized,
			authority.account_login,
			page.funding_id::text,
			page.instrument_id,
			page.instrument_revision,
			page.price_scale,
			page.quantity_scale,
			page.position_id::text,
			trim_scale(page.signed_quantity)::text,
			trim_scale(page.oracle_price)::text,
			trim_scale(page.funding_rate)::text,
			trim_scale(page.funding_amount)::text,
			page.settlement_currency,
			currency_scale.scale,
			page.funding_logical_time,
			total.value
		  FROM sentinel
		  LEFT JOIN authority ON sentinel.authorized
		  LEFT JOIN page ON sentinel.authorized
		  LEFT JOIN trading.currency_scales AS currency_scale
		    ON currency_scale.currency = page.settlement_currency
		  LEFT JOIN total ON sentinel.authorized
		 ORDER BY page.page_ordinal NULLS LAST`,
		pgx.QueryExecModeExec,
		brokerTenant,
		accountID,
		cursorTime,
		cursorID,
		cursor != nil,
		limit+1,
		forward,
	)
	if err != nil {
		return edge.FundingPage{}, fmt.Errorf("list broker funding: %w", err)
	}
	defer rows.Close()
	page, err := collectBrokerFundingRows(rows, limit, cursor, forward)
	if err != nil {
		return edge.FundingPage{}, fmt.Errorf("list broker funding: %w", err)
	}
	return page, nil
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

type brokerFundingRows interface {
	Next() bool
	Scan(...any) error
	Err() error
}

func collectBrokerFundingRows(
	rows brokerFundingRows,
	limit int,
	cursor *fundingHistoryCursor,
	forward bool,
) (edge.FundingPage, error) {
	history := make([]fundingHistoryRow, 0, limit+1)
	sawSentinel := false
	authorized := false
	var authoritativeLogin *int64
	var authoritativeTotal *int64
	for rows.Next() {
		var (
			rowAuthorized bool
			accountLogin  *int64
			fundingID     *string
			symbol        *string
			revision      *int64
			priceScale    *int16
			quantityScale *int16
			rawPositionID *string
			signedQty     *string
			oraclePrice   *string
			fundingRate   *string
			fundingAmount *string
			currencyCode  *string
			currencyScale *int16
			logicalTime   *int64
			total         *int64
		)
		if err := rows.Scan(
			&rowAuthorized,
			&accountLogin,
			&fundingID,
			&symbol,
			&revision,
			&priceScale,
			&quantityScale,
			&rawPositionID,
			&signedQty,
			&oraclePrice,
			&fundingRate,
			&fundingAmount,
			&currencyCode,
			&currencyScale,
			&logicalTime,
			&total,
		); err != nil {
			return edge.FundingPage{}, fmt.Errorf("scan funding: %w", err)
		}
		if sawSentinel && authorized != rowAuthorized {
			return edge.FundingPage{}, errors.New(
				"inconsistent broker funding authority sentinel",
			)
		}
		sawSentinel = true
		authorized = rowAuthorized

		payloadPresent := fundingID != nil ||
			symbol != nil ||
			revision != nil ||
			priceScale != nil ||
			quantityScale != nil ||
			rawPositionID != nil ||
			signedQty != nil ||
			oraclePrice != nil ||
			fundingRate != nil ||
			fundingAmount != nil ||
			currencyCode != nil ||
			currencyScale != nil ||
			logicalTime != nil
		if !authorized {
			if accountLogin != nil || total != nil || payloadPresent {
				return edge.FundingPage{}, errors.New(
					"unauthorized broker funding payload",
				)
			}
			continue
		}
		if accountLogin == nil || *accountLogin <= 0 {
			return edge.FundingPage{}, errors.New(
				"broker funding account login is unavailable",
			)
		}
		if authoritativeLogin == nil {
			login := *accountLogin
			authoritativeLogin = &login
		} else if *authoritativeLogin != *accountLogin {
			return edge.FundingPage{}, errors.New(
				"inconsistent broker funding account login",
			)
		}
		if cursor == nil {
			if total == nil || *total < 0 {
				return edge.FundingPage{}, errors.New(
					"broker funding total is unavailable",
				)
			}
			if authoritativeTotal == nil {
				value := *total
				authoritativeTotal = &value
			} else if *authoritativeTotal != *total {
				return edge.FundingPage{}, errors.New(
					"inconsistent broker funding total",
				)
			}
		} else if total != nil {
			return edge.FundingPage{}, errors.New(
				"cursor broker funding page exposed a total",
			)
		}
		if !payloadPresent {
			continue
		}
		if fundingID == nil ||
			symbol == nil ||
			revision == nil ||
			priceScale == nil ||
			quantityScale == nil ||
			rawPositionID == nil ||
			signedQty == nil ||
			oraclePrice == nil ||
			fundingRate == nil ||
			fundingAmount == nil ||
			currencyCode == nil ||
			currencyScale == nil ||
			logicalTime == nil ||
			*revision <= 0 ||
			*priceScale < 0 ||
			*priceScale > int16(economic.MaxScale) ||
			*quantityScale < 0 ||
			*quantityScale > int16(economic.MaxScale) ||
			*currencyScale < 0 ||
			*currencyScale > int16(economic.MaxScale) {
			return edge.FundingPage{}, errors.New(
				"broker funding row is incomplete",
			)
		}
		if _, err := engine.ParseID(*fundingID); err != nil {
			return edge.FundingPage{}, fmt.Errorf(
				"invalid broker funding ID: %w",
				err,
			)
		}
		if _, err := engine.ParseID(*rawPositionID); err != nil {
			return edge.FundingPage{}, fmt.Errorf(
				"invalid broker funding position ID: %w",
				err,
			)
		}
		instrument, err := domain.NewInstrumentRevision(
			*symbol,
			uint64(*revision),
			uint8(*priceScale),
			uint8(*quantityScale),
		)
		if err != nil {
			return edge.FundingPage{}, fmt.Errorf(
				"invalid broker funding instrument: %w",
				err,
			)
		}
		quantityMagnitude, negative := strings.CutPrefix(*signedQty, "-")
		quantity, err := domain.NewQuantity(quantityMagnitude, instrument)
		if err != nil {
			return edge.FundingPage{}, fmt.Errorf(
				"invalid broker funding signed quantity: %w",
				err,
			)
		}
		canonicalQuantity := quantity.Decimal().String()
		if negative && !quantity.Decimal().IsZero() {
			canonicalQuantity = "-" + canonicalQuantity
		}
		oracle, err := domain.NewPrice(*oraclePrice, instrument)
		if err != nil {
			return edge.FundingPage{}, fmt.Errorf(
				"invalid broker funding oracle price: %w",
				err,
			)
		}
		if oracle.Decimal().Sign() <= 0 {
			return edge.FundingPage{}, errors.New(
				"broker funding oracle price must be positive",
			)
		}
		rate, err := domain.NewRate(*fundingRate)
		if err != nil {
			return edge.FundingPage{}, fmt.Errorf(
				"invalid broker funding rate: %w",
				err,
			)
		}
		currency, err := domain.NewCurrency(
			*currencyCode,
			uint8(*currencyScale),
		)
		if err != nil {
			return edge.FundingPage{}, fmt.Errorf(
				"invalid broker funding currency: %w",
				err,
			)
		}
		amount, err := domain.NewMoney(*fundingAmount, currency)
		if err != nil {
			return edge.FundingPage{}, fmt.Errorf(
				"invalid broker funding amount: %w",
				err,
			)
		}
		row := fundingHistoryRow{
			logicalTime: *logicalTime,
			view: edge.FundingView{
				FundingID:              *fundingID,
				Symbol:                 instrument.ID(),
				PositionID:             hex.EncodeToString([]byte(*rawPositionID)),
				PositionSignedQuantity: canonicalQuantity,
				OraclePrice:            oracle.Decimal().String(),
				FundingRate:            rate.Decimal().String(),
				FundingAmount:          amount.Decimal().String(),
				Currency:               currency.Code(),
				FundingTime: time.Unix(0, *logicalTime).
					UTC().
					Format(time.RFC3339Nano),
				AccountLogin: authoritativeLogin,
			},
		}
		history = append(history, row)
	}
	if err := rows.Err(); err != nil {
		return edge.FundingPage{}, err
	}
	if !sawSentinel {
		return edge.FundingPage{}, errors.New(
			"broker funding authority sentinel is missing",
		)
	}
	if !authorized {
		return edge.FundingPage{}, edge.ErrForbidden
	}
	if authoritativeTotal != nil && *authoritativeTotal < int64(len(history)) {
		return edge.FundingPage{}, errors.New(
			"broker funding total is smaller than the page",
		)
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
		Total: authoritativeTotal,
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
	return page, nil
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
