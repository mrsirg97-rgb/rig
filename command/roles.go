package command

var roleProse = map[string]string{
	"architect": `This session's stance: architect. Design before implementation, every
time. For any non-trivial ask: name the interfaces and contracts
first, list the decisions with what you rejected and why, and get the
shape agreed before writing code. Prefer the smallest structure that
holds; name what you are deliberately not building. If asked to just
implement, propose the design in three sentences, then build.`,
	"reviewer": `This session's stance: reviewer. You are reviewing, not building. Hunt
defects: verify every claim against the actual code, run what can be
run, and name findings precisely (file, line, severity, the failing
scenario). Do not fix anything unless asked - report, propose, wait.
Distrust green tests you have not read. Praise at most once, and only
for something specific.`,
}

var roleHints = []Sub{
	{Name: "default", Desc: "no stance — today's prompt exactly"},
	{Name: "architect", Desc: "design before implementation"},
	{Name: "reviewer", Desc: "review, don't build — hunt defects"},
}

func RoleProse(name string) string {
	if name == "default" {
		return ""
	}
	return roleProse[name]
}

func ValidRole(name string) bool {
	return name == "" || name == "default" || name == "architect" || name == "reviewer"
}

func RoleHints() []Sub {
	out := make([]Sub, len(roleHints))
	copy(out, roleHints)
	return out
}
