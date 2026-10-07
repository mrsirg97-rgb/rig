package plugins

import (
	"fmt"
	"strings"
)

type Attr struct {
	Name string
	Kind string
}

type Contract struct {
	Kind  string
	Attrs []Attr
}

var PluginContract = Contract{
	Kind:  "plugin",
	Attrs: []Attr{{Name: "DESCRIPTION", Kind: "str"}, {Name: "SCHEMA", Kind: "dict"}, {Name: "run", Kind: "callable"}},
}

var TrainerContract = Contract{
	Kind:  "trainer",
	Attrs: []Attr{{Name: "train", Kind: "callable"}, {Name: "evaluate", Kind: "callable"}},
}

func (c Contract) registry() string {
	return "__rig_" + c.Kind + "s__"
}

func (c Contract) missing(source string) (string, bool) {
	for _, a := range c.Attrs {
		want := a.Name
		if a.Kind == "callable" {
			want = "def " + a.Name + "("
		}
		if !strings.Contains(source, want) {
			return want, true
		}
	}
	return "", false
}

func (c Contract) wants() string {
	parts := make([]string, 0, len(c.Attrs))
	for _, a := range c.Attrs {
		parts = append(parts, a.Name)
	}
	return strings.Join(parts, ", ")
}

func contractRefusal(c Contract, source string) error {
	if want, missing := c.missing(source); missing {
		return fmt.Errorf("the %s contract is %s: missing %s", c.Kind, c.wants(), want)
	}
	return nil
}
