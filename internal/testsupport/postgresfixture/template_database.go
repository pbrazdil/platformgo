package postgresfixture

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

const (
	TemplateDatabaseAuthorizationEnv   = "PLATFORMGO_TEST_POSTGRES_TEMPLATE_AUTHORIZED"
	TemplateDatabaseAuthorizationValue = "YES_I_UNDERSTAND_THIS_CREATES_AND_DROPS_DATABASES"
	TemplateRootDatabase               = "platformgo_template_root"

	TemplateCallerCurrentStore = "current-store"
	TemplateProfileCurrent     = "current"

	TemplateOperationCreateClone = "create-clone"
	TemplateOperationDropClone   = "drop-clone"
	TemplateOperationMarkClone   = "mark-clone"
	TemplateOperationCreateBase  = "create-template"
	TemplateOperationMarkBase    = "mark-template"
	TemplateOperationSealBase    = "seal-template"
	TemplateOperationUnsealBase  = "unseal-template"
	TemplateOperationDropBase    = "drop-template"

	templateHarnessVersion      = "2"
	templateRoleManifestVersion = "runtime-roles-v2-template-owner"
	templateAdvisoryLockKey     = int64(0x5047544d504c5631)
)

// TemplateBuildPhase identifies the two ownership-sensitive parts of a
// current-tip template build.  The first phase runs as the temporary exact
// owner while it is still a superuser so it can create the predecessor
// schema.  The second phase reconnects as that same owner after the role has
// been demoted, and must apply and verify the current tip without privileged
// catalog access.
type TemplateBuildPhase uint8

const (
	TemplateBuildPhasePreDemotion TemplateBuildPhase = iota + 1
	TemplateBuildPhasePostDemotion
)

// TemplatePrepare is the ownership-aware template build callback.
type TemplatePrepare func(context.Context, *pgxpool.Pool, TemplateBuildPhase) error

var (
	ErrTemplateDatabaseDDLNotAuthorized = errors.New("PostgreSQL template database DDL is not authorized")
	ErrTemplateClusterNotDedicated      = errors.New("PostgreSQL template cluster is not dedicated")
	ErrTemplateClusterNotPristine       = errors.New("PostgreSQL template cluster is not pristine")
	ErrTemplateRoleDrift                = errors.New("PostgreSQL template cluster roles changed")
)

// CurrentTemplateProperties are all non-file authorities that determine a
// reusable current-tip template. The digest deliberately excludes DSNs,
// credentials, process IDs, wall time, and filesystem enumeration order.
type CurrentTemplateProperties struct {
	HarnessVersion       string
	Caller               string
	Profile              string
	PostgresVersion      string
	Encoding             string
	Collation            string
	CType                string
	LocaleProvider       string
	Locale               string
	CollationVersion     string
	RoleManifestVersion  string
	MigrationManifestTip string
}

// CurrentTemplateDigest returns a canonical digest of migration paths, bytes,
// and the authority properties that affect the resulting database.
func CurrentTemplateDigest(migrations fs.FS, properties CurrentTemplateProperties) ([32]byte, error) {
	var zero [32]byte
	if migrations == nil {
		return zero, errors.New("template migration filesystem is required")
	}
	paths := make([]string, 0)
	err := fs.WalkDir(migrations, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".sql") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return zero, fmt.Errorf("enumerate template migrations: %w", err)
	}
	sort.Strings(paths)

	hash := sha256.New()
	writeFrame := func(value string) {
		var size [8]byte
		binary.BigEndian.PutUint64(size[:], uint64(len(value)))
		_, _ = hash.Write(size[:])
		_, _ = hash.Write([]byte(value))
	}
	writeFrame("platformgo-postgres-template")
	writeFrame(properties.HarnessVersion)
	writeFrame(properties.Caller)
	writeFrame(properties.Profile)
	writeFrame(properties.PostgresVersion)
	writeFrame(properties.Encoding)
	writeFrame(properties.Collation)
	writeFrame(properties.CType)
	writeFrame(properties.LocaleProvider)
	writeFrame(properties.Locale)
	writeFrame(properties.CollationVersion)
	writeFrame(properties.RoleManifestVersion)
	writeFrame(properties.MigrationManifestTip)
	for _, path := range paths {
		raw, readErr := fs.ReadFile(migrations, path)
		if readErr != nil {
			return zero, fmt.Errorf("read template migration %q: %w", path, readErr)
		}
		writeFrame(path)
		writeFrame(string(raw))
	}
	var digest [32]byte
	copy(digest[:], hash.Sum(nil))
	return digest, nil
}

// TemplateDatabaseNames derives bounded disposable names without embedding the
// caller's lease text in PostgreSQL catalogs or diagnostics.
func TemplateDatabaseNames(digest [32]byte, lease string) (string, string, error) {
	if strings.TrimSpace(lease) == "" {
		return "", "", errors.New("template clone lease is required")
	}
	digestText := hex.EncodeToString(digest[:])
	leaseDigest := sha256.Sum256([]byte(lease))
	templateName := "platformgo_test_tpl_" + digestText[:16]
	cloneName := "platformgo_test_clone_" + digestText[:12] + "_" + hex.EncodeToString(leaseDigest[:6])
	if len(templateName) > 63 || len(cloneName) > 63 ||
		!IsDisposableDatabaseName(templateName) || !IsDisposableDatabaseName(cloneName) {
		return "", "", errors.New("derived template database name is unsafe")
	}
	return templateName, cloneName, nil
}

// ValidateTemplateDatabaseAuthorization enforces an opt-in independent from
// the schema-reset authorization because this harness executes database DDL.
func ValidateTemplateDatabaseAuthorization() error {
	if os.Getenv(TemplateDatabaseAuthorizationEnv) != TemplateDatabaseAuthorizationValue {
		return fmt.Errorf(
			"%w: set %s to the exact required value only for a dedicated disposable cluster",
			ErrTemplateDatabaseDDLNotAuthorized,
			TemplateDatabaseAuthorizationEnv,
		)
	}
	return nil
}

type TemplateDatabaseConfig struct {
	PrimaryDSN  string
	TemplateDSN string
	Caller      string
	Profile     string
	Migrations  fs.FS
	AfterDDL    func(operation, databaseName string) error
	// AfterRoleDDL is a fault-injection hook for role DDL.  It is separate
	// from AfterDDL because role creation/demotion/cleanup happen while the
	// manager is still constructing or tearing down its private state.
	AfterRoleDDL func(operation, roleName string) error
}

type TemplateDatabaseManager struct {
	mu sync.Mutex

	rootConn                     *pgx.Conn
	inspectionConn               *pgx.Conn
	primaryPool                  *pgxpool.Pool
	templateConfig               *pgx.ConnConfig
	config                       TemplateDatabaseConfig
	templateName                 string
	templateMarker               string
	templateOwner                string
	templateOwnerPassword        string
	templateOwnerOID             uint32
	templateOwnerCreated         bool
	templateOwnerCreateAttempted bool
	templateOwnerManifest        templateOwnerRoleState
	clusterFacts                 clusterFacts
	initialRoles                 roleSnapshot
	baselineRoles                roleSnapshot
	ownedRoles                   []string
	clones                       map[string]*TemplateDatabase
	advisoryHeld                 bool
	closed                       bool
	poisoned                     error
}

type TemplateDatabase struct {
	mu      sync.Mutex
	manager *TemplateDatabaseManager
	name    string
	dsn     string
	marker  string
	closed  bool
}

func (database *TemplateDatabase) DSN() string { return database.dsn }

// Name returns the immutable disposable database name represented by this
// handle.  It is intentionally read-only so tests can inspect ownership
// without reaching into manager state.
func (database *TemplateDatabase) Name() string { return database.name }

// TemplateName returns the immutable disposable database name used for the
// sealed current-tip template.
func (manager *TemplateDatabaseManager) TemplateName() string {
	if manager == nil {
		return ""
	}
	return manager.templateName
}

