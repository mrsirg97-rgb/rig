package web

import (
	"context"
	"net"
	"net/netip"
)

func GuardURL(ctx context.Context, raw string, lookup LookupFn) (string, error) {
	u, _, err := guardedURL(ctx, raw, nil, systemLookup(lookup))
	if err != nil {
		return "", err
	}
	return u.String(), nil
}

func PinHost(ctx context.Context, host string, lookup LookupFn) ([]netip.Addr, error) {
	return pinHost(ctx, host, systemLookup(lookup))
}

func systemLookup(lookup LookupFn) LookupFn {
	if lookup != nil {
		return lookup
	}
	return func(ctx context.Context, host string) ([]string, error) {
		return net.DefaultResolver.LookupHost(ctx, host)
	}
}
