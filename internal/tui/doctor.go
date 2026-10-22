package tui

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	tea "github.com/charmbracelet/bubbletea"

	"github.com/ramsterr/Wombat/internal/config"
	"github.com/ramsterr/Wombat/internal/llm"
	"github.com/ramsterr/Wombat/internal/session"
)

type checkState struct {
	name    string
	detail  string
	ok      bool
	done    bool
	latency int64
}

type checkResultMsg struct {
	idx     int
	ok      bool
	detail  string
	latency int64
}

type doctorModel struct {
	opt     Options
	w, h    int
	checks  []checkState
	spin    spinner.Model
	running bool
	cur     int
}

func newDoctor(opt Options) doctorModel {
	names := []string{
		"config file",
		"api key",
		"provider reachable",
		"working dir writable",
		"session store",
	}
	checks := make([]checkState, len(names))
	for i, n := range names {
		checks[i] = checkState{name: n}
	}
	return doctorModel{
		opt:    opt,
		checks: checks,
		spin:   spinner.New(spinner.WithSpinner(spinner.Dot)),
	}
}

func (m doctorModel) Init() tea.Cmd {
	m.running = true
	return tea.Batch(m.spin.Tick, runCheck(m.opt, 0))
}

func runCheck(opt Options, idx int) tea.Cmd {
	return func() tea.Msg {
		switch idx {
		case 0:
			if _, err := config.Load(); err != nil {
				return checkResultMsg{idx: idx, ok: false, detail: err.Error()}
			}
			return checkResultMsg{idx: idx, ok: true, detail: config.Path()}
		case 1:
			cfg := opt.Cfg
			pc := cfg.Providers[cfg.Provider]
			if pc == nil {
				if ps, ok := config.PresetByName(cfg.Provider); ok {
					pc = &config.Provider{Kind: ps.Kind, BaseURL: ps.Base}
				} else {
					return checkResultMsg{idx: idx, ok: false, detail: "unknown provider " + cfg.Provider}
				}
			}
			key := cfg.APIKey(cfg.Provider, pc)
			if key == "" && !strings.Contains(pc.BaseURL, "127.0.0.1") && !strings.Contains(pc.BaseURL, "localhost") {
				return checkResultMsg{idx: idx, ok: false, detail: "no API key for " + cfg.Provider}
			}
			if key == "" {
				return checkResultMsg{idx: idx, ok: true, detail: "local provider, no key needed"}
			}
			return checkResultMsg{idx: idx, ok: true, detail: redact(key)}
		case 2:
			cfg := opt.Cfg
			pc := cfg.Providers[cfg.Provider]
			if pc == nil {
				if ps, ok := config.PresetByName(cfg.Provider); ok {
					pc = &config.Provider{Kind: ps.Kind, BaseURL: ps.Base}
				} else {
					return checkResultMsg{idx: idx, ok: false, detail: "unknown provider"}
				}
			}
			p, err := llm.New(cfg.Provider, pc, cfg.APIKey(cfg.Provider, pc))
			if err != nil {
				return checkResultMsg{idx: idx, ok: false, detail: err.Error()}
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			start := time.Now()
			models, err := p.ListModels(ctx)
			ms := time.Since(start).Milliseconds()
			if err != nil {
				return checkResultMsg{idx: idx, ok: false, detail: err.Error(), latency: ms}
			}
			return checkResultMsg{idx: idx, ok: true, detail: fmt.Sprintf("%d models · %s", len(models), hostOf(pc.BaseURL)), latency: ms}
		case 3:
			f, err := os.CreateTemp(opt.Cwd, ".wombat-check-*")
			if err != nil {
				return checkResultMsg{idx: idx, ok: false, detail: err.Error()}
			}
			name := f.Name()
			f.Close()
			os.Remove(name)
			return checkResultMsg{idx: idx, ok: true, detail: opt.Cwd}
		case 4:
			metas, err := session.List(opt.Cwd)
			if err != nil {
				return checkResultMsg{idx: idx, ok: false, detail: err.Error()}
			}
			dir := filepath.Join(config.Home(), "sessions")
			if err := os.MkdirAll(dir, 0o700); err != nil {
				return checkResultMsg{idx: idx, ok: false, detail: err.Error()}
			}
			return checkResultMsg{idx: idx, ok: true, detail: fmt.Sprintf("%d sessions here", len(metas))}
		}
		return checkResultMsg{idx: idx, ok: true, detail: ""}
	}
}

func hostOf(base string) string {
	s := strings.TrimPrefix(base, "https://")
	s = strings.TrimPrefix(s, "http://")
	if i := strings.IndexByte(s, '/'); i >= 0 {
		s = s[:i]
	}
	return s
}

func redact(key string) string {
	if len(key) <= 10 {
		return "••••"
	}
	return key[:7] + "…" + key[len(key)-4:]
}

func (m doctorModel) resize(w, h int) doctorModel {
	m.w, m.h = w, h
	return m
}

func (m doctorModel) update(msg tea.Msg) (doctorModel, tea.Cmd) {
	switch msg := msg.(type) {
	case checkResultMsg:
		if msg.idx < len(m.checks) {
			m.checks[msg.idx] = checkState{
				name:    m.checks[msg.idx].name,
				detail:  msg.detail,
				ok:      msg.ok,
				done:    true,
				latency: msg.latency,
			}
		}
		next := msg.idx + 1
		if next < len(m.checks) {
			m.cur = next
			return m, runCheck(m.opt, next)
		}
		m.running = false
		return m, nil
	case spinner.TickMsg:
		if !m.running {
			return m, nil
		}
		var cmd tea.Cmd
		m.spin, cmd = m.spin.Update(msg)
		return m, cmd
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "enter":
			return m, pop
		case "r":
			m.running = true
			m.cur = 0
			for i := range m.checks {
				m.checks[i] = checkState{name: m.checks[i].name}
			}
			return m, tea.Batch(m.spin.Tick, runCheck(m.opt, 0))
		}
	}
	return m, nil
}