func (database *TemplateDatabase) Close(ctx context.Context) error {
	database.mu.Lock()
	defer database.mu.Unlock()
	if database.closed {
		return nil
	}
	if err := database.manager.dropClone(ctx, database.name); err != nil {
		return err
	}
	database.closed = true
	return nil
}

type clusterFacts struct {
	DatabaseName     string
	CurrentUser      string
	SystemID         string
	ServerVersion    string
	ServerVersionNo  string
	Encoding         string
	Collation        string
	CType            string
	LocaleProvider   string
	Locale           string
	CollationVersion string
}

type roleSnapshot struct {
	rows  []string
	names []string
}

// NewTemplateDatabaseManager preserves the legacy callback shape for callers
// that only need a predecessor-phase failure fixture.  Current-tip template
// construction must use NewTemplateDatabaseManagerPhased so the callback can
// explicitly select the migration set for each ownership phase.
func NewTemplateDatabaseManager(
	ctx context.Context,
	config TemplateDatabaseConfig,
	prepare func(context.Context, *pgxpool.Pool) error,
) (*TemplateDatabaseManager, error) {
	if prepare == nil {
		return nil, errors.New("template prepare callback is required")
	}
	return NewTemplateDatabaseManagerPhased(ctx, config, func(
		phaseCtx context.Context,
		pool *pgxpool.Pool,
		phase TemplateBuildPhase,
	) error {
		if phase == TemplateBuildPhasePostDemotion {
			return errors.New("legacy template prepare callback cannot complete the demoted current-tip phase; use NewTemplateDatabaseManagerPhased")
		}
		return prepare(phaseCtx, pool)
	})
}

// NewTemplateDatabaseManagerPhased builds and seals a disposable current-tip
// template through two explicit ownership phases.  A manager-created exact
// owner is the database and relation owner for both phases; only its role
// attributes change between callbacks.
func NewTemplateDatabaseManagerPhased(
	ctx context.Context,
	config TemplateDatabaseConfig,
	prepare TemplatePrepare,
) (*TemplateDatabaseManager, error) {
	cleanupCtx := context.WithoutCancel(ctx)
	if err := ValidateTemplateDatabaseAuthorization(); err != nil {
		return nil, err
	}
	if config.PrimaryDSN == "" || config.TemplateDSN == "" || prepare == nil {
		return nil, errors.New("primary DSN, template DSN, and prepare callback are required")
	}
	if config.Caller != TemplateCallerCurrentStore || config.Profile != TemplateProfileCurrent {
		return nil, fmt.Errorf("unsupported template caller/profile %q/%q", config.Caller, config.Profile)
	}
	if config.Migrations == nil {
		return nil, errors.New("template migration filesystem is required")
	}

	primaryPool, err := pgxpool.New(ctx, config.PrimaryDSN)
	if err != nil {
		return nil, fmt.Errorf("open primary PostgreSQL cluster: %w", err)
	}
	closePrimary := true
	defer func() {
		if closePrimary {
			primaryPool.Close()
		}
	}()
	if pingErr := primaryPool.Ping(ctx); pingErr != nil {
		return nil, fmt.Errorf("ping primary PostgreSQL cluster: %w", pingErr)
	}

	templateConfig, err := pgx.ParseConfig(config.TemplateDSN)
	if err != nil {
		return nil, fmt.Errorf("parse template PostgreSQL DSN: %w", err)
	}
	rootConn, err := pgx.ConnectConfig(ctx, templateConfig.Copy())
	if err != nil {
		return nil, fmt.Errorf("open template PostgreSQL cluster: %w", err)
	}
	closeRoot := true
	defer func() {
		if closeRoot {
			_ = rootConn.Close(cleanupCtx)
		}
	}()

	primaryFacts, err := readClusterFacts(ctx, primaryPool)
	if err != nil {
		return nil, fmt.Errorf("inspect primary PostgreSQL cluster: %w", err)
	}
	templateFacts, err := readClusterFacts(ctx, rootConn)
	if err != nil {
		return nil, fmt.Errorf("inspect template PostgreSQL cluster: %w", err)
	}
	if primaryFacts.SystemID == templateFacts.SystemID {
		return nil, fmt.Errorf("%w: primary and template system identifiers are equal", ErrTemplateClusterNotDedicated)
	}
	if primaryFacts.ServerVersionNo != templateFacts.ServerVersionNo ||
		primaryFacts.ServerVersion != templateFacts.ServerVersion {
		return nil, fmt.Errorf(
			"template PostgreSQL version %s (%s) does not match primary %s (%s)",
			templateFacts.ServerVersion,
			templateFacts.ServerVersionNo,
			primaryFacts.ServerVersion,
			primaryFacts.ServerVersionNo,
		)
	}
	if primaryFacts.Encoding != templateFacts.Encoding ||
		primaryFacts.Collation != templateFacts.Collation ||
		primaryFacts.CType != templateFacts.CType ||
		primaryFacts.LocaleProvider != templateFacts.LocaleProvider ||
		primaryFacts.Locale != templateFacts.Locale ||
		primaryFacts.CollationVersion != templateFacts.CollationVersion {
		return nil, fmt.Errorf(
			"template PostgreSQL locale authority does not match primary: template %q/%q/%q/%q/%q/%q primary %q/%q/%q/%q/%q/%q",
			templateFacts.Encoding,
			templateFacts.Collation,
			templateFacts.CType,
			templateFacts.LocaleProvider,
			templateFacts.Locale,
			templateFacts.CollationVersion,
			primaryFacts.Encoding,
			primaryFacts.Collation,
			primaryFacts.CType,
			primaryFacts.LocaleProvider,
			primaryFacts.Locale,
			primaryFacts.CollationVersion,
		)
	}
	if !IsDisposableDatabaseName(primaryFacts.DatabaseName) {
		return nil, fmt.Errorf("%w: primary database %q", ErrUnsafeTestDatabase, primaryFacts.DatabaseName)
	}
	if templateFacts.DatabaseName != TemplateRootDatabase {
		return nil, fmt.Errorf(
			"%w: template DSN must connect to %q, got %q",
			ErrTemplateClusterNotDedicated,
			TemplateRootDatabase,
			templateFacts.DatabaseName,
		)
	}

	if _, lockErr := rootConn.Exec(ctx, `SELECT pg_advisory_lock($1)`, templateAdvisoryLockKey); lockErr != nil {
		return nil, fmt.Errorf("acquire template cluster lock: %w", lockErr)
	}
	unlockOnFailure := true
	defer func() {
		if unlockOnFailure {
			_, _ = rootConn.Exec(cleanupCtx, `SELECT pg_advisory_unlock($1)`, templateAdvisoryLockKey)
		}
	}()
	inspectionConn, err := pgx.ConnectConfig(ctx, templateConfig.Copy())
	if err != nil {
		return nil, fmt.Errorf("open template inspection connection: %w", err)
	}
	closeInspection := true
	defer func() {
		if closeInspection {
			_ = inspectionConn.Close(cleanupCtx)
		}
	}()

	lockedFacts, err := readClusterFacts(ctx, inspectionConn)
	if err != nil {
		return nil, fmt.Errorf("recheck locked template cluster: %w", err)
	}
	if lockedFacts.SystemID == primaryFacts.SystemID || lockedFacts.SystemID != templateFacts.SystemID {
		return nil, fmt.Errorf("%w: system identifier changed while acquiring lock", ErrTemplateClusterNotDedicated)
	}
	if pristineErr := validatePristineTemplateCluster(ctx, inspectionConn, lockedFacts.CurrentUser); pristineErr != nil {
		return nil, pristineErr
	}
	initialRoles, err := readRoleSnapshot(ctx, inspectionConn)
	if err != nil {
		return nil, fmt.Errorf("capture initial template roles: %w", err)
	}

	tip, err := migrationManifestTip(config.Migrations)
	if err != nil {
		return nil, err
	}
	properties := CurrentTemplateProperties{
		HarnessVersion:       templateHarnessVersion,
		Caller:               config.Caller,
		Profile:              config.Profile,
		PostgresVersion:      lockedFacts.ServerVersion,
		Encoding:             lockedFacts.Encoding,
		Collation:            lockedFacts.Collation,
		CType:                lockedFacts.CType,
		LocaleProvider:       lockedFacts.LocaleProvider,
		Locale:               lockedFacts.Locale,
		CollationVersion:     lockedFacts.CollationVersion,
		RoleManifestVersion:  templateRoleManifestVersion,
		MigrationManifestTip: tip,
	}
	digest, err := CurrentTemplateDigest(config.Migrations, properties)
	if err != nil {
		return nil, err
	}
	templateOwner, templateOwnerPassword := templateOwnerCredentials(digest)
	templateName, _, err := TemplateDatabaseNames(digest, "manager")
	if err != nil {
		return nil, err
	}
	marker := "platformgo-template:v2:" + hex.EncodeToString(digest[:]) + ":" + config.Caller + ":" + config.Profile

	manager := &TemplateDatabaseManager{
		rootConn:              rootConn,
		inspectionConn:        inspectionConn,
		primaryPool:           primaryPool,
		templateConfig:        templateConfig,
		config:                config,
		templateName:          templateName,
		templateMarker:        marker,
		templateOwner:         templateOwner,
		templateOwnerPassword: templateOwnerPassword,
		clusterFacts:          lockedFacts,
		initialRoles:          initialRoles,
		clones:                make(map[string]*TemplateDatabase),
		advisoryHeld:          true,
	}
	cleanupPending := true
	defer func() {
		if cleanupPending {
			_ = manager.cleanupFailedBuild(cleanupCtx)
		}
	}()
	if buildErr := manager.buildTemplate(ctx, prepare); buildErr != nil {
		cleanupErr := manager.cleanupFailedBuild(cleanupCtx)
		cleanupPending = false
		return nil, errors.Join(buildErr, cleanupErr)
	}
	baselineRoles, err := readRoleSnapshot(ctx, inspectionConn)
	if err != nil {
		cleanupErr := manager.cleanupFailedBuild(cleanupCtx)
		cleanupPending = false
		return nil, errors.Join(fmt.Errorf("capture prepared template roles: %w", err), cleanupErr)
	}
	manager.baselineRoles = baselineRoles
	manager.ownedRoles = addedRoleNames(initialRoles, baselineRoles)
	expectedRoles := append(append([]string(nil), runtimeRoleNames...), templateOwner)
	sort.Strings(expectedRoles)
	if !equalStrings(manager.ownedRoles, expectedRoles) {
		cleanupErr := manager.cleanupFailedBuild(cleanupCtx)
		cleanupPending = false
		return nil, errors.Join(
			fmt.Errorf("%w: prepared role inventory is outside the exact runtime-role manifest plus manager owner", ErrTemplateRoleDrift),
			cleanupErr,
		)
	}

	closePrimary = false
	closeRoot = false
	closeInspection = false
	unlockOnFailure = false
	cleanupPending = false
	return manager, nil
}

