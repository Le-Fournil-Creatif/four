package main

// Panneau des sessions (piste D de la planche) : un pane tmux à droite de
// chaque session, qui range les sessions par urgence. Ctrl-b Tab et
// Ctrl-b 1…9 sont des raccourcis tmux : ils marchent depuis Claude, sans
// passer par le panneau. Le panneau n'apparaît que si la fenêtre est assez
// large, Ctrl-b p le masque partout et Ctrl-b P le rétrécit.

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	panelW    = 48  // largeur du panneau
	panelNarW = 34  // largeur du panneau rétréci (Ctrl-b P)
	panelMinW = 130 // fenêtre plus étroite : pas de panneau, Claude garde la place
)

// ── Ordre : à traiter, au four, au repos ───────────────────────────────

type sessGroups struct{ todo, busy, idle []Session }

// Une attente déjà vue depuis plus d'une heure : le plus souvent Claude a fini
// et attend juste la suite. Elle passe au repos, en gris.
const staleWait = 3600

func waitStale(s Session, now int64) bool {
	return s.Mark == "wait" && s.Seen && now-s.MarkTime >= staleWait
}

// À traiter : Claude attend une réponse, ou a fini sans qu'on ait regardé.
// Les attentes d'abord, la plus ancienne en tête. Le reste garde l'ordre de
// listSessions (projet, puis lettre).
func groupSessions(ss []Session) sessGroups { return groupSessionsAt(ss, time.Now().Unix()) }

func groupSessionsAt(ss []Session, now int64) sessGroups {
	var g sessGroups
	for _, s := range ss {
		switch {
		case s.Mark == "wait" && !waitStale(s, now) || s.Mark == "done" && !s.Seen:
			g.todo = append(g.todo, s)
		case s.Mark == "busy" && s.Running():
			g.busy = append(g.busy, s)
		default:
			g.idle = append(g.idle, s)
		}
	}
	sort.SliceStable(g.todo, func(i, j int) bool {
		a, b := g.todo[i], g.todo[j]
		if (a.Mark == "wait") != (b.Mark == "wait") {
			return a.Mark == "wait"
		}
		return a.MarkTime < b.MarkTime
	})
	return g
}

func (g sessGroups) order() []Session {
	out := make([]Session, 0, len(g.todo)+len(g.busy)+len(g.idle))
	return append(append(append(out, g.todo...), g.busy...), g.idle...)
}

// Numéros 1…9 dans l'ordre affiché ; la session où l'on est n'en a pas.
func numbered(order []Session, cur string) map[string]int {
	n := map[string]int{}
	k := 1
	for _, s := range order {
		if k > 9 {
			break
		}
		if s.Name != cur {
			n[s.Name] = k
			k++
		}
	}
	return n
}

func nextTodo(ss []Session, cur string) (Session, bool) {
	for _, s := range groupSessions(ss).todo {
		if s.Name != cur {
			return s, true
		}
	}
	return Session{}, false
}

// ── Raccourcis : four _goto, four _next ─────────────────────────────────

func panelGoto(n int, client, cur string) {
	ss := listSessions()
	num := numbered(groupSessions(ss).order(), cur)
	for _, s := range ss {
		if num[s.Name] == n {
			switchTo(client, s)
			return
		}
	}
	say(client, "pas de session n° "+itoa(n))
}

func panelNext(client, cur string) {
	if s, ok := nextTodo(listSessions(), cur); ok {
		switchTo(client, s)
		return
	}
	say(client, "rien à traiter")
}

func switchTo(client string, s Session) {
	note := ""
	if !s.Running() && s.Shell() {
		_ = relaunch(s)
		note = s.Project + " · " + s.Letter + " · " + agentLabel(s.Agent) + " relancé"
	}
	markSeen(s.Name)
	args := []string{"switch-client"}
	if client != "" {
		args = append(args, "-c", client)
	}
	_ = tmuxCmd(append(args, "-t", "="+s.Name)...).Run()
	if note != "" {
		say(client, note)
	}
}

func say(client, msg string) {
	args := []string{"display-message"}
	if client != "" {
		args = append(args, "-c", client)
	}
	_ = tmuxCmd(append(args, "-d", "3000", msg)...).Run()
}

