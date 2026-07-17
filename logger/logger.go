package logger

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

// Field ...
type Field = zapcore.Field

var (
	// Int ..
	Int = zap.Int
	// String ...
	String = zap.String
	// Error ...
	Error = zap.Error
	// Bool ...
	Bool = zap.Bool
	// Any ...
	Any = zap.Any
	// Duration ...
	Duration = zap.Duration
	// Int64 ...
	Int64 = zap.Int64
)

// Proto returns a Field that serializes a protobuf message as proper JSON
// instead of Go's default proto text format.
func Proto(key string, msg proto.Message) Field {
	if msg == nil {
		return zap.Any(key, nil)
	}
	b, err := protojson.Marshal(msg)
	if err != nil {
		return zap.String(key, fmt.Sprintf("%v", msg))
	}
	return zap.Any(key, json.RawMessage(b))
}

// Logger ...
type LoggerI interface {
	Debug(msg string, fields ...Field)
	Info(msg string, fields ...Field)
	Warn(msg string, fields ...Field)
	Error(msg string, fields ...Field)
	DPanic(msg string, fields ...Field)
	Panic(msg string, fields ...Field)
	Fatal(msg string, fields ...Field)

	// WithCtx ...
	DebugWithCtx(ctx context.Context, msg string, fields ...Field)
	InfoWithCtx(ctx context.Context, msg string, fields ...Field)
	WarnWithCtx(ctx context.Context, msg string, fields ...Field)
	ErrorWithCtx(ctx context.Context, msg string, fields ...Field)
	DPanicWithCtx(ctx context.Context, msg string, fields ...Field)
	PanicWithCtx(ctx context.Context, msg string, fields ...Field)
	FatalWithCtx(ctx context.Context, msg string, fields ...Field)

	// Timer starts a timer and returns a function that logs the elapsed duration when called.
	// Usage: done := log.Timer("db_query"); defer done()
	Timer(msg string, fields ...Field) func()
	TimerWithCtx(ctx context.Context, msg string, fields ...Field) func()

	// HandleError
	HandleProtoError(err error, message string, req proto.Message, code codes.Code, reqId string) error
	HandleError(err error, message string, req any, code codes.Code, reqId string) error
	HandleProtoErrorCtx(ctx context.Context, err error, message string, req proto.Message, code codes.Code) error
	HandleErrorCtx(ctx context.Context, err error, message string, req any, code codes.Code) error
}

type loggerImpl struct {
	zap          *zap.Logger
	requestIdKey string
}

// NewLogger ...
// namespace - logger namespace
// level - logger level
// requestIdKey - request id key. default:request_id
func NewLogger(namespace string, level string, requestIdKey ...string) LoggerI {
	if level == "" {
		level = LevelInfo
	}

	if len(requestIdKey) == 0 {
		requestIdKey = []string{"request_id"}
	}

	logger := loggerImpl{
		zap:          newZapLogger(namespace, level),
		requestIdKey: requestIdKey[0],
	}

	return &logger
}

func (l *loggerImpl) Debug(msg string, fields ...Field) {
	l.zap.Debug(msg, fields...)
}

func (l *loggerImpl) Info(msg string, fields ...Field) {
	l.zap.Info(msg, fields...)
}

func (l *loggerImpl) Warn(msg string, fields ...Field) {
	l.zap.Warn(msg, fields...)
}

func (l *loggerImpl) Error(msg string, fields ...Field) {
	l.zap.Error(msg, fields...)
}

func (l *loggerImpl) DPanic(msg string, fields ...Field) {
	l.zap.DPanic(msg, fields...)
}

func (l *loggerImpl) Panic(msg string, fields ...Field) {
	l.zap.Panic(msg, fields...)
}

func (l *loggerImpl) Fatal(msg string, fields ...Field) {
	l.zap.Fatal(msg, fields...)
}

func (l *loggerImpl) DebugWithCtx(ctx context.Context, msg string, fields ...Field) {
	fields = append(fields, String(l.requestIdKey, l.getRequestId(ctx, l.requestIdKey)))
	l.zap.Debug(msg, fields...)
}

func (l *loggerImpl) InfoWithCtx(ctx context.Context, msg string, fields ...Field) {
	fields = append(fields, String(l.requestIdKey, l.getRequestId(ctx, l.requestIdKey)))
	l.zap.Info(msg, fields...)
}