func (manager *TemplateDatabaseManager) buildTemplate(
	ctx context.Context,
	prepare TemplatePrepare,
) error {
	if err := manager.createTemplateOwner(ctx); err != nil {
		return fmt.Errorf("create current template owner: %w", err)
	}
	if exists, _, _, err := manager.databaseState(ctx, manager.templateName); err != nil {
		return err
	} else if exists {
		return fmt.Errorf("%w: derived template database already exists", ErrTemplateClusterNotPristine)
	}
	identifier := pgx.Identifier{manager.templateName}.Sanitize()
	if err := manager.applyDatabaseMutation(
		ctx,
		TemplateOperationCreateBase,
		manager.templateName,
		"CREATE DATABASE "+identifier+" OWNER "+pgx.Identifier{manager.templateOwner}.Sanitize()+" TEMPLATE template0",
		func(reconcileCtx context.Context) error {
			return manager.requireDatabaseIdentity(reconcileCtx, manager.templateName, "")
		},
	); err != nil {
		return fmt.Errorf("create current template build database: %w", err)
	}

	poolConfig, err := pgxpool.ParseConfig(manager.config.TemplateDSN)
	if err != nil {
		return fmt.Errorf("configure template build pool: %w", err)
	}
	poolConfig.ConnConfig.Database = manager.templateName
	poolConfig.ConnConfig.User = manager.templateOwner
	poolConfig.ConnConfig.Password = manager.templateOwnerPassword
	poolConfig.MaxConns = 4
	buildPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("open template build pool: %w", err)
	}
	defer buildPool.Close()
	if err := buildPool.Ping(ctx); err != nil {
		buildPool.Close()
		return fmt.Errorf("ping template build database: %w", err)
	}
	if err := validateTemplate0Build(ctx, buildPool, manager.clusterFacts); err != nil {
		buildPool.Close()
		return err
	}
	prepareErr := prepare(ctx, buildPool, TemplateBuildPhasePreDemotion)
	buildPool.Close()
	if prepareErr != nil {
		return fmt.Errorf("prepare current template database before owner demotion: %w", prepareErr)
	}
	if err := manager.demoteTemplateOwner(ctx); err != nil {
		return fmt.Errorf("demote current template owner: %w", err)
	}
	postPool, err := pgxpool.NewWithConfig(ctx, poolConfig)
	if err != nil {
		return fmt.Errorf("open demoted template build pool: %w", err)
	}
	if err := postPool.Ping(ctx); err != nil {
		postPool.Close()
		return fmt.Errorf("ping demoted template build pool: %w", err)
	}
	if err := validateTemplateBuildOwner(ctx, postPool, manager.templateName, manager.templateOwner); err != nil {
		postPool.Close()
		return err
	}
	postPrepareErr := prepare(ctx, postPool, TemplateBuildPhasePostDemotion)
	postPool.Close()
	if postPrepareErr != nil {
		return fmt.Errorf("prepare current template database after owner demotion: %w", postPrepareErr)
	}
	if err := manager.validateDemotedTemplateOwner(ctx); err != nil {
		return err
	}
	if err := manager.applyDatabaseMutation(
		ctx,
		TemplateOperationMarkBase,
		manager.templateName,
		"COMMENT ON DATABASE "+identifier+" IS "+quoteLiteral(manager.templateMarker),
		func(reconcileCtx context.Context) error {
			return manager.requireDatabaseIdentity(reconcileCtx, manager.templateName, manager.templateMarker)
		},
	); err != nil {
		return fmt.Errorf("mark current template database: %w", err)
	}
	if err := manager.applyDatabaseMutation(
		ctx,
		TemplateOperationSealBase,
		manager.templateName,
		"ALTER DATABASE "+identifier+" WITH ALLOW_CONNECTIONS false IS_TEMPLATE true",
		func(reconcileCtx context.Context) error {
			return manager.requireDatabaseFlags(reconcileCtx, manager.templateName, false, true)
		},
	); err != nil {
		return fmt.Errorf("seal current template database: %w", err)
	}
	// Mark/seal hooks run on separate mutation connections and may observe or
	// mutate role state after their database postconditions succeed.  Recheck
	// the complete demoted owner manifest and database owner OID immediately
	// before the caller snapshots the prepared role baseline.
	if err := manager.validateDemotedTemplateOwner(ctx); err != nil {
		return fmt.Errorf("validate sealed current template owner: %w", err)
	}
	return nil
}

func (manager *TemplateDatabaseManager) validateDemotedTemplateOwner(ctx context.Context) error {
	state, err := readTemplateOwnerRole(ctx, manager.inspectionConn, manager.templateOwner)
	if err != nil {
		return err
	}
	if !roleStateMatches(manager.templateOwnerManifest, state, false) {
		return fmt.Errorf("%w: template owner role %q drifted after the demoted build phase", ErrTemplateRoleDrift, manager.templateOwner)
	}
	var databaseOwnerOID uint32
	if err := manager.inspectionConn.QueryRow(ctx, `
		SELECT datdba::integer
		  FROM pg_database
		 WHERE datname = $1`, manager.templateName).Scan(&databaseOwnerOID); err != nil {
		return fmt.Errorf("inspect current template database owner: %w", err)
	}
	if databaseOwnerOID != manager.templateOwnerOID {
		return fmt.Errorf("%w: current template database owner OID = %d, want %d", ErrTemplateRoleDrift, databaseOwnerOID, manager.templateOwnerOID)
	}
	return nil
}

