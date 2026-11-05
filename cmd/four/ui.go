package main

import (
	"os"
	"os/exec"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"
)

type scr int

const (
	scrDash scr = iota
	scrHelp
	scrAdd
	scrGitHub
	scrNew
	scrLink
	scrSteps
	scrSetup
	scrConfirm
)

type model struct {
	w, h  int
	popup bool
	scr   scr

	projects []*Project
	view     []int // index dans projects, après filtre
	cur      int
	off      int
	filter   input
	filtring bool
	focus    int // 0 projets, 1 sessions
	scur     int

	sessions []Session
	presence []Presence
	usage    Usage
	details  map[string]Details
	detAt    map[string]time.Time
	prod     map[string]bool
	gitAt    time.Time
	missing  int // étapes de configuration manquantes

	toast   string
	toastC  string
	toastAt time.Time
	busy    string
	busyFor string // dossier du projet concerné
	spin    int

	confirm confirmState
	add     addState
	gh      ghState
	nw      newState
	link    linkState
	steps   stepsState
	setup   setupState
	owners  []string
	ghRepos []GHRepo

	lastClickY int
	lastClickT time.Time
	openArg    string
	opened     bool
	selectDir  string // projet à sélectionner au prochain rechargement
}

type (
	tickMsg time.Time
	spinMsg time.Time
	liveMsg struct {
		ss []Session
		pr []Presence
	}
	projectsMsg []*Project
	detailsMsg  struct {
		dir string
		d   Details
	}
	usageMsg Usage
	openMsg  struct {
		name, msg string
		err       error
	}
	attachedMsg struct{ err error }
	toastMsg    struct{ text, color string }
)

func newModel(start scr, popup bool, arg string) model {
	m := model{scr: start, popup: popup, openArg: arg, details: map[string]Details{},
		detAt: map[string]time.Time{}, prod: prodSet()}
	m.filter.ph = "filtrer les projets…"
	m.projects = loadProjectCache()
	if m.projects == nil {
		m.projects = scanProjects()
	}
	sortProjects(m.projects)
	m.usage = cachedUsage()
	m.sessions = listSessions()
	m.presence = readPresence()
	m.missing = quickSetupMissing()
	m.refilter()
	if start == scrSetup {
		m.setup = newSetupState()
	}
	return m
}

func (m model) Init() tea.Cmd {
	cmds := []tea.Cmd{tick(), loadProjectsCmd(), liveCmd(), usageCmd()}
	if m.scr == scrSetup {
		cmds = append(cmds, setupCheckCmd())
	}
	return tea.Batch(cmds...)
}

func tick() tea.Cmd { return tea.Tick(time.Second, func(t time.Time) tea.Msg { return tickMsg(t) }) }
func spinTick() tea.Cmd {
	return tea.Tick(120*time.Millisecond, func(t time.Time) tea.Msg { return spinMsg(t) })
}

func loadProjectsCmd() tea.Cmd {
	return func() tea.Msg {
		ps := scanProjects()
		loadAll(ps)
		sortProjects(ps)
		saveProjectCache(ps)
		return projectsMsg(ps)
	}
}

func liveCmd() tea.Cmd {
	return func() tea.Msg {
		return liveMsg{ss: listSessions(), pr: readPresence()}
	}
}

func usageCmd() tea.Cmd { return func() tea.Msg { return usageMsg(cachedUsage()) } }

func detailsCmd(dir string) tea.Cmd {
	return func() tea.Msg { return detailsMsg{dir: dir, d: loadDetails(dir)} }
}

func (m *model) say(text, color string) {
	m.toast, m.toastC, m.toastAt = text, color, time.Now()
}

func (m *model) spinning() bool {
	return m.busy != "" || (m.scr == scrSteps || m.scr == scrNew || m.scr == scrLink) && m.steps.running() ||
		m.scr == scrSetup && m.setup.waiting() || m.scr == scrGitHub && m.gh.loading
}

// ── Projets visibles, sélection ─────────────────────────────────────────

func (m *model) refilter() {
	var keep string
	if p := m.sel(); p != nil {
		keep = p.Dir
	}
	q := strings.TrimSpace(m.filter.val())
	m.view = m.view[:0]
	if q == "" {
		for i := range m.projects {
			m.view = append(m.view, i)
		}
	} else {
		names := make([]string, len(m.projects))
		for i, p := range m.projects {
			names[i] = p.Name
		}
		for _, r := range fuzzy.Find(q, names) {
			m.view = append(m.view, r.Index)
		}
	}
	m.cur = 0
	for i, pi := range m.view {
		if m.projects[pi].Dir == keep {
			m.cur = i
		}
	}
	if q != "" {
		m.cur = 0
	}
	m.fix()
}

