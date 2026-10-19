package tui

import (
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"

	"github.com/ramsterr/Wombat/internal/config"
)

type Action struct {
	Kind string
	ID   string
}

type Options struct {
	Version string
	Cwd     string
	Cfg     *config.Config
}

type screenID int

const (
	scrHome screenID = iota
	scrSessions
	scrModels
	scrDoctor
	scrUsage
)

var (
	cYellow = lipgloss.Color("#FFC81E")
	cInk    = lipgloss.Color("#0B0B0B")
	cDim    = lipgloss.AdaptiveColor{Light: "242", Dark: "245"}
	cGreen  = lipgloss.AdaptiveColor{Light: "28", Dark: "42"}
	cRed    = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	cAccent = lipgloss.AdaptiveColor{Light: "130", Dark: "220"}
)

var (
	styleTitle  = lipgloss.NewStyle().Bold(true)
	styleDim    = lipgloss.NewStyle().Foreground(cDim)
	styleGood   = lipgloss.NewStyle().Foreground(cGreen)
	styleBad    = lipgloss.NewStyle().Foreground(cRed)
	styleAccent = lipgloss.NewStyle().Foreground(cAccent)
	styleButton = lipgloss.NewStyle().Background(cYellow).Foreground(cInk).Bold(true)
	stylePanel  = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cDim).
			Padding(0, 1)
	stylePanelTitle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
)

type loadErrMsg struct{ err error }

func nowAgo(t time.Time) string {
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return itoa(int(d.Minutes())) + "m ago"
	case d < 24*time.Hour:
		return itoa(int(d.Hours())) + "h ago"
	default:
		return itoa(int(d.Hours()/24)) + "d ago"
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}

func humanN(n int) string {
	switch {
	case n >= 1_000_000:
		return itoa(n/100_000) + "." + itoa(n/10_000%10) + "M"
	case n >= 10_000:
		return itoa(n/1000) + "k"
	case n >= 1000:
		return itoa(n/1000) + "." + itoa(n/100%10) + "k"
	default:
		return itoa(n)
	}
}

func clip(s string, n int) string {
	if n <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}
	if n <= 1 {
		return string(runes[:n])
	}
	return string(runes[:n-1]) + "…"
}

type rootModel struct {
	opt    Options
	screen screenID
	width  int
	height int

	home     homeModel
	sessions sessionsModel
	models   modelsModel
	doctor   doctorModel
	usage    usageModel

	action Action
	err    error
}

func Run(opt Options) (Action, error) {
	if opt.Cwd == "" {
		opt.Cwd, _ = os.Getwd()
	}
	m := rootModel{
		opt:    opt,
		home:   newHome(opt),
		doctor: newDoctor(opt),
		usage:  newUsage(opt),
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	final, err := p.Run()
	if err != nil {
		return Action{}, err
	}
	fm := final.(rootModel)
	return fm.action, fm.err
}

func (m rootModel) Init() tea.Cmd {
	return tea.Batch(m.home.Init())
}

func (m rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		switch m.screen {
		case scrHome:
			m.home = m.home.resize(msg.Width, msg.Height)
		case scrSessions:
			m.sessions = m.sessions.resize(msg.Width, msg.Height)
		case scrModels:
			m.models = m.models.resize(msg.Width, msg.Height)
		case scrDoctor:
			m.doctor = m.doctor.resize(msg.Width, msg.Height)
		case scrUsage:
			m.usage = m.usage.resize(msg.Width, msg.Height)
		}
		return m, nil
	case popMsg:
		m.screen = scrHome
		return m, nil
	case exitMsg:
		m.action = Action{Kind: msg.kind, ID: msg.id}
		return m, tea.Quit
	case errMsg:
		m.err = msg.err
		return m, tea.Quit
	}

	var cmd tea.Cmd
	switch m.screen {
	case scrHome:
		var out homeOut
		m.home, out, cmd = m.home.update(msg)
		if out.open != "" {
			switch out.open {
			case "sessions":
				m.sessions = newSessions(m.opt)
				m.sessions = m.sessions.resize(m.width, m.height)
				m.screen = scrSessions
				cmd = m.sessions.Init()
			case "models":
				m.models = newModels(m.opt)
				m.models = m.models.resize(m.width, m.height)
				m.screen = scrModels
				cmd = m.models.Init()
			case "doctor":
				m.doctor = newDoctor(m.opt)
				m.doctor = m.doctor.resize(m.width, m.height)
				m.screen = scrDoctor
				cmd = m.doctor.Init()
			case "usage":
				m.usage = newUsage(m.opt)
				m.usage = m.usage.resize(m.width, m.height)
				m.screen = scrUsage
				cmd = m.usage.Init()
			}
		}
	case scrSessions:
		m.sessions, cmd = m.sessions.update(msg)
	case scrModels:
		m.models, cmd = m.models.update(msg)
	case scrDoctor:
		m.doctor, cmd = m.doctor.update(msg)
	case scrUsage:
		m.usage, cmd = m.usage.update(msg)
	}
	return m, cmd
}

func (m rootModel) View() string {
	switch m.screen {
	case scrSessions:
		return m.sessions.view()
	case scrModels:
		return m.models.view()
	case scrDoctor:
		return m.doctor.view()
	case scrUsage:
		return m.usage.view()
	default:
		return m.home.view()
	}
}

type popMsg struct{}

type exitMsg struct {
	kind string
	id   string
}

type errMsg struct{ err error }

func pop() tea.Msg { return popMsg{} }
func exit(k, id string) tea.Cmd {
	return func() tea.Msg { return exitMsg{kind: k, id: id} }
}