const (
	templateRoleCreateOperation      = "create-template-owner"
	templateRoleDemoteOperation      = "demote-template-owner"
	templateRoleDropOperation        = "drop-template-owner"
	templateRoleDropRuntimeOperation = "drop-runtime-role"
)

type templateOwnerRoleState struct {
	exists       bool
	roleOID      uint32
	superuser    bool
	createdb     bool
	createrole   bool
	canLogin     bool
	inherit      bool
	connLimit    int32
	validUntil   string
	config       string
	passwordHash string
	replication  bool
	bypassRLS    bool
	membershipCt int
}

func templateOwnerCredentials(digest [32]byte) (string, string) {
	text := hex.EncodeToString(digest[:])
	// Keep both identifiers well below PostgreSQL's NAMEDATALEN limit and
	// derive them solely from the canonical template authority digest.
	return "platformgo_tpl_owner_" + text[:24], "platformgo_tpl_password_" + text
}

func (manager *TemplateDatabaseManager) createTemplateOwner(ctx context.Context) error {
	existing, err := readTemplateOwnerRole(ctx, manager.inspectionConn, manager.templateOwner)
	if err != nil {
		return err
	}
	if existing.exists {
		return fmt.Errorf("%w: derived template owner role %q already exists", ErrTemplateClusterNotPristine, manager.templateOwner)
	}
	manager.templateOwnerCreateAttempted = true
	ownerIdentifier := pgx.Identifier{manager.templateOwner}.Sanitize()
	statement := "CREATE ROLE " + ownerIdentifier + " LOGIN SUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS PASSWORD " + quoteLiteral(manager.templateOwnerPassword)
	if err := manager.applyRoleMutation(
		ctx,
		templateRoleCreateOperation,
		manager.templateOwner,
		statement,
		func(reconcileCtx context.Context) error {
			state, stateErr := readTemplateOwnerRole(reconcileCtx, manager.inspectionConn, manager.templateOwner)
			if stateErr != nil {
				return stateErr
			}
			if state.exists {
				manager.templateOwnerOID = state.roleOID
			}
			if !temporaryOwnerRoleState(state) {
				return fmt.Errorf("template owner role %q does not match the exact temporary-superuser manifest", manager.templateOwner)
			}
			return nil
		},
	); err != nil {
		return err
	}
	state, err := readTemplateOwnerRole(ctx, manager.inspectionConn, manager.templateOwner)
	if err != nil {
		return err
	}
	if !temporaryOwnerRoleState(state) {
		return fmt.Errorf("template owner role %q changed before its create postcondition was recorded", manager.templateOwner)
	}
	manager.templateOwnerOID = state.roleOID
	manager.templateOwnerManifest = state
	manager.templateOwnerCreated = true
	return nil
}

func (manager *TemplateDatabaseManager) demoteTemplateOwner(ctx context.Context) error {
	ownerIdentifier := pgx.Identifier{manager.templateOwner}.Sanitize()
	before, err := readTemplateOwnerRole(ctx, manager.inspectionConn, manager.templateOwner)
	if err != nil {
		return err
	}
	if !before.exists || before.roleOID != manager.templateOwnerOID || !roleStateMatches(manager.templateOwnerManifest, before, true) {
		return fmt.Errorf("%w: template owner role %q has unexpected memberships before demotion", ErrTemplateRoleDrift, manager.templateOwner)
	}
	statement := "ALTER ROLE " + ownerIdentifier + " NOSUPERUSER NOCREATEDB NOCREATEROLE NOREPLICATION NOBYPASSRLS"
	if err := manager.applyRoleMutation(
		ctx,
		templateRoleDemoteOperation,
		manager.templateOwner,
		statement,
		func(reconcileCtx context.Context) error {
			state, stateErr := readTemplateOwnerRole(reconcileCtx, manager.inspectionConn, manager.templateOwner)
			if stateErr != nil {
				return stateErr
			}
			if !state.exists || !roleStateMatches(manager.templateOwnerManifest, state, false) {
				return fmt.Errorf("%w: template owner role %q was not safely demoted", ErrTemplateRoleDrift, manager.templateOwner)
			}
			return nil
		},
	); err != nil {
		return err
	}
	return nil
}

func readTemplateOwnerRole(ctx context.Context, conn *pgx.Conn, roleName string) (templateOwnerRoleState, error) {
	var state templateOwnerRoleState
	err := conn.QueryRow(ctx, `
		SELECT r.oid::integer,
		       r.rolsuper,
		       r.rolcreatedb,
		       r.rolcreaterole,
		       r.rolcanlogin,
		       r.rolinherit,
		       r.rolconnlimit,
		       COALESCE(r.rolvaliduntil::text, ''),
		       COALESCE(array_to_string(r.rolconfig, ','), ''),
		       COALESCE(auth.rolpassword, ''),
		       r.rolreplication,
		       r.rolbypassrls,
		       (SELECT count(*) FROM pg_auth_members m
		          WHERE m.member = r.oid OR m.roleid = r.oid)
		  FROM pg_roles r
		  JOIN pg_catalog.pg_authid auth ON auth.oid = r.oid
		 WHERE r.rolname = $1`, roleName).Scan(
		&state.roleOID,
		&state.superuser,
		&state.createdb,
		&state.createrole,
		&state.canLogin,
		&state.inherit,
		&state.connLimit,
		&state.validUntil,
		&state.config,
		&state.passwordHash,
		&state.replication,
		&state.bypassRLS,
		&state.membershipCt,
	)
	if errors.Is(err, pgx.ErrNoRows) {
		return state, nil
	}
	if err != nil {
		return state, fmt.Errorf("inspect template owner role %q: %w", roleName, err)
	}
	state.exists = true
	return state, nil
}

func temporaryOwnerRoleState(state templateOwnerRoleState) bool {
	return state.exists &&
		state.superuser &&
		state.canLogin &&
		state.inherit &&
		state.connLimit == -1 &&
		state.validUntil == "" &&
		state.config == "" &&
		state.passwordHash != "" &&
		!state.createdb &&
		!state.createrole &&
		!state.replication &&
		!state.bypassRLS &&
		state.membershipCt == 0
}

func roleStateMatches(expected, actual templateOwnerRoleState, superuser bool) bool {
	return actual.exists &&
		actual.roleOID == expected.roleOID &&
		actual.superuser == superuser &&
		actual.canLogin == expected.canLogin &&
		actual.inherit == expected.inherit &&
		actual.connLimit == expected.connLimit &&
		actual.validUntil == expected.validUntil &&
		actual.config == expected.config &&
		actual.passwordHash == expected.passwordHash &&
		!actual.createdb &&
		!actual.createrole &&
		!actual.replication &&
		!actual.bypassRLS &&
		actual.membershipCt == 0
}

func validateTemplateBuildOwner(ctx context.Context, pool *pgxpool.Pool, databaseName, ownerName string) error {
	var actualDatabase, currentUser, sessionUser, databaseOwner string
	if err := pool.QueryRow(ctx, `
		SELECT current_database(),
		       current_user,
		       session_user,
		       pg_get_userbyid((SELECT datdba FROM pg_database WHERE datname = current_database()))`).Scan(
		&actualDatabase,
		&currentUser, &sessionUser, &databaseOwner); err != nil {
		return fmt.Errorf("inspect demoted template build owner: %w", err)
	}
	if actualDatabase != databaseName || currentUser != ownerName || sessionUser != ownerName || databaseOwner != ownerName {
		return fmt.Errorf("template build identity = database %q/session %q/current %q/owner %q, want database %q/owner %q", actualDatabase, sessionUser, currentUser, databaseOwner, databaseName, ownerName)
	}
	return nil
}

