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

	templateHarnessVersion      = "1"
	templateRoleManifestVersion = "runtime-roles-v1"
	templateAdvisoryLockKey     = int64(0x5047544d504c5631)
)

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
}

type TemplateDatabaseManager struct {
	mu sync.Mutex

	rootConn       *pgx.Conn
	primaryPool    *pgxpool.Pool
	templateConfig *pgx.ConnConfig
	config         TemplateDatabaseConfig
	templateName   string
	templateMarker string
	templateOwner  string
	clusterFacts   clusterFacts
	initialRoles   roleSnapshot
	baselineRoles  roleSnapshot
	ownedRoles     []string
	clones         map[string]*TemplateDatabase
	closed         bool
	poisoned       error
}

type TemplateDatabase struct {
	manager *TemplateDatabaseManager
	name    string
	dsn     string
	marker  string
	once    sync.Once
	err     error
}

func (database *TemplateDatabase) DSN() string { return database.dsn }

func (database *TemplateDatabase) Close(ctx context.Context) error {
	database.once.Do(func() {
		database.err = database.manager.dropClone(ctx, database.name)
	})
	return database.err
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

func NewTemplateDatabaseManager(
	ctx context.Context,
	config TemplateDatabaseConfig,
	prepare func(context.Context, *pgxpool.Pool) error,
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

	lockedFacts, err := readClusterFacts(ctx, rootConn)
	if err != nil {
		return nil, fmt.Errorf("recheck locked template cluster: %w", err)
	}
	if lockedFacts.SystemID == primaryFacts.SystemID || lockedFacts.SystemID != templateFacts.SystemID {
		return nil, fmt.Errorf("%w: system identifier changed while acquiring lock", ErrTemplateClusterNotDedicated)
	}
	if pristineErr := validatePristineTemplateCluster(ctx, rootConn, lockedFacts.CurrentUser); pristineErr != nil {
		return nil, pristineErr
	}
	initialRoles, err := readRoleSnapshot(ctx, rootConn)
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
	templateName, _, err := TemplateDatabaseNames(digest, "manager")
	if err != nil {
		return nil, err
	}
	marker := "platformgo-template:v1:" + hex.EncodeToString(digest[:]) + ":" + config.Caller + ":" + config.Profile

	manager := &TemplateDatabaseManager{
		rootConn:       rootConn,
		primaryPool:    primaryPool,
		templateConfig: templateConfig,
		config:         config,
		templateName:   templateName,
		templateMarker: marker,
		templateOwner:  lockedFacts.CurrentUser,
		clusterFacts:   lockedFacts,
		initialRoles:   initialRoles,
		clones:         make(map[string]*TemplateDatabase),
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
	baselineRoles, err := readRoleSnapshot(ctx, rootConn)
	if err != nil {
		cleanupErr := manager.cleanupFailedBuild(cleanupCtx)
		cleanupPending = false
		return nil, errors.Join(fmt.Errorf("capture prepared template roles: %w", err), cleanupErr)
	}
	manager.baselineRoles = baselineRoles
	manager.ownedRoles = addedRoleNames(initialRoles, baselineRoles)
	if !equalStrings(manager.ownedRoles, runtimeRoleNames) {
		cleanupErr := manager.cleanupFailedBuild(cleanupCtx)
		cleanupPending = false
		return nil, errors.Join(
			fmt.Errorf("%w: prepared role inventory is outside the exact runtime-role manifest", ErrTemplateRoleDrift),
			cleanupErr,
		)
	}

	closePrimary = false
	closeRoot = false
	unlockOnFailure = false
	cleanupPending = false
	return manager, nil
}

func (manager *TemplateDatabaseManager) buildTemplate(
	ctx context.Context,
	prepare func(context.Context, *pgxpool.Pool) error,
) error {
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
		"CREATE DATABASE "+identifier+" TEMPLATE template0",
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
	prepareErr := prepare(ctx, buildPool)
	buildPool.Close()
	if prepareErr != nil {
		return fmt.Errorf("prepare current template database: %w", prepareErr)
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
		"CREATE DATABASE "+cloneIdentifier+" TEMPLATE "+templateIdentifier,
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
	manager.closed = true
	var result error
	if manager.poisoned != nil {
		result = errors.Join(result, manager.poisoned)
	}
	if err := manager.validateRoles(ctx); err != nil {
		result = errors.Join(result, err)
	}

	names := make([]string, 0, len(manager.clones))
	for name := range manager.clones {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		database := manager.clones[name]
		exists, owner, marker, err := manager.databaseState(ctx, name)
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
		if err := manager.dropTemplate(ctx); err != nil {
			result = errors.Join(result, err)
		} else if err := manager.restoreInitialRoles(ctx); err != nil {
			result = errors.Join(result, err)
		} else if err := validatePristineTemplateCluster(ctx, manager.rootConn, manager.templateOwner); err != nil {
			result = errors.Join(result, err)
		}
	}
	if _, err := manager.rootConn.Exec(ctx, `SELECT pg_advisory_unlock($1)`, templateAdvisoryLockKey); err != nil {
		result = errors.Join(result, fmt.Errorf("release template cluster lock: %w", err))
	}
	if err := manager.rootConn.Close(ctx); err != nil {
		result = errors.Join(result, fmt.Errorf("close template cluster connection: %w", err))
	}
	manager.primaryPool.Close()
	return result
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
	currentRoles, err := readRoleSnapshot(ctx, manager.rootConn)
	if err != nil {
		result = errors.Join(result, fmt.Errorf("inspect roles after failed template build: %w", err))
	} else {
		manager.ownedRoles = managedRoleAdditions(manager.initialRoles, currentRoles)
	}
	if err := manager.restoreInitialRoles(ctx); err != nil {
		result = errors.Join(result, err)
	}
	return result
}

func (manager *TemplateDatabaseManager) validateRoles(ctx context.Context) error {
	current, err := readRoleSnapshot(ctx, manager.rootConn)
	if err != nil {
		return fmt.Errorf("inspect template cluster roles: %w", err)
	}
	if !equalStrings(current.rows, manager.baselineRoles.rows) {
		return fmt.Errorf("%w: expected prepared role manifest", ErrTemplateRoleDrift)
	}
	return nil
}

func (manager *TemplateDatabaseManager) restoreInitialRoles(ctx context.Context) error {
	current, err := readRoleSnapshot(ctx, manager.rootConn)
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
	if len(ownedPresent) > 0 {
		identifiers := make([]string, 0, len(ownedPresent))
		for _, name := range ownedPresent {
			identifiers = append(identifiers, pgx.Identifier{name}.Sanitize())
		}
		if _, dropErr := manager.rootConn.Exec(ctx, "DROP ROLE "+strings.Join(identifiers, ", ")); dropErr != nil {
			return fmt.Errorf("restore initial template roles: %w", dropErr)
		}
	}
	final, err := readRoleSnapshot(ctx, manager.rootConn)
	if err != nil {
		return err
	}
	if !equalStrings(final.rows, manager.initialRoles.rows) {
		return fmt.Errorf("%w: cleanup did not restore initial roles", ErrTemplateRoleDrift)
	}
	return nil
}

func (manager *TemplateDatabaseManager) databaseState(
	ctx context.Context,
	name string,
) (bool, string, string, error) {
	var owner, marker string
	err := manager.rootConn.QueryRow(ctx, `
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
		var count int
		if err := manager.rootConn.QueryRow(ctx, `
			SELECT count(*)
			  FROM pg_stat_activity
			 WHERE datname = $1
			   AND pid <> pg_backend_pid()`, name).Scan(&count); err != nil {
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
	err := manager.rootConn.QueryRow(ctx, `
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

func managedRoleAdditions(before, after roleSnapshot) []string {
	added := addedRoleNames(before, after)
	allowed := make(map[string]struct{}, len(runtimeRoleNames))
	for _, name := range runtimeRoleNames {
		allowed[name] = struct{}{}
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
