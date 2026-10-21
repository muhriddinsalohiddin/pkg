package watchdog

import (
	"context"
	"errors"
	"testing"

	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/metadata"
)

func timeNow() time.Time { return time.Now() }

func unaryInfo(method string) *grpc.UnaryServerInfo {
	return &grpc.UnaryServerInfo{FullMethod: method}
}

// The call is in the registry while the handler runs and gone after it.
func TestUnaryInterceptorRegistersWhileRunning(t *testing.T) {
	reg := NewRegistry()
	intercept := UnaryServerInterceptor(reg)

	var inFlight int

	_, err := intercept(context.Background(), nil, unaryInfo("/doc.Service/Get"),
		func(context.Context, any) (any, error) {
			inFlight = reg.Len()

			return "ok", nil
		})
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}

	if inFlight != 1 {
		t.Errorf("%d in flight during the handler, want 1", inFlight)
	}

	if got := reg.Len(); got != 0 {
		t.Errorf("%d left in the registry after the handler, want 0", got)
	}
}

// A handler that fails must still leave the registry. An entry that is never
// removed makes every later report wrong, and errors are the common path for
// exactly the calls worth watching.
func TestUnaryInterceptorClearsOnError(t *testing.T) {
	reg := NewRegistry()
	intercept := UnaryServerInterceptor(reg)

	wantErr := errors.New("backend down")

	_, err := intercept(context.Background(), nil, unaryInfo("/doc.Service/Get"),
		func(context.Context, any) (any, error) { return nil, wantErr })

	if !errors.Is(err, wantErr) {
		t.Errorf("error = %v, want %v", err, wantErr)
	}

	if got := reg.Len(); got != 0 {
		t.Errorf("%d left in the registry after a failed call", got)
	}
}

// And a handler that panics. The interceptor does not recover — that is
// somebody else's job — but the deferred removal still runs.
func TestUnaryInterceptorClearsOnPanic(t *testing.T) {
	reg := NewRegistry()
	intercept := UnaryServerInterceptor(reg)

	func() {
		defer func() { _ = recover() }()

		_, _ = intercept(context.Background(), nil, unaryInfo("/doc.Service/Get"),
			func(context.Context, any) (any, error) { panic("boom") })
	}()

	if got := reg.Len(); got != 0 {
		t.Errorf("%d left in the registry after a panic", got)
	}
}

// The request id ties a report entry to the logs, which is most of its value.
func TestUnaryInterceptorReadsTheRequestID(t *testing.T) {
	tests := []struct {
		name string
		md   metadata.MD
		want string
	}{
		{name: "request_id", md: metadata.Pairs("request_id", "abc"), want: "abc"},
		{name: "x-request-id", md: metadata.Pairs("x-request-id", "def"), want: "def"},
		{name: "request-id", md: metadata.Pairs("request-id", "ghi"), want: "ghi"},
		{name: "none", md: metadata.MD{}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg := NewRegistry()
			intercept := UnaryServerInterceptor(reg)
			ctx := metadata.NewIncomingContext(context.Background(), tt.md)

			var got string

			_, _ = intercept(ctx, nil, unaryInfo("/doc.Service/Get"),
				func(context.Context, any) (any, error) {
					if list := reg.Oldest(1, timeNow()); len(list) == 1 {
						got = list[0].ID
					}

					return nil, nil
				})

			if got != tt.want {
				t.Errorf("request id = %q, want %q", got, tt.want)
			}
		})
	}
}

// WithCaller fills in who is behind the call, so a report says which tenant
// is responsible rather than only which method was slow.
func TestUnaryInterceptorWithCaller(t *testing.T) {
	reg := NewRegistry()
	intercept := UnaryServerInterceptor(reg,
		WithCaller(func(context.Context) string { return "303303592" }))

	var got string

	_, _ = intercept(context.Background(), nil, unaryInfo("/doc.Service/Get"),
		func(context.Context, any) (any, error) {
			if list := reg.Oldest(1, timeNow()); len(list) == 1 {
				got = list[0].Caller
			}

			return nil, nil
		})

	if got != "303303592" {
		t.Errorf("caller = %q, want %q", got, "303303592")
	}
}

// A nil registry is a pass-through, so a service can chain the interceptor in
// unconditionally and decide separately whether to run a watchdog.
func TestUnaryInterceptorWithNilRegistry(t *testing.T) {
	intercept := UnaryServerInterceptor(nil)

	got, err := intercept(context.Background(), nil, unaryInfo("/doc.Service/Get"),
		func(context.Context, any) (any, error) { return "ok", nil })
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}

	if got != "ok" {
		t.Errorf("response = %v, want ok", got)
	}
}

type fakeStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (f fakeStream) Context() context.Context { return f.ctx }

// Streams stay open far longer than unary calls, which makes them exactly
// the entries a report should carry.
func TestStreamInterceptorRegisters(t *testing.T) {
	reg := NewRegistry()
	intercept := StreamServerInterceptor(reg)

	var (
		inFlight int
		method   string
	)

	err := intercept(nil, fakeStream{ctx: context.Background()},
		&grpc.StreamServerInfo{FullMethod: "/doc.Service/Watch"},
		func(any, grpc.ServerStream) error {
			inFlight = reg.Len()
			if list := reg.Oldest(1, timeNow()); len(list) == 1 {
				method = list[0].Method
			}

			return nil
		})
	if err != nil {
		t.Fatalf("interceptor: %v", err)
	}

	if inFlight != 1 {
		t.Errorf("%d in flight during the stream, want 1", inFlight)
	}

	if method != "stream" {
		t.Errorf("method = %q, want %q", method, "stream")
	}

	if got := reg.Len(); got != 0 {
		t.Errorf("%d left after the stream closed", got)
	}
}
