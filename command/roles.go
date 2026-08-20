package command

// roleProse is the shipped stances (SPEC_MODES 2), verbatim: the
// stance's prose sits between the system prompt and AGENTS.md — the
// runtime's identity first, the stance second, the operator's contract
// third — and never outranks the contract (AGENTS.md reads after it and
// wins conflicts by position). default injects nothing.
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

// roleHints is the menu's vocabulary (SPEC_MODES 3): the shipped three,
// each one-lined — default is the absence of a stance.
var roleHints = []Sub{
	{Name: "default", Desc: "no stance — today's prompt exactly"},
	{Name: "architect", Desc: "design before implementation"},
	{Name: "reviewer", Desc: "review, don't build — hunt defects"},
}

// RoleProse returns the stance's embedded prose for a role name;
// default (and the empty root state) injects nothing.
func RoleProse(name string) string {
	if name == "default" {
		return ""
	}
	return roleProse[name]
}

// ValidRole reports whether name is one of the shipped stances.
func ValidRole(name string) bool {
	return name == "" || name == "default" || name == "architect" || name == "reviewer"
}

// RoleHints is the role command's Sub() vocabulary: the shipped three,
// in order.
func RoleHints() []Sub {
	out := make([]Sub, len(roleHints))
	copy(out, roleHints)
	return out
}
