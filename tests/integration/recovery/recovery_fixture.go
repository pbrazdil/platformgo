package recovery

import (
	"errors"
	"fmt"
	"sort"

	"github.com/upcomers-org/platformgo/internal/decimal"
)

type Balance struct{ Total, Free, Locked decimal.Decimal }
type BalanceOp struct {
	ID, Login        string
	Amount           decimal.Decimal
	Claimed, Applied bool
}
type Position struct {
	ID, Login, Instrument, Quantity, AvgPrice, RealizedPnL string
	OpenedAt, ClosedAt                                     uint64
}
type Order struct {
	ID, BracketID, Leg, Side, Status        string
	Quantity, Filled, Price, MaxSlippageBPS string
	ReduceOnly                              bool
}
type Instrument struct{ ID, Currency string }

type Repository struct {
	Balances    map[string]Balance
	Ops         map[string]*BalanceOp
	Positions   map[string]Position
	Closed      map[string]uint64
	Orders      map[string]Order
	Instruments map[string]Instrument
	Provisioned map[string]bool
}

func NewRepository() *Repository {
	return &Repository{
		Balances: map[string]Balance{}, Ops: map[string]*BalanceOp{},
		Positions: map[string]Position{}, Closed: map[string]uint64{},
		Orders: map[string]Order{}, Instruments: map[string]Instrument{},
		Provisioned: map[string]bool{},
	}
}

type Failpoint uint8

const (
	NoFailpoint Failpoint = iota
	CrashAfterBalanceClaim
)

type Runtime struct{ repo *Repository }

func Restart(repo *Repository) *Runtime {
	r := &Runtime{repo: repo}
	for _, op := range repo.Ops {
		if op.Claimed && !op.Applied {
			r.applyBalance(op)
		}
	}
	return r
}
func (r *Runtime) Deposit(id, login, amount string, fail Failpoint) error {
	if op := r.repo.Ops[id]; op != nil {
		if !op.Applied {
			r.applyBalance(op)
		}
		return nil
	}
	value, err := decimal.Parse(amount)
	if err != nil {
		return err
	}
	op := &BalanceOp{ID: id, Login: login, Amount: value, Claimed: true}
	r.repo.Ops[id] = op
	if fail == CrashAfterBalanceClaim {
		return errors.New("semantic crash after durable claim")
	}
	r.applyBalance(op)
	return nil
}
func (r *Runtime) applyBalance(op *BalanceOp) {
	if op.Applied {
		return
	}
	b := r.repo.Balances[op.Login]
	b.Total = b.Total.Add(op.Amount)
	b.Free = b.Total.Sub(b.Locked)
	r.repo.Balances[op.Login] = b
	op.Applied = true
}
func (r *Runtime) LoadAccounts() map[string]Balance {
	out := map[string]Balance{}
	for login, b := range r.repo.Balances {
		b.Free = b.Total.Sub(b.Locked)
		out[login] = b
	}
	return out
}
func (r *Runtime) LoadPositions(logins map[string]bool) (map[string]Position, error) {
	out := map[string]Position{}
	for id, p := range r.repo.Positions {
		if logins != nil && !logins[p.Login] {
			continue
		}
		if p.RealizedPnL != "" {
			if _, err := decimal.Parse(p.RealizedPnL); err != nil {
				return nil, fmt.Errorf("malformed realized_pnl: %w", err)
			}
		}
		if p.AvgPrice != "" {
			if _, err := decimal.Parse(p.AvgPrice); err != nil {
				return nil, err
			}
		}
		out[id] = p
	}
	return out, nil
}
func (r *Runtime) UsedMargin() decimal.Decimal {
	total := decimal.Decimal{}
	for id, p := range r.repo.Positions {
		if closed, ok := r.repo.Closed[id]; ok && closed >= p.OpenedAt {
			continue
		}
		q, qe := decimal.Parse(p.Quantity)
		px, pe := decimal.Parse(p.AvgPrice)
		if qe == nil && pe == nil {
			total = total.Add(q.Mul(px))
		}
	}
	return total
}
func (r *Runtime) CanOpen() error {
	for id, p := range r.repo.Positions {
		if _, closed := r.repo.Closed[id]; closed {
			continue
		}
		if p.AvgPrice == "" {
			return errors.New("UNPRICED existing position: opening denied fail-closed")
		}
	}
	return nil
}

func (r *Runtime) WorkingOrders() []Order {
	var out []Order
	for _, o := range r.repo.Orders {
		if o.Status != "working" && o.Status != "partially_filled" {
			continue
		}
		if o.Status == "partially_filled" {
			q := decimal.MustParse(o.Quantity)
			f := decimal.MustParse(o.Filled)
			o.Quantity = q.Sub(f).String()
		}
		out = append(out, o)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return out
}

type ParkedSet struct {
	EntryID     string
	EntryFilled bool
	ExitSide    string
	TakeProfits []Order
	StopLoss    *Order
}

func (r *Runtime) ParkedProtectives() []ParkedSet {
	byBracket := map[string][]Order{}
	for _, o := range r.repo.Orders {
		if o.BracketID != "" {
			byBracket[o.BracketID] = append(byBracket[o.BracketID], o)
		}
	}
	var result []ParkedSet
	for _, legs := range byBracket {
		var entry Order
		for _, o := range legs {
			if o.Leg == "entry" {
				entry = o
			}
		}
		if entry.Status == "pending" {
			continue
		}
		set := ParkedSet{EntryID: entry.ID, EntryFilled: entry.Status == "filled", ExitSide: "SELL"}
		for _, o := range legs {
			if o.Leg == "take_profit" && o.Status == "pending" {
				set.TakeProfits = append(set.TakeProfits, o)
			}
			if o.Leg == "stop_loss" && o.Status == "pending" {
				copy := o
				set.StopLoss = &copy
			}
		}
		sort.Slice(set.TakeProfits, func(i, j int) bool { return set.TakeProfits[i].ID < set.TakeProfits[j].ID })
		if len(set.TakeProfits) > 0 || set.StopLoss != nil {
			result = append(result, set)
		}
	}
	return result
}
func (r *Runtime) MarkWorking(id string) {
	o := r.repo.Orders[id]
	o.Status = "working"
	if o.Leg != "entry" {
		o.ReduceOnly = true
	}
	r.repo.Orders[id] = o
}
func (r *Runtime) Fill(id, status, filled string) {
	o := r.repo.Orders[id]
	o.Status = status
	o.Filled = filled
	r.repo.Orders[id] = o
}

var RequiredPositionColumns = []string{"id", "trader_id", "strategy_id", "instrument_id", "account_id", "opening_order_id", "entry", "side", "quantity", "signed_qty", "avg_px_open", "realized_pnl", "ts_opened", "ts_init", "ts_closed"}
var RequiredAccountEventColumns = []string{"account_id", "balances"}

func SchemaColumns() map[string]map[string]bool {
	out := map[string]map[string]bool{"position": {}, "account_event": {}}
	for _, c := range RequiredPositionColumns {
		out["position"][c] = true
	}
	for _, c := range RequiredAccountEventColumns {
		out["account_event"][c] = true
	}
	return out
}