func (manager *TemplateDatabaseManager) Clone(ctx context.Context, lease string) (*TemplateDatabase, error) {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if manager.closed {
		return nil, errors.New("template database manager is closed")
	}
	if manager.poisoned != nil {
		return nil, manager.poisoned
	}
	if err := ValidateTemplateDatabaseAuthorization(); err != nil {
		return nil, err
	}
	if err := manager.validateRoles(ctx); err != nil {
		manager.poisoned = err
		return nil, err
	}
	digestText := strings.Split(manager.templateMarker, ":")[2]
	digestBytes, err := hex.DecodeString(digestText)
	if err != nil || len(digestBytes) != sha256.Size {
		return nil, errors.New("invalid internal template digest")
	}
	var digest [32]byte
	copy(digest[:], digestBytes)
	_, cloneName, err := TemplateDatabaseNames(digest, lease)
	if err != nil {
		return nil, err
	}
	if _, exists := manager.clones[cloneName]; exists {
		return nil, fmt.Errorf("template clone lease is already active")
	}
	if exists, _, _, stateErr := manager.databaseState(ctx, cloneName); stateErr != nil {
		return nil, stateErr
	} else if exists {
		return nil, fmt.Errorf("%w: unregistered clone database already exists", ErrTemplateClusterNotPristine)
	}
	cloneDSN, err := databaseDSN(manager.config.TemplateDSN, cloneName)
	if err != nil {
		return nil, err
	}

	cloneIdentifier := pgx.Identifier{cloneName}.Sanitize()
	templateIdentifier := pgx.Identifier{manager.templateName}.Sanitize()
	if err := manager.applyDatabaseMutation(
		ctx,
		TemplateOperationCreateClone,
		cloneName,
		"CREATE DATABASE "+cloneIdentifier+" OWNER "+pgx.Identifier{manager.templateOwner}.Sanitize()+" TEMPLATE "+templateIdentifier,
		func(reconcileCtx context.Context) error {
			return manager.requireDatabaseIdentity(reconcileCtx, cloneName, "")
		},
	); err != nil {
		return nil, fmt.Errorf("create current template clone: %w", err)
	}
	database := &TemplateDatabase{
		manager: manager,
		name:    cloneName,
		dsn:     cloneDSN,
	}
	manager.clones[cloneName] = database
	if err := manager.applyDatabaseMutation(
		ctx,
		TemplateOperationMarkClone,
		cloneName,
		"COMMENT ON DATABASE "+cloneIdentifier+" IS "+quoteLiteral(manager.templateMarker+":clone"),
		func(reconcileCtx context.Context) error {
			return manager.requireDatabaseIdentity(reconcileCtx, cloneName, manager.templateMarker+":clone")
		},
	); err != nil {
		return nil, fmt.Errorf("mark current template clone: %w", err)
	}
	database.marker = manager.templateMarker + ":clone"
	return database, nil
}

func (manager *TemplateDatabaseManager) dropClone(ctx context.Context, name string) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	database, registered := manager.clones[name]
	if !registered {
		return nil
	}
	if err := ValidateTemplateDatabaseAuthorization(); err != nil {
		return err
	}
	exists, owner, marker, err := manager.databaseState(ctx, name)
	if err != nil {
		return err
	}
	if !exists {
		delete(manager.clones, name)
		return nil
	}
	if owner != manager.templateOwner || marker != database.marker {
		return fmt.Errorf("%w: registered clone database identity changed", ErrTemplateClusterNotPristine)
	}
	if err := manager.ensureNoSessions(ctx, name); err != nil {
		return err
	}
	if err := manager.applyDatabaseMutation(
		ctx,
		TemplateOperationDropClone,
		name,
		"DROP DATABASE "+pgx.Identifier{name}.Sanitize(),
		func(reconcileCtx context.Context) error {
			return manager.requireDatabaseAbsent(reconcileCtx, name)
		},
	); err != nil {
		return fmt.Errorf("drop current template clone: %w", err)
	}
	delete(manager.clones, name)
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	if err := manager.validateRoles(reconcileCtx); err != nil {
		manager.poisoned = err
		return err
	}
	return nil
}

func (manager *TemplateDatabaseManager) Close(ctx context.Context) error {
	manager.mu.Lock()
	defer manager.mu.Unlock()
	if manager.closed {
		return nil
	}
	if err := manager.ensureInspectionConn(ctx); err != nil {
		return fmt.Errorf("reconnect template inspection connection: %w", err)
	}
	reconcileCtx, cancelReconcile := context.WithTimeout(ctx, 5*time.Second)
	defer cancelReconcile()
	var result error
	templateExists, _, _, stateErr := manager.databaseState(reconcileCtx, manager.templateName)
	if stateErr != nil {
		result = errors.Join(result, stateErr)
	} else if templateExists {
		if err := manager.validateRoles(reconcileCtx); err != nil {
			result = errors.Join(result, err)
		} else {
			// A prior Clone failure poisons new work, but a caller may repair the
			// foreign drift before retrying cleanup.  Do not make that transient
			// observation permanently block teardown.
			manager.poisoned = nil
		}
	} else {
		// The previous cleanup attempt may already have dropped the template
		// and manager-owned roles before a final pristine check failed.  A
		// retry must validate the initial snapshot, not the now-absent build
		// manifest.
		manager.poisoned = nil
	}

	names := make([]string, 0, len(manager.clones))
	for name := range manager.clones {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		database := manager.clones[name]
		exists, owner, marker, err := manager.databaseState(reconcileCtx, name)
		if err != nil {
			result = errors.Join(result, err)
			continue
		}
		if !exists {
			delete(manager.clones, name)
			continue
		}
		if owner != manager.templateOwner || marker != database.marker {
			result = errors.Join(result, fmt.Errorf("%w: registered clone database identity changed", ErrTemplateClusterNotPristine))
			continue
		}
		if err := manager.ensureNoSessions(ctx, name); err != nil {
			result = errors.Join(result, err)
			continue
		}
		if err := manager.applyDatabaseMutation(
			ctx,
			TemplateOperationDropClone,
			name,
			"DROP DATABASE "+pgx.Identifier{name}.Sanitize(),
			func(reconcileCtx context.Context) error {
				return manager.requireDatabaseAbsent(reconcileCtx, name)
			},
		); err != nil {
			result = errors.Join(result, fmt.Errorf("drop registered clone database: %w", err))
			continue
		}
		delete(manager.clones, name)
	}
	if len(manager.clones) == 0 {
		if err := manager.dropTemplate(reconcileCtx); err != nil {
			result = errors.Join(result, err)
		} else if err := manager.restoreInitialRoles(reconcileCtx); err != nil {
			result = errors.Join(result, err)
		} else if err := validatePristineTemplateCluster(reconcileCtx, manager.inspectionConn, manager.clusterFacts.CurrentUser); err != nil {
			result = errors.Join(result, err)
		}
	}
	if result != nil {
		return result
	}
	finalizeCtx := context.Background()
	if manager.advisoryHeld {
		if _, err := manager.rootConn.Exec(finalizeCtx, `SELECT pg_advisory_unlock($1)`, templateAdvisoryLockKey); err != nil {
			return fmt.Errorf("release template cluster lock: %w", err)
		}
		manager.advisoryHeld = false
	}
	// Unlock success is the irreversible finalization transition: all
	// catalog work is complete, so future Close calls must not attempt to use
	// connections that are being closed below.  Close every remaining resource
	// even when one close operation reports an error.
	manager.closed = true
	var closeErr error
	if manager.inspectionConn != nil {
		if err := manager.inspectionConn.Close(finalizeCtx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close template inspection connection: %w", err))
		}
	}
	if manager.rootConn != nil {
		if err := manager.rootConn.Close(finalizeCtx); err != nil {
			closeErr = errors.Join(closeErr, fmt.Errorf("close template cluster connection: %w", err))
		}
	}
	manager.primaryPool.Close()
	return closeErr
}

