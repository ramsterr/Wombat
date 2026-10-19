package tui

import (
	"fmt"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/ramsterr/Wombat/internal/session"
	"github.com/ramsterr/Wombat/internal/tokens"
)

type usageStats struct {
	sessions  int
	messages  int
	tokensEst int
	tools     map[string]int
	byModel   map[string]int
	byDay     []dayCount
	maxDay    int
	first     time.Time
	last      time.Time
}

type dayCount struct {
	day   time.Time
	count int
}

type usageStatsMsg struct{ stats usageStats }

type usageModel struct {
	opt   Options
	w, h  int
	stats usageStats
	ready bool
}

func newUsage(opt Options) usageModel { return usageModel{opt: opt} }

func (m usageModel) Init() tea.Cmd {
	cwd := m.opt.Cwd
	return func() tea.Msg {
		metas, _ := session.List(cwd)
		st := usageStats{
			tools:   map[string]int{},
			byModel: map[string]int{},
		}
		dayMap := map[string]int{}
		for _, mt := range metas {
			s, err := session.Open(mt.ID, cwd)
			if err != nil {
				continue
			}
			st.sessions++
			st.messages += len(s.Messages)
			st.byModel[mt.Provider+"/"+mt.Model]++
			if st.first.IsZero() || mt.Created.Before(st.first) {
				st.first = mt.Created
			}
			if mt.Updated.After(st.last) {
				st.last = mt.Updated
			}
			dayMap[mt.Updated.Format("2006-01-02")]++
			for _, msg := range s.Messages {
				st.tokensEst += tokens.Estimate(msg.Content)
				for _, tc := range msg.ToolCalls {
					st.tools[tc.Name]++
				}
			}
		}
		today := time.Now().Truncate(24 * time.Hour)
		for i := 13; i >= 0; i-- {
			d := today.AddDate(0, 0, -i)
			c := dayMap[d.Format("2006-01-02")]
			st.byDay = append(st.byDay, dayCount{day: d, count: c})
			if c > st.maxDay {
				st.maxDay = c
			}
		}
		return usageStatsMsg{stats: st}
	}
}

func (m usageModel) resize(w, h int) usageModel {
	m.w, m.h = w, h
	return m
}

func (m usageModel) update(msg tea.Msg) (usageModel, tea.Cmd) {
	switch msg := msg.(type) {
	case usageStatsMsg:
		m.stats = msg.stats
		m.ready = true
		return m, nil
	case tea.KeyMsg:
		switch msg.String() {
		case "esc", "q", "enter":
			return m, pop
		}
	}
	return m, nil
}

func (m usageModel) view() string {
	header := " " + styleTitle.Render("usage") + "  " +
		styleDim.Render("last 14 days · "+clip(m.opt.Cwd, 40)) + "  " + styleDim.Render("esc back")
	if !m.ready {
		return header + "\n\n  " + styleDim.Render("scanning sessions…")
	}
	st := m.stats
	if st.sessions == 0 {
		return header + "\n\n  " + styleDim.Render("no sessions yet — start a chat and come back")
	}

	stat := func(label, value, sub string) string {
		return lipglossColumn(label, value, sub)
	}
	cardW := m.w/4 - 3
	if cardW < 16 {
		cardW = 16
	}
	cards := lipglossJoin(
		stat("sessions", itoa(st.sessions), "in this dir"),
		stat("messages", humanN(st.messages), "across sessions"),
		stat("tokens", "~"+humanN(st.tokensEst), "estimated"),
		stat("span", spanText(st.first, st.last), "first → last"),
	)

	chart := m.sparkline()
	tools := m.toolBars()
	models := m.modelBars()

	row := lipglossJoinSide(chart, models)
	_ = cardW
	body := lipglossStack(cards, "", row, "", tools)
	return header + "\n\n" + body
}

