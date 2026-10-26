package webtools

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

type stub struct {
	calls    []string
	docker   string
	daemon   string
	services string
	psErr    string
	upErr    string
}

func (s *stub) Run(ctx context.Context, name string, argv ...string) (string, error) {
	line := name
	for _, a := range argv {
		line += " " + a
	}
	s.calls = append(s.calls, line)
	switch {
	case strings.Contains(line, "docker info"):
		if s.docker == "" {
			if s.daemon == "" {
				return "29.9.2\n", nil
			}
			return "Cannot connect to the Docker daemon. Is the docker daemon running on this host?\n", errorOf(s.daemon)
		}
		return "command not found: docker\n", fmt.Errorf("%s: %w", s.docker, exec.ErrNotFound)
	case strings.Contains(line, "volume create"):
		return "", nil
	case strings.Contains(line, " run --rm"):
		return "", nil
	case strings.Contains(line, "up -d --build"):
		if s.upErr != "" {
			return "Bind for 127.0.0.1:8888 failed: port is already allocated\n", errorOf(s.upErr)
		}
		return " Image external-tinyproxy Built \n Container rig-tinyproxy Started \n", nil
	case strings.Contains(line, " stop"):
		return " Container rig-searxng Stopped \n", nil
	case strings.Contains(line, "ps --services"):
		if s.psErr != "" {
			return "unsupported compose flag\n", errorOf(s.psErr)
		}
		return s.services, nil
	}
	return "", nil
}

func errorOf(text string) error { return &voice{text} }

type voice struct{ text string }

func (v *voice) Error() string { return v.text }

type settingStub struct {
	puts  map[string]string
	exist map[string]string
	err   string
}

func newSetting(existing map[string]string) *settingStub {
	m := map[string]string{}
	for k, v := range existing {
		m[k] = v
	}
	return &settingStub{puts: map[string]string{}, exist: m}
}

func (s *settingStub) Read(key string) (string, bool, error) {
	v, ok := s.exist[key]
	return v, ok, nil
}

func (s *settingStub) Put(key, value string) error {
	if s.err != "" {
		return errorOf(s.err)
	}
	s.puts[key] = value
	s.exist[key] = value
	return nil
}

func bundleTools(t *testing.T, home string, r Runner, st Setting) Tools {
	t.Helper()
	return New(home, WithRunner(r), WithSetting(st))
}

func mustErr(t *testing.T, err error, want string) string {
	t.Helper()
	if err == nil {
		t.Fatalf("want an error naming %q", want)
	}
	if !strings.Contains(err.Error(), want) {
		t.Fatalf("error = %q, want it to name %q", err.Error(), want)
	}
	return err.Error()
}

func has(t *testing.T, s string, subs ...string) {
	t.Helper()
	for _, sub := range subs {
		if !strings.Contains(s, sub) {
			t.Fatalf("%q, want it to contain %q", s, sub)
		}
	}
}