// ── Mise en place : four _panel-ensure, four _panel-toggle ──────────────

func panelOffFile() string { return filepath.Join(stateDir(), "panel-off") }

func panelOn() bool { return !exists(panelOffFile()) }

func panelNarrowFile() string { return filepath.Join(stateDir(), "panel-narrow") }

func panelWidth() int {
	if exists(panelNarrowFile()) {
		return panelNarW
	}
	return panelW
}

// Ajoute, recale ou retire le panneau d'une session (toutes si only est vide).
// Appelé par les hooks tmux (attache, changement de session, redimension).
func panelEnsure(only string) {
	f, err := os.OpenFile(filepath.Join(stateDir(), "panel.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err == nil {
		defer f.Close()
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_EX) // deux hooks à la fois : un seul panneau
	}
	on, pw := panelOn(), panelWidth()
	ss, err := scanSessions()
	if err != nil {
		return
	}
	for _, s := range ss {
		if only != "" && s.Name != only || s.Pane == "" {
			continue
		}
		want := on && s.WinW >= panelMinW
		switch {
		case want && s.PanelPane == "":
			_ = tmuxCmd("split-window", "-d", "-f", "-h", "-l", itoa(pw), "-t", s.Pane,
				shQuote(Bin)+" _panel").Run()
		case want:
			_ = tmuxCmd("resize-pane", "-t", s.PanelPane, "-x", itoa(pw)).Run()
		case s.PanelPane != "":
			_ = tmuxCmd("kill-pane", "-t", s.PanelPane).Run()
		}
	}
}

func panelToggle(client string) {
	msg := "panneau des sessions affiché"
	if panelOn() {
		_ = os.WriteFile(panelOffFile(), nil, 0o644)
		msg = "panneau des sessions masqué · Ctrl-b p pour le réafficher"
	} else {
		_ = os.Remove(panelOffFile())
	}
	panelEnsure("")
	say(client, msg)
}

func panelNarrowToggle(client string) {
	msg := "panneau des sessions rétréci · Ctrl-b P pour l'élargir"
	if exists(panelNarrowFile()) {
		_ = os.Remove(panelNarrowFile())
		msg = "panneau des sessions élargi"
	} else {
		_ = os.WriteFile(panelNarrowFile(), nil, 0o644)
	}
	panelEnsure("")
	say(client, msg)
}

// ── Le panneau : four _panel ────────────────────────────────────────────

type panelModel struct {
	w, h  int
	pane  string // le pane du panneau (%N)
	sess  string // sa session
	ss    []Session
	pr    []Presence
	msgs  map[string]string
	prod  map[string]bool
	spin  int
	ticks int

	focused bool   // le panneau a le focus (Ctrl-b →) : le curseur se montre
	cursor  string // session sous le curseur
	hover   string // session sous la souris
	hoverAt time.Time
}

// Survol : tmux n'envoie rien quand la souris quitte le panneau, le survol
// s'éteint donc seul après un moment sans mouvement.
const hoverFor = 2 * time.Second

type (
	panelTickMsg time.Time
	panelLiveMsg struct {
		ss   []Session
		ok   bool
		pr   []Presence
		msgs map[string]string
		prod map[string]bool
	}
)

func runPanel() {
	pane := os.Getenv("TMUX_PANE")
	sess, err := tmuxOut("display-message", "-p", "-t", pane, "#{session_name}")
	if pane == "" || err != nil || sess == "" {
		os.Stderr.WriteString("four : le panneau se lance dans une session four\n")
		os.Exit(1)
	}
	m := panelModel{pane: pane, sess: sess, prod: prodSet()}
	m.ss, _ = scanSessions()
	m.pr = readPresence()
	m.msgs = waitMsgs(m.ss)
	p := tea.NewProgram(m, tea.WithAltScreen(), tea.WithMouseAllMotion(), tea.WithReportFocus())
	if _, err := p.Run(); err != nil {
		os.Stderr.WriteString("four : " + err.Error() + "\n")
		os.Exit(1)
	}
}

func waitMsgs(ss []Session) map[string]string {
	out := map[string]string{}
	for _, s := range ss {
		if s.Mark == "wait" {
			if t := readWaitMsg(s.Name); t != "" {
				out[s.Name] = t
			}
		}
	}
	return out
}

