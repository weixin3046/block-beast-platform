package operations

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

var ErrAnnouncementNotFound = errors.New("announcement not found")
var ErrInvalidAnnouncement = errors.New("announcement title and body are required, and end time must be after start time")

type Announcement struct {
	ID        string     `json:"id"`
	Title     string     `json:"title"`
	Body      string     `json:"body"`
	Enabled   bool       `json:"enabled"`
	StartsAt  *time.Time `json:"starts_at,omitempty"`
	EndsAt    *time.Time `json:"ends_at,omitempty"`
	CreatedAt time.Time  `json:"created_at"`
}

type AnnouncementInput struct {
	Title    string     `json:"title"`
	Body     string     `json:"body"`
	Enabled  bool       `json:"enabled"`
	StartsAt *time.Time `json:"starts_at"`
	EndsAt   *time.Time `json:"ends_at"`
}

type AuditLog struct {
	ID          string          `json:"id"`
	ActorUserID *string         `json:"actor_user_id,omitempty"`
	Action      string          `json:"action"`
	TargetType  string          `json:"target_type"`
	TargetID    string          `json:"target_id"`
	RequestID   *string         `json:"request_id,omitempty"`
	Payload     json.RawMessage `json:"payload"`
	CreatedAt   time.Time       `json:"created_at"`
}

func (service *Service) ListAnnouncements(ctx context.Context, activeOnly bool, limit int) ([]Announcement, error) {
	if limit <= 0 || limit > 100 {
		limit = 50
	}
	rows, err := service.pool.Query(ctx, `
		SELECT id::text,title,body,enabled,starts_at,ends_at,created_at
		FROM announcements
		WHERE NOT $1 OR (
			enabled=true
			AND (starts_at IS NULL OR starts_at <= now())
			AND (ends_at IS NULL OR ends_at > now())
		)
		ORDER BY created_at DESC LIMIT $2`, activeOnly, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Announcement, 0)
	for rows.Next() {
		var item Announcement
		if err := rows.Scan(&item.ID, &item.Title, &item.Body, &item.Enabled, &item.StartsAt, &item.EndsAt, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (service *Service) CreateAnnouncement(ctx context.Context, input AnnouncementInput) (Announcement, error) {
	if err := validateAnnouncement(input); err != nil {
		return Announcement{}, err
	}
	var item Announcement
	err := service.pool.QueryRow(ctx, `
		INSERT INTO announcements (id,title,body,enabled,starts_at,ends_at)
		VALUES ($1,$2,$3,$4,$5,$6)
		RETURNING id::text,title,body,enabled,starts_at,ends_at,created_at`,
		uuid.NewString(), strings.TrimSpace(input.Title), strings.TrimSpace(input.Body), input.Enabled, input.StartsAt, input.EndsAt).
		Scan(&item.ID, &item.Title, &item.Body, &item.Enabled, &item.StartsAt, &item.EndsAt, &item.CreatedAt)
	return item, err
}

func (service *Service) UpdateAnnouncement(ctx context.Context, announcementID string, input AnnouncementInput) (Announcement, error) {
	if err := validateAnnouncement(input); err != nil {
		return Announcement{}, err
	}
	var item Announcement
	err := service.pool.QueryRow(ctx, `
		UPDATE announcements SET title=$2,body=$3,enabled=$4,starts_at=$5,ends_at=$6
		WHERE id=$1
		RETURNING id::text,title,body,enabled,starts_at,ends_at,created_at`,
		announcementID, strings.TrimSpace(input.Title), strings.TrimSpace(input.Body), input.Enabled, input.StartsAt, input.EndsAt).
		Scan(&item.ID, &item.Title, &item.Body, &item.Enabled, &item.StartsAt, &item.EndsAt, &item.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return Announcement{}, ErrAnnouncementNotFound
	}
	return item, err
}

func (service *Service) ListAuditLogs(ctx context.Context, action, actorUserID string, limit int) ([]AuditLog, error) {
	if limit <= 0 || limit > 200 {
		limit = 100
	}
	rows, err := service.pool.Query(ctx, `
		SELECT id::text,actor_user_id::text,action,target_type,target_id,request_id,payload,created_at
		FROM audit_logs
		WHERE ($1='' OR action=$1) AND ($2='' OR actor_user_id::text=$2)
		ORDER BY created_at DESC LIMIT $3`, action, actorUserID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]AuditLog, 0)
	for rows.Next() {
		var item AuditLog
		if err := rows.Scan(&item.ID, &item.ActorUserID, &item.Action, &item.TargetType, &item.TargetID, &item.RequestID, &item.Payload, &item.CreatedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func validateAnnouncement(input AnnouncementInput) error {
	if strings.TrimSpace(input.Title) == "" || strings.TrimSpace(input.Body) == "" {
		return ErrInvalidAnnouncement
	}
	if input.StartsAt != nil && input.EndsAt != nil && !input.EndsAt.After(*input.StartsAt) {
		return ErrInvalidAnnouncement
	}
	return nil
}
