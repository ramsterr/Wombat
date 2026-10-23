package tui

import (
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/config"
)

type providerConfiguredMsg struct{ name string }
type providerFormCancelMsg struct{}
type openConfigureMsg struct{ name string }

type configureModel struct {
	opt   Options
	name  string
	kind  string
	base  textinput.Model
	key   textinput.Model
	focus int
	w, h  int
	err   string
}

func newConfigure(opt Options, name string) configureModel {
	pc := opt.Cfg.Providers[name]
	kind := "openai"
	baseURL := ""
	apiKey := ""
	if pc != nil {
		kind = pc.Kind
		baseURL = pc.BaseURL
		apiKey = pc.APIKey
	}
	if preset, ok := config.PresetByName(name); ok {
		if kind == "openai" {
			kind = preset.Kind
		}
		if baseURL == "" {
			baseURL = preset.Base
		}
	}

	base := textinput.New()
	base.Prompt = ""
	base.Placeholder = "https://api.example.com/v1"
	base.SetValue(baseURL)
	base.CharLimit = 400
	base.Width = 60

	key := textinput.New()
	key.Prompt = ""
	key.Placeholder = apiPlaceholder(name)
	key.SetValue(apiKey)
	key.CharLimit = 500
	key.Width = 60

	m := configureModel{opt: opt, name: name, kind: kind, base: base, key: key}
	m.focus = 0
	m.base.Focus()
	return m
}

func apiPlaceholder(name string) string {
	if preset, ok := config.PresetByName(name); ok && preset.Local {
		return "optional for local providers"
	}
	return "paste your API key"
}

func (m configureModel) Init() tea.Cmd { return textinput.Blink }

func (m configureModel) resize(w, h int) configureModel {
	m.w, m.h = w, h
	inner := max(20, min(64, w-12))
	m.base.Width = inner - 2
	m.key.Width = inner - 2
	return m
}

func (m configureModel) isLocal() bool {
	if preset, ok := config.PresetByName(m.name); ok && preset.Local {
		return true
	}
	base := strings.ToLower(m.base.Value())
	return strings.Contains(base, "localhost") || strings.Contains(base, "127.0.0.1")
}

func (m configureModel) update(msg tea.Msg) (configureModel, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.String() {
		case "esc":
			return m, func() tea.Msg { return providerFormCancelMsg{} }
		case "tab", "down":
			m.focus = (m.focus + 1) % 2
			m.syncFocus()
			return m, textinput.Blink
		case "shift+tab", "up":
			m.focus = (m.focus + 1) % 2
			m.syncFocus()
			return m, textinput.Blink
		case "enter", "ctrl+s":
			return m.save()
		}
	}
	var cmd tea.Cmd
	if m.focus == 0 {
		m.base, cmd = m.base.Update(msg)
	} else {
		m.key, cmd = m.key.Update(msg)
	}
	return m, cmd
}

func (m *configureModel) syncFocus() {
	if m.focus == 0 {
		m.base.Focus()
		m.key.Blur()
	} else {
		m.base.Blur()
		m.key.Focus()
	}
}

func (m configureModel) save() (configureModel, tea.Cmd) {
	baseURL := strings.TrimSpace(m.base.Value())
	apiKey := strings.TrimSpace(m.key.Value())
	if baseURL == "" {
		if preset, ok := config.PresetByName(m.name); ok {
			baseURL = preset.Base
		}
	}
	if apiKey == "" && !m.isLocal() {
		m.err = "an API key is required for " + m.name
		return m, nil
	}
	pc := m.opt.Cfg.Providers[m.name]
	if pc == nil {
		pc = &config.Provider{}
		m.opt.Cfg.Providers[m.name] = pc
	}
	if m.kind != "" {
		pc.Kind = m.kind
	}
	pc.BaseURL = baseURL
	pc.APIKey = apiKey
	if err := m.opt.Cfg.Save(); err != nil {
		m.err = err.Error()
		return m, nil
	}
	return m, func() tea.Msg { return providerConfiguredMsg{name: m.name} }
}

func (m configureModel) view() string {
	header := chromeLine(screenTabs(scrProviderForm), keyHint("esc", "back"), m.w) + "\n" + rule(m.w)
	innerW := max(30, min(68, m.w-12))
	label := func(text string, active bool) string {
		if active {
			return styleAccent.Bold(true).Render(text)
		}
		return styleDim.Render(text)
	}
	body := []string{
		styleAccent.Bold(true).Render("CONFIGURE " + strings.ToUpper(m.name)),
		styleFaint.Render("kind  ") + styleDim.Render(m.kind),
		"",
		label("Base URL", m.focus == 0),
		" " + m.base.View(),
		"",
		label("API key", m.focus == 1),
		" " + m.key.View(),
		"",
		styleFaint.Render("tab/↑↓ move · enter save · esc back"),
	}
	if m.err != "" {
		body = append(body, "", styleBad.Render(m.err))
	}
	if m.isLocal() {
		body = append(body, "", styleFaint.Render("local provider — the API key can stay empty"))
	}
	card := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(cAccent).
		Padding(1, 2).
		Width(innerW).
		Render(strings.Join(body, "\n"))
	return header + "\n" + lipgloss.Place(m.w, max(6, m.h-3), lipgloss.Center, lipgloss.Center, card)
}
