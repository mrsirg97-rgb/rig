package view

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mrsirg97-rgb/rig/v3/tool/execwrap"
)

const (
	viewportW = 1280
	viewportH = 800

	startTimeout = 20 * time.Second
	loadTimeout  = 30 * time.Second
	pollEvery    = 50 * time.Millisecond
	navWindow    = 2 * time.Second
	settlePolls  = 10
	closeWait    = 5 * time.Second

	maxElements = 40
	maxName     = 80
	maxURLLen   = 300

	maxRefusals  = 20
	guardTimeout = 10 * time.Second
)

type element struct {
	Role string `json:"role"`
	Name string `json:"name"`
}

type outcome struct {
	text string
	src  string
	shot image.Image
}

type browser struct {
	cfg   Config
	queue chan struct{}

	mu        sync.Mutex
	p         *bProc
	lastDeath *deathNote
	refusals  []refusal
}

type refusal struct {
	url string
	why string
}

type deathNote struct {
	desc   string
	stderr string
}

type bProc struct {
	cmd     *exec.Cmd
	conn    *cdpConn
	session string
	profile string
	stderr  *tailBuffer
	dead    chan struct{}
}

func (b *browser) step(ctx context.Context, s Step) (outcome, error) {
	select {
	case b.queue <- struct{}{}:
	case <-ctx.Done():
		return outcome{}, fmt.Errorf("view: the step was cancelled before the browser queue took it: %w", ctx.Err())
	}
	defer func() { <-b.queue }()

	p, note, err := b.proc(ctx)
	if err != nil {
		return outcome{}, err
	}
	out, runErr := b.run(ctx, p, s)
	refused := b.drainRefusals()
	if runErr != nil {
		if refused != "" {
			return outcome{}, fmt.Errorf("%v\n%s", runErr, refused)
		}
		return outcome{}, runErr
	}
	if refused != "" {
		out.text += "\n" + refused
	}
	if note != "" {
		out.text = note + "\n" + out.text
	}
	return out, nil
}

func (b *browser) noteRefusal(raw, why string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.refusals) >= maxRefusals {
		return
	}
	b.refusals = append(b.refusals, refusal{url: plain(raw, maxURLLen), why: plain(why, maxURLLen)})
}

func (b *browser) drainRefusals() string {
	b.mu.Lock()
	refs := b.refusals
	b.refusals = nil
	b.mu.Unlock()
	var text strings.Builder
	for _, r := range refs {
		if text.Len() > 0 {
			text.WriteByte('\n')
		}
		fmt.Fprintf(&text, "refused %s: %s", r.url, r.why)
	}
	return text.String()
}

func (b *browser) watchFetch(p *bProc) {
	for {
		select {
		case <-p.conn.dead:
			return
		case m := <-p.conn.events:
			if m.Method == "Fetch.requestPaused" {
				b.paused(p, m)
			}
		}
	}
}

func (b *browser) paused(p *bProc, m cdpMessage) {
	var event struct {
		RequestID string `json:"requestId"`
		Request   struct {
			URL string `json:"url"`
		} `json:"request"`
	}
	if json.Unmarshal(m.Params, &event) != nil || event.RequestID == "" {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), guardTimeout)
	defer cancel()
	target, err := b.guardRequest(ctx, event.Request.URL)
	if err != nil {
		b.noteRefusal(event.Request.URL, err.Error())
		p.conn.call(ctx, "Fetch.failRequest", map[string]any{
			"requestId":   event.RequestID,
			"errorReason": "BlockedByClient",
		}, p.session)
		return
	}
	params := map[string]any{"requestId": event.RequestID}
	if target != event.Request.URL {
		params["url"] = target
	}
	p.conn.call(ctx, "Fetch.continueRequest", params, p.session)
}

func (b *browser) proc(ctx context.Context) (*bProc, string, error) {
	b.mu.Lock()
	p := b.p
	var note string
	if d := b.lastDeath; d != nil {
		note = d.note()
		b.lastDeath = nil
	}
	b.mu.Unlock()
	if p != nil {
		return p, note, nil
	}
	p, err := b.start(ctx)
	if err != nil {
		return nil, note, err
	}
	return p, note, nil
}

func (d *deathNote) note() string {
	s := "note: fresh browser; the previous browser exited (" + d.desc + "); the page and its refs are gone"
	if d.stderr != "" {
		s += "\n[stderr]\n" + d.stderr
	}
	return s
}

