package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/session"
)

type sessionItem struct {
	meta session.Meta
}

func (s sessionItem) Title() string {
	if s.meta.Title != "" {
		return clip(s.meta.Title, 60)
	}
	return styleDim.Render("untitled session")
}

func (s sessionItem) Description() string {
	parts := []string{whenLabel(s.meta.Updated)}
	if s.meta.Turns == 1 {
		parts = append(parts, "1 turn")
	} else if s.meta.Turns > 1 {
		parts = append(parts, itoa(s.meta.Turns)+" turns")
	}
	if s.meta.Model != "" {
		parts = append(parts, s.meta.Model)
	}
	return strings.Join(parts, " · ")
}

func (s sessionItem) FilterValue() string {
	return s.meta.Title + " " + s.meta.ID + " " + s.meta.Model
}

func whenLabel(t time.Time) string {
	if t.IsZero() {
		return "unknown"
	}
	now := time.Now()
	midnight := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
	then := time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, now.Location())
	days := int(midnight.Sub(then).Hours() / 24)
	clock := t.Format("15:04")
	switch {
	case days == 0:
		return "today " + clock
	case days == 1:
		return "yesterday " + clock
	case days > 1 && days < 7:
		return t.Format("Mon") + " " + clock
	default:
		return t.Format("Jan 2") + " " + clock
	}
}

type sessionsModel struct {
	opt     Options
	w, h    int
	list    list.Model
	preview viewport.Model
	spin    spinner.Model
	loading bool
	curID   string
	empty   bool
}

type previewMsg struct {
	id   string
	text string
}

func newSessions(opt Options) sessionsModel {
	metas, _ := session.List(opt.Cwd)
	items := make([]list.Item, 0, len(metas))
	for _, mt := range metas {
		items = append(items, sessionItem{meta: mt})
	}
	d := list.NewDefaultDelegate()
	d.ShowDescription = true
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.
		Background(cAccent).Foreground(cPanel).Bold(true).BorderLeftForeground(cAccent)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.
		Background(cAccent).Foreground(cPanel).BorderLeftForeground(cAccent)
	sp := spinner.New(spinner.WithSpinner(spinner.Dot))
	l := list.New(items, d, 40, 20)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(len(items) > 0)
	l.KeyMap.Quit = key.NewBinding()
	m := sessionsModel{
		opt:   opt,
		list:  l,
		spin:  sp,
		empty: len(items) == 0,
	}
	m.preview = viewport.New(40, 20)
	return m
}

func (m sessionsModel) Init() tea.Cmd {
	if m.empty {
		return nil
	}
	id := m.list.Items()[0].(sessionItem).meta.ID
	m.loading = true
	return tea.Batch(m.spin.Tick, loadPreview(m.opt.Cwd, id))
}

func loadPreview(cwd, id string) tea.Cmd {
	return func() tea.Msg {
		return previewMsg{id: id, text: renderPreview(cwd, id)}
	}
}

func renderPreview(cwd, id string) string {
	s, err := session.Open(id, cwd)
	if err != nil {
		return styleBad.Render("cannot open session: " + err.Error())
	}
	if len(s.Messages) == 0 {
		return styleDim.Render("(empty session)")
	}
	var lines []string
	for i, msg := range s.Messages {
		switch msg.Role {
		case "user":
			if i == 0 && strings.HasPrefix(msg.Content, "Today: ") {
				continue
			}
			if strings.HasPrefix(msg.Content, "[Conversation summary]") {
				lines = append(lines, "", styleDim.Render("(context summary)"))
				continue
			}
			lines = append(lines, "", styleAccent.Render("you"))
			lines = append(lines, wrap(msg.Content, 68)...)
			for _, a := range msg.Attachments {
				lines = append(lines, styleDim.Render("  ↳ "+a.Kind+" · "+a.Name))
			}
		case "assistant":
			lines = append(lines, "", styleTitle.Render("wombat"))
			if msg.Content != "" {
				lines = append(lines, wrap(msg.Content, 68)...)
			}
			for _, tc := range msg.ToolCalls {
				lines = append(lines, styleDim.Render("⚒ "+tc.Name+"("+clip(oneline(tc.Arguments), 60)+")"))
			}
		case "tool":
			out := oneline(msg.Content)
			if out == "" {
				out = "(no output)"
			}
			lines = append(lines, styleDim.Render("  → "+clip(out, 66)))
		}
	}
	if len(lines) > 240 {
		lines = lines[len(lines)-240:]
	}
	return strings.Join(lines, "\n")
}

