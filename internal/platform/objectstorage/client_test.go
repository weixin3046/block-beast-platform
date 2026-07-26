package objectstorage

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestPresignPutIncludesScopedSignature(t *testing.T) {
	client, err := NewClient(Config{
		Endpoint: "https://storage.example.com", Region: "ap-east-1", Bucket: "uploads",
		AccessKey: "access", SecretKey: "secret",
	}, nil)
	if err != nil {
		t.Fatal(err)
	}
	client.now = func() time.Time { return time.Date(2026, 7, 26, 12, 0, 0, 0, time.UTC) }
	signed, err := client.PresignPut("uploads/user/object", "image/png", 10*time.Minute)
	if err != nil {
		t.Fatal(err)
	}
	target, err := url.Parse(signed)
	if err != nil {
		t.Fatal(err)
	}
	if target.Path != "/uploads/uploads/user/object" {
		t.Fatalf("path = %q", target.Path)
	}
	for _, key := range []string{"X-Amz-Algorithm", "X-Amz-Credential", "X-Amz-Date", "X-Amz-Expires", "X-Amz-SignedHeaders", "X-Amz-Signature"} {
		if target.Query().Get(key) == "" {
			t.Fatalf("%s is missing", key)
		}
	}
	if target.Query().Get("X-Amz-SignedHeaders") != "content-type;host" {
		t.Fatalf("signed headers = %q", target.Query().Get("X-Amz-SignedHeaders"))
	}
}

func TestHeadSignsRequestAndReturnsMetadata(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodHead || request.URL.Path != "/bucket/key" {
			t.Fatalf("request = %s %s", request.Method, request.URL.Path)
		}
		if !strings.HasPrefix(request.Header.Get("Authorization"), "AWS4-HMAC-SHA256 Credential=access/") {
			t.Fatalf("authorization = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "image/webp")
		writer.Header().Set("Content-Length", "123")
		writer.WriteHeader(http.StatusOK)
	}))
	defer server.Close()
	client, err := NewClient(Config{
		Endpoint: server.URL, Region: "us-east-1", Bucket: "bucket",
		AccessKey: "access", SecretKey: "secret",
	}, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	info, err := client.Head(context.Background(), "key")
	if err != nil {
		t.Fatal(err)
	}
	if info.SizeBytes != 123 || info.ContentType != "image/webp" {
		t.Fatalf("info = %+v", info)
	}
}