func TestStartRunsVolumeCreateThenTheOwnershipHelperThenComposeUpBuild(t *testing.T) {
	home := t.TempDir()
	s := &stub{services: "tinyproxy\nvalkey\nsearxng\n"}
	tools := bundleTools(t, home, s, newSetting(nil))
	if _, err := tools.Generate(context.Background(), "", false); err != nil {
		t.Fatal(err)
	}
	s.calls = nil

	reply, err := tools.Start(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(s.calls) < 5 {
		t.Fatalf("calls = %v, want the probe, the creates, the helpers and the up", s.calls)
	}
	has(t, s.calls[0], "docker info")
	up := s.calls[len(s.calls)-1]
	has(t, up, "compose", "up -d --build")
	for _, c := range s.calls[1 : len(s.calls)-1] {
		if strings.Contains(c, "up -d") {
			t.Fatalf("compose up ran before the volumes were ready: %v", s.calls)
		}
		if !strings.Contains(c, "volume create") && !strings.Contains(c, "chown") {
			t.Fatalf("call %q, want a volume create or the ownership helper", c)
		}
	}
	if !strings.Contains(s.calls[len(s.calls)-2], "volume create") && !strings.Contains(s.calls[len(s.calls)-2], "chown") {
		t.Fatalf("the up must follow the volume work, got %v", s.calls)
	}
	has(t, reply, "started", "8888", "8889")
}

func TestTheOwnershipHelperFollowsTheCreateItFixes(t *testing.T) {
	home := t.TempDir()
	s := &stub{}
	tools := bundleTools(t, home, s, newSetting(nil))
	if _, err := tools.Generate(context.Background(), "", false); err != nil {
		t.Fatal(err)
	}
	s.calls = nil
	if _, err := tools.Start(context.Background()); err != nil {
		t.Fatal(err)
	}
	for i, c := range s.calls {
		if !strings.Contains(c, "chown") {
			continue
		}
		if i == 0 || !strings.Contains(s.calls[i-1], "volume create") {
			t.Fatalf("the helper at %d must follow its create, got %v", i, s.calls)
		}
	}
	if !strings.Contains(strings.Join(s.calls, "\n"), "chown") {
		t.Fatalf("no ownership helper: %v", s.calls)
	}
	has(t, strings.Join(s.calls, "\n"), "-R 999:1000", "-R 977:977", "rig-valkey-data", "rig-searxng-cache")
}

func TestStartRefusesWhenDockerIsAbsentAndNeverReachesCompose(t *testing.T) {
	home := t.TempDir()
	s := &stub{docker: "exec: docker: file not found"}
	tools := bundleTools(t, home, s, newSetting(nil))
	if _, err := tools.Generate(context.Background(), "", false); err != nil {
		t.Fatal(err)
	}
	s.calls = nil

	err := mustErr(t, startErr(tools), "docker is not on PATH")
	has(t, err, "rig webtools")
	if len(s.calls) != 1 {
		t.Fatalf("calls = %v, want only the probe", s.calls)
	}
	if strings.Contains(s.calls[0], "compose") {
		t.Fatalf("compose ran with no docker: %v", s.calls)
	}
}

func TestStartRefusesWhenTheDaemonDoesNotAnswer(t *testing.T) {
	home := t.TempDir()
	s := &stub{daemon: "cannot connect"}
	tools := bundleTools(t, home, s, newSetting(nil))
	if _, err := tools.Generate(context.Background(), "", false); err != nil {
		t.Fatal(err)
	}
	s.calls = nil
	err := mustErr(t, startErr(tools), "daemon")
	has(t, err, "cannot connect")
	if len(s.calls) != 1 {
		t.Fatalf("nothing may run after a failed probe: %v", s.calls)
	}
}

func TestStartRefusesWhenTheBundleIsAbsent(t *testing.T) {
	home := t.TempDir()
	s := &stub{}
	err := mustErr(t, startErr(bundleTools(t, home, s, newSetting(nil))), "no bundle")
	has(t, err, "rig webtools setup")
	if len(s.calls) != 0 {
		t.Fatalf("calls = %v, want none before the bundle check", s.calls)
	}
}

func TestStartNamesDockersOwnVoiceWhenAPortIsTaken(t *testing.T) {
	home := t.TempDir()
	s := &stub{upErr: "exit status 1"}
	tools := bundleTools(t, home, s, newSetting(nil))
	if _, err := tools.Generate(context.Background(), "", false); err != nil {
		t.Fatal(err)
	}
	err := mustErr(t, startErr(tools), "port is already allocated")
	has(t, err, "127.0.0.1:8888")
}

func TestStopRunsComposeStopAndRefusesWithoutTheBundle(t *testing.T) {
	home := t.TempDir()
	s := &stub{}
	tools := bundleTools(t, home, s, newSetting(nil))
	if _, err := tools.Generate(context.Background(), "", false); err != nil {
		t.Fatal(err)
	}
	s.calls = nil
	reply, err := tools.Stop(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	has(t, reply, "stopped")
	if !strings.Contains(s.calls[len(s.calls)-1], " stop") {
		t.Fatalf("the last call must be compose stop, got %v", s.calls)
	}
	for _, c := range s.calls {
		if strings.Contains(c, "up -d") || strings.Contains(c, "volume create") {
			t.Fatalf("stop must not build or create: %v", s.calls)
		}
	}
	other := t.TempDir()
	mustErr(t, stopErr(bundleTools(t, other, &stub{}, newSetting(nil))), "no bundle")
}

func TestGenerateWritesTheWholeBundleAtTheRightModes(t *testing.T) {
	home := t.TempDir()
	reply, err := generate(bundleTools(t, home, &stub{}, newSetting(nil)), "", false)
	if err != nil {
		t.Fatal(err)
	}
	files := map[string]bool{}
	walk(t, filepath.Join(home, "external"), func(path string, info os.FileInfo) {
		if !info.IsDir() {
			rel, _ := filepath.Rel(filepath.Join(home, "external"), path)
			files[rel] = true
		}
	})
	for _, want := range []string{
		"docker-compose.yml",
		filepath.Join("tinyproxy", "Dockerfile"),
		filepath.Join("tinyproxy", "tinyproxy.conf"),
		filepath.Join("tinyproxy", "filter"),
		filepath.Join("searxng", "settings.yml"),
		filepath.Join("searxng", "limiter.toml"),
		"README.md",
	} {
		if !files[want] {
			t.Fatalf("the bundle lacks %s (%v)", want, keysOf(files))
		}
	}
	if !strings.Contains(reply, "7 files") {
		t.Fatalf("reply = %q, want the seven files named", reply)
	}
	confMode := mode(t, filepath.Join(home, "external", "tinyproxy", "tinyproxy.conf"))
	if confMode&0o077 != 0 {
		t.Fatalf("the conf that holds the password is %o, want 0600", confMode)
	}
	if mode(t, filepath.Join(home, "external", "docker-compose.yml"))&0o077 != 0o044 {
		t.Fatalf("a generated file carries group or world bits, want 0644")
	}
	dockerfile := read(t, filepath.Join(home, "external", "tinyproxy", "Dockerfile"))
	has(t, dockerfile, "--chown=tinyproxy:tinyproxy", "--chmod=600")
}

func TestGenerateWritesNothingTwice(t *testing.T) {
	home := t.TempDir()
	tools := bundleTools(t, home, &stub{}, newSetting(nil))
	first, err := generate(tools, "", false)
	if err != nil {
		t.Fatal(err)
	}
	conf := read(t, filepath.Join(home, "external", "tinyproxy", "tinyproxy.conf"))
	second, err := generate(tools, "", false)
	if err != nil {
		t.Fatal(err)
	}
	if read(t, filepath.Join(home, "external", "tinyproxy", "tinyproxy.conf")) != conf {
		t.Fatal("the second write moved the conf: the password rotated")
	}
	has(t, second, "nothing written")
	if !strings.Contains(first, "wrote") {
		t.Fatalf("first = %q, want it to name what it wrote", first)
	}
}

func TestGenerateReusesThePasswordOfTheExistingConf(t *testing.T) {
	home := t.TempDir()
	tools := bundleTools(t, home, &stub{}, newSetting(nil))
	if _, err := generate(tools, "", false); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(home, "external", "tinyproxy", "tinyproxy.conf")
	conf := read(t, path)
	moved := strings.Replace(conf, "BasicAuth rig ", "BasicAuth rig otherotherotherother1234 ", 1)
	if moved == conf {
		t.Fatal("no BasicAuth line to move")
	}
	if err := os.WriteFile(path, []byte(moved), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := generate(tools, "", true); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(read(t, path), "otherotherotherother1234") {
		t.Fatalf("the reused password is gone: %s", read(t, path))
	}
}

func TestGenerateRefusesADifferingFileAndNamesIt(t *testing.T) {
	home := t.TempDir()
	tools := bundleTools(t, home, &stub{}, newSetting(nil))
	if _, err := generate(tools, "", false); err != nil {
		t.Fatal(err)
	}
	p := filepath.Join(home, "external", "tinyproxy", "filter")
	if err := os.WriteFile(p, []byte("# hand edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	err := mustErr(t, generateErr(tools, "", false), "tinyproxy/filter")
	has(t, err, "force")
	if _, err := generate(tools, "", true); err != nil {
		t.Fatalf("force: %v", err)
	}
	if read(t, p) == "# hand edited\n" {
		t.Fatal("force did not overwrite")
	}
}

func TestGenerateWritesOnlyTheMissingFileWhenTheBundleIsHalfThere(t *testing.T) {
	home := t.TempDir()
	tools := bundleTools(t, home, &stub{}, newSetting(nil))
	if _, err := generate(tools, "", false); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, "external", "README.md")); err != nil {
		t.Fatal(err)
	}
	reply, err := generate(tools, "", false)
	if err != nil {
		t.Fatal(err)
	}
	has(t, reply, "README.md")
	if read(t, filepath.Join(home, "external", "README.md")) == "" {
		t.Fatal("README.md did not come back")
	}
}

func TestAnExplicitPasswordOutsideTheRuleIsRefused(t *testing.T) {
	cases := []struct{ pw, why string }{
		{"short", "under"},
		{strings.Repeat("a", 129), "over"},
		{"has space", "space"},
		{`with"quote`, "quote"},
		{"with#hash", "hash"},
		{"ok_.-value" + strings.Repeat("x", 6), ""},
		{"", ""},
	}
	for _, c := range cases {
		home := t.TempDir()
		_, err := generate(bundleTools(t, home, &stub{}, newSetting(nil)), c.pw, false)
		if c.why == "" {
			if err != nil {
				t.Fatalf("password %q: %v", c.pw, err)
			}
			continue
		}
		errtext := mustErr(t, err, c.why)
		has(t, errtext, "16", "128")
		if !strings.Contains(errtext, "letter") {
			t.Fatalf("the refusal must name the accepted set: %s", errtext)
		}
	}
}

func TestAGeneratedPasswordIsThirtyHexAndTheSecretForty(t *testing.T) {
	home := t.TempDir()
	tools := bundleTools(t, home, &stub{}, newSetting(nil))
	if _, err := generate(tools, "", false); err != nil {
		t.Fatal(err)
	}
	pw := authLine(read(t, filepath.Join(home, "external", "tinyproxy", "tinyproxy.conf")))
	if len(pw) != 32 || !isHex(pw) {
		t.Fatalf("generated password %q, want 32 hex", pw)
	}
	secret := secretLine(read(t, filepath.Join(home, "external", "searxng", "settings.yml")))
	if len(secret) != 40 || !isHex(secret) {
		t.Fatalf("secret_key %q, want 40 hex", secret)
	}
	if secret == operatorSecret {
		t.Fatal("the template carries a fixed secret key")
	}
}

func TestNoTemplateSlotSurvivesInTheRenderedBundle(t *testing.T) {
	home := t.TempDir()
	if _, err := generate(bundleTools(t, home, &stub{}, newSetting(nil)), "", false); err != nil {
		t.Fatal(err)
	}
	walk(t, filepath.Join(home, "external"), func(path string, info os.FileInfo) {
		if info.IsDir() {
			return
		}
		if strings.Contains(read(t, path), "{{") {
			t.Fatalf("%s still carries a template slot", path)
		}
		if strings.Contains(read(t, path), operatorSecret) {
			t.Fatalf("%s carries the operator's own value", path)
		}
	})
}

func TestTheBundleCarriesTheHardeningTheOperatorMeasured(t *testing.T) {
	home := t.TempDir()
	if _, err := generate(bundleTools(t, home, &stub{}, newSetting(nil)), "", false); err != nil {
		t.Fatal(err)
	}
	compose := read(t, filepath.Join(home, "external", "docker-compose.yml"))
	has(t, compose, "no-new-privileges:true", "read_only: true", "mem_limit: 1g", "max-size: 5m",
		"172.30.30.0/24", "127.0.0.1:8889:8888", "127.0.0.1:8888:8080", "977:977", "999:1000",
		"valkey/valkey:9-alpine", "searxng/searxng:2026.10.25-54ca1d9cf", "name: rig-valkey-data")
	if strings.Contains(compose, "SEARXNG_REDIS_URL") {
		t.Fatal("the deprecated redis env var came back")
	}
	conf := read(t, filepath.Join(home, "external", "tinyproxy", "tinyproxy.conf"))
	has(t, conf, `BasicAuthRealm "proxy"`, "ConnectPort 443", "Allow 172.30.30.0/24",
		`Filter "/etc/tinyproxy/filter"`, "FilterDefaultDeny No")
	if strings.Contains(conf, "\nFilterType") {
		t.Fatal("FilterType is a measured trap: the conf may name it in a comment, never as a line")
	}
	limiter := read(t, filepath.Join(home, "external", "searxng", "limiter.toml"))
	has(t, limiter, "172.30.30.1/32")
	filter := read(t, filepath.Join(home, "external", "tinyproxy", "filter"))
	has(t, filter, "://127.*", "://169.254.*", "docker.internal")
	readme := read(t, filepath.Join(home, "external", "README.md"))
	has(t, readme, "rig webtools start", "--build", "CVE-2026-54387")
}

func TestGenerateWritesTheCredentialedProxyURLThroughTheSettingSeam(t *testing.T) {
	home := t.TempDir()
	st := newSetting(nil)
	tools := bundleTools(t, home, &stub{}, st)
	if _, err := generate(tools, "", false); err != nil {
		t.Fatal(err)
	}
	v, ok := st.puts["webFetchProxy"]
	if !ok {
		t.Fatalf("no settings write: %v", st.puts)
	}
	pw := authLine(read(t, filepath.Join(home, "external", "tinyproxy", "tinyproxy.conf")))
	if v != "http://rig:"+pw+"@127.0.0.1:8889" {
		t.Fatalf("webFetchProxy = %q, want the credentialed loopback URL", v)
	}
}

func TestGenerateMovesCredentialsOntoTheSameHostWithoutAsking(t *testing.T) {
	home := t.TempDir()
	st := newSetting(map[string]string{"webFetchProxy": "http://127.0.0.1:8889"})
	tools := bundleTools(t, home, &stub{}, st)
	if _, err := generate(tools, "", false); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.puts["webFetchProxy"], "@127.0.0.1:8889") {
		t.Fatalf("credentials were not added: %v", st.puts)
	}
}

func TestGenerateRefusesToMoveAProxyURLTheOperatorChose(t *testing.T) {
	home := t.TempDir()
	st := newSetting(map[string]string{"webFetchProxy": "http://proxy.corp:3128"})
	tools := bundleTools(t, home, &stub{}, st)
	err := mustErr(t, generateErr(tools, "", false), "proxy.corp:3128")
	has(t, err, "127.0.0.1:8889", "force")
	if _, ok := st.puts["webFetchProxy"]; ok {
		t.Fatal("the settings write happened on a refusal")
	}
	if _, err := generate(tools, "", true); err != nil {
		t.Fatalf("force: %v", err)
	}
	if !strings.Contains(st.puts["webFetchProxy"], "@127.0.0.1:8889") {
		t.Fatalf("force did not move it: %v", st.puts)
	}
}

func TestGenerateNamesTheSettingsFailureItHit(t *testing.T) {
	home := t.TempDir()
	st := newSetting(nil)
	st.err = "read-only filesystem"
	tools := bundleTools(t, home, &stub{}, st)
	err := mustErr(t, generateErr(tools, "", false), "read-only filesystem")
	has(t, err, "webFetchProxy")
}

func TestStatusNamesDockerMissingWithoutErroring(t *testing.T) {
	home := t.TempDir()
	s := &stub{docker: "exec: docker: file not found"}
	st, err := bundleTools(t, home, s, newSetting(nil)).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Docker, "docker") {
		t.Fatalf("docker field = %q, want the reason", st.Docker)
	}
	if len(st.Missing) == 0 {
		t.Fatalf("missing = %v, want the whole file set", st.Missing)
	}
	if !sortedStrings(st.Missing) {
		t.Fatalf("missing must be sorted: %v", st.Missing)
	}
	has(t, strings.Join(st.Lines(), "\n"), "docker", "rig webtools setup")
}

func TestStatusReportsTheDaemonAndTheStack(t *testing.T) {
	home := t.TempDir()
	s := &stub{services: "valkey\ntinyproxy\nsearxng\n"}
	tools := bundleTools(t, home, s, newSetting(nil))
	if _, err := generate(tools, "", false); err != nil {
		t.Fatal(err)
	}
	st, err := tools.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(st.Docker, "ok") {
		t.Fatalf("docker field = %q, want ok", st.Docker)
	}
	if len(st.Missing) != 0 || len(st.Differ) != 0 {
		t.Fatalf("missing/differ = %v/%v, want none on a current bundle", st.Missing, st.Differ)
	}
	if strings.Join(st.Services, ",") != "searxng,tinyproxy,valkey" {
		t.Fatalf("services must come back sorted: %v", st.Services)
	}
	if st.Stack != "" {
		t.Fatalf("stack = %q, want empty on a readable stack", st.Stack)
	}
	if !strings.Contains(st.Proxy, "@127.0.0.1:8889") {
		t.Fatalf("proxy = %q, want the credentialed URL redacted", st.Proxy)
	}
	pw := authLine(read(t, filepath.Join(home, "external", "tinyproxy", "tinyproxy.conf")))
	text := strings.Join(st.Lines(), "\n")
	if strings.Contains(st.Proxy, pw) || strings.Contains(text, pw) {
		t.Fatalf("the password must not be reported: %q", st.Proxy)
	}
}

func TestStatusNamesADifferingFileAndTheStackVoice(t *testing.T) {
	home := t.TempDir()
	s := &stub{}
	tools := bundleTools(t, home, s, newSetting(nil))
	if _, err := generate(tools, "", false); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, "external", "searxng", "settings.yml"), []byte("hand edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(home, "external", "README.md")); err != nil {
		t.Fatal(err)
	}
	s.services = ""
	s.psErr = "unsupported compose flag"
	st, err := tools.Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(st.Differ, ",") != "searxng/settings.yml" {
		t.Fatalf("differ = %v, want the hand-edited file", st.Differ)
	}
	if strings.Join(st.Missing, ",") != "README.md" {
		t.Fatalf("missing = %v, want the removed file", st.Missing)
	}
	if !strings.Contains(st.Stack, "unsupported compose flag") {
		t.Fatalf("stack = %q, want the docker voice", st.Stack)
	}
	has(t, strings.Join(st.Lines(), "\n"), "searxng/settings.yml", "README.md", "unsupported")
}