func (m sessionsModel) resize(w, h int) sessionsModel {
	m.w, m.h = w, h
	lw := m.listWidth()
	lh := h - 3
	if lh < 5 {
		lh = 5
	}
	m.list.SetSize(lw-2, lh-2)
	m.preview = viewport.New(w-lw-4, lh-4)
	if !m.empty {
		m.preview.SetContent(m.previewContent())
		m.preview.GotoBottom()
	}
	return m
}

func (m sessionsModel) previewContent() string {
	sel, ok := m.list.SelectedItem().(sessionItem)
	if !ok {
		return ""
	}
	return renderPreview(m.opt.Cwd, sel.meta.ID)
}

func (m sessionsModel) listWidth() int {
	lw := m.w * 45 / 100
	if lw < 34 {
		lw = 34
	}
	if lw > 60 {
		lw = 60
	}
	if lw > m.w-30 {
		lw = m.w - 30
	}
	return lw
}

func (m sessionsModel) update(msg tea.Msg) (sessionsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case previewMsg:
		if msg.id == m.currentID() {
			m.loading = false
			m.preview.SetContent(msg.text)
			m.preview.GotoBottom()
		}
		return m, nil
	case spinner.TickMsg:
		if !m.loading {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		filtering := m.list.FilterState() == list.Filtering
		if !filtering {
			switch msg.String() {
			case "esc", "q", "ctrl+c":
				return m, pop
			case "enter":
				if m.empty {
					return m, nil
				}
				sel := m.list.SelectedItem().(sessionItem)
				return m, func() tea.Msg { return pickSessionMsg{id: sel.meta.ID} }
			}
		}
		before := m.currentID()
		var cmd tea.Cmd
		m.list, cmd = m.list.Update(msg)
		m.preview = viewportSync(m.preview, msg)
		if after := m.currentID(); after != before {
			m.loading = true
			return m, tea.Batch(cmd, m.spin.Tick, loadPreview(m.opt.Cwd, after))
		}
		return m, cmd
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	m.preview, _ = m.preview.Update(msg)
	return m, cmd
}

func viewportSync(v viewport.Model, msg tea.Msg) viewport.Model {
	v, _ = v.Update(msg)
	return v
}

func (m sessionsModel) currentID() string {
	if m.empty {
		return ""
	}
	if it, ok := m.list.SelectedItem().(sessionItem); ok {
		return it.meta.ID
	}
	return ""
}

func (m sessionsModel) view() string {
	header := chromeLine(screenTabs(scrSessions), keyHint("esc", "back")+"  "+keyHint("enter", "resume")+"  "+keyHint("/", "filter"), m.w) + "\n" + rule(m.w)
	if m.empty {
		body := lipgloss.Place(m.w, m.h-2, lipgloss.Center, lipgloss.Center,
			styleDim.Render("no sessions in this directory yet — start a chat first"))
		return header + "\n" + body
	}
	lw := m.listWidth()
	listView := m.list.View()
	title := "preview"
	if m.loading {
		title = m.spin.View() + " loading"
	} else if it, ok := m.list.SelectedItem().(sessionItem); ok {
		if it.meta.Title != "" {
			title = clip(it.meta.Title, max(10, m.w-lw-8))
		} else {
			title = "untitled session"
		}
	}
	previewPanel := lipgloss.JoinVertical(lipgloss.Left,
		" "+stylePanelTitle.Render(title),
		stylePanel.Width(m.w-lw-4).Height(m.h-5).Render(m.preview.View()),
	)
	body := lipgloss.JoinHorizontal(lipgloss.Top, listView, "  ", previewPanel)
	return header + "\n" + body
}

func oneline(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\t", " ")
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return strings.TrimSpace(s)
}

func wrap(s string, width int) []string {
	var out []string
	for _, para := range strings.Split(s, "\n") {
		words := strings.Fields(para)
		if len(words) == 0 {
			out = append(out, "")
			continue
		}
		line := ""
		for _, wd := range words {
			if line == "" {
				line = wd
			} else if len(line)+1+len(wd) <= width {
				line += " " + wd
			} else {
				out = append(out, line)
				line = wd
			}
		}
		out = append(out, line)
	}
	return out
}
