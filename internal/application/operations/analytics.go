package operations

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"net/netip"
	"strconv"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type MonitorBet struct {
	BetID            string          `json:"bet_id"`
	UserID           int64           `json:"user_id"`
	LoginName        string          `json:"login_name"`
	DisplayName      string          `json:"display_name"`
	IsVirtual        bool            `json:"is_virtual"`
	GameType         string          `json:"game_type"`
	GameName         string          `json:"game_name"`
	RoundSequence    int64           `json:"round_sequence"`
	Currency         string          `json:"currency"`
	GameRoomID       string          `json:"game_room_id,omitempty"`
	GameRoomCode     string          `json:"game_room_code,omitempty"`
	GameRoomName     string          `json:"game_room_name,omitempty"`
	PlayMode         string          `json:"play_mode,omitempty"`
	Selection        json.RawMessage `json:"selection"`
	StakeMinor       int64           `json:"stake_minor"`
	PayoutMultiplier int64           `json:"payout_multiplier"`
	PayoutDivisor    int64           `json:"payout_divisor"`
	PayoutRate       string          `json:"payout_rate"`
	Status           string          `json:"status"`
	CreatedAt        time.Time       `json:"created_at"`
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
	bets, err := s.CurrentBets(ctx, userQuery, gameType, limit)
	if err != nil {
		return Monitor{}, err
	}
	rounds, err := s.RoundCountdowns(ctx)
	if err != nil {
		return Monitor{}, err
	}
	return Monitor{ServerTime: time.Now().UTC(), Bets: bets, Rounds: rounds}, nil
}
func (s *Service) CurrentBets(ctx context.Context, userQuery, gameType string, limit int) ([]MonitorBet, error) {
	return s.CurrentBetsFiltered(ctx, userQuery, gameType, limit, "real")
}

func (s *Service) CurrentBetsFiltered(ctx context.Context, userQuery, gameType string, limit int, playerType string) ([]MonitorBet, error) {
	if playerType != "" && playerType != "all" && playerType != "real" && playerType != "virtual" {
		return nil, ErrInvalidPlayerType
	}
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	result := []MonitorBet{}
	rows, err := s.pool.Query(ctx, `
		SELECT b.id::text,u.public_id,COALESCE(u.login_name,''),u.display_name,u.is_virtual,
			gt.code,gt.name,r.sequence,w.currency,COALESCE(b.game_room_id::text,''),
			COALESCE(gr.code,''),COALESCE(gr.name,''),COALESCE(b.play_mode,''),b.selection,b.stake_minor,
			COALESCE(b.payout_multiplier_snapshot,0),COALESCE(b.payout_divisor_snapshot,0),b.status,b.created_at
		FROM bets b JOIN users u ON u.id=b.user_id JOIN wallets w ON w.id=b.wallet_id
		JOIN rounds r ON r.id=b.round_id JOIN game_types gt ON gt.id=r.game_type_id
		LEFT JOIN game_rooms gr ON gr.id=b.game_room_id
		WHERE b.status='accepted' AND ($4='' OR $4='all' OR ($4='real' AND NOT u.is_virtual) OR ($4='virtual' AND u.is_virtual)) AND ($1='' OR gt.code=$1)
			AND ($2='' OR u.public_id::text=$2 OR u.login_name ILIKE '%'||$2||'%')
		ORDER BY b.created_at DESC,b.id DESC LIMIT $3`, gameType, userQuery, limit, playerType)
	if err != nil {
		return result, err
	}
	defer rows.Close()
	for rows.Next() {
		var v MonitorBet
		if err := rows.Scan(&v.BetID, &v.UserID, &v.LoginName, &v.DisplayName, &v.IsVirtual,
			&v.GameType, &v.GameName, &v.RoundSequence, &v.Currency, &v.GameRoomID, &v.GameRoomCode,
			&v.GameRoomName, &v.PlayMode, &v.Selection, &v.StakeMinor, &v.PayoutMultiplier,
			&v.PayoutDivisor, &v.Status, &v.CreatedAt); err != nil {
			return result, err
		}
		v.PayoutRate = payoutRate(v.PayoutMultiplier, v.PayoutDivisor)
		result = append(result, v)
	}
	return result, rows.Err()
}
func (s *Service) RoundCountdowns(ctx context.Context) ([]MonitorRound, error) {
	result := []MonitorRound{}
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
		result = append(result, v)
	}
	return result, rounds.Err()
}

