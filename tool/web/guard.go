package web

import (
	"context"
	"net"
)

func GuardURL(ctx context.Context, raw string, lookup LookupFn) (string, error) {
	if lookup == nil {
		lookup = func(ctx context.Context, host string) ([]string, error) {
			return net.DefaultResolver.LookupHost(ctx, host)
		}
	}
	u, _, err := guardedURL(ctx, raw, nil, lookup)
	if err != nil {
		return "", err
	}
	return u.String(), nil
}