// ensureInspectionConn keeps catalog reads/reconciliation reconnectable after
// a caller deadline cancels a blocked query.  The advisory-lock connection is
// intentionally never reused for this probe or replacement.
func (manager *TemplateDatabaseManager) ensureInspectionConn(ctx context.Context) error {
	if manager.inspectionConn != nil {
		pingCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), time.Second)
		_, pingErr := manager.inspectionConn.Exec(pingCtx, "SELECT 1")
		cancel()
		if pingErr == nil {
			return nil
		}
		_ = manager.inspectionConn.Close(context.Background())
		manager.inspectionConn = nil
	}
	connectCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	connection, err := pgx.ConnectConfig(connectCtx, manager.templateConfig.Copy())
	if err != nil {
		return err
	}
	manager.inspectionConn = connection
	return nil
}

func (manager *TemplateDatabaseManager) dropTemplate(ctx context.Context) error {
	exists, owner, marker, err := manager.databaseState(ctx, manager.templateName)
	if err != nil {
		return err
	}
	if !exists {
		return nil
	}
	if owner != manager.templateOwner || marker != manager.templateMarker {
		return fmt.Errorf("%w: template database identity changed", ErrTemplateClusterNotPristine)
	}
	if err := manager.ensureNoSessions(ctx, manager.templateName); err != nil {
		return err
	}
	identifier := pgx.Identifier{manager.templateName}.Sanitize()
	if err := manager.applyDatabaseMutation(
		ctx,
		TemplateOperationUnsealBase,
		manager.templateName,
		"ALTER DATABASE "+identifier+" WITH IS_TEMPLATE false ALLOW_CONNECTIONS true",
		func(reconcileCtx context.Context) error {
			return manager.requireDatabaseFlags(reconcileCtx, manager.templateName, true, false)
		},
	); err != nil {
		return fmt.Errorf("unseal template database: %w", err)
	}
	if err := manager.applyDatabaseMutation(
		ctx,
		TemplateOperationDropBase,
		manager.templateName,
		"DROP DATABASE "+identifier,
		func(reconcileCtx context.Context) error {
			return manager.requireDatabaseAbsent(reconcileCtx, manager.templateName)
		},
	); err != nil {
		return fmt.Errorf("drop template database: %w", err)
	}
	return nil
}

func (manager *TemplateDatabaseManager) cleanupFailedBuild(ctx context.Context) error {
	// A canceled build context may have left the long-lived inspection
	// connection in a poisoned/busy state.  Reconnect it from the
	// non-cancelable cleanup context before touching the disposable database or
	// role inventory; the advisory-lock holder must remain fenced until this
	// cleanup has a usable catalog connection.
	if err := manager.ensureInspectionConn(ctx); err != nil {
		return fmt.Errorf("reconnect template inspection connection for failed build cleanup: %w", err)
	}
	var result error
	if exists, _, _, err := manager.databaseState(ctx, manager.templateName); err != nil {
		result = errors.Join(result, err)
	} else if exists {
		identifier := pgx.Identifier{manager.templateName}.Sanitize()
		_ = manager.applyDatabaseMutation(
			ctx,
			TemplateOperationUnsealBase,
			manager.templateName,
			"ALTER DATABASE "+identifier+" WITH IS_TEMPLATE false ALLOW_CONNECTIONS true",
			func(reconcileCtx context.Context) error {
				return manager.requireDatabaseFlags(reconcileCtx, manager.templateName, true, false)
			},
		)
		if err := manager.applyDatabaseMutation(
			ctx,
			TemplateOperationDropBase,
			manager.templateName,
			"DROP DATABASE "+identifier,
			func(reconcileCtx context.Context) error {
				return manager.requireDatabaseAbsent(reconcileCtx, manager.templateName)
			},
		); err != nil {
			result = errors.Join(result, err)
		}
	}
	currentRoles, err := readRoleSnapshot(ctx, manager.inspectionConn)
	if err != nil {
		result = errors.Join(result, fmt.Errorf("inspect roles after failed template build: %w", err))
	} else {
		ownerCleanupEligible := manager.templateOwnerCreated ||
			(manager.templateOwnerCreateAttempted && manager.templateOwnerOID != 0)
		manager.ownedRoles = managedRoleAdditions(manager.initialRoles, currentRoles, manager.templateOwner, ownerCleanupEligible)
	}
	if err := manager.restoreInitialRoles(ctx); err != nil {
		result = errors.Join(result, err)
	}
	return result
}

func (manager *TemplateDatabaseManager) validateRoles(ctx context.Context) error {
	current, err := readRoleSnapshot(ctx, manager.inspectionConn)
	if err != nil {
		return fmt.Errorf("inspect template cluster roles: %w", err)
	}
	if !equalStrings(current.rows, manager.baselineRoles.rows) {
		return fmt.Errorf("%w: expected prepared role manifest", ErrTemplateRoleDrift)
	}
	return nil
}

func (manager *TemplateDatabaseManager) restoreInitialRoles(ctx context.Context) error {
	current, err := readRoleSnapshot(ctx, manager.inspectionConn)
	if err != nil {
		return err
	}
	currentNames := make(map[string]struct{}, len(current.names))
	for _, name := range current.names {
		currentNames[name] = struct{}{}
	}
	var ownedPresent []string
	for _, name := range manager.ownedRoles {
		if _, exists := currentNames[name]; exists {
			ownedPresent = append(ownedPresent, name)
		}
	}
	for _, name := range ownedPresent {
		if name == manager.templateOwner && (manager.templateOwnerCreated || manager.templateOwnerCreateAttempted) {
			state, stateErr := readTemplateOwnerRole(ctx, manager.inspectionConn, name)
			if stateErr != nil {
				return fmt.Errorf("inspect template owner before cleanup: %w", stateErr)
			}
			if !state.exists || manager.templateOwnerOID == 0 || state.roleOID != manager.templateOwnerOID {
				return fmt.Errorf("%w: refusing to drop a replacement template owner role %q", ErrTemplateRoleDrift, name)
			}
			// Membership drift is surfaced by the demotion preflight and is
			// never silently repaired while the template is being built.  At
			// this point cleanup has already torn down the disposable database,
			// so remove only the manager-owned role's memberships before DROP.
			if err := removeRoleMembershipsForCleanup(ctx, manager.inspectionConn, name); err != nil {
				return fmt.Errorf("cleanup template owner memberships: %w", err)
			}
		}
		operation := templateRoleDropRuntimeOperation
		if name == manager.templateOwner {
			operation = templateRoleDropOperation
		}
		if err := manager.applyRoleMutation(
			ctx,
			operation,
			name,
			"DROP ROLE "+pgx.Identifier{name}.Sanitize(),
			func(reconcileCtx context.Context) error {
				state, stateErr := readTemplateOwnerRole(reconcileCtx, manager.inspectionConn, name)
				if stateErr != nil {
					return stateErr
				}
				if state.exists {
					return fmt.Errorf("role %q remains after DROP", name)
				}
				return nil
			},
		); err != nil {
			return fmt.Errorf("restore initial template role %q: %w", name, err)
		}
	}
	final, err := readRoleSnapshot(ctx, manager.inspectionConn)
	if err != nil {
		return err
	}
	if !equalStrings(final.rows, manager.initialRoles.rows) {
		return fmt.Errorf("%w: cleanup did not restore initial roles", ErrTemplateRoleDrift)
	}
	return nil
}

