package command

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/mrsirg97-rgb/rig/v3/webtools"
)

type webtoolsCmd struct{}

func (webtoolsCmd) Name() string { return "webtools" }

func (webtoolsCmd) Description() string {
	return "the web tools: bare shows the bundle's state; /webtools status|setup [password] [force]|start|stop"
}

var webtoolsVerbs = []Sub{
	{Name: "status", Desc: "what is on disk, what differs, what runs, the proxy as reported"},
	{Name: "setup", Desc: "build the bundle for this machine and point webFetchProxy at it"},
	{Name: "start", Desc: "the volumes, their ownership, and compose up -d --build"},
	{Name: "stop", Desc: "compose stop; the volumes keep their data"},
}

func (webtoolsCmd) Sub() []Sub { return append([]Sub(nil), webtoolsVerbs...) }

func (webtoolsCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := envOf(env)
	if err != nil {
		return "", err
	}
	var tools webtools.Tools
	if e.WebTools != nil {
		tools = e.WebTools()
	}
	if tools == nil {
		return "", errors.New("webtools: no web tools (the root did not wire one)")
	}
	fields := strings.Fields(args)
	verb, rest := "status", []string(nil)
	if len(fields) > 0 {
		verb, rest = strings.ToLower(fields[0]), fields[1:]
	}
	password, force := "", false
	for _, f := range rest {
		switch {
		case f == "force":
			force = true
		case password == "":
			password = f
		default:
			return "", usageErr("setup")
		}
	}
	if verb != "setup" && (password != "" || force) {
		return "", fmt.Errorf("webtools: %q takes no password or force (those belong to setup)", verb)
	}
	switch verb {
	case "status":
		st, err := tools.Status(ctx)
		if err != nil {
			return "", err
		}
		return strings.Join(st.Lines(), "\n"), nil
	case "setup":
		return tools.Generate(ctx, password, force)
	case "start":
		return tools.Start(ctx)
	case "stop":
		return tools.Stop(ctx)
	default:
		return "", fmt.Errorf("webtools: %q is not a verb (%s)", verb, strings.Join(verbNames(), ", "))
	}
}

func usageErr(verb string) error {
	return fmt.Errorf("webtools: usage: webtools %s [<password>] [force]", verb)
}

func verbNames() []string {
	out := make([]string, 0, len(webtoolsVerbs))
	for _, v := range webtoolsVerbs {
		out = append(out, v.Name)
	}
	return out
}
