package tui

func (t *tui) RepaintTheme(th Theme) {
	t.mu.Lock()
	t.theme = th
	t.mu.Unlock()
}
