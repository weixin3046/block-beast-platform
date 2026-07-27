package objectstorage

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	Endpoint  string
	Region    string
	Bucket    string
	AccessKey string
	SecretKey string
}

type ObjectInfo struct {
	SizeBytes   int64
	ContentType string
}

type ReadSeekCloser interface {
	io.Reader
	io.Seeker
	io.Closer
}

var ErrSizeMismatch = errors.New("uploaded object size does not match authorization")
var ErrContentTypeMismatch = errors.New("uploaded object content does not match declared type")

type Client struct {
	config Config
	http   *http.Client
	now    func() time.Time
}

func NewClient(config Config, httpClient *http.Client) (*Client, error) {
	if config.Endpoint == "" || config.Region == "" || config.Bucket == "" || config.AccessKey == "" || config.SecretKey == "" {
		return nil, errors.New("object storage configuration is incomplete")
	}
	endpoint, err := url.Parse(config.Endpoint)
	if err != nil || endpoint.Scheme == "" || endpoint.Host == "" {
		return nil, errors.New("object storage endpoint is invalid")
	}
	config.Endpoint = strings.TrimRight(config.Endpoint, "/")
	if httpClient == nil {
		httpClient = &http.Client{Timeout: 15 * time.Second}
	}
	return &Client{config: config, http: httpClient, now: time.Now}, nil
}

func (client *Client) PresignPut(key, contentType string, ttl time.Duration) (string, error) {
	if ttl <= 0 || ttl > 7*24*time.Hour {
		return "", errors.New("presign TTL must be between 1 second and 7 days")
	}
	now := client.now().UTC()
	target, err := client.objectURL(key)
	if err != nil {
		return "", err
	}
	date := now.Format("20060102")
	credentialScope := date + "/" + client.config.Region + "/s3/aws4_request"
	query := target.Query()
	query.Set("X-Amz-Algorithm", "AWS4-HMAC-SHA256")
	query.Set("X-Amz-Credential", client.config.AccessKey+"/"+credentialScope)
	query.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	query.Set("X-Amz-Expires", strconv.FormatInt(int64(ttl/time.Second), 10))
	query.Set("X-Amz-SignedHeaders", "content-type;host")
	target.RawQuery = query.Encode()
	canonicalHeaders := "content-type:" + strings.TrimSpace(contentType) + "\n" + "host:" + target.Host + "\n"
	canonicalRequest := strings.Join([]string{
		http.MethodPut, target.EscapedPath(), target.RawQuery, canonicalHeaders,
		"content-type;host", "UNSIGNED-PAYLOAD",
	}, "\n")
	stringToSign := signatureString(now, credentialScope, canonicalRequest)
	query.Set("X-Amz-Signature", client.signature(date, stringToSign))
	target.RawQuery = query.Encode()
	return target.String(), nil
}

func (client *Client) Head(ctx context.Context, key string) (ObjectInfo, error) {
	target, err := client.objectURL(key)
	if err != nil {
		return ObjectInfo{}, err
	}
	now := client.now().UTC()
	payloadHash := hashHex(nil)
	date := now.Format("20060102")
	scope := date + "/" + client.config.Region + "/s3/aws4_request"
	canonicalHeaders := "host:" + target.Host + "\n" + "x-amz-content-sha256:" + payloadHash + "\n" + "x-amz-date:" + now.Format("20060102T150405Z") + "\n"
	signedHeaders := "host;x-amz-content-sha256;x-amz-date"
	canonicalRequest := strings.Join([]string{http.MethodHead, target.EscapedPath(), "", canonicalHeaders, signedHeaders, payloadHash}, "\n")
	authorization := "AWS4-HMAC-SHA256 Credential=" + client.config.AccessKey + "/" + scope +
		", SignedHeaders=" + signedHeaders + ", Signature=" + client.signature(date, signatureString(now, scope, canonicalRequest))
	request, err := http.NewRequestWithContext(ctx, http.MethodHead, target.String(), nil)
	if err != nil {
		return ObjectInfo{}, err
	}
	request.Header.Set("X-Amz-Date", now.Format("20060102T150405Z"))
	request.Header.Set("X-Amz-Content-Sha256", payloadHash)
	request.Header.Set("Authorization", authorization)
	response, err := client.http.Do(request)
	if err != nil {
		return ObjectInfo{}, fmt.Errorf("head object: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return ObjectInfo{}, fmt.Errorf("object storage returned HTTP %d", response.StatusCode)
	}
	return ObjectInfo{SizeBytes: response.ContentLength, ContentType: response.Header.Get("Content-Type")}, nil
}

func (client *Client) objectURL(key string) (*url.URL, error) {
	if key == "" || strings.Contains(key, "..") {
		return nil, errors.New("invalid object key")
	}
	target, err := url.Parse(client.config.Endpoint)
	if err != nil {
		return nil, err
	}
	target.Path = path.Join(target.Path, client.config.Bucket, key)
	return target, nil
}

func signatureString(now time.Time, scope, canonicalRequest string) string {
	return strings.Join([]string{"AWS4-HMAC-SHA256", now.Format("20060102T150405Z"), scope, hashHex([]byte(canonicalRequest))}, "\n")
}

func (client *Client) signature(date, value string) string {
	dateKey := hmacSum([]byte("AWS4"+client.config.SecretKey), date)
	regionKey := hmacSum(dateKey, client.config.Region)
	serviceKey := hmacSum(regionKey, "s3")
	signingKey := hmacSum(serviceKey, "aws4_request")
	return hex.EncodeToString(hmacSum(signingKey, value))
}

func hmacSum(key []byte, value string) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte(value))
	return mac.Sum(nil)
}

func hashHex(value []byte) string {
	sum := sha256.Sum256(value)
	return hex.EncodeToString(sum[:])
}
