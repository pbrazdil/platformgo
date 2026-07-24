package accounts

import (
	"errors"
	"fmt"
	"sort"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type Currency string

const (
	CurrencyUSDC Currency = "USDC"
	CurrencyUSD  Currency = "USD"
	CurrencyEUR  Currency = "EUR"

	VenueHyperliquid = "HYPERLIQUID"
	VenueFixCFD      = "FIX_CFD"
)

type ErrorKind string

const (
	ErrorBadRequest   ErrorKind = "BAD_REQUEST"
	ErrorForbidden    ErrorKind = "FORBIDDEN"
	ErrorUnauthorized ErrorKind = "UNAUTHORIZED"
	ErrorDenied       ErrorKind = "DENIED"
)

const ReasonOpenOrders = "OPEN_ORDERS"

type AppError struct {
	Kind    ErrorKind
	Reason  string
	Message string
}

func (e *AppError) Error() string {
	if e.Reason != "" {
		return string(e.Kind) + ": " + e.Reason
	}
	return string(e.Kind) + ": " + e.Message
}

func IsAppError(err error, kind ErrorKind) bool {
	var appError *AppError
	return errors.As(err, &appError) && appError.Kind == kind
}

type HTTPStatus int

const (
	StatusOK           HTTPStatus = 200
	StatusCreated      HTTPStatus = 201
	StatusAccepted     HTTPStatus = 202
	StatusBadRequest   HTTPStatus = 400
	StatusUnauthorized HTTPStatus = 401
	StatusForbidden    HTTPStatus = 403
)

type AccountStatus string

const (
	StatusPending   AccountStatus = "pending"
	StatusActive    AccountStatus = "active"
	StatusCloseOnly AccountStatus = "close_only"
	StatusFrozen    AccountStatus = "frozen"
	StatusSuspended AccountStatus = "suspended"
	StatusClosing   AccountStatus = "closing"
	StatusClosed    AccountStatus = "closed"
)

type AccountStatusInfo struct {
	Status        AccountStatus
	CanOpen       bool
	CanClose      bool
	Terminal      bool
	TransitionsTo []AccountStatus
}

func AccountStatusInfos() []AccountStatusInfo {
	return []AccountStatusInfo{
		{Status: StatusPending, TransitionsTo: []AccountStatus{StatusActive, StatusClosed}},
		{
			Status: StatusActive, CanOpen: true, CanClose: true,
			TransitionsTo: []AccountStatus{
				StatusCloseOnly, StatusFrozen, StatusSuspended, StatusClosing, StatusClosed,
			},
		},
		{
			Status: StatusCloseOnly, CanClose: true,
			TransitionsTo: []AccountStatus{StatusActive, StatusFrozen, StatusSuspended, StatusClosing, StatusClosed},
		},
		{
			Status:        StatusFrozen,
			TransitionsTo: []AccountStatus{StatusActive, StatusSuspended, StatusClosed},
		},
		{
			Status: StatusSuspended, CanClose: true,
			TransitionsTo: []AccountStatus{StatusActive, StatusClosed},
		},
		{Status: StatusClosing, CanClose: true, TransitionsTo: []AccountStatus{StatusClosed}},
		{Status: StatusClosed, Terminal: true},
	}
}

func statusInfo(status AccountStatus) (AccountStatusInfo, bool) {
	for _, info := range AccountStatusInfos() {
		if info.Status == status {
			return info, true
		}
	}
	return AccountStatusInfo{}, false
}

type OmsMode string

const (
	OmsModeNetting OmsMode = "NETTING"
	OmsModeHedging OmsMode = "HEDGING"
)

type User struct {
	ID    string
	Login string
}

func (user User) URN() string { return "urn:user:" + user.ID }

type Balance struct {
	Currency Currency
	Total    decimal.Decimal
}

type Account struct {
	ID               string
	Login            int64
	UserID           string
	BaseCurrency     Currency
	MarketVenue      string
	PermittedClasses []string
	Status           AccountStatus
	OmsMode          OmsMode
	Balances         []Balance
	Leverage         map[string]decimal.Decimal
	OpenOrders       map[string]bool
}

func (account Account) URN() string { return "urn:account:" + account.ID }

type Instrument struct {
	Symbol             string
	QuoteCurrency      Currency
	SettlementCurrency Currency
}

type BalanceOpKind string

const (
	BalanceDeposit  BalanceOpKind = "DEPOSIT"
	BalanceWithdraw BalanceOpKind = "WITHDRAW"
)

type SagaStatus string

const (
	SagaCompleted   SagaStatus = "completed"
	SagaCompensated SagaStatus = "compensated"
)

type BalanceCommand struct {
	AccountLogin int64
	Kind         BalanceOpKind
	Amount       decimal.Decimal
}

type BalanceSaga struct {
	Type        string
	BusinessKey int64
	Status      SagaStatus
	LastError   string
	Attempts    int
}

type BalanceFault uint8

const (
	BalanceApplies BalanceFault = iota
	BalanceRejectsPermanently
	BalanceDropsAll
)

type PrincipalKind uint8

const (
	PrincipalAnonymous PrincipalKind = iota
	PrincipalClient
	PrincipalAdmin
	PrincipalBroker
)

type Principal struct {
	Kind        PrincipalKind
	Permissions map[string]bool
	Scopes      map[string]bool
}

func AdminPrincipal(permissions ...string) Principal {
	result := Principal{Kind: PrincipalAdmin, Permissions: make(map[string]bool, len(permissions))}
	for _, permission := range permissions {
		result.Permissions[permission] = true
	}
	return result
}

func BrokerPrincipal(scopes ...string) Principal {
	result := Principal{Kind: PrincipalBroker, Scopes: make(map[string]bool, len(scopes))}
	for _, scope := range scopes {
		result.Scopes[scope] = true
	}
	return result
}

func (h *Harness) Authenticate(principal Principal, password string) HTTPStatus {
	if principal.Kind == PrincipalAnonymous || password == "" {
		return StatusUnauthorized
	}
	return StatusOK
}

type Harness struct {
	nextUserID    int
	nextAccountID int
	users         map[string]User
	accounts      map[string]*Account
	instruments   map[string]Instrument
	supported     map[Currency]bool
	sagas         []BalanceSaga
	received      []BalanceCommand
	balanceFault  BalanceFault
	rejectReason  string
}

func NewHarness() *Harness {
	return NewHarnessWithCurrencies(CurrencyUSDC, CurrencyUSD)
}

func NewHarnessWithCurrencies(currencies ...Currency) *Harness {
	supported := make(map[Currency]bool, len(currencies))
	for _, currency := range currencies {
		supported[currency] = true
	}
	return &Harness{
		users:       make(map[string]User),
		accounts:    make(map[string]*Account),
		instruments: make(map[string]Instrument),
		supported:   supported,
	}
}

func (h *Harness) SupportedCurrencies() []Currency {
	result := make([]Currency, 0, len(h.supported))
	for currency := range h.supported {
		result = append(result, currency)
	}
	sort.Slice(result, func(i, j int) bool {
		order := map[Currency]int{CurrencyUSDC: 0, CurrencyUSD: 1, CurrencyEUR: 2}
		return order[result[i]] < order[result[j]]
	})
	return result
}

func (h *Harness) CreateUser(login string) User {
	h.nextUserID++
	user := User{ID: fmt.Sprintf("user-%03d", h.nextUserID), Login: login}
	h.users[user.ID] = user
	return user
}

func (h *Harness) CreateAccount(userID string, baseCurrency *Currency, venue *string) (*Account, error) {
	if _, exists := h.users[userID]; !exists {
		return nil, &AppError{Kind: ErrorBadRequest, Message: "unknown user"}
	}
	denomination := CurrencyUSDC
	if baseCurrency != nil {
		denomination = *baseCurrency
	}
	if !h.supported[denomination] {
		return nil, &AppError{Kind: ErrorBadRequest, Message: "unsupported base currency " + string(denomination)}
	}
	marketVenue := VenueHyperliquid
	if venue != nil {
		marketVenue = *venue
	}
	h.nextAccountID++
	account := &Account{
		ID: fmt.Sprintf("account-%03d", h.nextAccountID), Login: int64(100000 + h.nextAccountID),
		UserID: userID, BaseCurrency: denomination, MarketVenue: marketVenue,
		PermittedClasses: []string{"CRYPTOCURRENCY"}, Status: StatusPending,
		OmsMode: OmsModeNetting, Leverage: make(map[string]decimal.Decimal),
		OpenOrders: make(map[string]bool),
	}
	if marketVenue == VenueFixCFD {
		account.PermittedClasses = []string{"FX", "CFD"}
	}
	h.accounts[account.ID] = account
	return account, nil
}

func (h *Harness) SeedActiveAccount(login string) (*Account, User, error) {
	user := h.CreateUser(login)
	account, err := h.CreateAccount(user.ID, nil, nil)
	if err != nil {
		return nil, User{}, err
	}
	account.Status = StatusActive
	return account, user, nil
}

func (h *Harness) SeedFundedAccount(login string) (*Account, User, error) {
	account, user, err := h.SeedActiveAccount(login)
	if err != nil {
		return nil, User{}, err
	}
	account.Balances = []Balance{{
		Currency: account.BaseCurrency,
		Total:    decimal.MustParse("1000000000"),
	}}
	return account, user, nil
}

func (h *Harness) ListAccounts(userID *string) []*Account {
	result := make([]*Account, 0)
	for _, account := range h.accounts {
		if userID == nil || account.UserID == *userID {
			result = append(result, account)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Login < result[j].Login })
	return result
}

func (h *Harness) AdminGetAccount(accountID string) (*Account, error) {
	account, exists := h.accounts[accountID]
	if !exists {
		return nil, &AppError{Kind: ErrorBadRequest, Message: "unknown account"}
	}
	return account, nil
}

func (h *Harness) AdminBalances(accountID string) ([]Balance, error) {
	account, err := h.AdminGetAccount(accountID)
	if err != nil {
		return nil, err
	}
	return append([]Balance(nil), account.Balances...), nil
}

func (h *Harness) UpsertInstrument(instrument Instrument) error {
	if !h.supported[instrument.QuoteCurrency] {
		return &AppError{Kind: ErrorBadRequest, Message: "unsupported quote currency " + string(instrument.QuoteCurrency)}
	}
	if !h.supported[instrument.SettlementCurrency] {
		return &AppError{Kind: ErrorBadRequest, Message: "unsupported settlement currency " + string(instrument.SettlementCurrency)}
	}
	h.instruments[instrument.Symbol] = instrument
	return nil
}

func (h *Harness) SetLeverage(principal Principal, accountID, symbol string, leverage decimal.Decimal) (string, error) {
	if principal.Kind != PrincipalAdmin {
		return "", &AppError{Kind: ErrorForbidden, Message: "admin control"}
	}
	account, err := h.AdminGetAccount(accountID)
	if err != nil {
		return "", err
	}
	account.Leverage[symbol] = leverage
	return symbol, nil
}

func (h *Harness) CloseAll(principal Principal, accountID, _ string) ([]string, error) {
	if principal.Kind != PrincipalAdmin {
		return nil, &AppError{Kind: ErrorForbidden, Message: "admin control"}
	}
	if _, err := h.AdminGetAccount(accountID); err != nil {
		return nil, err
	}
	return []string{}, nil
}

func (h *Harness) SetBalanceFault(fault BalanceFault, reason string) {
	h.balanceFault, h.rejectReason = fault, reason
}

func (h *Harness) AdjustBalance(accountID string, kind BalanceOpKind, amountText string) error {
	account, err := h.AdminGetAccount(accountID)
	if err != nil {
		return err
	}
	amount, err := decimal.Parse(amountText)
	if err != nil || amount.Sign() <= 0 {
		return &AppError{Kind: ErrorBadRequest, Message: "invalid amount"}
	}
	maximum := decimal.MustParse("9999999999999999999")
	if amount.Cmp(maximum) > 0 {
		return &AppError{Kind: ErrorBadRequest, Message: "amount exceeds money maximum"}
	}

	saga := BalanceSaga{Type: "balance_op", BusinessKey: account.Login}
	attempts := 1
	if h.balanceFault == BalanceDropsAll {
		attempts = 3
	}
	for range attempts {
		h.received = append(h.received, BalanceCommand{
			AccountLogin: account.Login, Kind: kind, Amount: amount,
		})
	}
	saga.Attempts = attempts
	switch h.balanceFault {
	case BalanceRejectsPermanently:
		saga.Status, saga.LastError = SagaCompensated, h.rejectReason
	case BalanceDropsAll:
		saga.Status, saga.LastError = SagaCompensated, "apply timed out after retries"
	default:
		saga.Status = SagaCompleted
	}
	h.sagas = append(h.sagas, saga)
	return nil
}

func (h *Harness) SagaCount(login int64) int {
	count := 0
	for _, saga := range h.sagas {
		if saga.Type == "balance_op" && saga.BusinessKey == login {
			count++
		}
	}
	return count
}

func (h *Harness) LastSaga(login int64) (BalanceSaga, bool) {
	for index := len(h.sagas) - 1; index >= 0; index-- {
		if h.sagas[index].BusinessKey == login {
			return h.sagas[index], true
		}
	}
	return BalanceSaga{}, false
}

func (h *Harness) ReceivedBalanceCommands() []BalanceCommand {
	return append([]BalanceCommand(nil), h.received...)
}

func (h *Harness) ProvisionAccount(
	principal Principal,
	userID string,
	baseCurrency *Currency,
	venue *string,
) (HTTPStatus, *Account) {
	switch principal.Kind {
	case PrincipalAdmin:
		if !principal.Permissions["accounts:create"] {
			return StatusForbidden, nil
		}
	case PrincipalBroker:
		if !principal.Scopes["accounts:write"] {
			return StatusForbidden, nil
		}
	default:
		return StatusUnauthorized, nil
	}
	account, err := h.CreateAccount(userID, baseCurrency, venue)
	if err != nil {
		return StatusBadRequest, nil
	}
	return StatusCreated, account
}

func (h *Harness) TransitionAccount(
	principal Principal,
	accountID string,
	target AccountStatus,
) HTTPStatus {
	authorized := principal.Kind == PrincipalAdmin && principal.Permissions["accounts:write"] ||
		principal.Kind == PrincipalBroker && principal.Scopes["accounts:write"]
	if !authorized {
		if principal.Kind == PrincipalAnonymous {
			return StatusUnauthorized
		}
		return StatusForbidden
	}
	account, err := h.AdminGetAccount(accountID)
	if err != nil {
		return StatusBadRequest
	}
	info, exists := statusInfo(account.Status)
	if !exists || account.Status == target {
		return StatusBadRequest
	}
	for _, allowed := range info.TransitionsTo {
		if allowed == target {
			account.Status = target
			return StatusOK
		}
	}
	return StatusBadRequest
}

func (h *Harness) SubmitOrder(accountID, intentID string, limit bool) HTTPStatus {
	account, err := h.AdminGetAccount(accountID)
	if err != nil {
		return StatusBadRequest
	}
	info, _ := statusInfo(account.Status)
	if !info.CanOpen {
		return StatusBadRequest
	}
	if limit {
		account.OpenOrders[intentID] = true
	}
	return StatusAccepted
}

func (h *Harness) ClosePosition(accountID string) HTTPStatus {
	account, err := h.AdminGetAccount(accountID)
	if err != nil {
		return StatusBadRequest
	}
	info, _ := statusInfo(account.Status)
	if !info.CanClose {
		return StatusBadRequest
	}
	return StatusOK
}

func (h *Harness) SetOmsMode(accountID string, mode OmsMode) (*Account, error) {
	account, err := h.AdminGetAccount(accountID)
	if err != nil {
		return nil, err
	}
	for _, open := range account.OpenOrders {
		if open {
			return nil, &AppError{Kind: ErrorDenied, Reason: ReasonOpenOrders}
		}
	}
	account.OmsMode = mode
	return account, nil
}

func (h *Harness) CancelOrder(accountID, orderID string) bool {
	account, err := h.AdminGetAccount(accountID)
	if err != nil || !account.OpenOrders[orderID] {
		return false
	}
	account.OpenOrders[orderID] = false
	return true
}
