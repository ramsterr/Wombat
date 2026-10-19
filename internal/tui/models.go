package tui

import (
	"context"
	"sort"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/tokens"
)

type modelItem struct {
	name    string
	current bool
}

func (m modelItem) Title() string {
	if m.current {
		return m.name + "  " + styleGood.Render("current")
	}
	return m.name
}

func (m modelItem) Description() string {
	w := tokens.Window(m.name)
	if w <= 0 {
		return "context —"
	}
	return "context " + humanN(w)
}

func (m modelItem) FilterValue() string { return m.name }

type modelsModel struct {
	opt       Options
	w, h      int
	providers []string
	pi        int
	list      list.Model
	spin      spinner.Model
	loading   bool
	loadErr   string
}

type modelListMsg struct {
	pi     int
	models []string
	err    error
	ms     int64
}

func newModels(opt Options) modelsModel {
	var providers []string
	for _, p := range config.Presets {
		providers = append(providers, p.Name)
	}
	for name := range opt.Cfg.Providers {
		if _, ok := config.PresetByName(name); !ok && name != "" {
			providers = append(providers, name)
		}
	}
	pi := 0
	for i, p := range providers {
		if p == opt.Cfg.Provider {
			pi = i
		}
	}
	sp := spinner.New(spinner.WithSpinner(spinner.Dot), spinner.WithStyle(styleAccent))
	d := list.NewDefaultDelegate()
	d.ShowDescription = true
	d.Styles.SelectedTitle = d.Styles.SelectedTitle.
		Background(cAccent).Foreground(cInk).Bold(true).BorderLeftForeground(cAccent)
	d.Styles.SelectedDesc = d.Styles.SelectedDesc.
		Background(cAccent).Foreground(cInk).BorderLeftForeground(cAccent)
	l := list.New(nil, d, 40, 20)
	l.SetShowTitle(false)
	l.SetShowHelp(false)
	l.SetShowStatusBar(false)
	l.KeyMap.Quit = key.NewBinding()
	return modelsModel{opt: opt, providers: providers, pi: pi, list: l, spin: sp, loading: true}
}

func (m modelsModel) provider() string {
	if len(m.providers) == 0 {
		return m.opt.Cfg.Provider
	}
	return m.providers[m.pi]
}

func (m modelsModel) providerConfig(name string) *config.Provider {
	if pc, ok := m.opt.Cfg.Providers[name]; ok && pc != nil {
		return pc
	}
	if ps, ok := config.PresetByName(name); ok {
		return &config.Provider{Kind: ps.Kind, BaseURL: ps.Base}
	}
	return &config.Provider{Kind: "openai"}
}

func fetchModels(cfg *config.Config, name string) tea.Cmd {
	return func() tea.Msg {
		pc := cfg.Providers[name]
		if pc == nil {
			if ps, ok := config.PresetByName(name); ok {
				pc = &config.Provider{Kind: ps.Kind, BaseURL: ps.Base}
			} else {
				return modelListMsg{err: errUnknownProvider(name)}
			}
		}
		key := cfg.APIKey(name, pc)
		p, err := llm.New(name, pc, key)
		if err != nil {
			return modelListMsg{err: err}
		}
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		start := time.Now()
		models, err := p.ListModels(ctx)
		ms := time.Since(start).Milliseconds()
		sort.Strings(models)
		return modelListMsg{models: models, err: err, ms: ms}
	}
}

func (m modelsModel) Init() tea.Cmd {
	return tea.Batch(m.spin.Tick, fetchModels(m.opt.Cfg, m.provider()))
}

func (m modelsModel) resize(w, h int) modelsModel {
	m.w, m.h = w, h
	lh := h - 5
	if lh < 5 {
		lh = 5
	}
	m.list.SetSize(lw2(w), lh)
	return m
}

func lw2(w int) int {
	lw := w * 60 / 100
	if lw < 34 {
		lw = 34
	}
	if lw > 72 {
		lw = 72
	}
	return lw
}

func errUnknownProvider(n string) error { return &unknownProviderErr{n} }

type unknownProviderErr struct{ name string }

func (e *unknownProviderErr) Error() string { return "unknown provider: " + e.name }

func (m modelsModel) update(msg tea.Msg) (modelsModel, tea.Cmd) {
	switch msg := msg.(type) {
	case modelListMsg:
		m.loading = false
		m.loadErr = ""
		if msg.err != nil {
			m.loadErr = msg.err.Error()
			return m, nil
		}
		items := make([]list.Item, 0, len(msg.models))
		for _, name := range msg.models {
			items = append(items, modelItem{name: name, current: name == m.opt.Cfg.ActiveModel() && m.provider() == m.opt.Cfg.Provider})
		}
		m.list.SetItems(items)
		m.list.ResetSelected()
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
			case "esc", "q":
				return m, pop
			case "tab", "p":
				if len(m.providers) > 1 {
					m.pi = (m.pi + 1) % len(m.providers)
					m.loading = true
					m.loadErr = ""
					m.list.SetItems(nil)
					return m, tea.Batch(m.spin.Tick, fetchModels(m.opt.Cfg, m.provider()))
				}
			case "r":
				m.loading = true
				m.loadErr = ""
				return m, tea.Batch(m.spin.Tick, fetchModels(m.opt.Cfg, m.provider()))
			case "enter":
				if m.loading || m.loadErr != "" {
					return m, nil
				}
				it, ok := m.list.SelectedItem().(modelItem)
				if !ok {
					return m, nil
				}
				m.opt.Cfg.Provider = m.provider()
				m.opt.Cfg.Model = it.name
				_ = m.opt.Cfg.Save()
				return m, func() tea.Msg { return pickModelMsg{} }
			}
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m modelsModel) view() string {
	header := " " + styleTitle.Render("models") + "  " +
		styleDim.Render("enter select · p switch provider · / filter · esc back")
	var tabs []string
	start, end := 0, len(m.providers)
	if max := 9; end-start > max {
		start = m.pi - max/2
		if start < 0 {
			start = 0
		}
		end = start + max
		if end > len(m.providers) {
			end = len(m.providers)
			start = end - max
		}
	}
	if start > 0 {
		tabs = append(tabs, styleDim.Render("…"))
	}
	for i := start; i < end; i++ {
		if i == m.pi {
			tabs = append(tabs, styleButton.Render(" "+m.providers[i]+" "))
		} else {
			tabs = append(tabs, styleDim.Render(" "+m.providers[i]+" "))
		}
	}
	if end < len(m.providers) {
		tabs = append(tabs, styleDim.Render("…"))
	}
	tabLine := " " + strings.Join(tabs, " ")

	var body string
	switch {
	case m.loading:
		body = "\n  " + m.spin.View() + styleDim.Render(" fetching models from "+m.provider()+"…")
	case m.loadErr != "":
		body = "\n  " + styleBad.Render("✗ "+clip(m.loadErr, m.w-8)) +
			"\n  " + styleDim.Render("check the provider config and API key (wombat setup)")
	default:
		body = "\n" + m.list.View()
	}
	return header + "\n" + tabLine + "\n" + body
}
