package identity

import (
	"fmt"
	"strings"
)

const (
	brokerTraderStatusOK           = 200
	brokerTraderStatusCreated      = 201
	brokerTraderStatusBadRequest   = 400
	brokerTraderStatusUnauthorized = 401
	brokerTraderStatusForbidden    = 403
	brokerTraderStatusConflict     = 409
)

type brokerTraderKey struct {
	ID, Token, Name, Owner, Tenant string
	Scopes                         map[string]bool
	IPAllowlist                    map[string]bool
	ExpiresAt                      int64
	HiddenFromAdmin                bool
}

type brokerTraderUser struct {
	ID, Login, Email, Password, Tenant string
}

type brokerTraderAccount struct {
	ID, UserID, Tenant, Status, MarginMode, BaseCurrency string
	Login                                                int64
	Balance                                              string
}

type brokerTraderAccess struct {
	UserID, Audience string
	ExpiresAt        int64
}

type brokerTraderEcho struct {
	Status int
	ID     string
}

type brokerTraderCreatedUser struct {
	Status  int
	ID      string
	Created bool
}

type brokerTraderMintedToken struct {
	Status, ExpiresInSecs int
	AccessToken           string
}

type brokerTraderProfile struct {
	Status                    int
	UserID, Login, Email      string
	KYCStatus, IdentityStatus string
}

type brokerTraderFixture struct {
	now           int64
	nextKey       int
	nextUser      int
	nextAccount   int
	nextEcho      int
	nextAccess    int
	nextTenant    int
	maxKeys       int
	keys          map[string]*brokerTraderKey
	users         map[string]*brokerTraderUser
	userByEmail   map[string]string
	accounts      map[string]*brokerTraderAccount
	access        map[string]brokerTraderAccess
	idempotency   map[string]brokerTraderEcho
	ownerKeyCount map[string]int
}

func brokerTraderNewFixture() *brokerTraderFixture {
	return &brokerTraderFixture{
		now:           1_000,
		maxKeys:       10,
		keys:          make(map[string]*brokerTraderKey),
		users:         make(map[string]*brokerTraderUser),
		userByEmail:   make(map[string]string),
		accounts:      make(map[string]*brokerTraderAccount),
		access:        make(map[string]brokerTraderAccess),
		idempotency:   make(map[string]brokerTraderEcho),
		ownerKeyCount: make(map[string]int),
	}
}

func (fixture *brokerTraderFixture) createTenant(slug string) string {
	fixture.nextTenant++
	return fmt.Sprintf("tenant-%d-%s", fixture.nextTenant, slug)
}

func (fixture *brokerTraderFixture) createBrokerKey(name string, scopes, allowlist []string, ttl int64, tenant string) string {
	fixture.nextKey++
	token := fmt.Sprintf("xbk_key%d.secret", fixture.nextKey)
	key := &brokerTraderKey{
		ID:          fmt.Sprintf("key-%d", fixture.nextKey),
		Token:       token,
		Name:        name,
		Owner:       "broker",
		Tenant:      tenant,
		Scopes:      make(map[string]bool),
		IPAllowlist: make(map[string]bool),
	}
	for _, scope := range scopes {
		key.Scopes[scope] = true
	}
	for _, ip := range allowlist {
		key.IPAllowlist[ip] = true
	}
	if ttl > 0 {
		key.ExpiresAt = fixture.now + ttl
	}
	fixture.keys[token] = key
	return token
}

func (fixture *brokerTraderFixture) authenticateBroker(token, sourceIP string) (*brokerTraderKey, int) {
	key, ok := fixture.keys[token]
	if !ok || (key.ExpiresAt > 0 && fixture.now >= key.ExpiresAt) {
		return nil, brokerTraderStatusUnauthorized
	}
	if len(key.IPAllowlist) > 0 && !key.IPAllowlist[sourceIP] {
		return nil, brokerTraderStatusUnauthorized
	}
	return key, brokerTraderStatusOK
}

func (fixture *brokerTraderFixture) brokerPing(token, sourceIP string) int {
	_, status := fixture.authenticateBroker(token, sourceIP)
	return status
}

func (fixture *brokerTraderFixture) brokerEcho(token, idempotencyKey string) brokerTraderEcho {
	key, status := fixture.authenticateBroker(token, "")
	if status != brokerTraderStatusOK {
		return brokerTraderEcho{Status: status}
	}
	cacheKey := key.ID + ":echo:" + idempotencyKey
	if idempotencyKey != "" {
		if response, ok := fixture.idempotency[cacheKey]; ok {
			return response
		}
	}
	fixture.nextEcho++
	response := brokerTraderEcho{Status: brokerTraderStatusOK, ID: fmt.Sprintf("echo-%d", fixture.nextEcho)}
	if idempotencyKey != "" {
		fixture.idempotency[cacheKey] = response
	}
	return response
}