func TestStatusSaysDirectWhenTheProxyIsUnset(t *testing.T) {
	home := t.TempDir()
	st, err := bundleTools(t, home, &stub{docker: "no docker"}, newSetting(map[string]string{"webFetchProxy": ""})).Status(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(st.Lines(), "\n"), "direct") {
		t.Fatalf("lines = %v, want the direct-egress line", st.Lines())
	}
}

func TestStatusLinesCarryEveryFact(t *testing.T) {
	st := Status{
		Dir:      "/home/u/.rig/external",
		Docker:   "ok 29.9.2",
		Missing:  []string{"README.md"},
		Differ:   []string{"tinyproxy/filter"},
		Proxy:    "http://rig@127.0.0.1:8889",
		Services: []string{"tinyproxy"},
		Stack:    "no containers",
	}
	text := strings.Join(st.Lines(), "\n")
	for _, want := range []string{"/home/u/.rig/external", "ok 29.9.2", "README.md", "tinyproxy/filter",
		"http://rig@127.0.0.1:8889", "tinyproxy", "no containers"} {
		if !strings.Contains(text, want) {
			t.Fatalf("lines = %q, want %q", text, want)
		}
	}
}

func TestAnEmptyHomeRefusesEveryVerb(t *testing.T) {
	tools := New("")
	verbs := []func() error{
		func() error { _, e := tools.Status(context.Background()); return e },
		func() error { _, e := tools.Generate(context.Background(), "x", true); return e },
		func() error { _, e := tools.Start(context.Background()); return e },
		func() error { _, e := tools.Stop(context.Background()); return e },
	}
	for i, v := range verbs {
		mustErr(t, v(), "no rig home")
		_ = i
	}
	if got := New("/tmp/x").Dir(); got != filepath.Join("/tmp/x", "external") {
		t.Fatalf("dir = %q, want the home's external directory", got)
	}
}