func panelTick() tea.Cmd {
	return tea.Tick(time.Second, func(t time.Time) tea.Msg { return panelTickMsg(t) })
}

func (m panelModel) Init() tea.Cmd {
	// Titre = nom d'hôte : la barre tmux le masque si le panneau a le focus.
	return tea.Batch(panelTick(), tea.SetWindowTitle(hostname()))
}

func (m panelModel) live(full bool) tea.Cmd {
	return func() tea.Msg {
		ss, err := scanSessions()
		msg := panelLiveMsg{ss: ss, ok: err == nil, msgs: waitMsgs(ss)}
		if full {
			msg.pr, msg.prod = readPresence(), prodSet()
		}
		return msg
	}
}

func (m panelModel) me() (Session, bool) {
	for _, s := range m.ss {
		if s.Name == m.sess {
			return s, true
		}
	}
	return Session{}, false
}

// Rend le focus à Claude (un clic sur le panneau le lui a pris).
func (m panelModel) focusMain() {
	if s, ok := m.me(); ok && s.Pane != "" {
		_ = tmuxCmd("select-pane", "-t", s.Pane).Run()
	}
}

func (m panelModel) visible() []Session {
	var out []Session
	for _, s := range m.ss {
		if s.Pane != "" {
			out = append(out, s)
		}
	}
	return out
}

func (m panelModel) goTo(name string) {
	for _, s := range m.visible() {
		if s.Name == name {
			m.focusMain()
			switchTo(currentClient(), s)
			return
		}
	}
}

func (m panelModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		return m, nil

	case panelTickMsg:
		if m.hover != "" && time.Since(m.hoverAt) > hoverFor {
			m.hover = ""
		}
		m.ticks++
		m.spin++
		return m, tea.Batch(panelTick(), m.live(m.ticks%3 == 0))

	case panelLiveMsg:
		if !msg.ok {
			return m, nil
		}
		m.ss, m.msgs = msg.ss, msg.msgs
		if msg.prod != nil {
			m.pr, m.prod = msg.pr, msg.prod
		}
		// Claude et son shell sont partis : le panneau part aussi, la session se ferme.
		if s, ok := m.me(); !ok || s.Pane == "" {
			return m, tea.Quit
		}
		return m, nil

	case tea.FocusMsg:
		m.focused, m.cursor = true, ""
		return m, nil

	case tea.BlurMsg:
		m.focused = false
		return m, nil

	case tea.KeyMsg:
		// Une touche arrive : le panneau a le focus, même sans focus-events.
		if !m.focused {
			m.focused, m.cursor = true, ""
		}
		order := groupSessions(m.visible()).order()
		k := msg.String()
		switch {
		case k == "up" || k == "k":
			m.cursor = m.step(order, -1)
			return m, nil
		case k == "down" || k == "j":
			m.cursor = m.step(order, 1)
			return m, nil
		case k == "enter":
			if c := m.cursorIn(order); c != m.sess {
				m.goTo(c)
				return m, nil
			}
		case len(k) == 1 && k[0] >= '1' && k[0] <= '9':
			for name, n := range numbered(order, m.sess) {
				if n == int(k[0]-'0') {
					m.goTo(name)
					return m, nil
				}
			}
			return m, nil
		case k == "tab":
			if s, ok := nextTodo(m.visible(), m.sess); ok {
				m.goTo(s.Name)
			}
			return m, nil
		}
		// Échap, ←, ou toute autre touche : retour à Claude.
		m.focusMain()
		return m, nil

	case tea.MouseMsg:
		if msg.Action == tea.MouseActionMotion {
			_, hits := m.lines()
			m.hover, m.hoverAt = hits[msg.Y], time.Now()
			return m, nil
		}
		if msg.Action != tea.MouseActionPress || msg.Button != tea.MouseButtonLeft {
			return m, nil
		}
		_, hits := m.lines()
		if name, ok := hits[msg.Y]; ok && name != m.sess {
			m.goTo(name)
			return m, nil
		}
		m.focusMain()
		return m, nil
	}
	return m, nil
}