func (m doctorModel) view() string {
	header := chromeLine(screenTabs(scrDoctor), keyHint("esc", "back")+"  "+keyHint("r", "re-run"), m.w) + "\n" + rule(m.w)
	var lines []string
	for i, c := range m.checks {
		var mark, detail string
		switch {
		case c.done && c.ok:
			mark = styleGood.Render("✓")
		case c.done && !c.ok:
			mark = styleBad.Render("✗")
		default:
			if i == m.cur && m.running {
				mark = m.spin.View()
			} else {
				mark = styleDim.Render("·")
			}
		}
		detail = styleDim.Render(clip(c.detail, m.w-32))
		lat := ""
		if c.latency > 0 {
			lat = " " + styleAccent.Render(fmtDur(c.latency)) + " " + styleDim.Render(latBar(c.latency))
		}
		lines = append(lines, fmt.Sprintf("  %s %-22s %s%s", mark, styleTitle.Render(c.name), detail, lat))
	}
	tail := ""
	if !m.running {
		bad := 0
		for _, c := range m.checks {
			if c.done && !c.ok {
				bad++
			}
		}
		if bad == 0 {
			tail = "\n  " + styleGood.Render("all checks passed")
		} else {
			tail = "\n  " + styleBad.Render(fmt.Sprintf("%d check(s) failed", bad)) +
				" " + styleDim.Render("— fix the above, then press r")
		}
	}
	return header + "\n\n" + strings.Join(lines, "\n") + tail
}

func fmtDur(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.2fs", float64(ms)/1000)
	default:
		return fmt.Sprintf("%.1fm", float64(ms)/60_000)
	}
}

func latBar(ms int64) string {
	cells := int(ms / 100)
	if cells > 20 {
		cells = 20
	}
	return strings.Repeat("▁", cells) + styleDim.Render(strings.Repeat("▁", 20-cells))
}