func (b *browser) run(ctx context.Context, p *bProc, s Step) (outcome, error) {
	switch s.Action {
	case "open":
		return b.open(ctx, p, s)
	case "click":
		return b.click(ctx, p, s)
	case "type":
		return b.typeText(ctx, p, s)
	case "scroll":
		return b.scroll(ctx, p, s)
	case "shot":
		return b.shot(ctx, p)
	case "close":
		b.teardown(p)
		return outcome{text: "browser closed; the next browser step starts a fresh one"}, nil
	default:
		return outcome{}, fmt.Errorf("view: unknown browser step %q; the steps are open, click, type, scroll, shot, close", s.Action)
	}
}

func (b *browser) open(ctx context.Context, p *bProc, s Step) (outcome, error) {
	if s.URL == "" {
		return outcome{}, errors.New("view: url required (the page to open)")
	}
	target, err := b.guard(ctx, s.URL)
	if err != nil {
		return outcome{}, err
	}
	if _, err := p.eval(ctx, "window.__rigPre = 1"); err != nil {
		return outcome{}, fmt.Errorf("view: the page did not accept the step: %v", err)
	}
	if err := p.navigate(ctx, target); err != nil {
		return outcome{}, err
	}
	if err := p.settle(ctx); err != nil {
		return outcome{}, err
	}
	if err := p.waitReady(ctx); err != nil {
		return outcome{}, err
	}
	return b.listed(ctx, p, true)
}

func (b *browser) click(ctx context.Context, p *bProc, s Step) (outcome, error) {
	if s.Ref <= 0 {
		return outcome{}, errors.New("view: ref required (the [n] ref a step listed)")
	}
	if _, err := p.eval(ctx, "window.__rigPre = 1"); err != nil {
		return outcome{}, fmt.Errorf("view: the page did not accept the step: %v", err)
	}
	expr := fmt.Sprintf(`(() => {
  const el = window.__rigRefs && window.__rigRefs[%d - 1];
  if (!el) return 'no';
  el.scrollIntoView({block: 'center', inline: 'center'});
  el.click();
  return 'ok';
})()`, s.Ref)
	res, err := p.evalString(ctx, expr)
	if err != nil {
		return outcome{}, fmt.Errorf("view: ref %d: %v", s.Ref, err)
	}
	if res != "ok" {
		return outcome{}, fmt.Errorf("view: ref %d is not on this page; a step that lists the elements again gives the live refs", s.Ref)
	}
	if err := p.settle(ctx); err != nil {
		return outcome{}, err
	}
	return b.listed(ctx, p, s.Shot)
}

func (b *browser) typeText(ctx context.Context, p *bProc, s Step) (outcome, error) {
	if s.Ref <= 0 {
		return outcome{}, errors.New("view: ref required (the [n] ref a step listed)")
	}
	quoted, err := json.Marshal(s.Text)
	if err != nil {
		return outcome{}, err
	}
	expr := fmt.Sprintf(`(() => {
  const el = window.__rigRefs && window.__rigRefs[%d - 1];
  if (!el) return 'no';
  el.focus();
  const text = %s;
  if ('value' in el) {
    el.value = text;
    el.dispatchEvent(new Event('input', {bubbles: true}));
    el.dispatchEvent(new Event('change', {bubbles: true}));
  } else {
    el.textContent = text;
    el.dispatchEvent(new Event('input', {bubbles: true}));
  }
  return 'ok';
})()`, s.Ref, quoted)
	res, err := p.evalString(ctx, expr)
	if err != nil {
		return outcome{}, fmt.Errorf("view: ref %d: %v", s.Ref, err)
	}
	if res != "ok" {
		return outcome{}, fmt.Errorf("view: ref %d is not on this page; a step that lists the elements again gives the live refs", s.Ref)
	}
	return b.listed(ctx, p, s.Shot)
}

func (b *browser) scroll(ctx context.Context, p *bProc, s Step) (outcome, error) {
	expr := fmt.Sprintf(`(() => {
  const y = %d === 0 ? window.innerHeight : %d;
  window.scrollBy(0, y);
  return 'ok';
})()`, s.Amount, s.Amount)
	if _, err := p.evalString(ctx, expr); err != nil {
		return outcome{}, fmt.Errorf("view: scroll: %v", err)
	}
	return b.listed(ctx, p, s.Shot)
}

func (b *browser) shot(ctx context.Context, p *bProc) (outcome, error) {
	return b.listed(ctx, p, true)
}