func (fixture *brokerTraderFixture) hasScope(key *brokerTraderKey, scope string) bool {
	return key.Scopes["*"] || key.Scopes[scope]
}

func (fixture *brokerTraderFixture) createUserForTenant(tenant, login, email, password string) brokerTraderCreatedUser {
	normalizedEmail := strings.ToLower(email)
	index := tenant + ":" + normalizedEmail
	if userID, ok := fixture.userByEmail[index]; ok {
		return brokerTraderCreatedUser{Status: brokerTraderStatusCreated, ID: userID, Created: false}
	}
	fixture.nextUser++
	id := fmt.Sprintf("urn:xb:user:%d", fixture.nextUser)
	fixture.users[id] = &brokerTraderUser{
		ID: id, Login: login, Email: normalizedEmail, Password: password, Tenant: tenant,
	}
	fixture.userByEmail[index] = id
	return brokerTraderCreatedUser{Status: brokerTraderStatusCreated, ID: id, Created: true}
}

func (fixture *brokerTraderFixture) brokerCreateUser(token, login, email string) brokerTraderCreatedUser {
	key, status := fixture.authenticateBroker(token, "")
	if status != brokerTraderStatusOK {
		return brokerTraderCreatedUser{Status: status}
	}
	if !fixture.hasScope(key, "accounts:write") {
		return brokerTraderCreatedUser{Status: brokerTraderStatusForbidden}
	}
	return fixture.createUserForTenant(key.Tenant, login, email, "")
}

func (fixture *brokerTraderFixture) createAccountForTenant(tenant, userID string) (*brokerTraderAccount, int) {
	user, ok := fixture.users[userID]
	if !ok || user.Tenant != tenant {
		return nil, brokerTraderStatusBadRequest
	}
	fixture.nextAccount++
	account := &brokerTraderAccount{
		ID:           fmt.Sprintf("urn:xb:account:%d", fixture.nextAccount),
		UserID:       userID,
		Tenant:       tenant,
		Status:       "active",
		MarginMode:   "cross",
		BaseCurrency: "USDC",
		Login:        int64(1000 + fixture.nextAccount),
		Balance:      "0",
	}
	fixture.accounts[account.ID] = account
	return account, brokerTraderStatusCreated
}

func (fixture *brokerTraderFixture) brokerCreateAccount(token, userID, idempotencyKey string) (*brokerTraderAccount, int) {
	key, status := fixture.authenticateBroker(token, "")
	if status != brokerTraderStatusOK {
		return nil, status
	}
	if !fixture.hasScope(key, "accounts:write") {
		return nil, brokerTraderStatusForbidden
	}
	cacheKey := key.ID + ":accounts:" + idempotencyKey
	if idempotencyKey != "" {
		if response, ok := fixture.idempotency[cacheKey]; ok {
			return fixture.accounts[response.ID], response.Status
		}
	}
	account, status := fixture.createAccountForTenant(key.Tenant, userID)
	if status == brokerTraderStatusCreated && idempotencyKey != "" {
		fixture.idempotency[cacheKey] = brokerTraderEcho{Status: status, ID: account.ID}
	}
	return account, status
}

func (fixture *brokerTraderFixture) exchangeOnBehalfOf(token, userURN string, ttl int) brokerTraderMintedToken {
	key, status := fixture.authenticateBroker(token, "")
	if status != brokerTraderStatusOK {
		return brokerTraderMintedToken{Status: status}
	}
	if !fixture.hasScope(key, "tokens:mint") {
		return brokerTraderMintedToken{Status: brokerTraderStatusForbidden}
	}
	user, ok := fixture.users[userURN]
	if !ok || user.Tenant != key.Tenant {
		return brokerTraderMintedToken{Status: brokerTraderStatusBadRequest}
	}
	if ttl == 0 {
		ttl = 300
	}
	fixture.nextAccess++
	accessToken := fmt.Sprintf("access-client-%d", fixture.nextAccess)
	fixture.access[accessToken] = brokerTraderAccess{
		UserID: user.ID, Audience: "client", ExpiresAt: fixture.now + int64(ttl),
	}
	return brokerTraderMintedToken{
		Status: brokerTraderStatusOK, ExpiresInSecs: ttl, AccessToken: accessToken,
	}
}

