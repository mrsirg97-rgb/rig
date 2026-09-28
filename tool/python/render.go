package python

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"syscall"
	"unicode"
)

func render(r Reply) string {
	var parts []string
	if r.Note != nil && strings.TrimSpace(*r.Note) != "" {
		parts = append(parts, strings.TrimRightFunc(*r.Note, unicode.IsSpace))
	}
	if r.Out != nil && strings.TrimSpace(*r.Out) != "" {
		parts = append(parts, strings.TrimRightFunc(*r.Out, unicode.IsSpace))
	}
	if r.Err != nil && strings.TrimSpace(*r.Err) != "" {
		parts = append(parts, "[stderr]\n"+strings.TrimRightFunc(*r.Err, unicode.IsSpace))
	}
	if r.Error != nil && *r.Error != "" {
		parts = append(parts, "[error]\n"+*r.Error)
	}
	if r.Result != nil && *r.Result != "" && (r.Out == nil || !strings.Contains(*r.Out, *r.Result)) {
		parts = append(parts, *r.Result)
	}
	if len(parts) == 0 {
		if r.Ok {
			return "(no output)"
		}
		return "(failed, no output)"
	}
	return strings.Join(parts, "\n")
}

func strPtr(s string) *string { return &s }

func exitDescription(st *os.ProcessState) string {
	if st == nil {
		return "code 1"
	}
	if ws, ok := st.Sys().(syscall.WaitStatus); ok {
		if ws.Signaled() {
			return "signal " + signalName(ws.Signal())
		}
		return fmt.Sprintf("code %d", ws.ExitStatus())
	}
	return fmt.Sprintf("code %d", st.ExitCode())
}

var signalNames = map[syscall.Signal]string{
	syscall.SIGHUP:    "SIGHUP",
	syscall.SIGINT:    "SIGINT",
	syscall.SIGQUIT:   "SIGQUIT",
	syscall.SIGILL:    "SIGILL",
	syscall.SIGTRAP:   "SIGTRAP",
	syscall.SIGABRT:   "SIGABRT",
	syscall.SIGBUS:    "SIGBUS",
	syscall.SIGFPE:    "SIGFPE",
	syscall.SIGKILL:   "SIGKILL",
	syscall.SIGUSR1:   "SIGUSR1",
	syscall.SIGSEGV:   "SIGSEGV",
	syscall.SIGUSR2:   "SIGUSR2",
	syscall.SIGPIPE:   "SIGPIPE",
	syscall.SIGALRM:   "SIGALRM",
	syscall.SIGTERM:   "SIGTERM",
	syscall.SIGCHLD:   "SIGCHLD",
	syscall.SIGCONT:   "SIGCONT",
	syscall.SIGSTOP:   "SIGSTOP",
	syscall.SIGTSTP:   "SIGTSTP",
	syscall.SIGTTIN:   "SIGTTIN",
	syscall.SIGTTOU:   "SIGTTOU",
	syscall.SIGURG:    "SIGURG",
	syscall.SIGXCPU:   "SIGXCPU",
	syscall.SIGXFSZ:   "SIGXFSZ",
	syscall.SIGVTALRM: "SIGVTALRM",
	syscall.SIGPROF:   "SIGPROF",
	syscall.SIGWINCH:  "SIGWINCH",
	syscall.SIGIO:     "SIGIO",
	syscall.SIGSYS:    "SIGSYS",
}

func signalName(s syscall.Signal) string {
	if n, ok := signalNames[s]; ok {
		return n
	}
	return "SIG" + strconv.Itoa(int(s))
}
