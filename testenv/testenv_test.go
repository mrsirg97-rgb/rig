package testenv_test

import (
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	sched "github.com/mrsirg97-rgb/rig/v3/store/scheduler"
	"github.com/mrsirg97-rgb/rig/v3/testenv"
)

func TestMain(m *testing.M) {
	testenv.Main(m)
}

func TestTransportRefusesANonServerHost(t *testing.T) {
	req, err := http.NewRequest(http.MethodGet, "http://127.0.0.1:8090/running", nil)
	if err != nil {
		t.Fatal(err)
	}
	_, err = testenv.Transport().RoundTrip(req)
	if err == nil || !strings.Contains(err.Error(), "127.0.0.1:8090") {
		t.Fatalf("a host that is not an httptest server must be refused, got %v", err)
	}
}

func TestTransportAllowsAServerHost(t *testing.T) {
	srv := testenv.Server(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "ok")
	}))
	req, err := http.NewRequest(http.MethodGet, srv.URL+"/v1/models", nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := testenv.Transport().RoundTrip(req)
	if err != nil {
		t.Fatalf("a registered httptest server must be dialable: %v", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if string(body) != "ok" {
		t.Fatalf("the server body = %q, want ok", body)
	}
}

func TestIsolatePinsTheSwapToAClosedPort(t *testing.T) {
	if got := os.Getenv("RIG_SWAP_URL"); got != testenv.ClosedSwapURL {
		t.Fatalf("RIG_SWAP_URL = %q, want the closed port", got)
	}
}

func TestIsolateUnsetsTheWorkerMarkers(t *testing.T) {
	if got := os.Getenv(sched.DelegateEnv); got != "" {
		t.Fatalf("the suite met %s=%q", sched.DelegateEnv, got)
	}
	if got := os.Getenv(sched.FleetEnv); got != "" {
		t.Fatalf("the suite met %s=%q", sched.FleetEnv, got)
	}
}

func TestAMainFailsLoudWhenTheTestHomeCannotBeBuilt(t *testing.T) {
	base := t.TempDir()
	ro := filepath.Join(base, "ro")
	if err := os.Mkdir(ro, 0o555); err != nil {
		t.Fatal(err)
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^$")
	cmd.Dir = base
	env := make([]string, 0, len(os.Environ())+1)
	for _, kv := range os.Environ() {
		if !strings.HasPrefix(kv, "TMPDIR=") {
			env = append(env, kv)
		}
	}
	cmd.Env = append(env, "TMPDIR="+filepath.Join(ro, "tmp"))
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("a test home that cannot be built must fail Main loud, it exited 0; output: %s", out)
	}
	var code int
	if ee, ok := err.(*exec.ExitError); ok {
		code = ee.ExitCode()
	}
	if code != 1 {
		t.Fatalf("Main must exit 1 on a broken test home, got %d; output: %s", code, out)
	}
	if !strings.Contains(string(out), "build the test home") {
		t.Fatalf("the refusal must name the step on stderr, got: %s", out)
	}
}