// La session sous le curseur : celle choisie si elle est toujours là, sinon
// la première où aller (la n° 1), sinon celle où l'on est.
func (m panelModel) cursorIn(order []Session) string {
	first := m.sess
	for _, s := range order {
		if s.Name == m.cursor {
			return m.cursor
		}
		if first == m.sess && s.Name != m.sess {
			first = s.Name
		}
	}
	return first
}

func (m panelModel) step(order []Session, d int) string {
	c := m.cursorIn(order)
	for i, s := range order {
		if s.Name == c {
			return order[max(0, min(len(order)-1, i+d))].Name
		}
	}
	return c
}

// La ligne en surbrillance : le curseur quand le panneau a le focus, sinon
// la session où l'on est.
func (m panelModel) highlight(order []Session) string {
	if m.focused {
		return m.cursorIn(order)
	}
	return m.sess
}

func (m panelModel) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	ls, _ := m.lines()
	return frame(rows(ls, m.w), m.w, m.h)
}

// ── Rendu ───────────────────────────────────────────────────────────────

type pline struct {
	l    Line
	sess string // session sous cette ligne (clic)
}

func (m panelModel) glyph(s Session) (string, string) {
	switch {
	case waitStale(s, time.Now().Unix()):
		return "◆", cD
	case s.Mark == "wait":
		return "◆", cWarn
	case s.Mark == "done" && s.Seen:
		return "✓", cD
	case s.Mark == "done":
		return "✓", cOk
	case s.Mark == "busy" && s.Running():
		return spinFrames[m.spin%len(spinFrames)], cAcc
	case s.Running():
		return "●", cOk
	}
	return "○", cDD
}

func sessAge(s Session) string {
	switch {
	case s.Mark != "":
		return dur(s.MarkTime)
	case s.Running():
		return dur(s.Created)
	}
	return "éteint"
}

func sessTitle(s Session) (string, string) {
	switch {
	case s.Title != "":
		return s.Title, cFg
	case s.Running():
		return agentLabel(s.Agent), cFg
	}
	return "shell", cD
}

// cur : la ligne en surbrillance ; hov : sous la souris (fond plus discret).
func (m panelModel) row(left, right []seg, cur, hov bool) Line {
	lead := Sp(1)
	switch {
	case cur:
		lead = S("▌", cAcc)
	case hov:
		lead = S("▌", cD)
	}
	l := lr(append([]seg{lead}, left...), append(right, Sp(1)), m.w)
	switch {
	case cur:
		l.bg = cHi
		for i, sg := range l.segs {
			if sg.fg == cD || sg.fg == cDD {
				l.segs[i].fg = cHiD
			}
		}
	case hov:
		l.bg = cHov
	}
	return l
}

// Une session : état, projet · lettre, âge et numéro ; le titre en dessous
// (full) ou sur la même ligne (au repos).
func (m panelModel) block(s Session, num map[string]int, hl string, full bool) []pline {
	cur := s.Name == hl
	g, gc := m.glyph(s)
	left := []seg{S(g+" ", gc), B(s.Project, projColor(s.Project))}
	if m.prod[s.Project] {
		left = append(left, S(" ▲", cErr))
	}
	left = append(left, S(" · ", cD), B(s.Letter, cAcc))
	if s.Name == m.sess {
		left = append(left, S("  ▣ ici", cD))
	}
	title, tc := sessTitle(s)
	if !full {
		left = append(left, S("  "+title, tc))
	}
	var right []seg
	if s.Remote {
		right = append(right, S("⇄ ", cOk))
	}
	right = append(right, S(sessAge(s), cD))
	if n, ok := num[s.Name]; ok {
		right = append(right, Sp(2), K(" "+itoa(n)+" "))
	}
	hov := s.Name == m.hover
	out := []pline{{m.row(left, right, cur, hov), s.Name}}
	if !full {
		return out
	}
	t := S(title, tc)
	if cur {
		t = B(title, tc)
	}
	out = append(out, pline{m.row([]seg{Sp(2), t}, nil, cur, hov), s.Name})
	if w := m.msgs[s.Name]; w != "" && s.Mark == "wait" {
		out = append(out, pline{m.row([]seg{Sp(2), S(w, cWarn)}, nil, cur, hov), s.Name})
	}
	return out
}

