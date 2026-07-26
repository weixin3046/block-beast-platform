package redpacket

import (
	"errors"
	"time"
)

var ErrInvalidPacket = errors.New("red packet amount, count, currency, or request ID is invalid")
var ErrPacketNotFound = errors.New("red packet not found")
var ErrPacketUnavailable = errors.New("red packet is exhausted, refunded, or expired")
var ErrAlreadyClaimed = errors.New("red packet already claimed")
var ErrSenderCannotClaim = errors.New("sender cannot claim own red packet")
var ErrRoomAccessDenied = errors.New("chat room access denied")
var ErrInsufficientBalance = errors.New("insufficient wallet balance")

type Packet struct {
	ID              string    `json:"id"`
	RoomID          string    `json:"room_id"`
	SenderUserID    string    `json:"sender_user_id"`
	ClientRequestID string    `json:"client_request_id"`
	Currency        string    `json:"currency"`
	Greeting        string    `json:"greeting"`
	TotalMinor      int64     `json:"total_minor"`
	RemainingMinor  int64     `json:"remaining_minor"`
	PacketCount     int       `json:"packet_count"`
	ClaimedCount    int       `json:"claimed_count"`
	Status          string    `json:"status"`
	ExpiresAt       time.Time `json:"expires_at"`
	CreatedAt       time.Time `json:"created_at"`
}

type CreateInput struct {
	RoomID          string
	SenderUserID    string
	ClientRequestID string
	Currency        string
	Greeting        string
	TotalMinor      int64
	PacketCount     int
}

type Claim struct {
	ID          string    `json:"id"`
	RedPacketID string    `json:"red_packet_id"`
	UserID      string    `json:"user_id"`
	Currency    string    `json:"currency"`
	AmountMinor int64     `json:"amount_minor"`
	ClaimedAt   time.Time `json:"claimed_at"`
}