type PlayerStatistic struct {
	Funds        []FundStatistic `json:"funds"`
	UserID       int64           `json:"user_id"`
	LoginName    string          `json:"login_name"`
	DisplayName  string          `json:"display_name"`
	BetCount     int64           `json:"bet_count"`
	StakeMinor   int64           `json:"-"`
	PayoutMinor  int64           `json:"-"`
	DepositMinor int64           `json:"-"`
	CreditMinor  int64           `json:"-"`
	BalanceMinor int64           `json:"-"`
}
type CurrencyStatistic struct {
	BetLoss        string `json:"bet_loss"`
	BetLossMinor   int64  `json:"-"`
	Decimals       int    `json:"decimals"`
	Stake          string `json:"stake"`
	Payout         string `json:"payout"`
	Deposit        string `json:"deposit"`
	Credit         string `json:"credit"`
	Clearance      string `json:"clearance"`
	Gift           string `json:"gift"`
	Penalty        string `json:"penalty"`
	Balance        string `json:"balance"`
	ClearanceMinor int64  `json:"clearance_minor"`
	GiftMinor      int64  `json:"gift_minor"`
	PenaltyMinor   int64  `json:"penalty_minor"`
	Currency       string `json:"currency"`
	BetCount       int64  `json:"bet_count"`
	StakeMinor     int64  `json:"stake_minor"`
	PayoutMinor    int64  `json:"payout_minor"`
	DepositMinor   int64  `json:"deposit_minor"`
	CreditMinor    int64  `json:"credit_minor"`
	BalanceMinor   int64  `json:"balance_minor"`
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
	rows, err := s.pool.Query(ctx, `SELECT u.public_id,COALESCE(u.login_name,''),u.display_name,count(b.id),0,0,0,0,0 FROM users u LEFT JOIN bets b ON b.user_id=u.id AND b.created_at >= $2 AND b.created_at < $3 WHERE NOT u.is_virtual AND ($1='' OR u.public_id::text=$1 OR u.login_name ILIKE '%'||$1||'%') GROUP BY u.id ORDER BY count(b.id) DESC,u.public_id LIMIT $4`, userQuery, from, to, limit)
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
	rows.Close()
	return result, s.dashboardFunds(ctx, &result, from, to)
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
	rows, err := s.pool.Query(ctx, `SELECT host(h.ip_address),min(h.logged_in_at),max(h.logged_in_at),count(*) FROM user_login_history h JOIN users u ON u.id=h.user_id WHERE u.public_id=$1 GROUP BY h.ip_address ORDER BY max(h.logged_in_at) DESC`, publicID)
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
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// Release the first query's connection before querying associated users.
	// Otherwise a single-connection pool (or concurrent saturated pools) stalls.
	rows.Close()
	for i := range out {
		users, err := s.UsersByLoginIP(ctx, out[i].IP)
		if err != nil {
			return nil, err
		}
		out[i].Users = users
	}
	return out, nil
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
	AvatarURL       string          `json:"avatar_url"`
	GameRoomID      *string         `json:"game_room_id"`
	PlayMode        string          `json:"play_mode"`
	UserStatus      string          `json:"user_status"`
	RunStatus       string          `json:"run_status"`
	LastRunAt       *time.Time      `json:"last_run_at"`
	LastFinishedAt  *time.Time      `json:"last_finished_at"`
	NextRunAt       *time.Time      `json:"next_run_at"`
	LastResults     json.RawMessage `json:"last_results"`
	UserID          int64           `json:"user_id"`
	LoginName       string          `json:"login_name"`
	DisplayName     string          `json:"display_name"`
	Enabled         bool            `json:"automation_enabled"`
	Currency        string          `json:"currency"`
	StakeMinor      int64           `json:"stake_minor"`
	GameTypeCodes   []string        `json:"game_type_codes"`
	IntervalSeconds int             `json:"interval_seconds"`
}
type VirtualAccountInput struct {
	ActorUserID     string           `json:"-"`
	AvatarURL       string           `json:"avatar_url"`
	LoginName       string           `json:"login_name"`
	DisplayName     string           `json:"display_name"`
	Password        string           `json:"password"`
	Count           int              `json:"count"`
	InitialBalances map[string]int64 `json:"initial_balances"`
}
type VirtualAutomationInput struct {
	GameRoomID      string   `json:"game_room_id"`
	PlayMode        string   `json:"play_mode"`
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
	in.AvatarURL = strings.TrimSpace(in.AvatarURL)
	if in.LoginName == "" || len(in.DisplayName) > 100 || len(in.AvatarURL) > 2048 || strings.TrimSpace(in.Password) == "" {
		return VirtualAccount{}, ErrInvalidVirtualAccount
	}
	passwordHash, err := identity.HashPassword(in.Password)
	if err != nil {
		return VirtualAccount{}, err
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return VirtualAccount{}, err
	}
	defer tx.Rollback(ctx)
	account, err := createPasswordAccount(ctx, tx, passwordAccountSpec{
		ActorUserID: in.ActorUserID, AvatarURL: in.AvatarURL, LoginName: in.LoginName,
		DisplayName: in.DisplayName, PasswordHash: passwordHash, IsVirtual: true,
	})
	if err != nil {
		return VirtualAccount{}, err
	}
	id := account.InternalID
	created := VirtualAccount{UserID: account.PublicID, LoginName: account.LoginName, DisplayName: account.DisplayName, UserStatus: account.Status, AvatarURL: account.AvatarURL}
	for currency, amount := range in.InitialBalances {
		if amount < 0 {
			return VirtualAccount{}, ErrInvalidVirtualAccount
		}
		var walletID string
		err = tx.QueryRow(ctx, `INSERT INTO wallets(id,user_id,currency,available_minor) SELECT gen_random_uuid(),$1,code,$3 FROM currencies WHERE code=$2 AND enabled ON CONFLICT(user_id,currency) DO UPDATE SET available_minor=EXCLUDED.available_minor RETURNING id`, id, currency, amount).Scan(&walletID)
		if errors.Is(err, pgx.ErrNoRows) {
			return VirtualAccount{}, ErrInvalidVirtualAccount
		}
		if err != nil {
			return VirtualAccount{}, err
		}
		if amount > 0 {
			if _, err = tx.Exec(ctx, `INSERT INTO ledger_entries(id,wallet_id,business_type,business_id,entry_type,amount_minor,balance_after_minor) VALUES(gen_random_uuid(),$1,'virtual_initial_balance',$2,'virtual_initial_balance',$3,$3)`, walletID, id, amount); err != nil {
				return VirtualAccount{}, err
			}
		}
	}
	if _, err = tx.Exec(ctx, `INSERT INTO virtual_account_automations(user_id) VALUES($1)`, id); err != nil {
		return VirtualAccount{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return VirtualAccount{}, err
	}
	return created, nil
}

var virtualNameParts = []string{"阿", "乐", "星", "小", "云", "森", "安", "米", "诺", "言", "夏", "青", "可", "元", "飞", "果"}

func randomVirtualDisplayName() string {
	length := 2
	var b [1]byte
	if _, err := rand.Read(b[:]); err == nil {
		length += int(b[0] % 4)
	}
	name := make([]string, length)
	for i := range name {
		if _, err := rand.Read(b[:]); err == nil {
			name[i] = virtualNameParts[int(b[0])%len(virtualNameParts)]
		} else {
			name[i] = virtualNameParts[i%len(virtualNameParts)]
		}
	}
	return strings.Join(name, "")
}

func (s *Service) CreateVirtualAccounts(ctx context.Context, in VirtualAccountInput) ([]VirtualAccount, error) {
	if in.Count == 0 {
		in.Count = 1
	}
	if in.Count < 1 || in.Count > 100 || strings.TrimSpace(in.LoginName) == "" {
		return nil, ErrInvalidVirtualAccount
	}
	accounts := make([]VirtualAccount, 0, in.Count)
	base := strings.TrimSpace(in.LoginName)
	for i := 1; i <= in.Count; i++ {
		item := in
		item.Count = 0
		if in.Count > 1 {
			item.LoginName = base + strconv.Itoa(i)
		}
		if strings.TrimSpace(item.DisplayName) == "" {
			item.DisplayName = randomVirtualDisplayName()
		}
		account, err := s.CreateVirtualAccount(ctx, item)
		if err != nil {
			return nil, err
		}
		accounts = append(accounts, account)
	}
	return accounts, nil
}
func (s *Service) virtualByID(ctx context.Context, id string) (VirtualAccount, error) {
	var v VirtualAccount
	err := s.pool.QueryRow(ctx, `SELECT u.public_id,COALESCE(u.login_name,''),u.display_name,a.enabled,a.currency,a.stake_minor,a.game_type_codes,a.interval_seconds,
 a.game_room_id::text,a.play_mode,u.status,
 CASE WHEN a.run_status='running' AND a.lease_until<now() THEN 'retry_pending' ELSE a.run_status END,
 a.last_run_at,a.last_finished_at,
 CASE WHEN a.enabled AND u.status='active' THEN GREATEST(COALESCE(a.last_run_at + make_interval(secs=>a.interval_seconds),now()),COALESCE(a.lease_until,now())) ELSE NULL END,
 a.last_results
 FROM users u JOIN virtual_account_automations a ON a.user_id=u.id WHERE u.id=$1 AND u.is_virtual=true`, id).Scan(&v.UserID, &v.LoginName, &v.DisplayName, &v.Enabled, &v.Currency, &v.StakeMinor, &v.GameTypeCodes, &v.IntervalSeconds, &v.GameRoomID, &v.PlayMode, &v.UserStatus, &v.RunStatus, &v.LastRunAt, &v.LastFinishedAt, &v.NextRunAt, &v.LastResults)
	return v, err
}
func (s *Service) GetVirtualAutomation(ctx context.Context, publicID int64) (VirtualAccount, error) {
	var id string
	err := s.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1 AND is_virtual=true`, publicID).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return VirtualAccount{}, ErrUserNotFound
	}
	if err != nil {
		return VirtualAccount{}, err
	}
	return s.virtualByID(ctx, id)
}
func (s *Service) SetVirtualAutomation(ctx context.Context, publicID int64, in VirtualAutomationInput) (VirtualAccount, error) {
	var id string
	if err := s.pool.QueryRow(ctx, `SELECT id::text FROM users WHERE public_id=$1 AND is_virtual=true`, publicID).Scan(&id); errors.Is(err, pgx.ErrNoRows) {
		return VirtualAccount{}, ErrUserNotFound
	} else if err != nil {
		return VirtualAccount{}, err
	}
	// Stopping never requires a valid room/currency or a sufficient balance.
	if !in.Enabled {
		_, err := s.pool.Exec(ctx, `UPDATE virtual_account_automations SET enabled=false,run_id=NULL,lease_until=NULL,run_status='stopped',updated_at=now() WHERE user_id=$1`, id)
		if err != nil {
			return VirtualAccount{}, err
		}
		return s.virtualByID(ctx, id)
	}
	in.Currency = strings.ToUpper(strings.TrimSpace(in.Currency))
	in.PlayMode = strings.ToLower(strings.TrimSpace(in.PlayMode))
	if err := validateVirtualAutomation(in); err != nil {
		return VirtualAccount{}, err
	}
	var valid int
	err := s.pool.QueryRow(ctx, `SELECT count(*) FROM game_room_types rt
 JOIN game_rooms r ON r.id=rt.room_id AND r.enabled AND r.game_kind='hash'
 JOIN game_types gt ON gt.id=rt.game_type_id AND gt.enabled
 JOIN hash_room_currency_configs c ON c.room_id=r.id AND c.currency=$2
 JOIN currencies cur ON cur.code=c.currency AND cur.enabled
 WHERE r.id=$1 AND gt.code=ANY($3) AND $4>=c.min_stake_minor
 AND $4<=CASE $5 WHEN 'guess' THEN c.guess_max_stake_minor WHEN 'dodge' THEN c.dodge_max_stake_minor ELSE c.road_max_stake_minor END`,
		in.GameRoomID, in.Currency, in.GameTypeCodes, in.StakeMinor, in.PlayMode).Scan(&valid)
	if err != nil {
		return VirtualAccount{}, err
	}
	if valid != len(in.GameTypeCodes) {
		return VirtualAccount{}, ErrInvalidVirtualAccount
	}
	_, err = s.pool.Exec(ctx, `UPDATE virtual_account_automations SET enabled=$2,currency=$3,stake_minor=$4,game_type_codes=$5,interval_seconds=$6,
 game_room_id=$7,play_mode=$8,run_id=NULL,lease_until=NULL,last_run_at=NULL,run_status='ready',updated_at=now() WHERE user_id=$1`, id, in.Enabled, in.Currency, in.StakeMinor, in.GameTypeCodes, in.IntervalSeconds, in.GameRoomID, in.PlayMode)
	if err != nil {
		return VirtualAccount{}, err
	}
	return s.virtualByID(ctx, id)
}

func validateVirtualAutomation(in VirtualAutomationInput) error {
	if _, err := uuid.Parse(in.GameRoomID); err != nil {
		return ErrInvalidVirtualAccount
	}
	if in.StakeMinor <= 0 || in.Currency == "" || in.IntervalSeconds < 3 || in.IntervalSeconds > 86400 || len(in.GameTypeCodes) == 0 || len(in.GameTypeCodes) > 6 {
		return ErrInvalidVirtualAccount
	}
	if in.PlayMode != "road" && in.PlayMode != "guess" && in.PlayMode != "dodge" {
		return ErrInvalidVirtualAccount
	}
	seen := map[string]bool{}
	for _, code := range in.GameTypeCodes {
		switch code {
		case "hash_9", "hash_13", "hash_17", "hash_19", "hash_23", "hash_29":
		default:
			return ErrInvalidVirtualAccount
		}
		if seen[code] {
			return ErrInvalidVirtualAccount
		}
		seen[code] = true
	}
	return nil
}
