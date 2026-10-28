package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mrsirg97-rgb/rig/v3/command"
	"github.com/mrsirg97-rgb/rig/v3/config"
	"github.com/mrsirg97-rgb/rig/v3/webtools"
)

type configSetting struct{ dir string }

func (s *configSetting) Read(key string) (string, bool, error) {
	return config.ReadKey(s.dir, key)
}

func (s *configSetting) Put(key, value string) error { return config.SetKey(s.dir, key, value) }

func newWebTools(home string) webtools.Tools {
	return webtools.New(home,
		webtools.WithRunner(webtools.NewRunner()),
		webtools.WithSetting(&configSetting{dir: home}),
		webtools.WithVersion(Version))
}

func runWebTools(args []string) int {
	home, err := rigHome()
	if err != nil {
		fmt.Fprintln(os.Stderr, "rig:", err)
		return 2
	}
	tools := newWebTools(home)
	for _, c := range command.All() {
		if c.Name() != "webtools" {
			continue
		}
		out, err := c.Run(context.Background(), strings.Join(args, " "), &command.Env{
			WebTools: func() webtools.Tools { return tools },
		})
		if err != nil {
			fmt.Fprintln(os.Stderr, "rig:", err)
			return 1
		}
		fmt.Println(out)
		return 0
	}
	fmt.Fprintln(os.Stderr, "rig: no webtools command")
	return 1
}
