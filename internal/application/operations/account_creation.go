package operations

import (
	"context"
	"errors"
	"regexp"
	"strings"
	"time"

	"github.com/block-beast/platform/internal/domain/identity"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
)

var ErrAdminCompletionForbidden = errors.New("仅管理员或运营人员可以执行此操作")
var ErrInvalidPlayerAccount = errors.New("玩家账号参数无效")

var playerLoginNamePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{3,32}$`)

type PlayerAccountInput struct {
	ActorUserID string `json:"-"`
	AvatarURL   string `json:"avatar_url"`
	LoginName   string `json:"login_name"`
	DisplayName string `json:"display_name"`
	Password    string `json:"password"`
}

type PlayerAccount struct {
	UserID      int64     `json:"user_id"`
	LoginName   string    `json:"login_name"`
	DisplayName string    `json:"display_name"`
	AvatarURL   string    `json:"avatar_url"`
	Status      string    `json:"status"`
	IsVirtual   bool      `json:"is_virtual"`
	Roles       []string  `json:"roles"`
	CreatedAt   time.Time `json:"created_at"`
}

type createdPasswordAccount struct {
	InternalID  string
	PublicID    int64
	LoginName   string
	DisplayName string
	AvatarURL   string
	Status      string
	CreatedAt   time.Time
}

type passwordAccountSpec struct {
	ActorUserID  string
	AvatarURL    string
	LoginName    string
	DisplayName  string
	PasswordHash string
	IsVirtual    bool
}

func (s *Service) CreatePlayerAccount(ctx context.Context, in PlayerAccountInput) (PlayerAccount, error) {
	in.LoginName = strings.TrimSpace(in.LoginName)
	in.DisplayName = strings.TrimSpace(in.DisplayName)
	in.AvatarURL = strings.TrimSpace(in.AvatarURL)
	if in.ActorUserID == "" || !playerLoginNamePattern.MatchString(in.LoginName) || strings.TrimSpace(in.Password) == "" || len(in.DisplayName) > 100 || len(in.AvatarURL) > 2048 {
		return PlayerAccount{}, ErrInvalidPlayerAccount
	}
	passwordHash, err := identity.HashPassword(in.Password)
	if err != nil {
		return PlayerAccount{}, ErrInvalidPlayerAccount
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return PlayerAccount{}, err
	}
	defer tx.Rollback(ctx)
	if _, err = requireAdminOperator(ctx, tx, in.ActorUserID); err != nil {
		return PlayerAccount{}, err
	}
	created, err := createPasswordAccount(ctx, tx, passwordAccountSpec{
		ActorUserID: in.ActorUserID, AvatarURL: in.AvatarURL, LoginName: in.LoginName,
		DisplayName: in.DisplayName, PasswordHash: passwordHash,
	})
	if err != nil {
		return PlayerAccount{}, err
	}
	out := PlayerAccount{
		UserID: created.PublicID, LoginName: created.LoginName, DisplayName: created.DisplayName,
		AvatarURL: created.AvatarURL, Status: created.Status, Roles: []string{identity.RolePlayer}, CreatedAt: created.CreatedAt,
	}
	if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload)
		VALUES($1,$2,'admin.player.create','user',$3,jsonb_build_object('user_id',$4::bigint,'login_name',$5::text))`,
		uuid.NewString(), in.ActorUserID, created.InternalID, created.PublicID, created.LoginName); err != nil {
		return PlayerAccount{}, err
	}
	if err = tx.Commit(ctx); err != nil {
		return PlayerAccount{}, err
	}
	return out, nil
}

func requireAdminOperator(ctx context.Context, tx pgx.Tx, actorID string) (int64, error) {
	var publicID int64
	err := tx.QueryRow(ctx, `SELECT u.public_id FROM users u
		WHERE u.id=$1 AND u.status='active' AND EXISTS(
			SELECT 1 FROM user_roles ur JOIN roles r ON r.id=ur.role_id
			WHERE ur.user_id=u.id AND r.code IN ('admin','operator'))`, actorID).Scan(&publicID)
	if errors.Is(err, pgx.ErrNoRows) {
		return 0, ErrAdminCompletionForbidden
	}
	return publicID, err
}

func createPasswordAccount(ctx context.Context, tx pgx.Tx, spec passwordAccountSpec) (createdPasswordAccount, error) {
	if spec.AvatarURL != "" {
		var valid bool
		if _, err := uuid.Parse(spec.ActorUserID); err != nil {
			return createdPasswordAccount{}, ErrInvalidAvatar
		}
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM uploads
			WHERE owner_user_id=$1 AND storage_key=$2 AND status='confirmed'
			AND lower(content_type) IN ('image/jpeg','image/png','image/webp'))`, spec.ActorUserID, spec.AvatarURL).Scan(&valid); err != nil {
			return createdPasswordAccount{}, err
		}
		if !valid {
			return createdPasswordAccount{}, ErrInvalidAvatar
		}
	}
	id := uuid.NewString()
	_, err := tx.Exec(ctx, `INSERT INTO users(id,login_name,display_name,is_virtual) VALUES($1,$2,$3,$4)`, id, spec.LoginName, spec.DisplayName, spec.IsVirtual)
	if err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return createdPasswordAccount{}, identity.ErrLoginNameTaken
		}
		return createdPasswordAccount{}, err
	}
	var created createdPasswordAccount
	created.InternalID = id
	err = tx.QueryRow(ctx, `UPDATE users SET
		display_name=CASE WHEN display_name='' THEN '用户'||public_id::text ELSE display_name END,
		avatar_url=$2
		WHERE id=$1
		RETURNING public_id,login_name,display_name,status,created_at,
		CASE WHEN avatar_url='' THEN '' ELSE '/v1/avatars/'||public_id::text||'?v='||regexp_replace(avatar_url,'^.*/','') END`, id, spec.AvatarURL).
		Scan(&created.PublicID, &created.LoginName, &created.DisplayName, &created.Status, &created.CreatedAt, &created.AvatarURL)
	if err != nil {
		return createdPasswordAccount{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO auth_identities(id,user_id,provider,subject,password_hash) VALUES($1,$2,'password',$3,$4)`, uuid.NewString(), id, spec.LoginName, spec.PasswordHash); err != nil {
		var pgErr *pgconn.PgError
		if errors.As(err, &pgErr) && pgErr.Code == "23505" {
			return createdPasswordAccount{}, identity.ErrLoginNameTaken
		}
		return createdPasswordAccount{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO roles(id,code,description) VALUES($1,'player','player') ON CONFLICT(code) DO NOTHING`, uuid.NewString()); err != nil {
		return createdPasswordAccount{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO user_roles(user_id,role_id) SELECT $1,id FROM roles WHERE code='player'`, id); err != nil {
		return createdPasswordAccount{}, err
	}
	if _, err = tx.Exec(ctx, `SELECT ensure_user_customer_service_rooms($1)`, id); err != nil {
		return createdPasswordAccount{}, err
	}
	if _, err = tx.Exec(ctx, `INSERT INTO wallets(id,user_id,currency) SELECT gen_random_uuid(),$1,code FROM currencies WHERE enabled AND create_on_registration`, id); err != nil {
		return createdPasswordAccount{}, err
	}
	return created, nil
}
