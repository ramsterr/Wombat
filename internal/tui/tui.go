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
	Version   string
	Cwd       string
	Cfg       *config.Config
	SessionID string
	Mouse     bool
}

type screenID int

const (
	scrChat screenID = iota
	scrSessions
	scrModels
	scrDoctor
	scrUsage
)

var (
	cInk    = lipgloss.Color("#0A1020")
	cDim    = lipgloss.AdaptiveColor{Light: "240", Dark: "244"}
	cFaint  = lipgloss.AdaptiveColor{Light: "248", Dark: "238"}
	cGreen  = lipgloss.AdaptiveColor{Light: "28", Dark: "78"}
	cRed    = lipgloss.AdaptiveColor{Light: "160", Dark: "203"}
	cWarn   = lipgloss.AdaptiveColor{Light: "166", Dark: "214"}
	cAccent = lipgloss.AdaptiveColor{Light: "26", Dark: "75"}
	cLink   = lipgloss.AdaptiveColor{Light: "25", Dark: "111"}
	cBorder = lipgloss.AdaptiveColor{Light: "110", Dark: "60"}
	cCode   = lipgloss.AdaptiveColor{Light: "25", Dark: "117"}
)

var (
	styleTitle   = lipgloss.NewStyle().Bold(true)
	styleDim     = lipgloss.NewStyle().Foreground(cDim)
	styleFaint   = lipgloss.NewStyle().Foreground(cFaint)
	styleThought = lipgloss.NewStyle().Foreground(cDim).Italic(true)
	styleGood    = lipgloss.NewStyle().Foreground(cGreen)
	styleBad     = lipgloss.NewStyle().Foreground(cRed)
	styleWarn    = lipgloss.NewStyle().Foreground(cWarn)
	styleAccent  = lipgloss.NewStyle().Foreground(cAccent)
	styleLink    = lipgloss.NewStyle().Foreground(cLink)
	styleButton  = lipgloss.NewStyle().Background(cAccent).Foreground(cInk).Bold(true)
	styleKey     = lipgloss.NewStyle().Foreground(cInk).Background(cAccent).Bold(true)
	stylePanel   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cBorder).
			Padding(0, 1)
	stylePanelTitle = lipgloss.NewStyle().Foreground(cAccent).Bold(true)
	styleCode       = lipgloss.NewStyle().Foreground(cCode)
	styleInputBox   = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cAccent).
			Padding(0, 1)
	styleInputIdle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(cBorder).
			Padding(0, 1)
)

type loadErrMsg struct{ err error }

type popMsg struct{}

type openScreenMsg struct{ id screenID }

type pickSessionMsg struct{ id string }

type pickModelMsg struct{}

type exitMsg struct {
	kind string
	id   string
}

type errMsg struct{ err error }

func pop() tea.Msg { return popMsg{} }

func openScreen(id screenID) tea.Cmd {
	return func() tea.Msg { return openScreenMsg{id: id} }
}

func exit(k, id string) tea.Cmd {
	return func() tea.Msg { return exitMsg{kind: k, id: id} }
}

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

	chat     *chat
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
	ch, err := newChat(opt)
	if err != nil {
		return Action{}, err
	}
	m := &rootModel{
		opt:    opt,
		chat:   ch,
		doctor: newDoctor(opt),
		usage:  newUsage(opt),
	}
	opts := []tea.ProgramOption{tea.WithAltScreen()}
	if opt.Mouse {
		opts = append(opts, tea.WithMouseCellMotion())
	}
	p := tea.NewProgram(m, opts...)
	m.chat.send = p.Send
	final, err := p.Run()
	if err != nil {
		return Action{}, err
	}
	fm := final.(*rootModel)
	return fm.action, fm.err
}

func (m *rootModel) Init() tea.Cmd {
	return m.chat.Init()
}

func (m *rootModel) open(id screenID) tea.Cmd {
	switch id {
	case scrSessions:
		m.sessions = newSessions(m.opt)
		m.sessions = m.sessions.resize(m.width, m.height)
		m.screen = scrSessions
		return m.sessions.Init()
	case scrModels:
		m.models = newModels(m.opt)
		m.models = m.models.resize(m.width, m.height)
		m.screen = scrModels
		return m.models.Init()
	case scrDoctor:
		m.doctor = newDoctor(m.opt)
		m.doctor = m.doctor.resize(m.width, m.height)
		m.screen = scrDoctor
		return m.doctor.Init()
	case scrUsage:
		m.usage = newUsage(m.opt)
		m.usage = m.usage.resize(m.width, m.height)
		m.screen = scrUsage
		return m.usage.Init()
	}
	return nil
}

func (m *rootModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.chat.resize(msg.Width, msg.Height)
		switch m.screen {
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
		m.screen = scrChat
		return m, m.chat.focusInput()
	case openScreenMsg:
		return m, m.open(msg.id)
	case pickSessionMsg:
		if err := m.chat.openSession(msg.id); err != nil {
			m.chat.note("error: "+err.Error(), true)
		}
		m.screen = scrChat
		return m, m.chat.focusInput()
	case pickModelMsg:
		if err := m.chat.rebuild(true); err != nil {
			m.chat.note("error: "+err.Error(), true)
		}
		m.screen = scrChat
		return m, m.chat.focusInput()
	case permAskMsg:
		m.screen = scrChat
		return m, m.chat.update(msg)
	case exitMsg:
		m.action = Action{Kind: msg.kind, ID: msg.id}
		return m, tea.Quit
	case errMsg:
		m.err = msg.err
		return m, tea.Quit
	}

	if m.screen == scrChat {
		return m, m.chat.update(msg)
	}

	var overlay tea.Cmd
	switch m.screen {
	case scrSessions:
		m.sessions, overlay = m.sessions.update(msg)
	case scrModels:
		m.models, overlay = m.models.update(msg)
	case scrDoctor:
		m.doctor, overlay = m.doctor.update(msg)
	case scrUsage:
		m.usage, overlay = m.usage.update(msg)
	}
	async := m.chat.updateAsync(msg)
	return m, tea.Batch(overlay, async)
}

func (m *rootModel) View() string {
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
		return m.chat.view()
	}
}