func removeRoleMembershipsForCleanup(ctx context.Context, conn *pgx.Conn, roleName string) error {
	rows, err := conn.Query(ctx, `
		SELECT parent.rolname, member.rolname
		  FROM pg_auth_members AS membership
		  JOIN pg_roles AS parent ON parent.oid = membership.roleid
		  JOIN pg_roles AS member ON member.oid = membership.member
		 WHERE membership.roleid = (SELECT oid FROM pg_roles WHERE rolname = $1)
		    OR membership.member = (SELECT oid FROM pg_roles WHERE rolname = $1)
		 ORDER BY parent.rolname, member.rolname`, roleName)
	if err != nil {
		return fmt.Errorf("inspect role memberships: %w", err)
	}
	type membership struct{ parent, member string }
	var memberships []membership
	for rows.Next() {
		var item membership
		if scanErr := rows.Scan(&item.parent, &item.member); scanErr != nil {
			rows.Close()
			return scanErr
		}
		memberships = append(memberships, item)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return err
	}
	rows.Close()
	for _, item := range memberships {
		if _, revokeErr := conn.Exec(
			ctx,
			"REVOKE "+pgx.Identifier{item.parent}.Sanitize()+" FROM "+pgx.Identifier{item.member}.Sanitize(),
		); revokeErr != nil {
			return fmt.Errorf("remove role membership %q -> %q: %w", item.parent, item.member, revokeErr)
		}
	}
	return nil
}

func (manager *TemplateDatabaseManager) databaseState(
	ctx context.Context,
	name string,
) (bool, string, string, error) {
	var owner, marker string
	err := manager.inspectionConn.QueryRow(ctx, `
		SELECT pg_get_userbyid(datdba), COALESCE(shobj_description(oid, 'pg_database'), '')
		  FROM pg_database
		 WHERE datname = $1`, name).Scan(&owner, &marker)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, "", "", nil
	}
	if err != nil {
		return false, "", "", fmt.Errorf("inspect database state: %w", err)
	}
	return true, owner, marker, nil
}

func (manager *TemplateDatabaseManager) ensureNoSessions(ctx context.Context, name string) error {
	for attempt := 0; attempt < 50; attempt++ {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("wait for database sessions to drain: %w", err)
		}
		var count int
		queryCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
		err := manager.inspectionConn.QueryRow(queryCtx, `
			SELECT count(*)
			  FROM pg_stat_activity
			 WHERE datname = $1
			   AND pid <> pg_backend_pid()`, name).Scan(&count)
		cancel()
		if err != nil {
			return fmt.Errorf("inspect database sessions: %w", err)
		}
		if count == 0 {
			return nil
		}
		if attempt == 49 {
			return fmt.Errorf("database %q has %d active sessions after bounded drain; refusing DROP", name, count)
		}
		timer := time.NewTimer(10 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			return fmt.Errorf("wait for database sessions to drain: %w", ctx.Err())
		case <-timer.C:
		}
	}
	return errors.New("database session drain exhausted unexpectedly")
}

func (manager *TemplateDatabaseManager) execDatabaseDDL(ctx context.Context, statement string) error {
	connection, err := pgx.ConnectConfig(ctx, manager.templateConfig.Copy())
	if err != nil {
		return fmt.Errorf("open isolated database-DDL connection: %w", err)
	}
	defer func() { _ = connection.Close(context.WithoutCancel(ctx)) }()
	if _, err := connection.Exec(ctx, statement); err != nil {
		return err
	}
	return nil
}

// execRoleDDL deliberately uses a short-lived maintenance connection instead
// of the advisory-lock holder.  If the transport disappears after PostgreSQL
// commits CREATE/ALTER/DROP ROLE, the root connection remains available for
// exact role-state reconciliation and deterministic cleanup.
func (manager *TemplateDatabaseManager) execRoleDDL(ctx context.Context, statement string) error {
	connection, err := pgx.ConnectConfig(ctx, manager.templateConfig.Copy())
	if err != nil {
		return fmt.Errorf("open isolated role-DDL connection: %w", err)
	}
	defer func() { _ = connection.Close(context.WithoutCancel(ctx)) }()
	if _, err := connection.Exec(ctx, statement); err != nil {
		return err
	}
	return nil
}

func (manager *TemplateDatabaseManager) applyDatabaseMutation(
	ctx context.Context,
	operation string,
	databaseName string,
	statement string,
	reconcile func(context.Context) error,
) error {
	mutationCtx, cancelMutation := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelMutation()
	mutationErr := manager.execDatabaseDDL(mutationCtx, statement)
	if mutationErr == nil && manager.config.AfterDDL != nil {
		mutationErr = manager.config.AfterDDL(operation, databaseName)
	}
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	reconcileErr := reconcile(reconcileCtx)
	if reconcileErr == nil {
		return nil
	}
	if mutationErr != nil {
		return errors.Join(mutationErr, reconcileErr)
	}
	return reconcileErr
}

func (manager *TemplateDatabaseManager) applyRoleMutation(
	ctx context.Context,
	operation string,
	roleName string,
	statement string,
	reconcile func(context.Context) error,
) error {
	mutationCtx, cancelMutation := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancelMutation()
	mutationErr := error(nil)
	if err := manager.execRoleDDL(mutationCtx, statement); err != nil {
		mutationErr = err
	}
	if mutationErr == nil && manager.config.AfterRoleDDL != nil {
		mutationErr = manager.config.AfterRoleDDL(operation, roleName)
	}
	reconcileCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
	defer cancel()
	reconcileErr := reconcile(reconcileCtx)
	if reconcileErr == nil {
		return nil
	}
	if mutationErr != nil {
		return errors.Join(mutationErr, reconcileErr)
	}
	return reconcileErr
}

func (manager *TemplateDatabaseManager) requireDatabaseIdentity(
	ctx context.Context,
	name string,
	marker string,
) error {
	exists, owner, actualMarker, err := manager.databaseState(ctx, name)
	if err != nil {
		return err
	}
	if !exists || owner != manager.templateOwner || actualMarker != marker {
		return fmt.Errorf(
			"%w: database %q identity does not match the registered operation",
			ErrTemplateClusterNotPristine,
			name,
		)
	}
	return nil
}

func (manager *TemplateDatabaseManager) requireDatabaseAbsent(ctx context.Context, name string) error {
	exists, _, _, err := manager.databaseState(ctx, name)
	if err != nil {
		return err
	}
	if exists {
		return fmt.Errorf("database %q remains after DROP", name)
	}
	return nil
}

func (manager *TemplateDatabaseManager) requireDatabaseFlags(
	ctx context.Context,
	name string,
	allowConnections bool,
	isTemplate bool,
) error {
	var actualAllowConnections, actualIsTemplate bool
	err := manager.inspectionConn.QueryRow(ctx, `
		SELECT datallowconn, datistemplate
		  FROM pg_database
		 WHERE datname = $1`, name).Scan(&actualAllowConnections, &actualIsTemplate)
	if errors.Is(err, pgx.ErrNoRows) {
		return fmt.Errorf("database %q is absent while reconciling flags", name)
	}
	if err != nil {
		return fmt.Errorf("inspect database flags: %w", err)
	}
	if actualAllowConnections != allowConnections || actualIsTemplate != isTemplate {
		return fmt.Errorf(
			"database %q flags = allow_connections %t is_template %t, want %t/%t",
			name,
			actualAllowConnections,
			actualIsTemplate,
			allowConnections,
			isTemplate,
		)
	}
	return nil
}

