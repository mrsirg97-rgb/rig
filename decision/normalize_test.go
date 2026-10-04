package decision

import (
	"os"
	"path/filepath"
	"testing"
)

func TestNormalizeStripsOneLeadingCdInsideTheWorkspace(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("no home directory: %v", err)
	}
	ws := filepath.Join(home, "Projects", "rig")
	for _, c := range []struct{ name, command, want string }{
		{"cd to the workspace by tilde", "cd ~/Projects/rig && rm -rf build", "rm -rf build"},
		{"cd under the workspace", "cd " + ws + "/build && make", "make"},
		{"cd relative to the working directory", "cd build && make", "make"},
		{"cd outside the workspace stays", "cd /etc && rm -rf build", "cd /etc && rm -rf build"},
		{"cd to a sibling of the workspace stays", "cd ~/Projects/hedge && make", "cd ~/Projects/hedge && make"},
		{"cd to the workspace's parent stays", "cd " + home + " && ls", "cd " + home + " && ls"},
		{"cd home stays", "cd && ls", "cd && ls"},
		{"cd without a follow-up stays", "cd /tmp &&", "cd /tmp &&"},
		{"bare cd stays", "cd /tmp", "cd /tmp"},
		{"no cd", "ls -la", "ls -la"},
		{"one leading strip", "cd " + ws + " && cd /var && ls", "cd /var && ls"},
		{"cd outside then inside stays", "cd /etc && cd " + ws + " && ls", "cd /etc && cd " + ws + " && ls"},
		{"cd into the workspace runs collapse", "cd   " + ws + "  &&   ls   -la", "ls -la"},
		{"trim", "  ls -la  ", "ls -la"},
		{"runs inside collapse", "rm   -rf   /tmp/x", "rm -rf /tmp/x"},
	} {
		if got := normalize(c.command, ws, ws); got != c.want {
			t.Errorf("%s: normalize(%q) = %q, want %q", c.name, c.command, got, c.want)
		}
	}
}

func TestNormalizeResolvesARelativeCdAgainstTheNamedWorkspace(t *testing.T) {
	home, err := os.UserHomeDir()
	if err != nil {
		t.Fatalf("no home directory: %v", err)
	}
	ws := filepath.Join(home, "Projects", "rig", "build")
	for _, c := range []struct{ name, command, want string }{
		{"relative to the named workspace", "cd src && make", "make"},
		{"outside the named workspace stays", "cd " + home + " && ls", "cd " + home + " && ls"},
	} {
		if got := normalize(c.command, ws, home); got != c.want {
			t.Errorf("%s: normalize(%q) = %q, want %q", c.name, c.command, got, c.want)
		}
	}
}
