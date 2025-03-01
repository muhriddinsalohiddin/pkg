package util

import (
	"context"
	"fmt"

	"google.golang.org/grpc/metadata"
)

type mdIncomingKey struct{}

func GetRequestId(ctx context.Context, key string) string {
	mds, ok := ctx.Value(mdIncomingKey{}).(metadata.MD)
	fmt.Println("mds:", mds, "ok:", ok)
	if md, ok := metadata.FromIncomingContext(ctx); ok {
		arr := md.Get(key)
		if len(arr) > 0 {
			return arr[0]
		}
	}

	if id, ok := ctx.Value(key).(string); ok {
		return id
	}

	fmt.Println("Context does not contain request_id:", ctx)

	return ""
}
