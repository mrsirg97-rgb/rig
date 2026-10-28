package command

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/webtools"
)

type fakeTools struct {
	status   webtools.Status
	statusEr error
	genOut   string
	genPw    string
	genForce bool
	startOut string
	startEr  error
	stopOut  string
	stopEr   error
	calls    []string
}

func (f *fakeTools) Dir() string { return "/home/u/.rig/external" }

func (f *fakeTools) Status(context.Context) (webtools.Status, error) {
	f.calls = append(f.calls, "status")
	return f.status, f.statusEr
}

func (f *fakeTools) Generate(_ context.Context, pw string, force bool) (string, error) {
	f.calls = append(f.calls, "setup")
	f.genPw, f.genForce = pw, force
	return f.genOut, nil
}

func (f *fakeTools) Start(context.Context) (string, error) {
	f.calls = append(f.calls, "start")
	return f.startOut, f.startEr
}

func (f *fakeTools) Stop(context.Context) (string, error) {
	f.calls = append(f.calls, "stop")
	return f.stopOut, f.stopEr
}

func toolsEnv(f *fakeTools) *Env {
	return &Env{WebTools: func() webtools.Tools { return f }}
}

func runWebTools(t *testing.T, env *Env, args string) (string, error) {
	t.Helper()
	return webtoolsCmd{}.Run(context.Background(), args, env)
}

func TestWebToolsBareReadsTheStatus(t *testing.T) {
	f := &fakeTools{status: webtools.Status{
		Dir: "/home/u/.rig/external", Docker: "ok 29.9.2",
		Missing: []string{}, Differ: []string{},
		Proxy: "http://rig@127.0.0.1:8889", Services: []string{"tinyproxy"},
	}}
	out, err := runWebTools(t, toolsEnv(f), "")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"/home/u/.rig/external", "ok 29.9.2", "http://rig@127.0.0.1:8889", "tinyproxy"} {
		if !strings.Contains(out, want) {
			t.Fatalf("reply = %q, want %s", out, want)
		}
	}
	if strings.Contains(out, "hunter2") {
		t.Fatalf("the reply carries a password: %q", out)
	}
	if len(f.calls) != 1 || f.calls[0] != "status" {
		t.Fatalf("calls = %v, want a bare status read", f.calls)
	}
}

func TestWebToolsSetupPassesThePasswordAndForce(t *testing.T) {
	f := &fakeTools{genOut: "wrote 7 of 7 files in /home/u/.rig/external"}
	out, err := runWebTools(t, toolsEnv(f), "setup hunter2longenough force")
	if err != nil {
		t.Fatal(err)
	}
	if f.genPw != "hunter2longenough" || !f.genForce {
		t.Fatalf("setup saw password %q force=%v", f.genPw, f.genForce)
	}
	if out != f.genOut {
		t.Fatalf("reply = %q, want the tools' own words", out)
	}
	if _, err := runWebTools(t, toolsEnv(&fakeTools{}), "SETUP"); err != nil {
		t.Fatalf("setup must answer in any case: %v", err)
	}
}

func TestWebToolsSetupRefusesAThirdArgument(t *testing.T) {
	_, err := runWebTools(t, toolsEnv(&fakeTools{}), "setup a b c")
	if err == nil || !strings.Contains(err.Error(), "usage") {
		t.Fatalf("err = %v, want the usage line", err)
	}
}

func TestWebToolsStartAndStopRunTheirVerbs(t *testing.T) {
	f := &fakeTools{startOut: "started the web-tools bundle", stopOut: "stopped the web-tools bundle"}
	if out, err := runWebTools(t, toolsEnv(f), "start"); err != nil || !strings.Contains(out, "started") {
		t.Fatalf("start = %q %v", out, err)
	}
	if out, err := runWebTools(t, toolsEnv(f), "stop"); err != nil || !strings.Contains(out, "stopped") {
		t.Fatalf("stop = %q %v", out, err)
	}
	if strings.Join(f.calls, ",") != "start,stop" {
		t.Fatalf("calls = %v, want start then stop", f.calls)
	}
}

func TestWebToolsRefusesAPasswordOnAVerbThatTakesNone(t *testing.T) {
	_, err := runWebTools(t, toolsEnv(&fakeTools{}), "start hunter2longenough")
	if err == nil || !strings.Contains(err.Error(), "setup") {
		t.Fatalf("err = %v, want it to name where the password belongs", err)
	}
}

func TestWebToolsRefusesAnUnknownVerbAndNamesTheFour(t *testing.T) {
	_, err := runWebTools(t, toolsEnv(&fakeTools{}), "remove")
	if err == nil {
		t.Fatal("want the unknown verb refused")
	}
	for _, want := range []string{"status", "setup", "start", "stop"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("err = %q, want it to name %s", err, want)
		}
	}
}

func TestWebToolsPassesTheToolsOwnRefusalThrough(t *testing.T) {
	f := &fakeTools{startEr: errors.New("webtools: docker is not on PATH")}
	_, err := runWebTools(t, toolsEnv(f), "start")
	if err == nil || !strings.Contains(err.Error(), "docker is not on PATH") {
		t.Fatalf("err = %v, want the tools' own reason", err)
	}
}

func TestWebToolsRefusesWithoutTheRootsWiring(t *testing.T) {
	_, err := runWebTools(t, &Env{}, "status")
	if err == nil || !strings.Contains(err.Error(), "webtools") {
		t.Fatalf("err = %v, want the missing seam named", err)
	}
	_, err = runWebTools(t, &Env{WebTools: func() webtools.Tools { return nil }}, "start")
	if err == nil || !strings.Contains(err.Error(), "webtools") {
		t.Fatalf("err = %v, want the missing seam named", err)
	}
}

func TestTheStandardSetCarriesWebTools(t *testing.T) {
	found := false
	for _, c := range All() {
		if c.Name() == "webtools" {
			found = true
			sub, ok := c.(interface{ Sub() []Sub })
			if !ok {
				t.Fatal("webtools must carry its verbs for the pager")
			}
			got := map[string]bool{}
			for _, s := range sub.Sub() {
				got[s.Name] = true
			}
			for _, want := range []string{"status", "setup", "start", "stop"} {
				if !got[want] {
					t.Fatalf("verbs = %v, want %s", got, want)
				}
			}
		}
	}
	if !found {
		t.Fatal("the standard set lacks webtools")
	}
}
