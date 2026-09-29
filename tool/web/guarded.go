package web

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strconv"
	"strings"
	"syscall"
	"time"
)

func (f *fetch) Guarded(ctx context.Context, raw string) (Fetched, error) {
	start := time.Now()
	budgetMs := 0
	if d, ok := ctx.Deadline(); ok {
		budgetMs = int(d.Sub(start) / time.Millisecond)
	}

	u, pins, err := guardedURL(ctx, raw, nil, f.lookup)
	if err != nil {
		return Fetched{}, err
	}
	current := u.String()
	for hop := 0; ; hop++ {
		if hop > maxHops {
			return Fetched{}, fmt.Errorf("too many redirects (>%d) starting from %s", maxHops, raw)
		}
		req, err := http.NewRequestWithContext(context.WithValue(ctx, pinKey{}, pins), http.MethodGet, u.String(), nil)
		if err != nil {
			return Fetched{}, fmt.Errorf("invalid URL: %s", raw)
		}
		req.Header.Set("User-Agent", "rig-web-fetch/1.0")
		req.Header.Set("Accept", "text/html,application/xhtml+xml,application/json,text/*;q=0.9,*/*;q=0.5")

		res, err := f.do(req)
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				if errors.Is(ctxErr, context.DeadlineExceeded) {
					return Fetched{}, fmt.Errorf("timed out after %dms fetching %s", budgetMs, current)
				}
				return Fetched{}, ctxErr
			}
			if f.proxy != "" && isConnRefused(err) {
				return Fetched{}, fmt.Errorf(
					"egress proxy %s is unreachable. Start it: cd ~/docker/web-tools && docker compose up -d", f.proxy)
			}
			return Fetched{}, err
		}

		if loc := res.Header.Get("Location"); res.StatusCode >= 300 && res.StatusCode < 400 && loc != "" {
			res.Body.Close()
			nu, np, err := guardedURL(ctx, loc, u, f.lookup)
			if err != nil {
				return Fetched{}, err
			}
			u, pins = nu, np
			current = nu.String()
			continue
		}

		if res.StatusCode < 200 || res.StatusCode >= 300 {
			res.Body.Close()
			return Fetched{}, fmt.Errorf("HTTP %d from %s", res.StatusCode, u.String())
		}
		ct := res.Header.Get("Content-Type")
		if ct == "" {
			ct = "text/plain"
		}
		if !textualRE.MatchString(ct) {
			res.Body.Close()
			return Fetched{}, fmt.Errorf("unsupported content type %s; only textual responses are fetchable",
				strings.Split(ct, ";")[0])
		}
		if cl, err := strconv.Atoi(res.Header.Get("Content-Length")); err == nil && cl > f.maxBytes {
			res.Body.Close()
			return Fetched{}, fmt.Errorf("response too large: %d bytes declared, cap is %d", cl, f.maxBytes)
		}
		body, truncated, err := readCapped(res.Body, f.maxBytes)
		res.Body.Close()
		if err != nil {
			if ctxErr := ctx.Err(); ctxErr != nil {
				return Fetched{}, ctxErr
			}
			return Fetched{}, err
		}
		return Fetched{
			FinalURL:      u.String(),
			Status:        res.StatusCode,
			ContentType:   ct,
			Body:          body,
			BodyTruncated: truncated,
		}, nil
	}
}

func readCapped(r io.Reader, max int) (string, bool, error) {
	buf, err := io.ReadAll(io.LimitReader(r, int64(max)))
	if err != nil {
		return string(buf), false, err
	}
	if _, err := r.Read([]byte{0}); err == nil {
		return string(buf), true, nil
	}
	return string(buf), false, nil
}

func isConnRefused(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) {
		var sysErr *os.SyscallError
		if errors.As(opErr.Err, &sysErr) {
			if errno, ok := sysErr.Err.(syscall.Errno); ok {
				return errno == syscall.ECONNREFUSED
			}
		}
		if errno, ok := opErr.Err.(syscall.Errno); ok {
			return errno == syscall.ECONNREFUSED
		}
	}
	return strings.Contains(err.Error(), "connection refused")
}

func (f *fetch) exec(ctx context.Context, raw string, maxC, timeoutMs int) (string, error) {
	cctx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMs)*time.Millisecond)
	defer cancel()

	fetched, err := f.Guarded(cctx, raw)
	if err != nil {
		return "", fmt.Errorf("web: %s", err)
	}

	var readable string
	if reply, ok := jsonReply(fetched); ok {
		readable = reply
	} else if htmlishRE.MatchString(fetched.ContentType) {
		var note string
		readable, note = ExtractReadable(cctx, fetched.Body, &f.traf)
		if note != "" {
			readable += "\n\n" + note
		}
	} else {
		readable = strings.TrimSpace(fetched.Body)
	}

	text := CapChars(readable, maxC)
	if fetched.BodyTruncated {
		text += "\n\n[TRUNCATED: download hit the byte cap; content is partial.]"
	}
	if text == "" {
		text = "(no content extracted)"
	}
	return text, nil
}
