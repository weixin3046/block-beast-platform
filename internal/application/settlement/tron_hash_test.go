package settlement

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/block-beast/platform/internal/domain/game"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/dynamicpb"
)

func tronTestServer(t *testing.T, handler http.HandlerFunc) (*httptest.Server, TronHashResultSource) {
	t.Helper()
	server := httptest.NewServer(handler)
	return server, newTronHashResultSourceForEndpoint(server.URL, "test-key")
}

func TestTronHashOutcomeUsesOfficialBlockEndpoint(t *testing.T) {
	server, source := tronTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/wallet/getblockbynum" || request.Header.Get("TRON-PRO-API-KEY") != "test-key" {
			t.Fatalf("request = %s, api key = %q", request.URL.Path, request.Header.Get("TRON-PRO-API-KEY"))
		}
		var body map[string]int64
		_ = json.NewDecoder(request.Body).Decode(&body)
		if body["num"] != 84687810 {
			t.Fatalf("height = %d", body["num"])
		}
		_, _ = writer.Write([]byte(`{"blockID":"abcdef5"}`))
	})
	defer server.Close()

	outcome, err := source.Outcome(context.Background(), game.Round{Sequence: 84687810, BetClosesAt: time.Now()}, game.Rules{Outcomes: []string{"0", "1", "2", "3", "4", "5", "6", "7", "8", "9"}, Source: "tron_hash"})
	if err != nil || len(outcome) != 1 || outcome[0] != "5" {
		t.Fatalf("outcome = %v, err = %v", outcome, err)
	}
}

func TestTronGRPCBlockDecodesOfficialBlockExtentionWireFields(t *testing.T) {
	raw := dynamicpb.NewMessage(tronGRPCDescriptors.rawBlockHeader)
	raw.Set(tronGRPCDescriptors.rawBlockHeader.Fields().ByNumber(1), protoreflect.ValueOfInt64(1700000000123))
	raw.Set(tronGRPCDescriptors.rawBlockHeader.Fields().ByNumber(7), protoreflect.ValueOfInt64(84687810))
	header := dynamicpb.NewMessage(tronGRPCDescriptors.blockHeader)
	header.Set(tronGRPCDescriptors.blockHeader.Fields().ByNumber(1), protoreflect.ValueOfMessage(raw))
	response := dynamicpb.NewMessage(tronGRPCDescriptors.blockExtension)
	response.Set(tronGRPCDescriptors.blockExtension.Fields().ByNumber(2), protoreflect.ValueOfMessage(header))
	response.Set(tronGRPCDescriptors.blockExtension.Fields().ByNumber(3), protoreflect.ValueOfBytes([]byte{0xab, 0xcd, 0xef, 0x05}))

	block, err := tronBlockFromGRPC(response)
	if err != nil {
		t.Fatal(err)
	}
	if block.Hash != "abcdef05" || block.Number() != 84687810 || block.Timestamp() != 1700000000123 {
		t.Fatalf("block = %#v", block)
	}
}

func TestCurrentTronBlockUsesOfficialEndpoint(t *testing.T) {
	server, source := tronTestServer(t, func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/wallet/getnowblock" {
			t.Fatalf("path = %s", request.URL.Path)
		}
		_, _ = writer.Write([]byte(`{"blockID":"abc","block_header":{"raw_data":{"number":84687810,"timestamp":1700000000123}}}`))
	})
	defer server.Close()

	height, blockAt, err := source.CurrentBlock(context.Background())
	if err != nil || height != 84687810 || blockAt.UnixMilli() != 1700000000123 {
		t.Fatalf("height = %d, time = %s, err = %v", height, blockAt, err)
	}
}

func TestTronHashBlockNotFound(t *testing.T) {
	server, source := tronTestServer(t, func(writer http.ResponseWriter, request *http.Request) { _, _ = writer.Write([]byte(`{}`)) })
	defer server.Close()
	_, err := source.Outcome(context.Background(), game.Round{Sequence: 1}, game.Rules{Source: "tron_hash"})
	if !errors.Is(err, ErrBlockNotFound) {
		t.Fatalf("err = %v", err)
	}
}