func (m *model) sel() *Project {
	if m.cur < 0 || m.cur >= len(m.view) {
		return nil
	}
	return m.projects[m.view[m.cur]]
}

func (m model) sessionsOf(p *Project) []Session {
	if p == nil {
		return nil
	}
	var out []Session
	for _, s := range m.sessions {
		if s.Dir == p.Dir || s.Dir == "" && s.Project == p.Name {
			out = append(out, s)
		}
	}
	return out
}

func (m model) othersOn(p *Project) []Presence {
	var out []Presence
	for _, pr := range m.presence {
		if pr.User != me && p != nil && pr.Dir == p.Dir {
			out = append(out, pr)
		}
	}
	return out
}

type lrow struct {
	header string
	vi     int // index dans view
}

func (m model) listRows() []lrow {
	var rs []lrow
	filtering := strings.TrimSpace(m.filter.val()) != ""
	legacy := false
	for vi, pi := range m.view {
		if p := m.projects[pi]; p.Legacy && !legacy && !filtering {
			legacy = true
			rs = append(rs, lrow{header: "ancien emplacement · " + Legacy, vi: -1})
		}
		rs = append(rs, lrow{vi: vi})
	}
	return rs
}

type layout struct {
	wide         bool
	lw, rw, bh   int
	top          int
	listY, listH int
}

func (m model) lay() layout {
	l := layout{top: 2, bh: m.h - 4}
	if l.bh < 6 {
		l.bh = 6
	}
	l.wide = m.w >= 100
	if l.wide {
		l.lw = 44
		if m.w >= 130 {
			l.lw = 48
		}
		l.rw = m.w - l.lw
	} else {
		l.lw, l.rw = m.w, m.w
	}
	l.listY = l.top + 3
	l.listH = l.bh - 2 - 4
	if l.listH < 1 {
		l.listH = 1
	}
	return l
}

// Garde la sélection visible.
func (m *model) fix() {
	if m.cur >= len(m.view) {
		m.cur = len(m.view) - 1
	}
	if m.cur < 0 {
		m.cur = 0
	}
	rs := m.listRows()
	row := 0
	for i, r := range rs {
		if r.vi == m.cur {
			row = i
		}
	}
	h := m.lay().listH
	if row < m.off {
		m.off = row
		if row > 0 && rs[row-1].vi < 0 {
			m.off = row - 1
		}
	}
	if row >= m.off+h {
		m.off = row - h + 1
	}
	if max := len(rs) - h; m.off > max {
		m.off = max
	}
	if m.off < 0 {
		m.off = 0
	}
	ns := len(m.sessionsOf(m.sel()))
	if m.scur > ns {
		m.scur = ns
	}
}

func (m *model) wantDetails() tea.Cmd {
	p := m.sel()
	if p == nil || !p.Git {
		return nil
	}
	if t, ok := m.detAt[p.Dir]; ok && time.Since(t) < 5*time.Second {
		return nil
	}
	m.detAt[p.Dir] = time.Now()
	return detailsCmd(p.Dir)
}