func (l *loggerImpl) WarnWithCtx(ctx context.Context, msg string, fields ...Field) {
	fields = append(fields, String(l.requestIdKey, l.getRequestId(ctx, l.requestIdKey)))
	l.zap.Warn(msg, fields...)
}

func (l *loggerImpl) ErrorWithCtx(ctx context.Context, msg string, fields ...Field) {
	fields = append(fields, String(l.requestIdKey, l.getRequestId(ctx, l.requestIdKey)))
	l.zap.Error(msg, fields...)
}

func (l *loggerImpl) DPanicWithCtx(ctx context.Context, msg string, fields ...Field) {
	fields = append(fields, String(l.requestIdKey, l.getRequestId(ctx, l.requestIdKey)))
	l.zap.DPanic(msg, fields...)
}

func (l *loggerImpl) PanicWithCtx(ctx context.Context, msg string, fields ...Field) {
	fields = append(fields, String(l.requestIdKey, l.getRequestId(ctx, l.requestIdKey)))
	l.zap.Panic(msg, fields...)
}

func (l *loggerImpl) FatalWithCtx(ctx context.Context, msg string, fields ...Field) {
	fields = append(fields, String(l.requestIdKey, l.getRequestId(ctx, l.requestIdKey)))
	l.zap.Fatal(msg, fields...)
}

func (l *loggerImpl) Timer(msg string, fields ...Field) func() {
	start := time.Now()
	return func() {
		fields = append(fields, Duration("duration", time.Since(start)))
		l.zap.Info(msg, fields...)
	}
}

func (l *loggerImpl) TimerWithCtx(ctx context.Context, msg string, fields ...Field) func() {
	start := time.Now()
	return func() {
		fields = append(fields, String(l.requestIdKey, l.getRequestId(ctx, l.requestIdKey)), Duration("duration", time.Since(start)))
		l.zap.Info(msg, fields...)
	}
}

func (l *loggerImpl) HandleProtoError(err error, message string, req proto.Message, code codes.Code, reqId string) error {
	if err == nil {
		return status.Error(code, message)
	}
	l.Error(message, Error(err), Proto("req", req), String(l.requestIdKey, reqId))
	return status.Error(code, message+" "+err.Error())
}

func (l *loggerImpl) HandleError(err error, message string, req any, code codes.Code, reqId string) error {
	if err == nil {
		return status.Error(code, message)
	}
	l.Error(message, Error(err), Any("req", req), String(l.requestIdKey, reqId))
	return status.Error(code, message+" "+err.Error())
}

func (l *loggerImpl) HandleProtoErrorCtx(ctx context.Context, err error, message string, req proto.Message, code codes.Code) error {
	return l.HandleProtoError(err, message, req, code, l.getRequestId(ctx, l.requestIdKey))
}

func (l *loggerImpl) HandleErrorCtx(ctx context.Context, err error, message string, req any, code codes.Code) error {
	return l.HandleError(err, message, req, code, l.getRequestId(ctx, l.requestIdKey))
}

// GetNamed ...
func GetNamed(l LoggerI, name string) LoggerI {
	switch v := l.(type) {
	case *loggerImpl:
		v.zap = v.zap.Named(name)
		return v
	default:
		l.Info("logger.GetNamed: invalid logger type")
		return l
	}
}

// WithFields ...
func WithFields(l LoggerI, fields ...Field) LoggerI {
	switch v := l.(type) {
	case *loggerImpl:
		return &loggerImpl{
			zap:          v.zap.With(fields...),
			requestIdKey: v.requestIdKey,
		}
	default:
		l.Info("logger.WithFields: invalid logger type")
		return l
	}
}

// Cleanup ...
func Cleanup(l LoggerI) error {
	switch v := l.(type) {
	case *loggerImpl:
		return v.zap.Sync()
	default:
		l.Info("logger.Cleanup: invalid logger type")
		return nil
	}
}

func (l *loggerImpl) getRequestId(ctx context.Context, key string) string {
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		arr := md.Get(key)
		if len(arr) > 0 {
			return arr[0]
		}
	}

	if id, ok := ctx.Value(key).(string); ok {
		return id
	}

	return ""
}
