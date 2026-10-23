package view

import (
	"bytes"
	"context"
	_ "embed"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os/exec"
	"syscall"
	"time"
)

//go:embed page.js
var pageJS string

func (p *bProc) handshake(ctx context.Context) error {
	cctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	res, err := p.conn.call(cctx, "Target.createTarget", map[string]any{"url": "about:blank"}, "")
	if err != nil {
		return err
	}
	var target struct {
		TargetID string `json:"targetId"`
	}
	if err := json.Unmarshal(res, &target); err != nil || target.TargetID == "" {
		return fmt.Errorf("Target.createTarget answered %s", res)
	}
	res, err = p.conn.call(cctx, "Target.attachToTarget", map[string]any{"targetId": target.TargetID, "flatten": true}, "")
	if err != nil {
		return err
	}
	var attached struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(res, &attached); err != nil || attached.SessionID == "" {
		return fmt.Errorf("Target.attachToTarget answered %s", res)
	}
	p.session = attached.SessionID
	if _, err := p.conn.call(cctx, "Page.enable", map[string]any{}, p.session); err != nil {
		return err
	}
	if _, err := p.conn.call(cctx, "Runtime.enable", map[string]any{}, p.session); err != nil {
		return err
	}
	return nil
}

func (p *bProc) navigate(ctx context.Context, target string) error {
	res, err := p.conn.call(ctx, "Page.navigate", map[string]any{"url": target}, p.session)
	if err != nil {
		return fmt.Errorf("view: %s: %v", target, err)
	}
	var nav struct {
		ErrorText string `json:"errorText"`
	}
	if err := json.Unmarshal(res, &nav); err != nil {
		return fmt.Errorf("view: %s: %v", target, err)
	}
	if nav.ErrorText != "" {
		return fmt.Errorf("view: %s: %s", target, nav.ErrorText)
	}
	return nil
}

func (p *bProc) waitReady(ctx context.Context) error {
	deadline := time.Now().Add(loadTimeout)
	for {
		state, err := p.evalString(ctx, "document.readyState")
		if err == nil && state == "complete" {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("view: the page did not finish loading within %s", loadTimeout)
		}
		if err := sleep(ctx, pollEvery); err != nil {
			return err
		}
	}
}

func (p *bProc) settle(ctx context.Context) error {
	deadline := time.Now().Add(navWindow)
	complete := 0
	for time.Now().Before(deadline) {
		state, err := p.evalString(ctx, `typeof window.__rigPre === 'undefined' ? 'gone' : document.readyState`)
		if err != nil {
			state = "gone"
		}
		switch state {
		case "gone":
			return p.waitReady(ctx)
		case "complete":
			complete++
			if complete >= settlePolls {
				return nil
			}
		default:
			complete = 0
		}
		if err := sleep(ctx, pollEvery); err != nil {
			return err
		}
	}
	return nil
}

func (p *bProc) eval(ctx context.Context, expr string) (json.RawMessage, error) {
	res, err := p.conn.call(ctx, "Runtime.evaluate", map[string]any{
		"expression":    expr,
		"returnByValue": true,
	}, p.session)
	if err != nil {
		return nil, err
	}
	var r struct {
		Result struct {
			Value json.RawMessage `json:"value"`
		} `json:"result"`
		ExceptionDetails *struct {
			Text      string `json:"text"`
			Exception *struct {
				Description string `json:"description"`
			} `json:"exception"`
		} `json:"exceptionDetails"`
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return nil, err
	}
	if r.ExceptionDetails != nil {
		msg := r.ExceptionDetails.Text
		if e := r.ExceptionDetails.Exception; e != nil && e.Description != "" {
			msg = e.Description
		}
		return nil, errors.New(msg)
	}
	return r.Result.Value, nil
}

func (p *bProc) evalString(ctx context.Context, expr string) (string, error) {
	raw, err := p.eval(ctx, expr)
	if err != nil {
		return "", err
	}
	var s string
	if err := json.Unmarshal(raw, &s); err != nil {
		return "", fmt.Errorf("the page answered %s, not a string", raw)
	}
	return s, nil
}

func (p *bProc) snapshot(ctx context.Context) ([]element, error) {
	raw, err := p.evalString(ctx, pageJS)
	if err != nil {
		return nil, fmt.Errorf("view: the page did not list its elements: %v", err)
	}
	var els []element
	if err := json.Unmarshal([]byte(raw), &els); err != nil {
		return nil, fmt.Errorf("view: the page's element list: %v", err)
	}
	return els, nil
}

func (p *bProc) capture(ctx context.Context) (image.Image, error) {
	res, err := p.conn.call(ctx, "Page.captureScreenshot", map[string]any{"format": "png"}, p.session)
	if err != nil {
		return nil, fmt.Errorf("view: the screenshot: %v", err)
	}
	var shot struct {
		Data string `json:"data"`
	}
	if err := json.Unmarshal(res, &shot); err != nil || shot.Data == "" {
		return nil, errors.New("view: the screenshot came back empty")
	}
	body, err := base64.StdEncoding.DecodeString(shot.Data)
	if err != nil {
		return nil, fmt.Errorf("view: the screenshot's bytes: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("view: the screenshot is not a png: %v", err)
	}
	if int64(cfg.Width)*int64(cfg.Height) > maxPixels {
		return nil, fmt.Errorf("view: the screenshot is %dx%d, over the %d-pixel cap", cfg.Width, cfg.Height, maxPixels)
	}
	img, _, err := image.Decode(bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("view: the screenshot: %v", err)
	}
	return img, nil
}

func exitDescription(err error) string {
	if err == nil {
		return "code 0"
	}
	var ee *exec.ExitError
	if errors.As(err, &ee) {
		if ws, ok := ee.Sys().(syscall.WaitStatus); ok && ws.Signaled() {
			return "signal " + ws.Signal().String()
		}
		return fmt.Sprintf("code %d", ee.ExitCode())
	}
	return err.Error()
}

func sleep(ctx context.Context, d time.Duration) error {
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(d):
		return nil
	}
}
