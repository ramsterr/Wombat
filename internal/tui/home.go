package tui

import (
	"os"
	"path/filepath"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/session"
)

type homeItem struct {
	key   string
	label string
	hint  string
}

type homeOut struct {
	open string
}

type homeModel struct {
	opt     Options
	width   int
	height  int
	cursor  int
	info    sessionInfoMsg
	hasInfo bool
}

type sessionInfoMsg struct {
	count   int
	last    session.Meta
	hasLast bool
}

func newHome(opt Options) homeModel {
	return homeModel{opt: opt}
}

func loadSessionInfo(cwd string) tea.Cmd {
	return func() tea.Msg {
		metas, err := session.List(cwd)
		if err != nil || len(metas) == 0 {
			return sessionInfoMsg{}
		}
		return sessionInfoMsg{count: len(metas), last: metas[0], hasLast: true}
	}
}

func (m homeModel) Init() tea.Cmd {
	return loadSessionInfo(m.opt.Cwd)
}

func (m homeModel) resize(w, h int) homeModel {
	m.width, m.height = w, h
	return m
}

func (m homeModel) items() []homeItem {
	model := m.opt.Cfg.ActiveModel()
	chatHint := "start a new session"
	if model != "" {
		chatHint = "with " + m.opt.Cfg.Provider + "/" + model
	}
	items := []homeItem{{key: "chat", label: "Chat", hint: chatHint}}
	if m.hasInfo && m.info.hasLast {
		title := m.info.last.Title
		if title == "" {
			title = m.info.last.ID
		}
		items = append(items, homeItem{key: "resume", label: "Resume last session", hint: clip(title, 34)})
	}
	items = append(items,
		homeItem{key: "sessions", label: "Sessions", hint: "browse and resume"},
		homeItem{key: "models", label: "Models", hint: "browse and switch"},
		homeItem{key: "usage", label: "Usage", hint: "tokens and activity"},
		homeItem{key: "doctor", label: "Doctor", hint: "health checks"},
		homeItem{key: "configure", label: "Configure", hint: "provider, key, model"},
		homeItem{key: "quit", label: "Quit", hint: ""},
	)
	return items
}

func (m homeModel) update(msg tea.Msg) (homeModel, homeOut, tea.Cmd) {
	switch msg := msg.(type) {
	case sessionInfoMsg:
		m.info, m.hasInfo = msg, true
		return m, homeOut{}, nil
	case popMsg:
		return m, homeOut{}, loadSessionInfo(m.opt.Cwd)
	case tea.KeyMsg:
		items := m.items()
		switch msg.String() {
		case "up", "k":
			m.cursor = (m.cursor + len(items) - 1) % len(items)
		case "down", "j":
			m.cursor = (m.cursor + 1) % len(items)
		case "q", "ctrl+c":
			return m, homeOut{}, exit("quit", "")
		case "c":
			return m, homeOut{}, exit("chat", "")
		case "r":
			if m.hasInfo && m.info.hasLast {
				return m, homeOut{}, exit("resume", "last")
			}
		case "s":
			return m, homeOut{open: "sessions"}, nil
		case "m":
			return m, homeOut{open: "models"}, nil
		case "u":
			return m, homeOut{open: "usage"}, nil
		case "d":
			return m, homeOut{open: "doctor"}, nil
		case "p":
			return m, homeOut{}, exit("configure", "")
		case "enter":
			it := items[m.cursor]
			switch it.key {
			case "quit":
				return m, homeOut{}, exit("quit", "")
			case "configure":
				return m, homeOut{}, exit("configure", "")
			case "resume":
				return m, homeOut{}, exit("resume", "last")
			case "chat":
				return m, homeOut{}, exit("chat", "")
			default:
				return m, homeOut{open: it.key}, nil
			}
		}
	}
	return m, homeOut{}, nil
}

func (m homeModel) view() string {
	if m.width == 0 || m.height == 0 {
		return ""
	}
	art := m.artBlock()
	right := m.rightColumn()
	if art == "" {
		return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, right)
	}
	block := lipgloss.JoinHorizontal(lipgloss.Top, art, "  ", right)
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, block)
}

func (m homeModel) usePlainArt() bool {
	return os.Getenv("NO_COLOR") != "" || os.Getenv("TERM") == "dumb"
}

func (m homeModel) artBlock() string {
	var rows []string
	useLarge := m.width >= 104 && m.height >= 24
	if m.usePlainArt() {
		if useLarge {
			rows = artPlainLarge
		} else if m.width >= 76 {
			rows = artPlainSmall
		}
	} else {
		if useLarge {
			rows = artLarge
		} else if m.width >= 76 && m.height >= 20 {
			rows = artSmall
		}
	}
	if rows == nil {
		return ""
	}
	return strings.Join(rows, "\n")
}

func (m homeModel) rightColumn() string {
	cfg := m.opt.Cfg
	label := lipgloss.NewStyle().Foreground(cDim).Width(10)

	statusLines := []string{
		label.Render("provider") + boldVal(cfg.Provider),
		label.Render("model") + boldVal(orDash(cfg.ActiveModel())),
	}
	cwd := m.opt.Cwd
	if home, err := os.UserHomeDir(); err == nil {
		if rel, err := filepath.Rel(home, cwd); err == nil && !strings.HasPrefix(rel, "..") {
			cwd = "~/" + rel
		}
	}
	statusLines = append(statusLines, label.Render("dir")+styleDim.Render(clip(cwd, 34)))
	if m.hasInfo {
		statusLines = append(statusLines, label.Render("sessions")+boldVal(itoa(m.info.count)))
		if m.info.hasLast {
			frac := m.info.last.Updated.Format("01-02 15:04")
			title := m.info.last.Title
			if title == "" {
				title = m.info.last.ID
			}
			statusLines = append(statusLines, label.Render("last")+styleDim.Render(frac+" ")+clip(title, 22))
		}
	}
	status := panel("status", strings.Join(statusLines, "\n"))

	var menuLines []string
	for i, it := range m.items() {
		hint := ""
		if it.hint != "" {
			hint = "  " + styleDim.Render(it.hint)
		}
		if i == m.cursor {
			menuLines = append(menuLines, styleButton.Render(" ▸ "+it.label+" ")+hint)
		} else {
			menuLines = append(menuLines, "   "+styleTitle.Render(it.label)+hint)
		}
	}
	menu := panel("menu", strings.Join(menuLines, "\n"))

	hint := styleDim.Render("↑/↓ move · enter open · q quit")
	right := lipgloss.JoinVertical(lipgloss.Left, status, menu, "  "+hint)
	if m.width >= 104 {
		right = lipgloss.NewStyle().Width(58).Render(right)
	}
	return right
}

func boldVal(s string) string { return styleTitle.Render(s) }

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func panel(title, content string) string {
	t := stylePanelTitle.Render(title)
	return lipgloss.JoinVertical(lipgloss.Left,
		"  "+t,
		stylePanel.Render(content),
	)
}
