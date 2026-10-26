package webtools

import (
	"context"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	webtool "github.com/mrsirg97-rgb/rig/v3/tool/web"
)

//go:embed files
var bundleFS embed.FS

const settingsKey = "webFetchProxy"

type Tools interface {
	Dir() string
	Status(ctx context.Context) (Status, error)
	Generate(ctx context.Context, password string, force bool) (string, error)
	Start(ctx context.Context) (string, error)
	Stop(ctx context.Context) (string, error)
}

type Setting interface {
	Read(key string) (string, bool, error)
	Put(key, value string) error
}

type Option func(*options)

type options struct {
	runner  Runner
	setting Setting
	version string
}

func WithRunner(runner Runner) Option { return func(o *options) { o.runner = runner } }
func WithSetting(setting Setting) Option {
	return func(o *options) { o.setting = setting }
}
func WithVersion(version string) Option {
	return func(o *options) { o.version = version }
}

func New(home string, opts ...Option) Tools {
	var o options
	for _, opt := range opts {
		opt(&o)
	}
	dir := ""
	if home != "" {
		dir = filepath.Join(home, dirName)
	}
	return &tools{home: home, dir: dir, runner: o.runner, setting: o.setting, version: o.version}
}

type Status struct {
	Dir      string   `json:"dir"`
	Docker   string   `json:"docker"`
	Missing  []string `json:"missing"`
	Differ   []string `json:"differ"`
	Proxy    string   `json:"proxy"`
	Services []string `json:"services"`
	Stack    string   `json:"stack"`
}

func (s Status) Lines() []string {
	out := []string{"bundle: " + s.Dir, "docker: " + s.Docker}
	if len(s.Missing) > 0 {
		out = append(out, "missing: "+strings.Join(s.Missing, ", "))
	}
	if len(s.Differ) > 0 {
		out = append(out, "differs: "+strings.Join(s.Differ, ", "))
	}
	if s.Proxy == "" {
		out = append(out, "proxy: direct (settings.json "+settingsKey+" unset)")
	} else {
		out = append(out, "proxy: "+s.Proxy)
	}
	if len(s.Services) > 0 {
		out = append(out, "running: "+strings.Join(s.Services, ", "))
	} else {
		out = append(out, "running: nothing")
	}
	if s.Stack != "" {
		out = append(out, "stack: "+s.Stack)
	}
	return out
}

type tools struct {
	home    string
	dir     string
	runner  Runner
	setting Setting
	version string
}

func (t *tools) Dir() string { return t.dir }

func (t *tools) composePath() string { return filepath.Join(t.dir, "docker-compose.yml") }

func (t *tools) homeErr() error {
	if t.home == "" {
		return errors.New("webtools: no rig home (the root did not pass one to webtools.New)")
	}
	return nil
}

func (t *tools) runnerErr() error {
	if t.runner == nil {
		return errors.New("webtools: no runner (the root did not pass webtools.WithRunner)")
	}
	return nil
}

func (t *tools) settingErr() error {
	if t.setting == nil {
		return errors.New("webtools: no settings seam (the root did not pass webtools.WithSetting), so " + settingsKey + " cannot be written")
	}
	return nil
}

func bundleFiles() ([]string, error) {
	out := []string{}
	err := fs.WalkDir(bundleFS, "files", func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !d.IsDir() {
			out = append(out, strings.TrimPrefix(p, "files/"))
		}
		return nil
	})
	if err != nil {
		return nil, fmt.Errorf("webtools: the embedded bundle: %v", err)
	}
	sort.Strings(out)
	return out, nil
}

func templateFor(rel string) (string, error) {
	b, err := bundleFS.ReadFile("files/" + rel)
	if err != nil {
		return "", fmt.Errorf("webtools: the embedded %s: %v", rel, err)
	}
	return string(b), nil
}

func (t *tools) read(rel string) (string, bool, error) {
	b, err := os.ReadFile(filepath.Join(t.dir, rel))
	if err != nil {
		if os.IsNotExist(err) {
			return "", false, nil
		}
		return "", false, fmt.Errorf("webtools: %s/%s could not be read: %v", t.dir, rel, err)
	}
	return string(b), true, nil
}

func (t *tools) values(explicit string, generate bool) (render, error) {
	r := render{version: t.version}
	if explicit != "" {
		if err := passwordError(explicit); err != nil {
			return r, err
		}
		r.pw = explicit
	} else if conf, ok, err := t.read(proxyConfPath); err != nil {
		return r, err
	} else if ok && passwordOf(conf) != "" {
		r.pw = passwordOf(conf)
	} else if generate {
		pw, err := newHex(hexPassword)
		if err != nil {
			return r, err
		}
		r.pw = pw
	}
	settings, ok, err := t.read(valkeyPath)
	if err != nil {
		return r, err
	}
	if ok && secretOf(settings) != "" {
		r.secret = secretOf(settings)
	} else if generate {
		secret, err := newHex(hexSecret)
		if err != nil {
			return r, err
		}
		r.secret = secret
	}
	return r, nil
}