func (m panelModel) section(t string, n int) pline {
	var right []seg
	if n >= 0 {
		right = []seg{S(itoa(n), cDD), Sp(1)}
	}
	return pline{l: lr([]seg{B(" "+t, cD)}, right, m.w)}
}

func (m panelModel) brigade() []pline {
	now := time.Now()
	var out []pline
	for _, p := range m.pr {
		if p.User == me || hiddenUsers[p.User] || !p.active(now) {
			continue
		}
		st := []seg{S("◐ au four", cAcc)}
		switch p.State {
		case "done":
			st = []seg{S("✓ cuit", cOk), S(" · "+dur(p.Since), cD)}
		case "wait":
			st = []seg{S("◆ attend", cWarn), S(" · "+dur(p.Since), cD)}
		}
		left := append(padSegs([]seg{S(p.User, userColor(p.User))}, 9), S(p.Project, cFg))
		out = append(out, pline{l: lr(append([]seg{Sp(1)}, left...), append(st, Sp(1)), m.w)})
		if len(out) == 4 {
			break
		}
	}
	if len(out) == 0 {
		return nil
	}
	return append([]pline{{}, m.section("BRIGADE", -1)}, out...)
}

func (m panelModel) footer() []pline {
	k := func(key, txt string) pline {
		return pline{l: Ln(append(padSegs([]seg{Sp(1), K(" " + key + " ")}, 14), S(txt, cD))...)}
	}
	rule := pline{l: Ln(Sp(1), S(strings.Repeat("─", max(m.w-2, 0)), cBd))}
	if m.focused {
		return []pline{rule, k("↑↓", "choisir"), k("↵", "ouvrir"), k("échap", "revenir à la session")}
	}
	return []pline{rule, k("ctrl-b tab", "prochaine à traiter"), k("ctrl-b →", "naviguer ici"),
		k("ctrl-b 1…9", "aller"), k("ctrl-b p", "masquer le panneau")}
}

// Les lignes du panneau, ajustées à la hauteur : on lâche d'abord la dernière
// aide, puis la brigade, puis les sessions au repos (« +N »), puis l'aide.
func (m panelModel) lines() ([]Line, map[int]string) {
	g := groupSessions(m.visible())
	num := numbered(g.order(), m.sess)
	hl := m.highlight(g.order())

	core := []pline{m.section("À TRAITER", len(g.todo))}
	if len(g.todo) == 0 {
		core = append(core, pline{l: Ln(S("   rien à traiter", cDD))})
	}
	for _, s := range g.todo {
		core = append(core, m.block(s, num, hl, true)...)
	}
	if len(g.busy) > 0 {
		core = append(core, pline{}, m.section("AU FOUR", len(g.busy)))
		for _, s := range g.busy {
			core = append(core, m.block(s, num, hl, true)...)
		}
	}
	idleN := len(g.idle)
	idle := func() []pline {
		if len(g.idle) == 0 {
			return nil
		}
		out := []pline{{}, m.section("AU REPOS", len(g.idle))}
		for _, s := range g.idle[:idleN] {
			out = append(out, m.block(s, num, hl, false)...)
		}
		if idleN < len(g.idle) {
			out = append(out, pline{l: Ln(S("   +"+itoa(len(g.idle)-idleN)+" au repos · Ctrl-b Espace", cDD))})
		}
		return out
	}
	brig, foot := m.brigade(), m.footer()
	total := func() int { return len(core) + len(idle()) + len(brig) + len(foot) }
fit:
	for total() > m.h {
		switch {
		case len(foot) > 3:
			foot = foot[:3]
		case brig != nil:
			brig = nil
		case idleN > 0:
			idleN--
		case foot != nil:
			foot = nil
		default:
			break fit
		}
	}
	all := append(append(append([]pline{}, core...), idle()...), brig...)
	for len(all)+len(foot) < m.h {
		all = append(all, pline{})
	}
	all = append(all, foot...)
	if len(all) > m.h {
		all = all[:m.h]
	}
	out := make([]Line, len(all))
	hits := map[int]string{}
	for i, p := range all {
		out[i] = p.l
		if p.sess != "" {
			hits[i] = p.sess
		}
	}
	return out, hits
}
