package operations

import (
	"context"
	"encoding/json"
	"errors"
	"net/netip"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type MonitorBet struct {
	BetID         string          `json:"bet_id"`
	UserID        int64           `json:"user_id"`
	LoginName     string          `json:"login_name"`
	DisplayName   string          `json:"display_name"`
	GameType      string          `json:"game_type"`
	RoundSequence int64           `json:"round_sequence"`
	Currency      string          `json:"currency"`
	Selection     json.RawMessage `json:"selection"`
	StakeMinor    int64           `json:"stake_minor"`
	Status        string          `json:"status"`
	CreatedAt     time.Time       `json:"created_at"`
}
type MonitorRound struct {
	GameType    string    `json:"game_type"`
	Name        string    `json:"name"`
	Sequence    int64     `json:"sequence"`
	Status      string    `json:"status"`
	BetClosesAt time.Time `json:"bet_closes_at"`
	ResultAt    time.Time `json:"result_at"`
}
type Monitor struct {
	ServerTime time.Time      `json:"server_time"`
	Bets       []MonitorBet   `json:"bets"`
	Rounds     []MonitorRound `json:"rounds"`
}

func (s *Service) Monitor(ctx context.Context, userQuery, gameType string, limit int) (Monitor, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	result := Monitor{ServerTime: time.Now().UTC(), Bets: []MonitorBet{}, Rounds: []MonitorRound{}}
	rows, err := s.pool.Query(ctx, `SELECT b.id::text,u.public_id,COALESCE(u.login_name,''),u.display_name,gt.code,r.sequence,w.currency,b.selection,b.stake_minor,b.status,b.created_at FROM bets b JOIN users u ON u.id=b.user_id JOIN wallets w ON w.id=b.wallet_id JOIN rounds r ON r.id=b.round_id JOIN game_types gt ON gt.id=r.game_type_id WHERE b.status='accepted' AND ($1='' OR gt.code=$1) AND ($2='' OR u.public_id::text=$2 OR u.login_name ILIKE '%'||$2||'%') ORDER BY b.created_at DESC LIMIT $3`, gameType, userQuery, limit)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var v MonitorBet
		if err := rows.Scan(&v.BetID, &v.UserID, &v.LoginName, &v.DisplayName, &v.GameType, &v.RoundSequence, &v.Currency, &v.Selection, &v.StakeMinor, &v.Status, &v.CreatedAt); err != nil {
			return result, err
		}
		result.Bets = append(result.Bets, v)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	rounds, err := s.pool.Query(ctx, `SELECT DISTINCT ON (gt.id) gt.code,gt.name,r.sequence,r.status,r.bet_closes_at,COALESCE(r.result_at,r.bet_closes_at) FROM rounds r JOIN game_types gt ON gt.id=r.game_type_id WHERE gt.enabled=true AND r.status IN ('open','closed') ORDER BY gt.id,r.sequence`)
	if err != nil {
		return result, err
	}
	defer rounds.Close()
	for rounds.Next() {
		var v MonitorRound
		if err := rounds.Scan(&v.GameType, &v.Name, &v.Sequence, &v.Status, &v.BetClosesAt, &v.ResultAt); err != nil {
			return result, err
		}
		result.Rounds = append(result.Rounds, v)
	}
	return result, rounds.Err()
}

type PlayerStatistic struct {
	UserID       int64  `json:"user_id"`
	LoginName    string `json:"login_name"`
	DisplayName  string `json:"display_name"`
	BetCount     int64  `json:"bet_count"`
	StakeMinor   int64  `json:"stake_minor"`
	PayoutMinor  int64  `json:"payout_minor"`
	DepositMinor int64  `json:"deposit_minor"`
	CreditMinor  int64  `json:"credit_minor"`
	BalanceMinor int64  `json:"balance_minor"`
}
type CurrencyStatistic struct {
	Currency     string `json:"currency"`
	BetCount     int64  `json:"bet_count"`
	StakeMinor   int64  `json:"stake_minor"`
	PayoutMinor  int64  `json:"payout_minor"`
	DepositMinor int64  `json:"deposit_minor"`
	CreditMinor  int64  `json:"credit_minor"`
	BalanceMinor int64  `json:"balance_minor"`
}
type Dashboard struct {
	ServerTime time.Time           `json:"server_time"`
	Players    []PlayerStatistic   `json:"players"`
	Global     []CurrencyStatistic `json:"global"`
}

func (s *Service) Dashboard(ctx context.Context, userQuery string, from, to time.Time, limit int) (Dashboard, error) {
	if from.IsZero() {
		from = time.Now().UTC().AddDate(0, 0, -1)
	}
	if to.IsZero() {
		to = time.Now().UTC()
	}
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	result := Dashboard{ServerTime: time.Now().UTC(), Players: []PlayerStatistic{}, Global: []CurrencyStatistic{}}
	rows, err := s.pool.Query(ctx, `SELECT u.public_id,COALESCE(u.login_name,''),u.display_name,count(DISTINCT b.id),COALESCE(sum(b.stake_minor),0),COALESCE(sum(b.payout_minor),0),COALESCE((SELECT sum(d.amount_minor) FROM deposits d JOIN chain_addresses ca ON ca.id=d.chain_address_id WHERE ca.user_id=u.id AND d.status='credited' AND d.confirmed_at BETWEEN $2 AND $3),0),COALESCE((SELECT sum(le.amount_minor) FROM ledger_entries le JOIN wallets cw ON cw.id=le.wallet_id WHERE cw.user_id=u.id AND le.business_type='admin_credit' AND le.occurred_at BETWEEN $2 AND $3),0),COALESCE((SELECT sum(w2.available_minor+w2.frozen_minor) FROM wallets w2 WHERE w2.user_id=u.id),0) FROM users u LEFT JOIN bets b ON b.user_id=u.id AND b.created_at BETWEEN $2 AND $3 WHERE u.is_virtual=false AND ($1='' OR u.public_id::text=$1 OR u.login_name ILIKE '%'||$1||'%') GROUP BY u.id ORDER BY COALESCE(sum(b.stake_minor),0) DESC LIMIT $4`, userQuery, from, to, limit)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var v PlayerStatistic
		if err := rows.Scan(&v.UserID, &v.LoginName, &v.DisplayName, &v.BetCount, &v.StakeMinor, &v.PayoutMinor, &v.DepositMinor, &v.CreditMinor, &v.BalanceMinor); err != nil {
			return result, err
		}
		result.Players = append(result.Players, v)
	}
	if err := rows.Err(); err != nil {
		return result, err
	}
	g, err := s.pool.Query(ctx, `SELECT c.currency,
		(SELECT count(*) FROM bets b JOIN wallets w ON w.id=b.wallet_id JOIN users u ON u.id=b.user_id WHERE u.is_virtual=false AND w.currency=c.currency AND b.created_at BETWEEN $1 AND $2),
		COALESCE((SELECT sum(b.stake_minor) FROM bets b JOIN wallets w ON w.id=b.wallet_id JOIN users u ON u.id=b.user_id WHERE u.is_virtual=false AND w.currency=c.currency AND b.created_at BETWEEN $1 AND $2),0),
		COALESCE((SELECT sum(b.payout_minor) FROM bets b JOIN wallets w ON w.id=b.wallet_id JOIN users u ON u.id=b.user_id WHERE u.is_virtual=false AND w.currency=c.currency AND b.created_at BETWEEN $1 AND $2),0),
		COALESCE((SELECT sum(le.amount_minor) FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id JOIN users u ON u.id=w.user_id WHERE u.is_virtual=false AND w.currency=c.currency AND le.business_type='deposit' AND le.occurred_at BETWEEN $1 AND $2),0),
		COALESCE((SELECT sum(le.amount_minor) FROM ledger_entries le JOIN wallets w ON w.id=le.wallet_id JOIN users u ON u.id=w.user_id WHERE u.is_virtual=false AND w.currency=c.currency AND le.business_type='admin_credit' AND le.occurred_at BETWEEN $1 AND $2),0),
		COALESCE((SELECT sum(w.available_minor+w.frozen_minor) FROM wallets w JOIN users u ON u.id=w.user_id WHERE u.is_virtual=false AND w.currency=c.currency),0)
	FROM (SELECT DISTINCT currency FROM wallets) c ORDER BY c.currency`, from, to)
	if err != nil {
		return result, err
	}
	defer g.Close()
	for g.Next() {
		var v CurrencyStatistic
		if err := g.Scan(&v.Currency, &v.BetCount, &v.StakeMinor, &v.PayoutMinor, &v.DepositMinor, &v.CreditMinor, &v.BalanceMinor); err != nil {
			return result, err
		}
		result.Global = append(result.Global, v)
	}
	return result, g.Err()
}

