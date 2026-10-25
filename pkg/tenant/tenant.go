// Package tenant carries the authenticated tenant through a request: the gateway sets it in gRPC metadata,
// services read it with the server interceptors, and the client interceptor forwards it to downstream calls.
//
// Services trust this metadata only because callers are restricted to the gateway (and each other) by the network;
// the tenant is never taken from a request body.
package tenant

import (
	"context"

	"github.com/0furkancolak/payrudder/pkg/errkey"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/google/uuid"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
)

// Metadata keys.
const (
	MDTenantID    = "x-payrudder-tenant-id"
	MDEnvironment = "x-payrudder-environment"
	MDTimezone    = "x-payrudder-timezone"
	MDAPIKeyID    = "x-payrudder-api-key-id" //nolint:gosec // metadata key name, not a credential
	MDTier        = "x-payrudder-tier"
	MDOperator    = "x-payrudder-operator"
)

// Environment values (ADR 0011).
const (
	EnvSandbox = "sandbox"
	EnvLive    = "live"
)

// Info is the tenant of the current request.
type Info struct {
	ID          uuid.UUID
	Environment string
	Timezone    string
	// Tier is "free", "standard" or "enterprise" (rate limits, storage quota).
	Tier     string
	APIKeyID string
}

type infoKey struct{}
type operatorKey struct{}

// NewContext returns ctx carrying info.
func NewContext(ctx context.Context, info Info) context.Context {
	return context.WithValue(ctx, infoKey{}, info)
}

// FromContext returns the tenant of ctx, if any.
func FromContext(ctx context.Context) (Info, bool) {
	info, ok := ctx.Value(infoKey{}).(Info)
	return info, ok && info.ID != uuid.Nil
}

// Require returns the tenant of ctx or an Unauthenticated status error.
func Require(ctx context.Context) (Info, error) {
	info, ok := FromContext(ctx)
	if !ok {
		return Info{}, errkey.New(codes.Unauthenticated, "identity.tenant.metadata_missing", "tenant metadata missing")
	}
	return info, nil
}

// WithOperator marks ctx as an operator call (the gateway checked the operator token).
func WithOperator(ctx context.Context) context.Context {
	return context.WithValue(ctx, operatorKey{}, true)
}

// IsOperator reports whether ctx is an operator call.
func IsOperator(ctx context.Context) bool {
	v, _ := ctx.Value(operatorKey{}).(bool)
	return v
}

// RequireOperator returns PermissionDenied unless ctx is an operator call.
func RequireOperator(ctx context.Context) error {
	if !IsOperator(ctx) {
		return errkey.New(codes.PermissionDenied, "identity.operator.forbidden", "operator only")
	}
	return nil
}

func first(md metadata.MD, key string) string {
	if v := md.Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

// fromIncoming reads tenant and operator metadata into ctx. A malformed tenant ID is an error.
func fromIncoming(ctx context.Context) (context.Context, error) {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx, nil
	}
	if first(md, MDOperator) == "true" {
		ctx = WithOperator(ctx)
	}
	raw := first(md, MDTenantID)
	if raw == "" {
		return ctx, nil
	}
	id, err := uuid.Parse(raw)
	if err != nil {
		return ctx, errkey.Newf(codes.InvalidArgument, "identity.tenant.bad_metadata", "malformed %s", MDTenantID)
	}
	trace.SpanFromContext(ctx).SetAttributes(attribute.String("payrudder.tenant_id", id.String()))
	return NewContext(ctx, Info{
		ID:          id,
		Environment: first(md, MDEnvironment),
		Timezone:    first(md, MDTimezone),
		Tier:        first(md, MDTier),
		APIKeyID:    first(md, MDAPIKeyID),
	}), nil
}

// UnaryServerInterceptor puts tenant and operator metadata into the handler context. It doesn't enforce presence:
// handlers call Require or RequireOperator.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		ctx, err := fromIncoming(ctx)
		if err != nil {
			return nil, err
		}
		return handler(ctx, req)
	}
}

type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w wrappedStream) Context() context.Context { return w.ctx }

// StreamServerInterceptor is the streaming counterpart of UnaryServerInterceptor.
func StreamServerInterceptor() grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, _ *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx, err := fromIncoming(ss.Context())
		if err != nil {
			return err
		}
		return handler(srv, wrappedStream{ServerStream: ss, ctx: ctx})
	}
}

// OutgoingContext copies the tenant and operator flag of ctx into outgoing gRPC metadata.
func OutgoingContext(ctx context.Context) context.Context {
	var kv []string
	if info, ok := FromContext(ctx); ok {
		kv = append(kv, MDTenantID, info.ID.String())
		if info.Environment != "" {
			kv = append(kv, MDEnvironment, info.Environment)
		}
		if info.Timezone != "" {
			kv = append(kv, MDTimezone, info.Timezone)
		}
		if info.Tier != "" {
			kv = append(kv, MDTier, info.Tier)
		}
		if info.APIKeyID != "" {
			kv = append(kv, MDAPIKeyID, info.APIKeyID)
		}
	}
	if IsOperator(ctx) {
		kv = append(kv, MDOperator, "true")
	}
	if len(kv) == 0 {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, kv...)
}

// UnaryClientInterceptor forwards the tenant of ctx to the callee.
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(OutgoingContext(ctx), method, req, reply, cc, opts...)
	}
}

// StreamClientInterceptor forwards the tenant of ctx on streaming calls.
func StreamClientInterceptor() grpc.StreamClientInterceptor {
	return func(ctx context.Context, desc *grpc.StreamDesc, cc *grpc.ClientConn, method string, streamer grpc.Streamer, opts ...grpc.CallOption) (grpc.ClientStream, error) {
		return streamer(OutgoingContext(ctx), desc, cc, method, opts...)
	}
}
