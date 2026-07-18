package chain

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"strconv"
	"time"
)

var ErrTimestampOutOfRange = errors.New("webhook timestamp outside allowed window")
var ErrInvalidSignature = errors.New("invalid webhook signature")

func VerifyWebhook(secret string, method string, path string, timestamp string, nonce string, rawBody []byte, signature string, now time.Time, allowedSkew time.Duration) error {
	milliseconds, err := strconv.ParseInt(timestamp, 10, 64)
	if err != nil {
		return ErrTimestampOutOfRange
	}
	requestTime := time.UnixMilli(milliseconds)
	if now.Sub(requestTime) > allowedSkew || requestTime.Sub(now) > allowedSkew {
		return ErrTimestampOutOfRange
	}
	bodyHash := sha256.Sum256(rawBody)
	stringToSign := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", method, path, timestamp, nonce, hex.EncodeToString(bodyHash[:]))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(stringToSign))
	expected := hex.EncodeToString(mac.Sum(nil))
	if !hmac.Equal([]byte(expected), []byte(signature)) {
		return ErrInvalidSignature
	}
	return nil
}

func SignWebhook(secret string, method string, path string, timestamp string, nonce string, rawBody []byte) string {
	bodyHash := sha256.Sum256(rawBody)
	stringToSign := fmt.Sprintf("%s\n%s\n%s\n%s\n%s", method, path, timestamp, nonce, hex.EncodeToString(bodyHash[:]))
	mac := hmac.New(sha256.New, []byte(secret))
	_, _ = mac.Write([]byte(stringToSign))
	return hex.EncodeToString(mac.Sum(nil))
}