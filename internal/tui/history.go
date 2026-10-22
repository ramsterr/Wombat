package tui

// recordHistory remembers a submitted prompt for ctrl+p / ctrl+n. Repeated
// consecutive prompts are collapsed so the history stays useful.
func (c *chat) recordHistory(text string) {
	text = trimPrompt(text)
	if text == "" {
		return
	}
	if n := len(c.prompts); n > 0 && c.prompts[n-1] == text {
		c.histIdx = len(c.prompts)
		c.histDraft = ""
		return
	}
	c.prompts = append(c.prompts, text)
	if len(c.prompts) > 200 {
		c.prompts = c.prompts[len(c.prompts)-200:]
	}
	c.histIdx = len(c.prompts)
	c.histDraft = ""
}

// historyPrev walks backwards through submitted prompts.
func (c *chat) historyPrev() {
	if len(c.prompts) == 0 {
		return
	}
	if c.histIdx >= len(c.prompts) {
		c.histDraft = c.ta.Value()
		c.histIdx = len(c.prompts)
	}
	if c.histIdx > 0 {
		c.histIdx--
	}
	c.ta.SetValue(c.prompts[c.histIdx])
	c.ta.CursorEnd()
	c.layout()
	c.refresh()
}

// historyNext walks forward; moving past the newest prompt restores the
// unfinished draft.
func (c *chat) historyNext() {
	if len(c.prompts) == 0 {
		return
	}
	if c.histIdx < len(c.prompts)-1 {
		c.histIdx++
		c.ta.SetValue(c.prompts[c.histIdx])
	} else {
		c.histIdx = len(c.prompts)
		c.ta.SetValue(c.histDraft)
	}
	c.ta.CursorEnd()
	c.layout()
	c.refresh()
}

func trimPrompt(s string) string {
	for len(s) > 0 && (s[0] == ' ' || s[0] == '\t' || s[0] == '\n') {
		s = s[1:]
	}
	for len(s) > 0 && (s[len(s)-1] == ' ' || s[len(s)-1] == '\t' || s[len(s)-1] == '\n') {
		s = s[:len(s)-1]
	}
	return s
}