func (fixture *brokerTraderFixture) login(login, password, audience string) (string, int) {
	for _, user := range fixture.users {
		if user.Login == login && user.Password == password {
			fixture.nextAccess++
			token := fmt.Sprintf("access-%s-%d", audience, fixture.nextAccess)
			fixture.access[token] = brokerTraderAccess{UserID: user.ID, Audience: audience}
			return token, brokerTraderStatusOK
		}
	}
	return "", brokerTraderStatusUnauthorized
}

func (fixture *brokerTraderFixture) myProfile(token string) brokerTraderProfile {
	access, ok := fixture.access[token]
	if !ok || access.Audience != "client" || (access.ExpiresAt > 0 && fixture.now >= access.ExpiresAt) {
		return brokerTraderProfile{Status: brokerTraderStatusUnauthorized}
	}
	user := fixture.users[access.UserID]
	return brokerTraderProfile{
		Status: brokerTraderStatusOK, UserID: user.ID, Login: user.Login, Email: user.Email,
		KYCStatus: "none", IdentityStatus: "active",
	}
}

func (fixture *brokerTraderFixture) brokerPrincipal(tenant string) string {
	token := fixture.createBrokerKey("tenant-principal", []string{"*"}, nil, 0, tenant)
	fixture.keys[token].HiddenFromAdmin = true
	return token
}

func (fixture *brokerTraderFixture) brokerListAccounts(token string) ([]brokerTraderAccount, int) {
	key, status := fixture.authenticateBroker(token, "")
	if status != brokerTraderStatusOK {
		return nil, status
	}
	accounts := make([]brokerTraderAccount, 0)
	for _, account := range fixture.accounts {
		if account.Tenant == key.Tenant {
			accounts = append(accounts, *account)
		}
	}
	return accounts, brokerTraderStatusOK
}

func (fixture *brokerTraderFixture) brokerGetAccount(token, accountID string) (*brokerTraderAccount, int) {
	key, status := fixture.authenticateBroker(token, "")
	if status != brokerTraderStatusOK {
		return nil, status
	}
	account, ok := fixture.accounts[accountID]
	if !ok || account.Tenant != key.Tenant {
		return nil, brokerTraderStatusBadRequest
	}
	return account, brokerTraderStatusOK
}

func (fixture *brokerTraderFixture) brokerAdjustBalance(token, accountID, amount string) int {
	account, status := fixture.brokerGetAccount(token, accountID)
	if status != brokerTraderStatusOK {
		return status
	}
	account.Balance = amount
	return brokerTraderStatusOK
}

func (fixture *brokerTraderFixture) adminUsers(tenant string) []brokerTraderUser {
	users := make([]brokerTraderUser, 0)
	for _, user := range fixture.users {
		if tenant == "" || user.Tenant == tenant {
			users = append(users, *user)
		}
	}
	return users
}

func (fixture *brokerTraderFixture) adminAccounts(tenant string) []brokerTraderAccount {
	accounts := make([]brokerTraderAccount, 0)
	for _, account := range fixture.accounts {
		if tenant == "" || account.Tenant == tenant {
			accounts = append(accounts, *account)
		}
	}
	return accounts
}

func (fixture *brokerTraderFixture) adminKeys(tenant string) []brokerTraderKey {
	keys := make([]brokerTraderKey, 0)
	for _, key := range fixture.keys {
		if !key.HiddenFromAdmin && (tenant == "" || key.Tenant == tenant) {
			keys = append(keys, *key)
		}
	}
	return keys
}

func brokerTraderTenantChannel(tenant string) string {
	return "tenant:" + tenant
}

func (fixture *brokerTraderFixture) listMyAccounts(token string) ([]brokerTraderAccount, int) {
	access, ok := fixture.access[token]
	if !ok || access.Audience != "client" {
		return nil, brokerTraderStatusUnauthorized
	}
	accounts := make([]brokerTraderAccount, 0)
	for _, account := range fixture.accounts {
		if account.UserID == access.UserID {
			accounts = append(accounts, *account)
		}
	}
	return accounts, brokerTraderStatusOK
}

func (fixture *brokerTraderFixture) createMyAPIKey(token, name string, scopes []string) (string, int) {
	access, ok := fixture.access[token]
	if !ok || access.Audience != "client" {
		return "", brokerTraderStatusUnauthorized
	}
	if fixture.ownerKeyCount[access.UserID] >= fixture.maxKeys {
		return "", brokerTraderStatusConflict
	}
	fixture.ownerKeyCount[access.UserID]++
	created := fixture.createBrokerKey(name, scopes, nil, 0, fixture.users[access.UserID].Tenant)
	fixture.keys[created].Owner = access.UserID
	return created, brokerTraderStatusCreated
}