func TestToolsWithoutTheSettingSeamRefuseTheSettingsWrite(t *testing.T) {
	home := t.TempDir()
	err := mustErr(t, generateErr(New(home, WithRunner(&stub{})), "", false), "settings")
	has(t, err, "webtools")
}

func TestToolsWithoutARunnerRefuseTheVerbsThatNeedDocker(t *testing.T) {
	home := t.TempDir()
	tools := New(home, WithSetting(newSetting(nil)))
	if _, err := generate(tools, "", false); err != nil {
		t.Fatalf("generate needs no docker: %v", err)
	}
	mustErr(t, startErr(tools), "no runner")
	mustErr(t, stopErr(tools), "no runner")
	_, err := tools.Status(context.Background())
	mustErr(t, err, "no runner")
}

func TestEveryRefusalNamesWhatToDoAboutIt(t *testing.T) {
	home := t.TempDir()
	tools := bundleTools(t, home, &stub{docker: "not found"}, newSetting(nil))
	_, err := generate(tools, "x", false)
	mustErr(t, err, "webtools:")
	_, err = tools.Start(context.Background())
	mustErr(t, err, "webtools:")
	_, err = tools.Stop(context.Background())
	mustErr(t, err, "webtools:")
}

func TestTheStatusJSONShapeIsTheFieldsAsRead(t *testing.T) {
	raw, err := json.Marshal(Status{Dir: "d", Docker: "ok", Proxy: "http://rig@127.0.0.1:8889",
		Missing: []string{}, Differ: []string{}, Services: []string{}})
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{`"dir":"d"`, `"docker":"ok"`, `"proxy":"http://rig@127.0.0.1:8889"`, `"missing":[]`} {
		if !strings.Contains(string(raw), want) {
			t.Fatalf("json = %s, want %s", raw, want)
		}
	}
}