// ── Update ──────────────────────────────────────────────────────────────

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.w, m.h = msg.Width, msg.Height
		m.fix()
		return m, nil

	case tickMsg:
		cmds := []tea.Cmd{tick()}
		sec := time.Time(msg).Unix()
		if sec%2 == 0 {
			cmds = append(cmds, liveCmd())
		}
		if time.Since(m.gitAt) > 15*time.Second {
			m.gitAt = time.Now()
			cmds = append(cmds, loadProjectsCmd())
		}
		if sec%10 == 0 {
			cmds = append(cmds, usageCmd())
		}
		if m.scr == scrDash {
			cmds = append(cmds, m.wantDetails())
		}
		return m, tea.Batch(cmds...)

	case spinMsg:
		m.spin++
		if m.spinning() {
			return m, spinTick()
		}
		return m, nil

	case liveMsg:
		m.sessions, m.presence = msg.ss, msg.pr
		m.fix()
		return m, nil

	case projectsMsg:
		var keep string
		if p := m.sel(); p != nil {
			keep = p.Dir
		}
		m.projects = msg
		m.prod = prodSet()
		m.gitAt = time.Now()
		m.refilter()
		if m.selectDir != "" {
			keep, m.selectDir = m.selectDir, ""
		}
		if keep != "" && strings.TrimSpace(m.filter.val()) == "" {
			for i, pi := range m.view {
				if m.projects[pi].Dir == keep {
					m.cur = i
				}
			}
			m.fix()
		}
		var cmd tea.Cmd
		if m.openArg != "" && !m.opened {
			m.opened = true
			cmd = m.openByName(m.openArg)
		}
		return m, tea.Batch(cmd, m.wantDetails())

	case detailsMsg:
		m.details[msg.dir] = msg.d
		return m, nil

	case usageMsg:
		if Usage(msg).OK {
			m.usage = Usage(msg)
		}
		return m, nil

	case toastMsg:
		m.say(msg.text, msg.color)
		return m, nil

	case stepMsg:
		return m.onStep(msg)

	case ownersMsg:
		if len(msg) > 0 {
			m.owners = msg
		}
		return m, nil

	case ghReposMsg:
		if m.scr != scrGitHub && m.scr != scrLink {
			if msg.err == nil {
				m.ghRepos = msg.rs
			}
			return m, nil
		}

	case openMsg:
		m.busy, m.busyFor = "", ""
		if msg.err != nil {
			m.say("✖ "+msg.err.Error(), cErr)
			return m, liveCmd()
		}
		nc := m.attach(msg.name, msg.msg)
		return m, nc

	case attachedMsg:
		if msg.err != nil {
			m.say("✖ tmux : "+msg.err.Error(), cErr)
		}
		m.scr = scrDash
		m.gitAt = time.Now()
		return m, tea.Batch(liveCmd(), loadProjectsCmd(), usageCmd(), tea.ClearScreen)

	case tea.MouseMsg:
		return m.mouse(msg)

	case tea.KeyMsg:
		if msg.Type == tea.KeyCtrlC {
			return m, tea.Quit
		}
	}

	switch m.scr {
	case scrHelp:
		if k, ok := msg.(tea.KeyMsg); ok {
			_ = k
			m.scr = scrDash
		}
		return m, nil
	case scrConfirm:
		return m.updateConfirm(msg)
	case scrAdd:
		return m.updateAdd(msg)
	case scrGitHub:
		return m.updateGitHub(msg)
	case scrNew:
		return m.updateNew(msg)
	case scrLink:
		return m.updateLink(msg)
	case scrSteps:
		return m.updateSteps(msg)
	case scrSetup:
		return m.updateSetup(msg)
	}
	if k, ok := msg.(tea.KeyMsg); ok {
		return m.keyDash(k)
	}
	return m, nil
}

func (m model) keyDash(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	if m.filtring {
		switch k.Type {
		case tea.KeyEsc:
			m.filtring = false
			m.filter.set("")
			m.refilter()
			nc := m.wantDetails()
			return m, nc
		case tea.KeyEnter:
			m.filtring = false
			nc := m.open(m.sel())
			return m, nc
		case tea.KeyUp, tea.KeyDown, tea.KeyPgUp, tea.KeyPgDown:
			// navigation pendant la recherche
		case tea.KeyTab:
			m.filtring = false
			return m, nil
		default:
			if m.filter.key(k) {
				m.refilter()
				nc := m.wantDetails()
				return m, nc
			}
			return m, nil
		}
	}
	s := k.String()
	if m.focus == 1 {
		return m.keySessions(k)
	}
	switch s {
	case "q", "esc":
		if s == "esc" && strings.TrimSpace(m.filter.val()) != "" {
			m.filter.set("")
			m.refilter()
			return m, nil
		}
		if s == "esc" && !m.popup {
			return m, nil
		}
		return m, tea.Quit
	case "up", "k", "shift+tab":
		m.cur--
	case "down", "j":
		m.cur++
	case "pgup":
		m.cur -= m.lay().listH
	case "pgdown":
		m.cur += m.lay().listH
	case "home":
		m.cur = 0
	case "end", "G":
		m.cur = len(m.view) - 1
	case "enter":
		nc := m.open(m.sel())
		return m, nc
	case "right", "l", "tab":
		if m.sel() != nil {
			m.focus, m.scur = 1, 0
		}
	case "n":
		nc := m.startNew(m.sel(), false)
		return m, nc
	case "a":
		m.add = addState{}
		m.scr = scrAdd
		return m, nil
	case "g":
		return m.openLink(m.sel())
	case "/":
		m.filtring = true
		return m, nil
	case "?":
		m.scr = scrHelp
		return m, nil
	case "c":
		m.scr = scrSetup
		m.setup = newSetupState()
		return m, tea.Batch(setupCheckCmd(), spinTick())
	case "r":
		m.say("◐ actualisation…", cD)
		return m, tea.Batch(loadProjectsCmd(), liveCmd(), usageCmd())
	default:
		return m, nil
	}
	m.fix()
	nc := m.wantDetails()
	return m, nc
}

