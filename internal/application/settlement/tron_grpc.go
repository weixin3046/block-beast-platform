package settlement

import (
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/reflect/protodesc"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/descriptorpb"
	"google.golang.org/protobuf/types/dynamicpb"
)

const tronGridGRPCFullNodeEndpoint = "grpc.trongrid.io:50051"

// tronGRPCBlockClient calls the official TRON Wallet gRPC service. The message
// descriptors below are the wire-compatible subset of TRON's official
// api.proto/core/Tron.proto required by GetNowBlock2/GetBlockByNum2.
type tronGRPCBlockClient struct {
	endpoint string
	apiKey   string

	mu   sync.Mutex
	conn *grpc.ClientConn
}

func newTronGRPCBlockClient(endpoint, apiKey string) *tronGRPCBlockClient {
	return &tronGRPCBlockClient{endpoint: endpoint, apiKey: apiKey}
}

func (client *tronGRPCBlockClient) Close() error {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.conn == nil {
		return nil
	}
	err := client.conn.Close()
	client.conn = nil
	return err
}

func (client *tronGRPCBlockClient) block(ctx context.Context, method string, height *int64) (tronBlock, error) {
	connection, err := client.connection(ctx)
	if err != nil {
		return tronBlock{}, err
	}
	request := dynamicpb.NewMessage(tronGRPCDescriptors.empty)
	if height != nil {
		request = dynamicpb.NewMessage(tronGRPCDescriptors.number)
		request.Set(tronGRPCDescriptors.number.Fields().ByNumber(1), protoreflect.ValueOfInt64(*height))
	}
	response := dynamicpb.NewMessage(tronGRPCDescriptors.blockExtension)
	if client.apiKey != "" {
		ctx = metadata.AppendToOutgoingContext(ctx, "TRON-PRO-API-KEY", client.apiKey)
	}
	if err := connection.Invoke(ctx, method, request, response); err != nil {
		return tronBlock{}, fmt.Errorf("call TRON gRPC: %w", err)
	}
	return tronBlockFromGRPC(response)
}

func (client *tronGRPCBlockClient) connection(ctx context.Context) (*grpc.ClientConn, error) {
	client.mu.Lock()
	defer client.mu.Unlock()
	if client.conn != nil {
		return client.conn, nil
	}
	connection, err := grpc.DialContext(ctx, client.endpoint, grpc.WithTransportCredentials(insecure.NewCredentials()), grpc.WithBlock())
	if err != nil {
		return nil, fmt.Errorf("connect TRON gRPC endpoint %q: %w", client.endpoint, err)
	}
	client.conn = connection
	return connection, nil
}

func tronBlockFromGRPC(message *dynamicpb.Message) (tronBlock, error) {
	blockID := message.Get(tronGRPCDescriptors.blockExtension.Fields().ByNumber(3)).Bytes()
	header := message.Get(tronGRPCDescriptors.blockExtension.Fields().ByNumber(2)).Message()
	if !header.IsValid() {
		return tronBlock{}, errors.New("TRON gRPC response has no block header")
	}
	raw := header.Get(tronGRPCDescriptors.blockHeader.Fields().ByNumber(1)).Message()
	if !raw.IsValid() {
		return tronBlock{}, errors.New("TRON gRPC response has no raw block header")
	}
	block := tronBlock{Hash: hex.EncodeToString(blockID)}
	block.Header.RawData.Timestamp = raw.Get(tronGRPCDescriptors.rawBlockHeader.Fields().ByNumber(1)).Int()
	block.Header.RawData.Number = raw.Get(tronGRPCDescriptors.rawBlockHeader.Fields().ByNumber(7)).Int()
	return block, nil
}

var tronGRPCDescriptors = newTronGRPCDescriptors()

type tronGRPCDescriptorSet struct {
	empty          protoreflect.MessageDescriptor
	number         protoreflect.MessageDescriptor
	rawBlockHeader protoreflect.MessageDescriptor
	blockHeader    protoreflect.MessageDescriptor
	blockExtension protoreflect.MessageDescriptor
}

func newTronGRPCDescriptors() tronGRPCDescriptorSet {
	optional := descriptorpb.FieldDescriptorProto_LABEL_OPTIONAL
	int64Type := descriptorpb.FieldDescriptorProto_TYPE_INT64
	bytesType := descriptorpb.FieldDescriptorProto_TYPE_BYTES
	messageType := descriptorpb.FieldDescriptorProto_TYPE_MESSAGE
	file, err := protodesc.NewFile(&descriptorpb.FileDescriptorProto{
		Name:    stringPointer("tron_grpc_subset.proto"),
		Syntax:  stringPointer("proto3"),
		Package: stringPointer("protocol"),
		MessageType: []*descriptorpb.DescriptorProto{
			{Name: stringPointer("EmptyMessage")},
			{Name: stringPointer("NumberMessage"), Field: []*descriptorpb.FieldDescriptorProto{{Name: stringPointer("num"), Number: int32Pointer(1), Label: &optional, Type: &int64Type}}},
			{Name: stringPointer("RawBlockHeader"), Field: []*descriptorpb.FieldDescriptorProto{{Name: stringPointer("timestamp"), Number: int32Pointer(1), Label: &optional, Type: &int64Type}, {Name: stringPointer("number"), Number: int32Pointer(7), Label: &optional, Type: &int64Type}}},
			{Name: stringPointer("BlockHeader"), Field: []*descriptorpb.FieldDescriptorProto{{Name: stringPointer("raw_data"), Number: int32Pointer(1), Label: &optional, Type: &messageType, TypeName: stringPointer(".protocol.RawBlockHeader")}}},
			{Name: stringPointer("BlockExtention"), Field: []*descriptorpb.FieldDescriptorProto{{Name: stringPointer("block_header"), Number: int32Pointer(2), Label: &optional, Type: &messageType, TypeName: stringPointer(".protocol.BlockHeader")}, {Name: stringPointer("blockid"), Number: int32Pointer(3), Label: &optional, Type: &bytesType}}},
		},
	}, nil)
	if err != nil {
		panic(fmt.Sprintf("build TRON gRPC descriptors: %v", err))
	}
	messages := file.Messages()
	return tronGRPCDescriptorSet{empty: messages.ByName("EmptyMessage"), number: messages.ByName("NumberMessage"), rawBlockHeader: messages.ByName("RawBlockHeader"), blockHeader: messages.ByName("BlockHeader"), blockExtension: messages.ByName("BlockExtention")}
}

func stringPointer(value string) *string { return &value }
func int32Pointer(value int32) *int32    { return &value }

func tronGRPCNowBlock(ctx context.Context, client *tronGRPCBlockClient) (tronBlock, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return client.block(requestCtx, "/protocol.Wallet/GetNowBlock2", nil)
}

func tronGRPCBlockByNumber(ctx context.Context, client *tronGRPCBlockClient, height int64) (tronBlock, error) {
	requestCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	return client.block(requestCtx, "/protocol.Wallet/GetBlockByNum2", &height)
}
