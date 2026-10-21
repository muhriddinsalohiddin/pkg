package watchdog

import (
	"context"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/peer"
)

// DefaultRequestIDKeys are the metadata keys looked at for a request id, in
// order. gRPC lowercases metadata keys, so these are all lowercase.
var DefaultRequestIDKeys = []string{"request_id", "x-request-id", "request-id"}

// InterceptorOption configures the interceptors.
type InterceptorOption func(*interceptorConfig)

type interceptorConfig struct {
	requestIDKeys []string
	caller        func(context.Context) string
}

// WithRequestIDKeys replaces the metadata keys searched for a request id.
// A report whose entries carry the same id as the logs is the difference
// between "something was slow" and knowing which call it was.
func WithRequestIDKeys(keys ...string) InterceptorOption {
	return func(c *interceptorConfig) { c.requestIDKeys = keys }
}

// WithCaller supplies who is behind the call — a TIN, a tenant, a user id.
// It runs before the handler, so it should be cheap; anything that needs the
// handler's own work should use Entry.SetCaller instead.
func WithCaller(f func(context.Context) string) InterceptorOption {
	return func(c *interceptorConfig) { c.caller = f }
}

func newInterceptorConfig(opts []InterceptorOption) interceptorConfig {
	cfg := interceptorConfig{requestIDKeys: DefaultRequestIDKeys}
	for _, o := range opts {
		o(&cfg)
	}

	return cfg
}

// UnaryServerInterceptor registers every unary call for as long as it runs,
// so a watchdog report can say what the process was doing.
//
// Put it in the chain with whatever else is there:
//
//	grpc.NewServer(grpc.ChainUnaryInterceptor(
//		logger.UnaryServerInterceptor(log),
//		watchdog.UnaryServerInterceptor(reg),
//	))
//
// A nil registry makes this a pass-through, so a service can wire it in
// unconditionally and decide later whether to run a watchdog.
func UnaryServerInterceptor(reg *Registry, opts ...InterceptorOption) grpc.UnaryServerInterceptor {
	cfg := newInterceptorConfig(opts)

	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		_, done := reg.Begin(Work{
			ID:     requestID(ctx, cfg.requestIDKeys),
			Method: "unary",
			Path:   info.FullMethod,
			IP:     peerAddr(ctx),
			Caller: callerOf(ctx, cfg),
		})
		defer done()

		return handler(ctx, req)
	}
}

// StreamServerInterceptor is the same for streams, which matter more here
// than they do for logging: a stream can stay open for minutes, and those are
// exactly the entries a report should show.
func StreamServerInterceptor(reg *Registry, opts ...InterceptorOption) grpc.StreamServerInterceptor {
	cfg := newInterceptorConfig(opts)

	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		ctx := ss.Context()

		_, done := reg.Begin(Work{
			ID:     requestID(ctx, cfg.requestIDKeys),
			Method: "stream",
			Path:   info.FullMethod,
			IP:     peerAddr(ctx),
			Caller: callerOf(ctx, cfg),
		})
		defer done()

		return handler(srv, ss)
	}
}

func callerOf(ctx context.Context, cfg interceptorConfig) string {
	if cfg.caller == nil {
		return ""
	}

	return cfg.caller(ctx)
}

func requestID(ctx context.Context, keys []string) string {
	md, ok := metadata.FromIncomingContext(ctx)
	if !ok {
		return ""
	}

	for _, k := range keys {
		if v := md.Get(k); len(v) > 0 && v[0] != "" {
			return v[0]
		}
	}

	return ""
}

func peerAddr(ctx context.Context) string {
	p, ok := peer.FromContext(ctx)
	if !ok || p.Addr == nil {
		return ""
	}

	return p.Addr.String()
}