func (t *tools) checkSettings(target string, force bool) error {
	v, ok, err := t.setting.Read(settingsKey)
	if err != nil {
		return fmt.Errorf("webtools: settings.json could not be read for %s: %v", settingsKey, err)
	}
	if !ok || v == "" || v == target || sameProxyHost(v, target) {
		return nil
	}
	if force {
		return nil
	}
	return fmt.Errorf("webtools: settings.json %s already points at %s, not the bundle's 127.0.0.1:%s; pass force to move it",
		settingsKey, webtool.ProxyDisplay(v), proxyPort)
}

func sameProxyHost(a, b string) bool {
	au, aerr := url.Parse(a)
	bu, berr := url.Parse(b)
	if aerr != nil || berr != nil {
		return false
	}
	return au.Host == bu.Host
}

func (t *tools) classify(files []string, r render) (missing, differ []string, err error) {
	missing, differ = []string{}, []string{}
	for _, rel := range files {
		tmpl, terr := templateFor(rel)
		if terr != nil {
			return nil, nil, terr
		}
		have, present, rerr := t.read(rel)
		if rerr != nil {
			return nil, nil, rerr
		}
		switch {
		case !present:
			missing = append(missing, rel)
		case have != r.apply(tmpl):
			differ = append(differ, rel)
		}
	}
	return missing, differ, nil
}