type LoginIP struct {
	IP         string        `json:"ip"`
	FirstSeen  time.Time     `json:"first_seen"`
	LastSeen   time.Time     `json:"last_seen"`
	LoginCount int64         `json:"login_count"`
	Users      []LoginIPUser `json:"users,omitempty"`
}
type LoginIPUser struct {
	UserID      int64     `json:"user_id"`
	LoginName   string    `json:"login_name"`
	DisplayName string    `json:"display_name"`
	LastSeen    time.Time `json:"last_seen"`
	LoginCount  int64     `json:"login_count"`
}

func (s *Service) RecordLogin(ctx context.Context, userID, ip, audience string) error {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return err
	}
	_, err = s.pool.Exec(ctx, `INSERT INTO user_login_history(id,user_id,ip_address,audience) VALUES($1,$2,$3,$4)`, uuid.NewString(), userID, addr.String(), audience)
	return err
}
func (s *Service) UserLoginIPs(ctx context.Context, publicID int64) ([]LoginIP, error) {
	rows, err := s.pool.Query(ctx, `SELECT h.ip_address::text,min(h.logged_in_at),max(h.logged_in_at),count(*) FROM user_login_history h JOIN users u ON u.id=h.user_id WHERE u.public_id=$1 GROUP BY h.ip_address ORDER BY max(h.logged_in_at) DESC`, publicID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LoginIP{}
	for rows.Next() {
		var v LoginIP
		if err := rows.Scan(&v.IP, &v.FirstSeen, &v.LastSeen, &v.LoginCount); err != nil {
			return nil, err
		}
		users, err := s.UsersByLoginIP(ctx, v.IP)
		if err != nil {
			return nil, err
		}
		v.Users = users
		out = append(out, v)
	}
	return out, rows.Err()
}
func (s *Service) UsersByLoginIP(ctx context.Context, ip string) ([]LoginIPUser, error) {
	addr, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return nil, err
	}
	rows, err := s.pool.Query(ctx, `SELECT u.public_id,COALESCE(u.login_name,''),u.display_name,max(h.logged_in_at),count(*) FROM user_login_history h JOIN users u ON u.id=h.user_id WHERE h.ip_address=$1 GROUP BY u.id ORDER BY max(h.logged_in_at) DESC`, addr.String())
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []LoginIPUser{}
	for rows.Next() {
		var v LoginIPUser
		if err := rows.Scan(&v.UserID, &v.LoginName, &v.DisplayName, &v.LastSeen, &v.LoginCount); err != nil {
			return nil, err
		}
		out = append(out, v)
	}
	return out, rows.Err()
}

