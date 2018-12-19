// Code generated by protoc-gen-go. DO NOT EDIT.
// source: invoicesrpc/invoices.proto

package invoicesrpc // import "github.com/decred/dcrlnd/lnrpc/invoicesrpc"

import proto "github.com/golang/protobuf/proto"
import fmt "fmt"
import math "math"

import (
	context "golang.org/x/net/context"
	grpc "google.golang.org/grpc"
)

// Reference imports to suppress errors if they are not otherwise used.
var _ = proto.Marshal
var _ = fmt.Errorf
var _ = math.Inf

// This is a compile-time assertion to ensure that this generated file
// is compatible with the proto package it is being compiled against.
// A compilation error at this line likely means your copy of the
// proto package needs to be updated.
const _ = proto.ProtoPackageIsVersion2 // please upgrade the proto package

// Reference imports to suppress errors if they are not otherwise used.
var _ context.Context
var _ grpc.ClientConn

// This is a compile-time assertion to ensure that this generated file
// is compatible with the grpc package it is being compiled against.
const _ = grpc.SupportPackageIsVersion4

// InvoicesClient is the client API for Invoices service.
//
// For semantics around ctx use and closing/ending streaming RPCs, please refer to https://godoc.org/google.golang.org/grpc#ClientConn.NewStream.
type InvoicesClient interface {
}

type invoicesClient struct {
	cc *grpc.ClientConn
}

func NewInvoicesClient(cc *grpc.ClientConn) InvoicesClient {
	return &invoicesClient{cc}
}

// InvoicesServer is the server API for Invoices service.
type InvoicesServer interface {
}

func RegisterInvoicesServer(s *grpc.Server, srv InvoicesServer) {
	s.RegisterService(&_Invoices_serviceDesc, srv)
}

var _Invoices_serviceDesc = grpc.ServiceDesc{
	ServiceName: "invoicesrpc.Invoices",
	HandlerType: (*InvoicesServer)(nil),
	Methods:     []grpc.MethodDesc{},
	Streams:     []grpc.StreamDesc{},
	Metadata:    "invoicesrpc/invoices.proto",
}

func init() {
	proto.RegisterFile("invoicesrpc/invoices.proto", fileDescriptor_invoices_0ccb75223f0cc77e)
}

var fileDescriptor_invoices_0ccb75223f0cc77e = []byte{
	// 98 bytes of a gzipped FileDescriptorProto
	0x1f, 0x8b, 0x08, 0x00, 0x00, 0x00, 0x00, 0x00, 0x02, 0xff, 0xe2, 0x92, 0xca, 0xcc, 0x2b, 0xcb,
	0xcf, 0x4c, 0x4e, 0x2d, 0x2e, 0x2a, 0x48, 0xd6, 0x87, 0xb1, 0xf5, 0x0a, 0x8a, 0xf2, 0x4b, 0xf2,
	0x85, 0xb8, 0x91, 0xe4, 0x8c, 0xb8, 0xb8, 0x38, 0x3c, 0xa1, 0x5c, 0x27, 0x9d, 0x28, 0xad, 0xf4,
	0xcc, 0x92, 0x8c, 0xd2, 0x24, 0xbd, 0xe4, 0xfc, 0x5c, 0xfd, 0x94, 0xd4, 0xe4, 0xa2, 0xd4, 0x14,
	0xfd, 0x94, 0xe4, 0xa2, 0x9c, 0xbc, 0x14, 0xfd, 0x9c, 0x3c, 0x64, 0x93, 0x8a, 0x0a, 0x92, 0x93,
	0xd8, 0xc0, 0xa6, 0x19, 0x03, 0x02, 0x00, 0x00, 0xff, 0xff, 0x6f, 0xad, 0x40, 0xc7, 0x6b, 0x00,
	0x00, 0x00,
}