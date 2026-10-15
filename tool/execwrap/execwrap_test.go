package execwrap_test

import (
	"reflect"
	"testing"

	"github.com/mrsirg97-rgb/rig/v3/tool/execwrap"
)

func TestArgsPrependsTheEnvNamedWrapper(t *testing.T) {
	t.Setenv(execwrap.Env, "/usr/bin/rig")
	got := execwrap.Args([]string{"bash", "-c", "ls -l"})
	want := []string{"/usr/bin/rig", "-exec", "bash", "-c", "ls -l"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("argv = %v, want the env-named helper prepended: %v", got, want)
	}
}

func TestArgsWithoutTheEnvPassesArgvThrough(t *testing.T) {
	t.Setenv(execwrap.Env, "")
	argv := []string{"bash", "-c", "ls"}
	got := execwrap.Args(argv)
	if !reflect.DeepEqual(got, argv) {
		t.Fatalf("argv = %v, want it through untouched when no helper is named", got)
	}
}

func TestArgsIndexContractHoldsTheHelperShape(t *testing.T) {
	t.Setenv(execwrap.Env, "/usr/bin/rig")
	got := execwrap.Args([]string{"bash", "-c", "ls"})
	if got[0] != "/usr/bin/rig" {
		t.Fatalf("argv[0] = %q, want the helper itself", got[0])
	}
	if got[1] != "-exec" {
		t.Fatalf("argv[1] = %q, want the helper's exec flag", got[1])
	}
	if !reflect.DeepEqual(got[2:], []string{"bash", "-c", "ls"}) {
		t.Fatalf("the tool's argv must start at index 2, got %v", got[2:])
	}
}