func TestTheRunnerKeepsTheTailAndNamesTheDrop(t *testing.T) {
	out := capTail(strings.Repeat("x", maxRunBytes+128), maxRunBytes)
	if !strings.Contains(out, "[TRUNCATED") || !strings.Contains(out, "128") {
		t.Fatalf("capped output = %q, want the loud marker", out)
	}
	if len([]rune(out)) > maxRunBytes+128 {
		t.Fatalf("the cap did not bound the bytes")
	}
	if capTail("short", maxRunBytes) != "short" {
		t.Fatal("a short output must pass through unchanged")
	}
}

func TestTheRealRunnerRunsArgvAndNamesItsFailure(t *testing.T) {
	r := NewRunner()
	out, err := r.Run(context.Background(), "/bin/echo", "hello", "webtools")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "hello webtools") {
		t.Fatalf("out = %q, want the echo", out)
	}
	_, err = r.Run(context.Background(), "sh", "-c", "exit 3")
	mustErr(t, err, "exit status 3")
}

func generate(tools Tools, pw string, force bool) (string, error) {
	return tools.Generate(context.Background(), pw, force)
}

func generateErr(tools Tools, pw string, force bool) error {
	_, err := tools.Generate(context.Background(), pw, force)
	return err
}

func startErr(tools Tools) error {
	_, err := tools.Start(context.Background())
	return err
}

