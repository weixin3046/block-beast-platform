package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var (
	ErrPhraseInvalid   = errors.New("话术参数无效，请检查标题、内容、分类、排序和编号")
	ErrPhraseNotFound  = errors.New("话术不存在")
	ErrPhraseForbidden = errors.New("无话术配置权限")
	ErrPhraseConflict  = errors.New("话术创建请求编号已使用或对应话术已删除")
)

type PhraseInput struct {
	Title    string `json:"title"`
	Content  string `json:"content"`
	Category string `json:"category"`
	Sort     int64  `json:"sort"`
	Enabled  bool   `json:"enabled"`
}
type Phrase struct {
	ID int64 `json:"id"`
	PhraseInput
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}
type PhrasePage struct {
	Count int64    `json:"count"`
	Items []Phrase `json:"items"`
}
type PhraseFilter struct {
	Category string
	Enabled  *bool
	Page     int64
	Limit    int
}

func normalizePhrase(in PhraseInput) (PhraseInput, error) {
	in.Title = strings.TrimSpace(in.Title)
	in.Content = strings.TrimSpace(in.Content)
	in.Category = strings.TrimSpace(in.Category)
	// 与参考项目 JS string.length 一致，emoji 等补充平面字符占两个 UTF-16 单位。
	size := func(s string) int { return len(utf16.Encode([]rune(s))) }
	if in.Title == "" || in.Content == "" || size(in.Title) > 100 || size(in.Content) > 2000 || size(in.Category) > 50 || in.Sort < 0 {
		return in, ErrPhraseInvalid
	}
	return in, nil
}
func phraseAccess(ctx context.Context, tx pgx.Tx, actor string) error {
	if _, err := uuid.Parse(actor); err != nil {
		return ErrPhraseForbidden
	}
	var allowed bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM users u JOIN user_roles ur ON ur.user_id=u.id JOIN roles r ON r.id=ur.role_id WHERE u.id=$1 AND u.status='active' AND r.code IN ('admin','operator'))`, actor).Scan(&allowed); err != nil {
		return err
	}
	if !allowed {
		return ErrPhraseForbidden
	}
	return nil
}

const phraseColumns = `id,title,content,category,sort,enabled,created_at,updated_at`

func scanPhrase(row pgx.Row) (Phrase, error) {
	var p Phrase
	err := row.Scan(&p.ID, &p.Title, &p.Content, &p.Category, &p.Sort, &p.Enabled, &p.CreatedAt, &p.UpdatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		err = ErrPhraseNotFound
	}
	return p, err
}
func (s *Service) ListPhrases(ctx context.Context, actor string, f PhraseFilter) (PhrasePage, error) {
	out := PhrasePage{Items: []Phrase{}}
	if f.Page < 0 || f.Page > 92233720368547758 {
		return out, ErrPhraseInvalid
	}
	if f.Limit == 0 {
		f.Limit = 20
	}
	if f.Limit < 1 {
		f.Limit = 1
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	tx, err := s.pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.RepeatableRead, AccessMode: pgx.ReadOnly})
	if err != nil {
		return out, err
	}
	defer tx.Rollback(ctx)
	if err = phraseAccess(ctx, tx, actor); err != nil {
		return out, err
	}
	args := []any{strings.TrimSpace(f.Category), f.Enabled}
	where := ` FROM customer_phrases WHERE NOT deleted AND ($1='' OR category=$1) AND ($2::boolean IS NULL OR enabled=$2)`
	if err = tx.QueryRow(ctx, `SELECT count(*)`+where, args...).Scan(&out.Count); err != nil {
		return out, err
	}
	rows, err := tx.Query(ctx, `SELECT `+phraseColumns+where+` ORDER BY sort,id DESC LIMIT $3 OFFSET $4`, append(args, f.Limit, f.Page*int64(f.Limit))...)
	if err != nil {
		return out, err
	}
	defer rows.Close()
	for rows.Next() {
		p, e := scanPhrase(rows)
		if e != nil {
			return out, e
		}
		out.Items = append(out.Items, p)
	}
	if err = rows.Err(); err != nil {
		return out, err
	}
	return out, tx.Commit(ctx)
}

// 所有变更使用同一短事务表锁，保证排序与同时新增/删除/编辑不会交错；查询不阻塞。
func (s *Service) phraseWrite(ctx context.Context, actor, action string, fn func(pgx.Tx) (any, string, error)) (any, error) {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	if err = phraseAccess(ctx, tx, actor); err != nil {
		return nil, err
	}
	if _, err = tx.Exec(ctx, `LOCK TABLE customer_phrases IN SHARE ROW EXCLUSIVE MODE`); err != nil {
		return nil, err
	}
	result, target, err := fn(tx)
	if err != nil {
		return nil, err
	}
	if target != "" {
		payload, e := json.Marshal(result)
		if e != nil {
			return nil, e
		}
		if _, err = tx.Exec(ctx, `INSERT INTO audit_logs(id,actor_user_id,action,target_type,target_id,payload) VALUES($1,$2,$3,'customer_phrase',$4,$5)`, uuid.NewString(), actor, "phrase."+action, target, payload); err != nil {
			return nil, err
		}
	}
	return result, tx.Commit(ctx)
}
func (s *Service) SavePhrase(ctx context.Context, actor string, id int64, requestID string, in PhraseInput) (Phrase, error) {
	in, err := normalizePhrase(in)
	if err != nil || id < 0 {
		return Phrase{}, ErrPhraseInvalid
	}
	if id == 0 {
		if _, err = uuid.Parse(requestID); err != nil {
			return Phrase{}, ErrPhraseInvalid
		}
	}
	action := "update"
	if id == 0 {
		action = "create"
	}
	result, err := s.phraseWrite(ctx, actor, action, func(tx pgx.Tx) (any, string, error) {
		if id == 0 {
			data, _ := json.Marshal(in)
			var existing int64
			var same, deleted bool
			err := tx.QueryRow(ctx, `SELECT id,create_input=$3::jsonb,deleted FROM customer_phrases WHERE created_by=$1 AND request_id=$2`, actor, requestID, data).Scan(&existing, &same, &deleted)
			if err == nil {
				if !same || deleted {
					return nil, "", ErrPhraseConflict
				}
				p, e := scanPhrase(tx.QueryRow(ctx, `SELECT `+phraseColumns+` FROM customer_phrases WHERE id=$1`, existing))
				return p, "", e
			}
			if !errors.Is(err, pgx.ErrNoRows) {
				return nil, "", err
			}
			p, e := scanPhrase(tx.QueryRow(ctx, `INSERT INTO customer_phrases(title,content,category,sort,enabled,created_by,request_id,create_input) VALUES($1,$2,$3,$4,$5,$6,$7,$8) RETURNING `+phraseColumns, in.Title, in.Content, in.Category, in.Sort, in.Enabled, actor, requestID, data))
			return p, phraseTarget(p.ID), e
		}
		p, e := scanPhrase(tx.QueryRow(ctx, `UPDATE customer_phrases SET title=$2,content=$3,category=$4,sort=$5,enabled=$6,updated_at=now() WHERE id=$1 AND NOT deleted RETURNING `+phraseColumns, id, in.Title, in.Content, in.Category, in.Sort, in.Enabled))
		return p, phraseTarget(id), e
	})
	if err != nil {
		return Phrase{}, err
	}
	return result.(Phrase), nil
}
func phraseTarget(id int64) string { b, _ := json.Marshal(id); return string(b) }
func (s *Service) DeletePhrase(ctx context.Context, actor string, id int64) error {
	if id <= 0 {
		return ErrPhraseInvalid
	}
	_, err := s.phraseWrite(ctx, actor, "delete", func(tx pgx.Tx) (any, string, error) {
		tag, e := tx.Exec(ctx, `UPDATE customer_phrases SET deleted=true,updated_at=now() WHERE id=$1 AND NOT deleted`, id)
		if e != nil {
			return nil, "", e
		}
		if tag.RowsAffected() == 0 {
			return nil, "", ErrPhraseNotFound
		}
		return map[string]int64{"id": id}, phraseTarget(id), nil
	})
	return err
}
func (s *Service) SetPhraseEnabled(ctx context.Context, actor string, id int64, enabled bool) (Phrase, error) {
	if id <= 0 {
		return Phrase{}, ErrPhraseInvalid
	}
	out, err := s.phraseWrite(ctx, actor, "enabled", func(tx pgx.Tx) (any, string, error) {
		p, e := scanPhrase(tx.QueryRow(ctx, `UPDATE customer_phrases SET enabled=$2,updated_at=now() WHERE id=$1 AND NOT deleted RETURNING `+phraseColumns, id, enabled))
		return p, phraseTarget(id), e
	})
	if err != nil {
		return Phrase{}, err
	}
	return out.(Phrase), nil
}
func (s *Service) ReorderPhrases(ctx context.Context, actor string, ids []int64) ([]Phrase, error) {
	if ids == nil {
		return nil, ErrPhraseInvalid
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return nil, ErrPhraseInvalid
		}
		seen[id] = true
	}
	out, err := s.phraseWrite(ctx, actor, "reorder", func(tx pgx.Tx) (any, string, error) {
		rows, e := tx.Query(ctx, `SELECT `+phraseColumns+` FROM customer_phrases WHERE NOT deleted ORDER BY sort,id DESC`)
		if e != nil {
			return nil, "", e
		}
		all := []Phrase{}
		byID := map[int64]Phrase{}
		for rows.Next() {
			p, e := scanPhrase(rows)
			if e != nil {
				rows.Close()
				return nil, "", e
			}
			all = append(all, p)
			byID[p.ID] = p
		}
		rows.Close()
		if e = rows.Err(); e != nil {
			return nil, "", e
		}
		ordered := []Phrase{}
		for _, id := range ids {
			p, ok := byID[id]
			if !ok {
				return nil, "", ErrPhraseNotFound
			}
			ordered = append(ordered, p)
		}
		for _, p := range all {
			if !seen[p.ID] {
				ordered = append(ordered, p)
			}
		}
		orderIDs := make([]int64, len(ordered))
		for i, p := range ordered {
			orderIDs[i] = p.ID
		}
		_, e = tx.Exec(ctx, `UPDATE customer_phrases p SET sort=o.n-1,updated_at=now() FROM unnest($1::bigint[]) WITH ORDINALITY AS o(id,n) WHERE p.id=o.id`, orderIDs)
		if e != nil {
			return nil, "", e
		}
		var now time.Time
		if e = tx.QueryRow(ctx, `SELECT now()`).Scan(&now); e != nil {
			return nil, "", e
		}
		for i := range ordered {
			ordered[i].Sort = int64(i)
			ordered[i].UpdatedAt = now
		}
		return ordered, "all", nil
	})
	if err != nil {
		return nil, err
	}
	return out.([]Phrase), nil
}
