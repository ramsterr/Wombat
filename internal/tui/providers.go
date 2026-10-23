package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/config"
)

type providerItem struct {
	name       string
	kind       string
	base       string
	configured bool
	hasKey     bool
}

func (p providerItem) Title() string {
	title := p.name
	if p.configured {
		title += "  " + styleGood.Render("configured")
	}
	return title
}

func (p providerItem) Description() string {
	parts := []string{p.kind, hostOf(p.base)}
	if p.hasKey {
		parts = append(parts, "key set")
	} else {
		parts = append(parts, "no key")
	}
	return strings.Join(parts, " · ")
}

func (p providerItem) FilterValue() string { return p.name + " " + p.kind }

type providersModel struct {
	opt  Options
	w, h int
	list list.Model
}

func newProviders(opt Options) providersModel {
	seen := map[string]bool{}
	var items []list.Item
	current := opt.Cfg.Provider
	currentIndex := 0
	add := func(name string, pc *config.Provider) {
		if name == "" || seen[name] {
			return
		}
		seen[name] = true
		item := providerItem{name: name, kind: "openai", base: "", configured: name == current}
		if pc != nil {
			item.kind = pc.Kind
			item.base = pc.BaseURL
			item.hasKey = opt.Cfg.APIKey(name, pc) != ""
		}
		if item.hasKey {
			// configured true is only about the active provider; key state is
			// reported separately.
		}
		if name == current {
			currentIndex = len(items)
		}
		items = append(items, item)
	}
	for _, preset := range config.Presets {
		add(preset.Name, opt.Cfg.Providers[preset.Name])
	}
	for name, pc := range opt.Cfg.Providers {
		add(name, pc)
	}

	d := list.NewDefaultDelegate()
	d.ShowDescription = true
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.
		Background(cAccent).Foreground(cPanel).Bold(true).BorderLeftForeground(cAccent)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.
		Background(cAccent).Foreground(cPanel).BorderLeftForeground(cAccent)
	l := list.New(items, d, 40, 20)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(len(items) > 0)
	l.KeyMap.Quit = key.NewBinding()
	l.Select(currentIndex)
	return providersModel{opt: opt, list: l}
}

func (m providersModel) Init() tea.Cmd { return nil }

func (m providersModel) resize(w, h int) providersModel {
	m.w, m.h = w, h
	m.list.SetSize(max(30, w-4), max(5, h-5))
	return m
}

func (m providersModel) update(msg tea.Msg) (providersModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		filtering := m.list.FilterState() == list.Filtering
		if !filtering {
			switch msg.String() {
			case "esc", "q", "ctrl+c":
				return m, pop
			case "enter":
				if it, ok := m.list.SelectedItem().(providerItem); ok {
					return m, func() tea.Msg { return pickProviderMsg{name: it.name} }
				}
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m providersModel) view() string {
	header := chromeLine(screenTabs(scrProviders),
		keyHint("esc", "back")+"  "+keyHint("enter", "choose")+"  "+keyHint("/", "filter"), m.w) + "\n" + rule(m.w)
	body := lipgloss.NewStyle().Padding(1, 1).Render(m.list.View())
	return header + "\n" + body
}