func stopErr(tools Tools) error {
	_, err := tools.Stop(context.Background())
	return err
}

func read(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(b)
}

func mode(t *testing.T, path string) os.FileMode {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat %s: %v", path, err)
	}
	return info.Mode()
}

func walk(t *testing.T, root string, fn func(path string, info os.FileInfo)) {
	t.Helper()
	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		fn(path, info)
		return nil
	})
	if err != nil {
		t.Fatalf("walk %s: %v", root, err)
	}
}

func authLine(conf string) string {
	for _, l := range strings.Split(conf, "\n") {
		if strings.HasPrefix(l, "BasicAuth rig ") {
			return strings.TrimSpace(strings.TrimPrefix(l, "BasicAuth rig "))
		}
	}
	return ""
}

func secretLine(settings string) string {
	for _, l := range strings.Split(settings, "\n") {
		if strings.Contains(l, "secret_key:") {
			return strings.Trim(strings.TrimSpace(strings.SplitAfter(l, "secret_key:")[1]), `"'`)
		}
	}
	return ""
}

func isHex(s string) bool {
	for _, r := range s {
		if !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') {
			return false
		}
	}
	return s != ""
}

func sortedStrings(s []string) bool {
	for i := 1; i < len(s); i++ {
		if s[i-1] > s[i] {
			return false
		}
	}
	return true
}

func keysOf(m map[string]bool) []string {
	out := []string{}
	for k := range m {
		out = append(out, k)
	}
	return out
}