func spanText(a, b time.Time) string {
	if a.IsZero() {
		return "—"
	}
	d := b.Sub(a)
	switch {
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 48*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func (m usageModel) sparkline() string {
	st := m.stats
	width := len(st.byDay) * 2
	var lines []string
	lines = append(lines, stylePanelTitle.Render("activity"))
	for level := 6; level >= 1; level-- {
		var row strings.Builder
		for _, d := range st.byDay {
			h := 0
			if st.maxDay > 0 {
				h = d.count * 6 / st.maxDay
			}
			if h >= level {
				row.WriteString("██")
			} else {
				row.WriteString("  ")
			}
		}
		lines = append(lines, row.String())
	}
	var axis strings.Builder
	for _, d := range st.byDay {
		if d.day.Day()%2 == 0 {
			axis.WriteString(styleDim.Render(d.day.Format("02")))
		} else {
			axis.WriteString(styleDim.Render("  "))
		}
	}
	lines = append(lines, axis.String())
	lines = append(lines, styleDim.Render(fmt.Sprintf("%d sessions · peak %d/day", st.sessions, st.maxDay)))
	_ = width
	return strings.Join(lines, "\n")
}

func (m usageModel) toolBars() string {
	type kv struct {
		k string
		v int
	}
	var list []kv
	total := 0
	for k, v := range m.stats.tools {
		list = append(list, kv{k, v})
		total += v
	}
	if len(list) == 0 {
		return ""
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	max := list[0].v
	var lines []string
	lines = append(lines, stylePanelTitle.Render(fmt.Sprintf("tools · %d calls", total)))
	barW := 34
	if m.w < 80 {
		barW = 20
	}
	for _, it := range list {
		if len(lines) > 9 {
			break
		}
		n := it.v * barW / max
		if n < 1 {
			n = 1
		}
		bar := styleAccent.Render(strings.Repeat("█", n)) + styleDim.Render(strings.Repeat("·", barW-n))
		lines = append(lines, fmt.Sprintf("%-9s %s %4d", it.k, bar, it.v))
	}
	return strings.Join(lines, "\n")
}

func (m usageModel) modelBars() string {
	type kv struct {
		k string
		v int
	}
	var list []kv
	for k, v := range m.stats.byModel {
		list = append(list, kv{k, v})
	}
	sort.Slice(list, func(i, j int) bool {
		if list[i].v != list[j].v {
			return list[i].v > list[j].v
		}
		return list[i].k < list[j].k
	})
	lines := []string{stylePanelTitle.Render("models")}
	max := 1
	if len(list) > 0 {
		max = list[0].v
	}
	barW := 18
	if m.w < 100 {
		barW = 10
	}
	for i, it := range list {
		if i >= 6 {
			break
		}
		n := it.v * barW / max
		if n < 1 {
			n = 1
		}
		label := it.k
		if label == "/" {
			label = "(unset)"
		}
		bar := styleGood.Render(strings.Repeat("▰", n)) + styleDim.Render(strings.Repeat("▱", barW-n))
		lines = append(lines, clip(label, 26))
		lines = append(lines, "  "+bar+" "+styleDim.Render(itoa(it.v)))
	}
	return strings.Join(lines, "\n")
}

func lipglossColumn(label, value, sub string) string {
	return styleDim.Render(label) + "\n" + styleTitle.Render(value) + "\n" + styleDim.Render(sub)
}

func lipglossJoin(parts ...string) string {
	var out []string
	for _, p := range parts {
		out = append(out, p)
	}
	cols := make([][]string, len(out))
	maxLines := 0
	for i, p := range out {
		cols[i] = strings.Split(p, "\n")
		if len(cols[i]) > maxLines {
			maxLines = len(cols[i])
		}
	}
	var lines []string
	for row := 0; row < maxLines; row++ {
		var cells []string
		for i := range cols {
			cell := ""
			if row < len(cols[i]) {
				cell = cols[i][row]
			}
			cells = append(cells, padRight(cell, 20))
		}
		lines = append(lines, strings.Join(cells, "  "))
	}
	return strings.Join(lines, "\n")
}

func lipglossJoinSide(a, b string) string {
	al := strings.Split(a, "\n")
	bl := strings.Split(b, "\n")
	n := len(al)
	if len(bl) > n {
		n = len(bl)
	}
	w := 0
	for _, l := range al {
		if plainLen(l) > w {
			w = plainLen(l)
		}
	}
	var lines []string
	for i := 0; i < n; i++ {
		left, right := "", ""
		if i < len(al) {
			left = al[i]
		}
		if i < len(bl) {
			right = bl[i]
		}
		lines = append(lines, left+strings.Repeat(" ", maxInt(0, w-plainLen(left)+4))+right)
	}
	return strings.Join(lines, "\n")
}

func lipglossStack(parts ...string) string {
	return strings.Join(parts, "\n")
}

func padRight(s string, w int) string {
	n := w - plainLen(s)
	if n <= 0 {
		return s
	}
	return s + strings.Repeat(" ", n)
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

func plainLen(s string) int {
	n := 0
	inEsc := false
	for _, r := range s {
		if r == '\x1b' {
			inEsc = true
			continue
		}
		if inEsc {
			if r == 'm' {
				inEsc = false
			}
			continue
		}
		n++
	}
	return n
}