func (m model) keySessions(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	p := m.sel()
	ss := m.sessionsOf(p)
	s := k.String()
	switch s {
	case "q":
		return m, tea.Quit
	case "esc", "left", "h", "tab", "shift+tab":
		m.focus = 0
	case "up", "k":
		if m.scur > 0 {
			m.scur--
		}
	case "down", "j":
		if m.scur < len(ss) {
			m.scur++
		}
	case "enter":
		if m.scur >= len(ss) {
			nc := m.startNew(p, false)
			return m, nc
		}
		nc := m.attachSession(ss[m.scur])
		return m, nc
	case "n":
		nc := m.startNew(p, false)
		return m, nc
	case "x":
		if m.scur < len(ss) {
			t := ss[m.scur]
			m.confirm = confirmState{
				title: "Fermer la session " + p.Name + " · " + t.Letter + " ?",
				lines: []string{"Claude s'arrête, la conversation reste dans son historique (claude --resume)."},
				yes: func(m *model) tea.Cmd {
					if err := killSession(t.Name); err != nil {
						m.say("✖ fermeture impossible", cErr)
					} else {
						m.say("✓ session "+p.Name+" · "+t.Letter+" fermée", cOk)
					}
					return liveCmd()
				},
			}
			m.scr = scrConfirm
		}
	case "?":
		m.scr = scrHelp
	default:
		if len(s) == 1 && strings.Contains(letters, s) {
			for _, t := range ss {
				if t.Letter == s {
					nc := m.attachSession(t)
					return m, nc
				}
			}
		}
	}
	return m, nil
}

// ── Ouvrir, créer, rejoindre ────────────────────────────────────────────

func (m *model) openByName(name string) tea.Cmd {
	for i, pi := range m.view {
		if strings.EqualFold(m.projects[pi].Name, name) {
			m.cur = i
			m.fix()
			return m.open(m.projects[pi])
		}
	}
	m.filter.set(name)
	m.filtring = true
	m.refilter()
	return nil
}

// ↵ sur un projet : la session la plus récente, sinon une nouvelle.
func (m *model) open(p *Project) tea.Cmd {
	if p == nil {
		return nil
	}
	ss := m.sessionsOf(p)
	if len(ss) == 0 {
		return m.startNew(p, false)
	}
	best := ss[0]
	for _, s := range ss[1:] {
		if s.Activity > best.Activity {
			best = s
		}
	}
	return m.attachSession(best)
}

func (m *model) startNew(p *Project, force bool) tea.Cmd {
	if p == nil || m.busy != "" {
		return nil
	}
	if others := m.othersOn(p); len(others) > 0 && !force {
		o := others[0]
		m.confirm = confirmState{
			title: o.User + " a Claude ouvert sur " + p.Name + " depuis " + dur(o.Started),
			lines: []string{"Deux Claude sur le même dossier peuvent modifier les mêmes fichiers.",
				"Ouvrir quand même une session ?"},
			yes: func(m *model) tea.Cmd { return m.startNew(p, true) },
		}
		m.scr = scrConfirm
		return nil
	}
	letter := freeLetter(m.sessionsOf(p), p.Name)
	if letter == "" {
		m.say("◆ plus de lettre libre sur "+p.Name+" : ferme des sessions (→ puis x)", cWarn)
		return nil
	}
	prod := m.prod[p.Name]
	m.busy, m.busyFor = "démarrage de Claude sur "+p.Name, p.Dir
	pp := *p
	return tea.Batch(spinTick(), func() tea.Msg {
		note := pullIfClean(&pp)
		name, err := newSession(&pp, letter, prod)
		label := p.Name + " · " + letter
		msg := label + " · nouvelle session Claude"
		if prod {
			msg += " · ▲ prod : mode normal"
		}
		if note != "" {
			msg += " · " + note
		}
		msg += " · Ctrl-b d menu · Ctrl-b Espace basculer"
		return openMsg{name: name, msg: msg, err: err}
	})
}