func (t *tools) Generate(ctx context.Context, password string, force bool) (string, error) {
	if err := t.homeErr(); err != nil {
		return "", err
	}
	if err := t.settingErr(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	r, err := t.values(password, true)
	if err != nil {
		return "", err
	}
	files, err := bundleFiles()
	if err != nil {
		return "", err
	}
	target := "http://" + proxyUser + ":" + r.pw + "@127.0.0.1:" + proxyPort
	if err := t.checkSettings(target, force); err != nil {
		return "", err
	}
	missing, differ, err := t.classify(files, r)
	if err != nil {
		return "", err
	}
	if len(differ) > 0 && !force {
		return "", fmt.Errorf("webtools: %d file(s) differ from what rig generates (%s); pass force to overwrite them",
			len(differ), strings.Join(differ, ", "))
	}
	if err := os.MkdirAll(t.dir, 0o755); err != nil {
		return "", fmt.Errorf("webtools: cannot create %s: %v", t.dir, err)
	}
	wrote := append(append([]string{}, missing...), differ...)
	for _, rel := range wrote {
		tmpl, err := templateFor(rel)
		if err != nil {
			return "", err
		}
		path := filepath.Join(t.dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return "", fmt.Errorf("webtools: cannot create %s: %v", filepath.Dir(path), err)
		}
		if err := os.WriteFile(path, []byte(r.apply(tmpl)), modeFor(rel)); err != nil {
			return "", fmt.Errorf("webtools: cannot write %s: %v", path, err)
		}
		if err := os.Chmod(path, modeFor(rel)); err != nil {
			return "", fmt.Errorf("webtools: cannot set %s to %o: %v", path, modeFor(rel), err)
		}
	}
	already := false
	if v, ok, err := t.setting.Read(settingsKey); err == nil && ok && v == target {
		already = true
	}
	if !already {
		if err := t.setting.Put(settingsKey, target); err != nil {
			return "", fmt.Errorf("webtools: the %s settings write failed: %v", settingsKey, err)
		}
	}
	lines := []string{}
	if len(wrote) == 0 {
		lines = append(lines, fmt.Sprintf("nothing written: %d of %d files in %s are already what rig generates",
			len(files), len(files), t.dir))
	} else {
		lines = append(lines, fmt.Sprintf("wrote %d of %d files in %s: %s",
			len(wrote), len(files), t.dir, strings.Join(wrote, ", ")))
	}
	if already {
		lines = append(lines, "settings.json "+settingsKey+" already points at "+webtool.ProxyDisplay(target))
	} else {
		lines = append(lines, "settings.json "+settingsKey+" is now "+webtool.ProxyDisplay(target))
	}
	lines = append(lines, "start it: rig webtools start (docker compose up -d --build applies a later edit)")
	return strings.Join(lines, "; "), nil
}

func (t *tools) probe(ctx context.Context) (string, error) {
	out, err := t.runner.Run(ctx, "docker", "info", "--format", "{{.ServerVersion}}")
	if err != nil {
		return out, err
	}
	return strings.TrimSpace(out), nil
}

func (t *tools) verified(ctx context.Context) error {
	out, err := t.probe(ctx)
	if err == nil {
		return nil
	}
	if errors.Is(err, exec.ErrNotFound) {
		return fmt.Errorf("webtools: docker is not on PATH: install Docker (or Docker Desktop) and rerun rig webtools start")
	}
	return fmt.Errorf("webtools: the docker daemon does not answer: %s", dockerVoice(err, out))
}

func (t *tools) Start(ctx context.Context) (string, error) {
	if err := t.homeErr(); err != nil {
		return "", err
	}
	if err := t.runnerErr(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := os.Stat(t.composePath()); err != nil {
		return "", fmt.Errorf("webtools: no bundle in %s (run rig webtools setup)", t.dir)
	}
	if err := t.verified(ctx); err != nil {
		return "", err
	}
	steps := [][]string{
		{"volume", "create", valkeyVolume},
		{"run", "--rm", "--user", "0:0", "--entrypoint", "chown", "-v", valkeyVolume + ":/data",
			helperImage, "-R", valkeyUID, "/data"},
		{"volume", "create", cacheVolume},
		{"run", "--rm", "--user", "0:0", "--entrypoint", "chown", "-v", cacheVolume + ":/var/cache/searxng",
			helperImage, "-R", searxUID, "/var/cache/searxng"},
	}
	for _, step := range steps {
		out, err := t.runner.Run(ctx, "docker", step...)
		if err != nil {
			return "", fmt.Errorf("webtools: docker %s failed: %s", strings.Join(step, " "), dockerVoice(err, out))
		}
	}
	out, err := t.runner.Run(ctx, "docker", "compose", "-f", t.composePath(), "up", "-d", "--build")
	if err != nil {
		return "", fmt.Errorf("webtools: docker compose up refused: %s", dockerVoice(err, out))
	}
	return fmt.Sprintf("started the web-tools bundle in %s: search 127.0.0.1:%s, fetch egress through 127.0.0.1:%s",
		t.dir, searxPort, proxyPort), nil
}

func (t *tools) Stop(ctx context.Context) (string, error) {
	if err := t.homeErr(); err != nil {
		return "", err
	}
	if err := t.runnerErr(); err != nil {
		return "", err
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	if _, err := os.Stat(t.composePath()); err != nil {
		return "", fmt.Errorf("webtools: no bundle in %s (run rig webtools setup)", t.dir)
	}
	if err := t.verified(ctx); err != nil {
		return "", err
	}
	out, err := t.runner.Run(ctx, "docker", "compose", "-f", t.composePath(), "stop")
	if err != nil {
		return "", fmt.Errorf("webtools: docker compose stop refused: %s", dockerVoice(err, out))
	}
	return fmt.Sprintf("stopped the web-tools bundle in %s (the volumes keep their data; rig webtools start brings it back)", t.dir), nil
}

func (t *tools) Status(ctx context.Context) (Status, error) {
	if err := t.homeErr(); err != nil {
		return Status{}, err
	}
	if err := t.runnerErr(); err != nil {
		return Status{}, err
	}
	files, err := bundleFiles()
	if err != nil {
		return Status{}, err
	}
	st := Status{Dir: t.dir, Missing: []string{}, Differ: []string{}, Services: []string{}}
	version, err := t.probe(ctx)
	switch {
	case err == nil:
		st.Docker = "ok " + version
	case errors.Is(err, exec.ErrNotFound):
		st.Docker = "docker is not on PATH: install Docker, then rig webtools setup"
	default:
		st.Docker = "the docker daemon does not answer: " + dockerVoice(err, version)
	}
	r, err := t.values("", false)
	if err != nil {
		return st, err
	}
	missing, differ, err := t.classify(files, r)
	if err != nil {
		return st, err
	}
	st.Missing, st.Differ = missing, differ
	if strings.HasPrefix(st.Docker, "ok ") {
		if _, err := os.Stat(t.composePath()); err == nil {
			out, err := t.runner.Run(ctx, "docker", "compose", "-f", t.composePath(), "ps",
				"--services", "--filter", "status=running")
			if err != nil {
				st.Stack = dockerVoice(err, out)
			} else {
				st.Services = sortedLines(out)
			}
		}
	}
	if t.setting != nil {
		v, ok, err := t.setting.Read(settingsKey)
		switch {
		case err != nil:
			st.Stack = first(st.Stack, "settings.json "+settingsKey+": "+err.Error())
		case ok && v != "":
			st.Proxy = webtool.ProxyDisplay(v)
		}
	}
	return st, nil
}

func sortedLines(out string) []string {
	list := []string{}
	for _, l := range strings.Split(strings.TrimSpace(out), "\n") {
		if l = strings.TrimSpace(l); l != "" {
			list = append(list, l)
		}
	}
	sort.Strings(list)
	return list
}

func first(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