type VirtualAccount struct {
	UserID          int64    `json:"user_id"`
	LoginName       string   `json:"login_name"`
	DisplayName     string   `json:"display_name"`
	Enabled         bool     `json:"automation_enabled"`
	Currency        string   `json:"currency"`
	StakeMinor      int64    `json:"stake_minor"`
	GameTypeCodes   []string `json:"game_type_codes"`
	IntervalSeconds int      `json:"interval_seconds"`
}
type VirtualAccountInput struct {
	LoginName       string           `json:"login_name"`
	DisplayName     string           `json:"display_name"`
	InitialBalances map[string]int64 `json:"initial_balances"`
}
type VirtualAutomationInput struct {
	Enabled         bool     `json:"enabled"`
	Currency        string   `json:"currency"`
	StakeMinor      int64    `json:"stake_minor"`
	GameTypeCodes   []string `json:"game_type_codes"`
	IntervalSeconds int      `json:"interval_seconds"`
}

var ErrInvalidVirtualAccount = errors.New("invalid virtual account")

func (s *Service) CreateVirtualAccount(ctx context.Context, in VirtualAccountInput) (VirtualAccount, error) {
	in.LoginName = strings.TrimSpace(in.LoginName)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	if in.LoginName == "" || in.DisplayName == "" {
		return VirtualAccount{}, ErrInvalidVirtualAccount
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VirtualAccount{}, err
	}
	defer tx.Rollback(ctx)
	id := uuid.NewString()
	if _, err = tx.Exec(ctx, `INSERT INTO users(id,login_name,display_name,is_virtual) VALUES($1,$2,$3,true)`, id, in.LoginName, in.DisplayName); err != nil {
		return VirtualAccount{}, err
	}
	for _, currency := range []string{"USDT", "POINTS", "JADE", "ORIGIN_STONE", "STAMINA"} {
		amount := in.InitialBalances[currency]
		if amount < 0 {
			return VirtualAccount{}, ErrInvalidVirtualAccount
		}
		if _, err = tx.Exec(ctx, `INSERT INTO wallets(id,user_id,currency,available_minor) VALUES($1,$2,$3,$4)`, uuid.NewString(), id, currency, amount); err != nil {
			return VirtualAccount{}, err
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO virtual_account_automations(user_id) VALUES($1)`, id); err != nil {
		return VirtualAccount{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return VirtualAccount{}, err
	}
	return s.virtualByID(ctx, id)
}
func (s *Service) virtualByID(ctx context.Context, id string) (VirtualAccount, error) {
	var v VirtualAccount
	err := s.pool.QueryRow(ctx, `SELECT u.public_id,COALESCE(u.login_name,''),u.display_name,a.enabled,a.currency,a.stake_minor,a.game_type_codes,a.interval_seconds FROM users u JOIN virtual_account_automations a ON a.user_id=u.id WHERE u.id=$1 AND u.is_virtual=true`, id).Scan(&v.UserID, &v.LoginName, &v.DisplayName, &v.Enabled, &v.Currency, &v.StakeMinor, &v.GameTypeCodes, &v.IntervalSeconds)
	return v, err
}
func (s *Service) SetVirtualAutomation(ctx context.Context, publicID int64, in VirtualAutomationInput) (VirtualAccount, error) {
	if in.StakeMinor <= 0 || in.IntervalSeconds < 3 || in.IntervalSeconds > 86400 || strings.TrimSpace(in.Currency) == "" {
		return VirtualAccount{}, ErrInvalidVirtualAccount
	}
	var id string
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1 AND is_virtual=true`, publicID).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return VirtualAccount{}, ErrUserNotFound
	} else if err != nil {
		return VirtualAccount{}, err
	}
	_, err := s.pool.Exec(ctx, `UPDATE virtual_account_automations SET enabled=$2,currency=$3,stake_minor=$4,game_type_codes=$5,interval_seconds=$6,updated_at=now() WHERE user_id=$1`, id, in.Enabled, strings.ToUpper(in.Currency), in.StakeMinor, in.GameTypeCodes, in.IntervalSeconds)
	if err != nil {
		return VirtualAccount{}, err
	}
	return s.virtualByID(ctx, id)
}
