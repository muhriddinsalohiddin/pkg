package logger

import (
	"context"
	"time"

	"go.uber.org/zap"
	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
)

// UnaryServerInterceptor returns a gRPC unary server interceptor that logs
// each request with method, duration, status code, and request_id (from metadata).
func UnaryServerInterceptor(l LoggerI) grpc.UnaryServerInterceptor {
	return func(
		ctx context.Context,
		req any,
		info *grpc.UnaryServerInfo,
		handler grpc.UnaryHandler,
	) (any, error) {
		start := time.Now()

		resp, err := handler(ctx, req)

		duration := time.Since(start)
		code := status.Code(err)

		fields := []Field{
			String("grpc_method", info.FullMethod),
			Duration("duration", duration),
			String("grpc_code", code.String()),
		}

		// extract request_id from metadata
		if impl, ok := l.(*loggerImpl); ok {
			if requestID := impl.getRequestId(ctx, impl.requestIdKey); requestID != "" {
				fields = append(fields, String(impl.requestIdKey, requestID))
			}
		}

		if err != nil {
			fields = append(fields, zap.Error(err))
			l.Error("grpc_request", fields...)
		} else {
			l.Info("grpc_request", fields...)
		}

		return resp, err
	}
}

// StreamServerInterceptor returns a gRPC stream server interceptor that logs
// each stream with method, duration, status code, and request_id.
func StreamServerInterceptor(l LoggerI) grpc.StreamServerInterceptor {
	return func(
		srv any,
		ss grpc.ServerStream,
		info *grpc.StreamServerInfo,
		handler grpc.StreamHandler,
	) error {
		start := time.Now()
		ctx := ss.Context()

		err := handler(srv, ss)

		duration := time.Since(start)
		code := status.Code(err)

		fields := []Field{
			String("grpc_method", info.FullMethod),
			Duration("duration", duration),
			String("grpc_code", code.String()),
		}

		if impl, ok := l.(*loggerImpl); ok {
			if requestID := impl.getRequestId(ctx, impl.requestIdKey); requestID != "" {
				fields = append(fields, String(impl.requestIdKey, requestID))
			}
		}

		if err != nil {
			fields = append(fields, zap.Error(err))
			l.Error("grpc_stream", fields...)
		} else {
			l.Info("grpc_stream", fields...)
		}

		return err
	}
}

// UnaryClientInterceptor returns a gRPC unary client interceptor that logs
// outgoing requests with method, duration, and status code.
// It also forwards request_id from context to outgoing metadata.
func UnaryClientInterceptor(l LoggerI) grpc.UnaryClientInterceptor {
	return func(
		ctx context.Context,
		method string,
		req, reply any,
		cc *grpc.ClientConn,
		invoker grpc.UnaryInvoker,
		opts ...grpc.CallOption,
	) error {
		// forward request_id to outgoing metadata
		if impl, ok := l.(*loggerImpl); ok {
			if requestID := impl.getRequestId(ctx, impl.requestIdKey); requestID != "" {
				ctx = metadata.AppendToOutgoingContext(ctx, impl.requestIdKey, requestID)
			}
		}

		start := time.Now()

		err := invoker(ctx, method, req, reply, cc, opts...)

		duration := time.Since(start)
		code := status.Code(err)

		fields := []Field{
			String("grpc_method", method),
			Duration("duration", duration),
			String("grpc_code", code.String()),
		}

		if err != nil {
			fields = append(fields, zap.Error(err))
			l.Error("grpc_client_request", fields...)
		} else {
			l.Info("grpc_client_request", fields...)
		}

		return err
	}
}