func validateTemplate0Build(ctx context.Context, pool *pgxpool.Pool, expected clusterFacts) error {
	actual, err := readClusterFacts(ctx, pool)
	if err != nil {
		return fmt.Errorf("inspect template0-derived database properties: %w", err)
	}
	if actual.SystemID != expected.SystemID ||
		actual.ServerVersion != expected.ServerVersion ||
		actual.ServerVersionNo != expected.ServerVersionNo ||
		actual.Encoding != expected.Encoding ||
		actual.Collation != expected.Collation ||
		actual.CType != expected.CType ||
		actual.LocaleProvider != expected.LocaleProvider ||
		actual.Locale != expected.Locale ||
		actual.CollationVersion != expected.CollationVersion {
		return fmt.Errorf("%w: template0-derived database properties differ from the maintenance root", ErrTemplateClusterNotPristine)
	}
	var userObjects int
	if err := pool.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM pg_namespace
			  WHERE nspname <> 'public'
			    AND nspname <> 'information_schema'
			    AND nspname !~ '^pg_')
			+
			(SELECT count(*) FROM pg_class c
			  JOIN pg_namespace n ON n.oid = c.relnamespace
			 WHERE n.nspname = 'public')
			+
			(SELECT count(*) FROM pg_proc p
			  JOIN pg_namespace n ON n.oid = p.pronamespace
			 WHERE n.nspname = 'public')
			+
			(SELECT count(*) FROM pg_type t
			  JOIN pg_namespace n ON n.oid = t.typnamespace
			 WHERE n.nspname = 'public')
			+
			(SELECT count(*) FROM pg_extension WHERE extname <> 'plpgsql')`).Scan(&userObjects); err != nil {
		return fmt.Errorf("inspect template0-derived build database: %w", err)
	}
	if userObjects != 0 {
		return fmt.Errorf("%w: template0-derived build contains user objects", ErrTemplateClusterNotPristine)
	}
	return nil
}

type factsQuerier interface {
	QueryRow(context.Context, string, ...any) pgx.Row
}

func readClusterFacts(ctx context.Context, querier factsQuerier) (clusterFacts, error) {
	var facts clusterFacts
	err := querier.QueryRow(ctx, `
		SELECT
			current_database(),
			current_user,
			(pg_control_system()).system_identifier::text,
			current_setting('server_version'),
			current_setting('server_version_num'),
			pg_encoding_to_char(encoding),
			datcollate,
			datctype,
			datlocprovider::text,
			COALESCE(datlocale, ''),
			COALESCE(datcollversion, '')
		  FROM pg_database
		 WHERE datname = current_database()`).Scan(
		&facts.DatabaseName,
		&facts.CurrentUser,
		&facts.SystemID,
		&facts.ServerVersion,
		&facts.ServerVersionNo,
		&facts.Encoding,
		&facts.Collation,
		&facts.CType,
		&facts.LocaleProvider,
		&facts.Locale,
		&facts.CollationVersion,
	)
	return facts, err
}

func validatePristineTemplateCluster(ctx context.Context, conn *pgx.Conn, currentUser string) error {
	rows, err := conn.Query(ctx, `SELECT datname FROM pg_database ORDER BY datname`)
	if err != nil {
		return fmt.Errorf("inspect template database inventory: %w", err)
	}
	var databases []string
	for rows.Next() {
		var name string
		if scanErr := rows.Scan(&name); scanErr != nil {
			rows.Close()
			return scanErr
		}
		databases = append(databases, name)
	}
	rows.Close()
	if rowsErr := rows.Err(); rowsErr != nil {
		return rowsErr
	}
	wantDatabases := []string{TemplateRootDatabase, "postgres", "template0", "template1"}
	sort.Strings(wantDatabases)
	if !equalStrings(databases, wantDatabases) {
		return fmt.Errorf("%w: database inventory %v", ErrTemplateClusterNotPristine, databases)
	}

	roleRows, err := conn.Query(ctx, `
		SELECT rolname
		  FROM pg_roles
		 WHERE rolname !~ '^pg_'
		 ORDER BY rolname`)
	if err != nil {
		return fmt.Errorf("inspect template role inventory: %w", err)
	}
	var roles []string
	for roleRows.Next() {
		var name string
		if err := roleRows.Scan(&name); err != nil {
			roleRows.Close()
			return err
		}
		roles = append(roles, name)
	}
	roleRows.Close()
	if err := roleRows.Err(); err != nil {
		return err
	}
	if !equalStrings(roles, []string{currentUser}) {
		return fmt.Errorf("%w: non-system role inventory %v", ErrTemplateClusterNotPristine, roles)
	}

	var objectCount int
	if err := conn.QueryRow(ctx, `
		SELECT
			(SELECT count(*) FROM pg_namespace
			  WHERE nspname <> 'public'
			    AND nspname <> 'information_schema'
			    AND nspname !~ '^pg_')
			+
			(SELECT count(*) FROM pg_class c
			  JOIN pg_namespace n ON n.oid = c.relnamespace
			 WHERE n.nspname = 'public')
			+
			(SELECT count(*) FROM pg_proc p
			  JOIN pg_namespace n ON n.oid = p.pronamespace
			 WHERE n.nspname = 'public')
			+
			(SELECT count(*) FROM pg_type t
			  JOIN pg_namespace n ON n.oid = t.typnamespace
			 WHERE n.nspname = 'public')
			+
			(SELECT count(*) FROM pg_extension WHERE extname <> 'plpgsql')`).Scan(&objectCount); err != nil {
		return fmt.Errorf("inspect template root objects: %w", err)
	}
	if objectCount != 0 {
		return fmt.Errorf("%w: maintenance root contains user objects", ErrTemplateClusterNotPristine)
	}
	return nil
}

func readRoleSnapshot(ctx context.Context, conn *pgx.Conn) (roleSnapshot, error) {
	rows, err := conn.Query(ctx, `
		SELECT
			r.rolname,
			concat_ws('|', r.rolsuper, r.rolinherit, r.rolcreaterole, r.rolcreatedb,
				r.rolcanlogin, r.rolreplication, r.rolbypassrls, r.rolconnlimit,
				COALESCE(r.rolvaliduntil::text, ''),
				COALESCE(array_to_string(r.rolconfig, ','), ''),
				COALESCE((
					SELECT string_agg(
						concat_ws(':', parent.rolname, membership.admin_option,
							membership.inherit_option, membership.set_option),
						',' ORDER BY parent.rolname
					)
					  FROM pg_auth_members membership
					  JOIN pg_roles parent ON parent.oid = membership.roleid
					 WHERE membership.member = r.oid
				), ''))
		  FROM pg_roles r
		 ORDER BY r.rolname`)
	if err != nil {
		return roleSnapshot{}, err
	}
	defer rows.Close()
	var snapshot roleSnapshot
	for rows.Next() {
		var name, attributes string
		if err := rows.Scan(&name, &attributes); err != nil {
			return roleSnapshot{}, err
		}
		snapshot.names = append(snapshot.names, name)
		snapshot.rows = append(snapshot.rows, name+"|"+attributes)
	}
	return snapshot, rows.Err()
}

func migrationManifestTip(migrations fs.FS) (string, error) {
	var paths []string
	err := fs.WalkDir(migrations, ".", func(path string, entry fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() && strings.HasSuffix(path, ".up.sql") {
			paths = append(paths, path)
		}
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("enumerate current migration manifest: %w", err)
	}
	if len(paths) == 0 {
		return "", errors.New("current migration manifest is empty")
	}
	sort.Strings(paths)
	return paths[len(paths)-1], nil
}

func equalStrings(left, right []string) bool {
	return slices.Equal(left, right)
}

func addedRoleNames(before, after roleSnapshot) []string {
	existing := make(map[string]struct{}, len(before.names))
	for _, name := range before.names {
		existing[name] = struct{}{}
	}
	var additions []string
	for _, name := range after.names {
		if _, exists := existing[name]; !exists {
			additions = append(additions, name)
		}
	}
	return additions
}

func managedRoleAdditions(before, after roleSnapshot, owner string, ownerCreated bool) []string {
	added := addedRoleNames(before, after)
	allowed := make(map[string]struct{}, len(runtimeRoleNames))
	for _, name := range runtimeRoleNames {
		allowed[name] = struct{}{}
	}
	if ownerCreated && owner != "" {
		allowed[owner] = struct{}{}
	}
	managed := make([]string, 0, len(added))
	for _, name := range added {
		if _, exists := allowed[name]; exists {
			managed = append(managed, name)
		}
	}
	return managed
}

func quoteLiteral(value string) string {
	return "'" + strings.ReplaceAll(value, "'", "''") + "'"
}

func databaseDSN(original, databaseName string) (string, error) {
	parsed, err := url.Parse(original)
	if err != nil || parsed.Scheme == "" {
		return "", errors.New("template PostgreSQL DSN must use URL syntax")
	}
	parsed.Path = "/" + databaseName
	parsed.RawPath = ""
	return parsed.String(), nil
}
