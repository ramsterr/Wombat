package tui

import (
	"os"
	"time"

	tea "github.com/charmbracelet/bubbletea"

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
	scrProviders
	scrProviderForm
	scrModels
	scrDoctor
	scrUsage
)

type loadErrMsg struct{ err error }

type popMsg struct{}

type openScreenMsg struct{ id screenID }

type pickSessionMsg struct{ id string }

type pickModelMsg struct{}

type pickProviderMsg struct{ name string }

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

	chat      *chat
	sessions  sessionsModel
	providers providersModel
	configure configureModel
	models    modelsModel
	doctor    doctorModel
	usage     usageModel

	action Action
	err    error
}

func Run(opt Options) (Action, error) {
	if opt.Cwd == "" {
		opt.Cwd, _ = os.Getwd()
	}
	if opt.Cfg != nil {
		terminalBackground = opt.Cfg.PaintBackground
	}
	defer resetTerminalBackground()
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
	if ch.ag != nil && ch.ag.Env != nil {
		ch.ag.Env.JobManager().Close()
		ch.ag.Env.CloseLSP()
		ch.ag.Env.CloseBrowser()
	}
	if err != nil {
		return Action{}, err
	}
	fm := final.(*rootModel)
	return fm.action, fm.err
}

func (m *rootModel) Init() tea.Cmd {
	cmds := []tea.Cmd{m.chat.Init()}
	if !m.opt.Mouse {
		// Make sure a previous session that crashed in mouse mode cannot keep
		// capturing the pointer. Selection must work out of the box.
		cmds = append(cmds, tea.DisableMouse)
	}
	return tea.Batch(cmds...)
}

func (m *rootModel) open(id screenID) tea.Cmd {
	switch id {
	case scrSessions:
		m.sessions = newSessions(m.opt)
		m.sessions = m.sessions.resize(m.width, m.height)
		m.screen = scrSessions
		return m.sessions.Init()
	case scrProviders:
		m.providers = newProviders(m.opt)
		m.providers = m.providers.resize(m.width, m.height)
		m.screen = scrProviders
		return m.providers.Init()
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
	if key, ok := msg.(tea.KeyMsg); ok {
		if cmd, handled := m.globalKey(key); handled {
			return m, cmd
		}
	}
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.chat.resize(msg.Width, msg.Height)
		switch m.screen {
		case scrSessions:
			m.sessions = m.sessions.resize(msg.Width, msg.Height)
		case scrProviders:
			m.providers = m.providers.resize(msg.Width, msg.Height)
		case scrProviderForm:
			m.configure = m.configure.resize(msg.Width, msg.Height)
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
	case pickProviderMsg:
		m.configure = newConfigure(m.opt, msg.name)
		m.configure = m.configure.resize(m.width, m.height)
		m.screen = scrProviderForm
		return m, m.configure.Init()
	case openConfigureMsg:
		m.configure = newConfigure(m.opt, msg.name)
		m.configure = m.configure.resize(m.width, m.height)
		m.screen = scrProviderForm
		return m, m.configure.Init()
	case providerConfiguredMsg:
		m.models = newModelsFor(m.opt, msg.name)
		m.models = m.models.resize(m.width, m.height)
		m.screen = scrModels
		return m, m.models.Init()
	case providerFormCancelMsg:
		return m, m.open(scrProviders)
	case pickModelMsg:
		if err := m.chat.rebuild(true); err != nil {
			m.chat.note("error: "+err.Error(), true)
		}
		m.screen = scrChat
		m.chat.maybeOpenReasoningPicker()
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
	case scrProviders:
		m.providers, overlay = m.providers.update(msg)
	case scrProviderForm:
		m.configure, overlay = m.configure.update(msg)
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

// globalKey switches screens from anywhere with alt+1..5. It stays out of
// the way while a prompt or overlay owns the keyboard.
func (m *rootModel) globalKey(key tea.KeyMsg) (tea.Cmd, bool) {
	if m.chat == nil {
		return nil, false
	}
	if m.chat.streaming || m.chat.perm != nil || m.chat.question != nil || m.chat.modal != modalNone {
		return nil, false
	}
	switch key.String() {
	case "alt+1":
		if m.screen == scrChat {
			return nil, false
		}
		m.screen = scrChat
		return m.chat.focusInput(), true
	case "alt+2":
		if m.screen == scrSessions {
			return nil, false
		}
		return m.open(scrSessions), true
	case "alt+3":
		if m.screen == scrModels {
			return nil, false
		}
		return m.open(scrModels), true
	case "alt+4":
		if m.screen == scrUsage {
			return nil, false
		}
		return m.open(scrUsage), true
	case "alt+5":
		if m.screen == scrDoctor {
			return nil, false
		}
		return m.open(scrDoctor), true
	}
	return nil, false
}

func (m *rootModel) View() string {
	switch m.screen {
	case scrSessions:
		return m.sessions.view()
	case scrProviders, scrProviderForm:
		if m.screen == scrProviderForm {
			return m.configure.view()
		}
		return m.providers.view()
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