func (m *model) attachSession(s Session) tea.Cmd {
	label := s.Project + " · " + s.Letter
	msg := label
	if !s.Running() && s.Shell() {
		_ = relaunch(s.Name)
		msg += " · Claude relancé"
	} else if s.Running() {
		msg += " · Claude tourne"
	}
	msg += " · Ctrl-b d menu · Ctrl-b Espace basculer"
	return m.attach(s.Name, msg)
}

func (m *model) attach(name, msg string) tea.Cmd {
	markSeen(name)
	cmd, inside := attachCmd(name, msg)
	if inside {
		_ = cmd.Run()
		return tea.Quit
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg { return attachedMsg{err} })
}

// ── Souris ──────────────────────────────────────────────────────────────

func (m model) mouse(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	switch m.scr {
	case scrAdd:
		return m.mouseAdd(e)
	case scrGitHub:
		return m.mouseGitHub(e)
	}
	if m.scr != scrDash {
		return m, nil
	}
	l := m.lay()
	leftPane := !l.wide && m.focus == 0 || l.wide && e.X < l.lw
	switch e.Button {
	case tea.MouseButtonWheelUp, tea.MouseButtonWheelDown:
		if e.Action != tea.MouseActionPress {
			return m, nil
		}
		if leftPane {
			if e.Button == tea.MouseButtonWheelUp {
				m.cur--
			} else {
				m.cur++
			}
			m.fix()
			nc := m.wantDetails()
			return m, nc
		}
		return m, nil
	case tea.MouseButtonLeft:
	default:
		return m, nil
	}
	if e.Action != tea.MouseActionPress {
		return m, nil
	}
	double := e.Y == m.lastClickY && time.Since(m.lastClickT) < 400*time.Millisecond
	m.lastClickY, m.lastClickT = e.Y, time.Now()
	if leftPane {
		r := e.Y - l.listY
		rs := m.listRows()
		if r >= 0 && r < l.listH && m.off+r < len(rs) {
			if vi := rs[m.off+r].vi; vi >= 0 {
				m.focus = 0
				was := m.cur == vi
				m.cur = vi
				m.fix()
				if double && was {
					nc := m.open(m.sel())
					return m, nc
				}
				nc := m.wantDetails()
				return m, nc
			}
		}
		// « + Ajouter un projet » : avant-dernière ligne de la boîte
		if e.Y == l.top+l.bh-2 {
			m.add = addState{}
			m.scr = scrAdd
		}
		return m, nil
	}
	// panneau de droite : lignes de session
	_, hits := m.rightLines(m.sel(), l.rw-2, l.bh-2)
	if i, ok := hits[e.Y-l.top-1]; ok {
		ss := m.sessionsOf(m.sel())
		m.focus = 1
		m.scur = i
		if i >= len(ss) {
			nc := m.startNew(m.sel(), false)
			return m, nc
		}
		nc := m.attachSession(ss[i])
		return m, nc
	}
	return m, nil
}

// ── Lancement ───────────────────────────────────────────────────────────

func runTUI(start scr, popup bool, arg string) {
	writeConf()
	if start == scrDash && arg == "" && !popup && m0SetupFirst() {
		start = scrSetup
	}
	m := newModel(start, popup, arg)
	if popup {
		// Par-dessus une session : le curseur part sur son projet.
		if out, err := tmuxOut("display-message", "-p", "#{@four_dir}"); err == nil {
			m.selectDir = strings.TrimSpace(out)
		}
	}
	opts := []tea.ProgramOption{tea.WithAltScreen(), tea.WithMouseCellMotion()}
	p := tea.NewProgram(m, opts...)
	if _, err := p.Run(); err != nil {
		os.Stderr.WriteString("four : " + err.Error() + "\n")
		os.Exit(1)
	}
}

// Première ouverture : l'assistant, tant qu'il reste quelque chose à régler
// et qu'on ne l'a pas remis à plus tard.
func m0SetupFirst() bool {
	if exists(dismissFile()) {
		return false
	}
	return quickSetupMissing() > 0
}

var _ = exec.Command
