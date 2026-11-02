package audit

import (
	"context"
	"net/http"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

// The actor travels from the gateway to the services as metadata (gRPC) or headers (HTTP), the way the tenant does (pkg/tenant): services believe it because only
// the gateway and the services can reach them. The request id already travels as x-request-id / X-Request-Id.
const (
	mdActorType  = "x-payrudder-audit-actor-type"
	mdActorID    = "x-payrudder-audit-actor-id"
	mdActorEmail = "x-payrudder-audit-actor-email"
	mdIP         = "x-payrudder-audit-ip"
	mdRequestID  = "x-request-id"

	headerActorType  = "X-Payrudder-Audit-Actor-Type"
	headerActorID    = "X-Payrudder-Audit-Actor-Id"
	headerActorEmail = "X-Payrudder-Audit-Actor-Email"
	headerIP         = "X-Payrudder-Audit-Ip"
	headerRequestID  = "X-Request-Id"
)

func first(md metadata.MD, key string) string {
	if v := md.Get(key); len(v) > 0 {
		return v[0]
	}
	return ""
}

// capped keeps a header from becoming a log line of any length; Record cuts again to the column sizes.
func capped(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// UnaryServerInterceptor puts the caller's actor on the handler's context.
func UnaryServerInterceptor() grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, _ *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		return handler(fromIncoming(ctx), req)
	}
}

func fromIncoming(ctx context.Context) context.Context {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ctx
	}
	a := Actor{Type: capped(first(md, mdActorType), 20), ID: capped(first(md, mdActorID), 120), Email: capped(first(md, mdActorEmail), 254),
		IP: capped(first(md, mdIP), 45), RequestID: capped(first(md, mdRequestID), 120)}
	if a == (Actor{}) {
		return ctx
	}
	return NewContext(ctx, a)
}

// UnaryClientInterceptor forwards the actor of ctx to the callee (the request id is the caller's own x-request-id when it set one).
func UnaryClientInterceptor() grpc.UnaryClientInterceptor {
	return func(ctx context.Context, method string, req, reply any, cc *grpc.ClientConn, invoker grpc.UnaryInvoker, opts ...grpc.CallOption) error {
		return invoker(outgoing(ctx), method, req, reply, cc, opts...)
	}
}

func outgoing(ctx context.Context) context.Context {
	a := FromContext(ctx)
	var kv []string
	for k, v := range map[string]string{mdActorType: a.Type, mdActorID: a.ID, mdActorEmail: a.Email, mdIP: a.IP} {
		if v != "" {
			kv = append(kv, k, v)
		}
	}
	if a.RequestID != "" {
		if md, ok := metadata.FromOutgoingContext(ctx); !ok || len(md.Get(mdRequestID)) == 0 {
			kv = append(kv, mdRequestID, a.RequestID)
		}
	}
	if len(kv) == 0 {
		return ctx
	}
	return metadata.AppendToOutgoingContext(ctx, kv...)
}

// SetHeaders writes the actor of ctx into the headers of an outgoing HTTP request (the gateway to payments-worker). Headers the caller sent are replaced, never kept.
func SetHeaders(ctx context.Context, h http.Header) {
	a := FromContext(ctx)
	for k, v := range map[string]string{headerActorType: a.Type, headerActorID: a.ID, headerActorEmail: a.Email, headerIP: a.IP} {
		if v == "" {
			h.Del(k)
		} else {
			h.Set(k, v)
		}
	}
	if a.RequestID != "" {
		h.Set(headerRequestID, a.RequestID)
	}
}

// Middleware puts the actor in the headers of a request on its context. It is for an HTTP service that only the gateway reaches (payments-worker).
func Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		a := Actor{Type: capped(r.Header.Get(headerActorType), 20), ID: capped(r.Header.Get(headerActorID), 120), Email: capped(r.Header.Get(headerActorEmail), 254),
			IP: capped(r.Header.Get(headerIP), 45), RequestID: capped(r.Header.Get(headerRequestID), 120)}
		if a != (Actor{}) {
			r = r.WithContext(NewContext(r.Context(), a))
		}
		next.ServeHTTP(w, r)
	})
}
