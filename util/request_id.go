package util

import (
	"context"

	"google.golang.org/grpc/metadata"
)

func GetRequestId(ctx context.Context, key string) string {
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
