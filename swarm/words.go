package swarm

func plural(n int, word string) string {
	if n == 1 {
		return word
	}
	return word + "s"
}