func (b *browser) listed(ctx context.Context, p *bProc, wantShot bool) (outcome, error) {
	els, err := p.snapshot(ctx)
	if err != nil {
		return outcome{}, err
	}
	page, err := p.evalString(ctx, "location.href")
	if err != nil {
		return outcome{}, fmt.Errorf("view: the page did not say its address: %v", err)
	}
	title, _ := p.evalString(ctx, "document.title")
	page = plain(page, maxURLLen)
	title = plain(title, maxName)
	out := outcome{src: page, text: pageLine(page, title) + "\n" + list(els)}
	if wantShot {
		shot, err := p.capture(ctx)
		if err != nil {
			return outcome{}, err
		}
		out.shot = shot
	}
	return out, nil
}

func (b *browser) start(ctx context.Context) (*bProc, error) {
	bin, err := b.binary()
	if err != nil {
		return nil, err
	}
	profile, err := os.MkdirTemp("", "rig-browser-")
	if err != nil {
		return nil, fmt.Errorf("view: the browser's profile directory: %v", err)
	}
	cmdR, cmdW, err := os.Pipe()
	if err != nil {
		os.RemoveAll(profile)
		return nil, fmt.Errorf("view: the browser's pipe: %v", err)
	}
	resR, resW, err := os.Pipe()
	if err != nil {
		cmdR.Close()
		cmdW.Close()
		os.RemoveAll(profile)
		return nil, fmt.Errorf("view: the browser's pipe: %v", err)
	}
	cmd := exec.Command(bin, b.argv(profile)...)
	cmd.Dir = b.cfg.Cwd
	attr := &syscall.SysProcAttr{Setpgid: true}
	execwrap.ParentDeathAttr(attr)
	cmd.SysProcAttr = attr
	cmd.ExtraFiles = []*os.File{cmdR, resW}
	tail := &tailBuffer{}
	cmd.Stderr = tail
	if err := cmd.Start(); err != nil {
		cmdR.Close()
		cmdW.Close()
		resR.Close()
		resW.Close()
		os.RemoveAll(profile)
		return nil, fmt.Errorf("view: the browser %q did not start: %v%s", bin, err, tailNote(tail))
	}
	cmdR.Close()
	resW.Close()
	p := &bProc{
		cmd:     cmd,
		conn:    newCDP(cmdW, resR),
		profile: profile,
		stderr:  tail,
		dead:    make(chan struct{}),
	}
	b.mu.Lock()
	b.p = p
	b.mu.Unlock()
	go b.waitLoop(p)
	if err := p.handshake(ctx); err != nil {
		b.teardown(p)
		return nil, fmt.Errorf("view: the browser %q did not answer: %v%s", bin, err, tailNote(tail))
	}
	go b.watchFetch(p)
	cctx, cancel := context.WithTimeout(ctx, startTimeout)
	defer cancel()
	if _, err := p.conn.call(cctx, "Fetch.enable", map[string]any{
		"patterns": []map[string]any{{"urlPattern": "*", "requestStage": "Request"}},
	}, p.session); err != nil {
		b.teardown(p)
		return nil, fmt.Errorf("view: the browser %q cannot guard its requests: %v", bin, err)
	}
	return p, nil
}

func (b *browser) waitLoop(p *bProc) {
	err := p.cmd.Wait()
	desc := exitDescription(err)
	b.mu.Lock()
	current := b.p == p
	if current {
		b.p = nil
		b.lastDeath = &deathNote{desc: desc, stderr: strings.TrimSpace(p.stderr.String())}
	}
	b.mu.Unlock()
	p.conn.close()
	close(p.dead)
	if current {
		os.RemoveAll(p.profile)
	}
}

func (b *browser) teardown(p *bProc) {
	b.mu.Lock()
	if b.p == p {
		b.p = nil
	}
	b.mu.Unlock()
	p.conn.close()
	if p.cmd.Process != nil {
		syscall.Kill(-p.cmd.Process.Pid, syscall.SIGKILL)
	}
	select {
	case <-p.dead:
	case <-time.After(closeWait):
	}
	os.RemoveAll(p.profile)
}

func (b *browser) close() {
	b.mu.Lock()
	p := b.p
	b.p = nil
	b.mu.Unlock()
	if p == nil {
		return
	}
	b.teardown(p)
}

func (v *toolView) browserFor() *browser {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.b == nil {
		v.b = &browser{cfg: v.cfg, queue: make(chan struct{}, 1)}
	}
	return v.b
}

func (v *toolView) Browser(ctx context.Context, step Step) (string, error) {
	out, err := v.browserFor().step(ctx, step)
	if err != nil {
		return "", err
	}
	text := out.text
	if out.shot != nil {
		marker, err := v.publish(ctx, out.shot, still, out.src)
		if err != nil {
			return "", fmt.Errorf("view: the screenshot: %v", err)
		}
		text += "\n" + marker
	}
	return text, nil
}
