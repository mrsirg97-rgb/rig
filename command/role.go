package command

import (
	"context"
	"errors"
	"fmt"
	"strings"
)

type roleCmd struct{}

func (roleCmd) Sub() []Sub { return RoleHints() }

func (roleCmd) Name() string { return "role" }

func (roleCmd) Description() string {
	return "the session's stance from the next turn: default, architect, or reviewer"
}

func (roleCmd) Run(ctx context.Context, args string, env any) (string, error) {
	e, err := EnvOf(env)
	if err != nil {
		return "", err
	}
	if e.Role == nil {
		return "", errors.New("role: no role seam (the root did not wire one)")
	}
	fields := strings.Fields(args)
	if len(fields) > 1 {
		return "", errors.New("role: usage: role [<name>]")
	}
	if len(fields) == 1 {
		if !ValidRole(fields[0]) {
			return "", fmt.Errorf("role: %q is not a role (default, architect, reviewer)", fields[0])
		}
		if e.SetRole == nil {
			return "", errors.New("role: no set seam (the root did not wire one)")
		}
		e.SetRole(ctx, fields[0])
		return "role: " + fields[0] + " (next turn)", nil
	}
	label := e.Role()
	if label == "" {
		label = "default"
	}
	names := make([]string, 0, len(roleHints))
	descs := map[string]string{}
	for _, h := range roleHints {
		names = append(names, h.Name)
		descs[h.Name] = h.Desc
	}
	return choices(plural(len(names), "role"), label, names, descs), nil
}
