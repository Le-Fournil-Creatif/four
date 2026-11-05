package main

// Écrans d'action : ajouter un projet (depuis GitHub ou nouveau), donner un
// repo GitHub à un projet, confirmation, et le moteur d'étapes qui les
// exécute en montrant chaque étape.

import (
	"errors"
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/sahilm/fuzzy"
)

type (
	ghReposMsg struct {
		rs  []GHRepo
		err error
	}
	ownersMsg []string
	stepMsg   struct {
		gen, i int
		note   string
		err    error
	}
)

func ghReposCmd() tea.Cmd {
	return func() tea.Msg { rs, err := fetchGHRepos(); return ghReposMsg{rs, err} }
}

func ownersCmd() tea.Cmd { return func() tea.Msg { return ownersMsg(ghOwners()) } }

func (m model) ownersOr() []string {
	if len(m.owners) == 0 {
		return []string{DefaultOwner}
	}
	return m.owners
}

// Retour à la ligne simple, par mots.
func wrap(s string, w int) []string {
	if w < 8 {
		w = 8
	}
	var out []string
	var cur []rune
	for _, word := range strings.Fields(s) {
		wr := []rune(word)
		if len(cur) > 0 && len(cur)+1+len(wr) > w {
			out = append(out, string(cur))
			cur = nil
		}
		if len(cur) > 0 {
			cur = append(cur, ' ')
		}
		cur = append(cur, wr...)
	}
	if len(cur) > 0 {
		out = append(out, string(cur))
	}
	return out
}

// ── Confirmation ────────────────────────────────────────────────────────

type confirmState struct {
	title string
	lines []string
	yes   func(m *model) tea.Cmd
}

func (m model) updateConfirm(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "o", "y", "enter":
		m.scr = scrDash
		yes := m.confirm.yes
		m.confirm = confirmState{}
		if yes != nil {
			cmd := yes(&m)
			return m, cmd
		}
	case "n", "esc", "q":
		m.scr = scrDash
		m.confirm = confirmState{}
	}
	return m, nil
}

// ── Moteur d'étapes ─────────────────────────────────────────────────────

type step struct {
	label, sub string
	run        func() (string, error)
	state      int // 0 attente, 1 en cours, 2 fait, 3 échec
	note       string
}

type stepsState struct {
	title []string
	list  []*step
	i     int
	gen   int
	err   error
	ok    bool
	hint  string // conseil affiché en cas d'échec
	after func(m *model) tea.Cmd
}

func (s stepsState) running() bool { return len(s.list) > 0 && !s.ok && s.err == nil }

var stepGen int

func (m *model) runSteps(title []string, list []*step, screen scr, hint string, after func(*model) tea.Cmd) tea.Cmd {
	stepGen++
	m.steps = stepsState{title: title, list: list, gen: stepGen, hint: hint, after: after}
	m.scr = screen
	return tea.Batch(m.nextStep(), spinTick())
}

func (m *model) nextStep() tea.Cmd {
	s := &m.steps
	if s.i >= len(s.list) {
		s.ok = true
		if s.after != nil {
			return s.after(m)
		}
		return nil
	}
	st := s.list[s.i]
	st.state, st.note = 1, ""
	gen, i, run := s.gen, s.i, st.run
	return func() tea.Msg {
		note, err := run()
		return stepMsg{gen: gen, i: i, note: note, err: err}
	}
}

func (m model) onStep(msg stepMsg) (tea.Model, tea.Cmd) {
	s := &m.steps
	if msg.gen != s.gen || msg.i != s.i || s.i >= len(s.list) {
		return m, nil
	}
	st := s.list[s.i]
	if msg.err != nil {
		st.state, st.note, s.err = 3, msg.err.Error(), msg.err
		return m, nil
	}
	st.state = 2
	if msg.note != "" {
		st.note = msg.note
	}
	s.i++
	cmd := m.nextStep()
	return m, cmd
}

func (m model) retryStep() (tea.Model, tea.Cmd) {
	m.steps.err = nil
	cmd := m.nextStep()
	return m, tea.Batch(cmd, spinTick())
}

func stepLines(list []*step, spin string, iw int) []Line {
	var ls []Line
	for _, st := range list {
		mk, lc := S(" ○", cDD), cD
		switch st.state {
		case 1:
			mk, lc = S(" "+spin, cAcc), cAcc
		case 2:
			mk, lc = S(" ✓", cOk), cFg
		case 3:
			mk, lc = S(" ✖", cErr), cErr
		}
		ls = append(ls, Ln(mk, S(" "+st.label, lc)))
		sub, sc := st.sub, cD
		if st.note != "" {
			sub = st.note
			if st.state == 3 {
				sc = cErr
			}
		}
		if sub != "" {
			for _, w := range wrap(sub, iw-4) {
				ls = append(ls, Ln(S("   "+w, sc)))
			}
		}
	}
	return ls
}

func (m model) updateSteps(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	s := m.steps
	switch k.String() {
	case "enter", "r":
		if s.err != nil {
			return m.retryStep()
		}
		if s.ok {
			m.scr = scrDash
		}
	case "esc", "q":
		if !s.running() {
			m.scr = scrDash
			return m, loadProjectsCmd()
		}
	}
	return m, nil
}

func (m model) viewSteps() []string {
	s := m.steps
	bw := min(m.w, 78)
	out := []string{crumb(s.title...).R(m.w, ""), Ln().R(m.w, "")}
	body := append([]Line{Ln()}, stepLines(s.list, m.spinner(), bw-2)...)
	body = append(body, Ln())
	rt := []seg{S(itoa(min(s.i, len(s.list)))+" / "+itoa(len(s.list)), cD)}
	bc := cBd
	if s.err != nil {
		bc = cErr
	}
	out = append(out, box(bw, len(body)+2, body, []seg{B("En cours", cFg)}, rt, bRound, bc)...)
	out = append(out, Ln().R(m.w, ""))
	switch {
	case s.err != nil:
		if s.hint != "" {
			for _, w := range wrap(s.hint, bw-4) {
				out = append(out, Ln(S("  "+w, cD)).R(m.w, ""))
			}
			out = append(out, Ln().R(m.w, ""))
		}
		out = append(out, keys(" ", m.w, "↵", "réessayer", "échap", "retour au menu").R(m.w, ""))
	case s.ok:
		out = append(out, keys(" ", m.w, "↵", "menu").R(m.w, ""))
	default:
		out = append(out, Ln(S("  "+m.spinner()+" patiente, ça ne prend que quelques secondes", cD)).R(m.w, ""))
	}
	return out
}

// ── Petites opérations sur le disque et git ─────────────────────────────

// Droits de groupe sur un dossier cloné : tout le groupe y écrit.
func shareTree(dir string) error {
	return filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.Type()&fs.ModeSymlink != 0 {
			return nil
		}
		fi, err := d.Info()
		if err != nil {
			return nil
		}
		mode := fi.Mode()
		want := mode.Perm() | 0o060
		if d.IsDir() {
			want |= 0o010
			_ = os.Chmod(p, want|os.ModeSetgid)
			return nil
		}
		if mode&0o100 != 0 {
			want |= 0o010
		}
		if want != mode.Perm() {
			_ = os.Chmod(p, want)
		}
		return nil
	})
}

func mkProjectDir(dir string) error {
	if exists(dir) {
		return errors.New(dir + " existe déjà")
	}
	if err := os.Mkdir(dir, 0o2775); err != nil {
		return errors.New("impossible de créer " + dir + " : " + permHint(err))
	}
	_ = os.Chmod(dir, 0o2775|os.ModeSetgid)
	return nil
}

func permHint(err error) string {
	if errors.Is(err, fs.ErrPermission) {
		return "droits refusés (groupe devs ? c pour vérifier)"
	}
	return err.Error()
}

func gitRun(dir string, args ...string) error {
	out, err := gitCmd(dir, args...).CombinedOutput()
	if err != nil {
		if l := lastLine(string(out)); l != "" {
			return errors.New(l)
		}
		return err
	}
	return nil
}

// git avec le compte GitHub de l'utilisateur (gh sert d'aide d'identification).
func gitNet(dir string, args ...string) error {
	pre := []string{}
	if gh, err := exec.LookPath("gh"); err == nil {
		pre = []string{"-c", "credential.https://github.com.helper=", "-c", "credential.https://github.com.helper=!" + gh + " auth git-credential"}
	}
	return gitRun(dir, append(pre, args...)...)
}

func gitInit(dir string) error {
	if err := gitRun(dir, "init", "-q", "-b", "main"); err != nil {
		return err
	}
	return gitRun(dir, "config", "core.sharedRepository", "group")
}

var templates = []string{"vide + CLAUDE.md", "Node · TypeScript", "Python"}

func writeTemplate(dir, name string, tmpl int) error {
	ignore := ".env\n.env.*\n!.env.example\n.DS_Store\n*.log\n"
	stack := ""
	switch tmpl {
	case 1:
		ignore += "node_modules/\ndist/\nbuild/\ncoverage/\n"
		stack = "\nStack : Node · TypeScript.\n"
	case 2:
		ignore += "__pycache__/\n*.pyc\n.venv/\n.pytest_cache/\n"
		stack = "\nStack : Python.\n"
	}
	files := map[string]string{
		"README.md":  "# " + name + "\n",
		".gitignore": ignore,
		"CLAUDE.md": "# " + name + "\n" + stack + "\nProjet partagé, dans " + dir + ".\n\n" +
			"- Messages de commit courts, en français.\n" +
			"- Jamais de secret dans le dépôt : ils vont dans .env (ignoré par git).\n",
	}
	for f, c := range files {
		p := filepath.Join(dir, f)
		if exists(p) {
			continue
		}
		if err := os.WriteFile(p, []byte(c), 0o664); err != nil {
			return err
		}
		_ = os.Chmod(p, 0o664)
	}
	return nil
}

// .gitignore de base pour un dossier qui n'était pas encore sous git.
func ensureGitignore(dir string) {
	p := filepath.Join(dir, ".gitignore")
	if !exists(p) {
		_ = os.WriteFile(p, []byte(".env\n.env.*\n!.env.example\n.DS_Store\n*.log\nnode_modules/\n__pycache__/\n.venv/\n"), 0o664)
	}
}

func firstCommit(dir string) (string, error) {
	if err := gitRun(dir, "add", "-A"); err != nil {
		return "", err
	}
	args := []string{"commit", "-q", "-m", "Premier commit"}
	note := ""
	if e, _ := gitOut(dir, "config", "user.email"); strings.TrimSpace(e) == "" {
		// Pas d'identité git : celle du compte GitHub connecté pour ce commit,
		// sinon une provisoire. La configuration (c) la règle pour de bon.
		name, email := meName, me+"@"+hostname()
		if u, err := ghUser(); err == nil {
			name, email = u.Login, u.ID+"+"+u.Login+"@users.noreply.github.com"
			if u.Name != "" {
				name = u.Name
			}
		}
		args = append([]string{"-c", "user.name=" + name, "-c", "user.email=" + email}, args...)
		note = "identité git pas réglée : c pour la configurer"
	}
	return note, gitRun(dir, args...)
}

// ── Ajouter un projet ───────────────────────────────────────────────────

type addState struct{ sel int }

func (m model) updateAdd(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	switch k.String() {
	case "left", "h", "shift+tab", "up", "k":
		m.add.sel = 0
	case "right", "l", "tab", "down", "j":
		m.add.sel = 1
	case "1", "g":
		m.add.sel = 0
		return m.addGo()
	case "2", "n":
		m.add.sel = 1
		return m.addGo()
	case "enter":
		return m.addGo()
	case "esc", "q":
		m.scr = scrDash
	}
	return m, nil
}

func (m model) addGo() (tea.Model, tea.Cmd) {
	if m.add.sel == 0 {
		return m.openGitHub()
	}
	return m.openNew()
}

const addCardW, addCardH, addTop = 35, 9, 4

func (m model) mouseAdd(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	if e.Button != tea.MouseButtonLeft || e.Action != tea.MouseActionPress {
		return m, nil
	}
	if e.Y < addTop || e.Y >= addTop+addCardH {
		return m, nil
	}
	switch {
	case e.X >= 3 && e.X < 3+addCardW:
		m.add.sel = 0
	case e.X >= 5+addCardW && e.X < 5+2*addCardW:
		m.add.sel = 1
	default:
		return m, nil
	}
	return m.addGo()
}

func (m model) viewAdd() []string {
	card := func(i int, title string, txt []string) []string {
		body := []Line{Ln(), Ln(B(" "+title, cFg)), Ln()}
		for _, t := range txt {
			body = append(body, Ln(S(" "+t, cD)))
		}
		if i == m.add.sel {
			return box(addCardW, addCardH, body, nil, nil, bHeavy, cAcc)
		}
		return box(addCardW, addCardH, body, nil, nil, bRound, cBd)
	}
	c1 := card(0, "J'ai déjà un repo GitHub", []string{"Tes repos et ceux de tes orgas,", "ou une URL collée. Clonage dans", Root + "/<nom>."})
	c2 := card(1, "Nouveau projet", []string{"Dossier et git init. Le repo", "GitHub se crée tout de suite", "ou plus tard, touche g."})
	out := []string{crumb("Ajouter un projet").R(m.w, ""), Ln().R(m.w, ""),
		Ln(P("   D'où part le projet ?")).R(m.w, ""), Ln().R(m.w, "")}
	out = append(out, indent(3, hcat(2, [][]string{c1, c2}, []int{addCardW, addCardW}))...)
	out = append(out, Ln().R(m.w, ""))
	out = append(out, keys("   ", m.w, "←→", "choisir", "↵", "valider", "échap", "retour").R(m.w, ""))
	if t, ok := m.toastLine(); ok {
		out = append(out, Ln().R(m.w, ""), t.R(m.w, ""))
	}
	return out
}

// ── Depuis GitHub ───────────────────────────────────────────────────────

type ghState struct {
	q        input
	tab      int
	tabs     []string
	list     []GHRepo
	cur, off int
	loading  bool
	err      string
}

func (m model) openGitHub() (tea.Model, tea.Cmd) {
	if !ghInstalled() {
		m.say("✖ gh (GitHub CLI) n'est pas installé sur le serveur", cErr)
		return m, nil
	}
	if !ghLoggedInFast() {
		m.say("◆ connecte d'abord ton compte GitHub", cWarn)
		return m.openSetup("github")
	}
	m.gh = ghState{loading: true}
	m.gh.q.ph = "chercher un repo, ou coller une URL"
	if len(m.ghRepos) == 0 {
		m.ghRepos = cachedGHRepos()
	}
	m.scr = scrGitHub
	m.ghRefilter()
	return m, tea.Batch(ghReposCmd(), spinTick())
}

func (m *model) ghRefilter() {
	g := &m.gh
	count := map[string]int{}
	for _, r := range m.ghRepos {
		count[r.Owner]++
	}
	tabs := []string{"Tous"}
	if count[DefaultOwner] > 0 {
		tabs = append(tabs, DefaultOwner)
	}
	var rest []string
	for o := range count {
		if o != DefaultOwner {
			rest = append(rest, o)
		}
	}
	sort.Slice(rest, func(i, j int) bool {
		if count[rest[i]] != count[rest[j]] {
			return count[rest[i]] > count[rest[j]]
		}
		return rest[i] < rest[j]
	})
	g.tabs = append(tabs, rest...)
	if g.tab >= len(g.tabs) {
		g.tab = 0
	}
	var pool []GHRepo
	for _, r := range m.ghRepos {
		if g.tab == 0 || r.Owner == g.tabs[g.tab] {
			pool = append(pool, r)
		}
	}
	q := strings.TrimSpace(g.q.val())
	var list []GHRepo
	switch {
	case strings.Contains(q, "/") && func() bool { o, _ := parseRepoInput(q); return o != "" }():
		o, n := parseRepoInput(q)
		found := false
		for _, r := range m.ghRepos {
			if strings.EqualFold(r.Full(), o+"/"+n) {
				list, found = append(list, r), true
				break
			}
		}
		if !found {
			list = append(list, GHRepo{Owner: o, Name: n, Desc: "URL collée"})
		}
	case q == "":
		for _, r := range pool {
			if !r.Archived {
				list = append(list, r)
			}
		}
	default:
		names := make([]string, len(pool))
		for i, r := range pool {
			names[i] = r.Name
		}
		for _, mt := range fuzzy.Find(q, names) {
			list = append(list, pool[mt.Index])
		}
	}
	g.list = list
	g.cur, g.off = 0, 0
}

func (m model) clonedProject(r GHRepo) *Project {
	for _, p := range m.projects {
		if strings.EqualFold(p.Owner, r.Owner) && strings.EqualFold(p.Repo, r.Name) {
			return p
		}
	}
	return nil
}

func (m model) ghListH() int { return max(3, m.h-13) }

func (m *model) ghFix() {
	g := &m.gh
	g.cur = max(0, min(g.cur, len(g.list)-1))
	h := m.ghListH()
	if g.cur < g.off {
		g.off = g.cur
	}
	if g.cur >= g.off+h {
		g.off = g.cur - h + 1
	}
}

func (m model) updateGitHub(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ghReposMsg:
		m.gh.loading = false
		if msg.err != nil {
			m.gh.err = msg.err.Error()
			return m, nil
		}
		m.ghRepos = msg.rs
		cur := m.gh.cur
		m.ghRefilter()
		m.gh.cur = cur
		m.ghFix()
		return m, nil
	case tea.KeyMsg:
		g := &m.gh
		switch msg.Type {
		case tea.KeyEsc:
			if g.q.val() != "" {
				g.q.set("")
				m.ghRefilter()
				return m, nil
			}
			m.scr = scrAdd
			return m, nil
		case tea.KeyUp:
			g.cur--
		case tea.KeyDown:
			g.cur++
		case tea.KeyPgUp:
			g.cur -= m.ghListH()
		case tea.KeyPgDown:
			g.cur += m.ghListH()
		case tea.KeyTab:
			g.tab = (g.tab + 1) % max(1, len(g.tabs))
			m.ghRefilter()
		case tea.KeyShiftTab:
			g.tab = (g.tab - 1 + max(1, len(g.tabs))) % max(1, len(g.tabs))
			m.ghRefilter()
		case tea.KeyEnter:
			return m.ghCloneSel()
		default:
			if g.q.key(msg) {
				m.ghRefilter()
			}
			return m, nil
		}
		m.ghFix()
	}
	return m, nil
}

func (m model) mouseGitHub(e tea.MouseMsg) (tea.Model, tea.Cmd) {
	if e.Action != tea.MouseActionPress {
		return m, nil
	}
	switch e.Button {
	case tea.MouseButtonWheelUp:
		m.gh.cur--
	case tea.MouseButtonWheelDown:
		m.gh.cur++
	case tea.MouseButtonLeft:
		r := e.Y - 7
		if r < 0 || r >= m.ghListH() || m.gh.off+r >= len(m.gh.list) {
			return m, nil
		}
		double := e.Y == m.lastClickY && time.Since(m.lastClickT) < 400*time.Millisecond
		m.lastClickY, m.lastClickT = e.Y, time.Now()
		was := m.gh.cur == m.gh.off+r
		m.gh.cur = m.gh.off + r
		if double && was {
			return m.ghCloneSel()
		}
	}
	m.ghFix()
	return m, nil
}

func (m model) ghCloneSel() (tea.Model, tea.Cmd) {
	g := m.gh
	if g.cur < 0 || g.cur >= len(g.list) {
		return m, nil
	}
	r := g.list[g.cur]
	if p := m.clonedProject(r); p != nil {
		m.scr = scrDash
		m.filter.set("")
		m.refilter()
		for i, pi := range m.view {
			if m.projects[pi] == p {
				m.cur = i
			}
		}
		m.fix()
		m.say("✓ "+r.Full()+" est déjà dans "+p.Dir, cOk)
		return m, nil
	}
	dest := filepath.Join(Root, r.Name)
	if exists(dest) {
		m.say("◆ "+dest+" existe déjà : renomme-le d'abord, ou lie-le avec g", cWarn)
		return m, nil
	}
	var sess string
	list := []*step{
		{label: "clone de " + r.Full(), sub: "→ " + dest, run: func() (string, error) {
			if err := ghClone(r.Owner, r.Name, dest); err != nil {
				return "", err
			}
			return "", nil
		}},
		{label: "droits partagés", sub: "groupe " + GroupName + ", lecture et écriture pour tous", run: func() (string, error) {
			return "", shareTree(dest)
		}},
		sessionStep(r.Name, dest, &sess),
	}
	cmd := m.runSteps([]string{"Ajouter un projet", "Depuis GitHub", r.Name}, list, scrSteps,
		"Repo privé d'une orga ? Vérifie que ton compte GitHub y a accès (c pour voir lequel est connecté).",
		func(m *model) tea.Cmd { return m.afterCreate(r.Name, dest, sess, "projet cloné") })
	return m, cmd
}

// Dernière étape commune : la session Claude du nouveau projet.
func sessionStep(name, dir string, out *string) *step {
	return &step{label: "session Claude", sub: "tmux " + Socket + ", Claude démarre dans " + name, run: func() (string, error) {
		p := &Project{Name: name, Dir: dir, Git: exists(filepath.Join(dir, ".git"))}
		loadGit(p)
		letter := freeLetter(listSessions(), name)
		if letter == "" {
			return "", errors.New("plus de lettre libre pour " + name)
		}
		s, err := newSession(p, letter, prodSet()[name])
		*out = s
		return name + " · " + letter, err
	}}
}

func (m *model) afterCreate(name, dir, sess, what string) tea.Cmd {
	m.selectDir = dir
	m.gitAt = time.Time{}
	if sess == "" {
		m.scr = scrDash
		return loadProjectsCmd()
	}
	return m.attach(sess, name+" · a · "+what+" · Ctrl-b d menu · Ctrl-b Espace basculer")
}

func (m model) viewGitHub() []string {
	g := m.gh
	bw := min(m.w, 100)
	iw := bw - 2
	stW, whenW, visW := 13, 14, 8
	nameW := iw - stW - whenW - visW
	body := []Line{Ln(S(" › ", cAcc)).Add(g.q.segs(true, cFg, true)...), Ln()}
	tl := Ln(Sp(1))
	for i, t := range g.tabs {
		if i == g.tab {
			tl = tl.Add(K(" " + t + " "))
		} else {
			tl = tl.Add(S("  "+t+" ", cD))
		}
		tl = tl.Add(Sp(1))
	}
	body = append(body, tl, Ln())
	h := m.ghListH()
	for i := 0; i < h; i++ {
		ri := g.off + i
		if ri >= len(g.list) {
			if i == 0 {
				switch {
				case g.err != "":
					body = append(body, Ln(S(" ✖ GitHub : "+g.err, cErr)))
				case g.loading:
					body = append(body, Ln(S(" "+m.spinner()+" lecture de tes repos GitHub…", cD)))
				default:
					body = append(body, Ln(S(" aucun repo ne correspond", cD)))
				}
				continue
			}
			body = append(body, Ln())
			continue
		}
		r := g.list[ri]
		sel := ri == g.cur
		mk := S(" ", cAcc)
		nm := P(r.Name)
		if sel {
			mk, nm = S("▌", cAcc), B(r.Name, cFg)
		}
		name := padSegs([]seg{mk, S(" "+r.Owner+"/", cD), nm}, nameW)
		vis := "public"
		if r.Private {
			vis = "privé"
		}
		if r.Desc == "URL collée" && r.URL == "" {
			vis = ""
		}
		var st []seg
		switch {
		case m.clonedProject(r) != nil:
			st = []seg{S("✓ déjà cloné", cOk)}
		case r.Archived:
			st = []seg{S("archivé", cDD)}
		case r.Desc == "URL collée" && r.URL == "":
			st = []seg{S("URL collée", cLnk)}
		}
		ln := Ln(name...).Add(padSegs([]seg{S(vis, cD)}, visW)...).
			Add(padSegs([]seg{S(relDate(r.Pushed), cD)}, whenW)...).Add(padSegs(st, stW)...)
		if sel {
			ln = ln.Sel()
		}
		body = append(body, ln)
	}
	body = append(body, Ln(), Ln(S("  ou colle une URL : ", cD), S("https://github.com/…", cLnk)))
	rt := []seg{S(itoa(len(g.list))+" sur "+itoa(len(m.ghRepos)), cD)}
	if g.loading {
		rt = append([]seg{S(m.spinner()+" ", cAcc)}, rt...)
	}
	out := []string{crumb("Ajouter un projet", "Depuis GitHub").R(m.w, ""), Ln().R(m.w, "")}
	out = append(out, box(bw, len(body)+2, body, []seg{B("Repos GitHub", cFg)}, rt, bRound, cBd)...)
	dest := Ln()
	if g.cur >= 0 && g.cur < len(g.list) {
		r := g.list[g.cur]
		switch p := m.clonedProject(r); {
		case p != nil:
			dest = Ln(S("  ✓ déjà dans "+p.Dir+" · ↵ le sélectionne", cOk))
		case exists(filepath.Join(Root, r.Name)):
			dest = Ln(S("  ◆ "+filepath.Join(Root, r.Name)+" existe déjà", cWarn))
		default:
			dest = Ln(S("  → "+filepath.Join(Root, r.Name)+", puis Claude dans une session tmux", cD))
		}
	}
	if t, ok := m.toastLine(); ok {
		dest = t
	}
	out = append(out, dest.R(m.w, ""), Ln().R(m.w, ""),
		keys(" ", m.w, "↵", "cloner", "↑↓", "choisir", "tab", "compte", "échap", "retour").R(m.w, ""))
	return out
}

// ── Nouveau projet ──────────────────────────────────────────────────────

type newState struct {
	name    input
	field   int // 0 nom, 1 modèle, 2 GitHub, 3 compte, 4 visibilité, 5 bouton
	tmpl    int
	ghNow   bool
	owner   int
	private bool
}

func (m model) openNew() (tea.Model, tea.Cmd) {
	m.nw = newState{ghNow: ghLoggedInFast(), private: true}
	m.nw.name.ph = "nom-du-projet"
	m.steps = stepsState{}
	m.scr = scrNew
	if len(m.owners) == 0 && ghLoggedInFast() {
		return m, ownersCmd()
	}
	return m, nil
}

func (m model) newName() string { return strings.TrimSpace(m.nw.name.val()) }

func (m model) newProblem() string {
	n := m.newName()
	switch {
	case n == "":
		return "donne un nom au projet"
	case !validName(n):
		return "lettres, chiffres, - _ . seulement"
	case exists(filepath.Join(Root, n)):
		return filepath.Join(Root, n) + " existe déjà"
	}
	return ""
}

func (m model) newFieldOn(f int) bool {
	if f == 3 || f == 4 {
		return m.nw.ghNow
	}
	return true
}

func (m model) newSteps(sess *string) []*step {
	n := m.nw
	name := m.newName()
	if name == "" {
		name = "<nom>"
	}
	dir := filepath.Join(Root, name)
	owners := m.ownersOr()
	owner := owners[min(n.owner, len(owners)-1)]
	tmpl, private := n.tmpl, n.private
	list := []*step{
		{label: "dossier créé", sub: dir, run: func() (string, error) { return "", mkProjectDir(dir) }},
		{label: "git init · main", run: func() (string, error) { return "", gitInit(dir) }},
		{label: "README, .gitignore, CLAUDE.md", run: func() (string, error) { return "", writeTemplate(dir, name, tmpl) }},
		{label: "premier commit", run: func() (string, error) { return firstCommit(dir) }},
	}
	if n.ghNow {
		vis := "privé"
		if !private {
			vis = "public"
		}
		list = append(list,
			&step{label: "repo GitHub " + vis, sub: owner + "/" + name, run: func() (string, error) {
				return "", ghCreate(owner, name, private, dir)
			}},
			&step{label: "premier push", run: func() (string, error) {
				return "", gitNet(dir, "push", "-q", "-u", "origin", "main")
			}})
	}
	return append(list, sessionStep(name, dir, sess))
}

func (m model) updateNew(msg tea.Msg) (tea.Model, tea.Cmd) {
	k, ok := msg.(tea.KeyMsg)
	if !ok {
		return m, nil
	}
	// Création lancée : l'écran suit les étapes.
	if len(m.steps.list) > 0 {
		switch k.String() {
		case "enter", "r":
			if m.steps.err != nil {
				return m.retryStep()
			}
		case "esc", "q":
			if !m.steps.running() {
				m.scr = scrDash
				m.selectDir = filepath.Join(Root, m.newName())
				return m, loadProjectsCmd()
			}
		}
		return m, nil
	}
	n := &m.nw
	move := func(d int) {
		for f := n.field + d; f >= 0 && f <= 5; f += d {
			if m.newFieldOn(f) {
				n.field = f
				return
			}
		}
	}
	change := func(d int) {
		switch n.field {
		case 1:
			n.tmpl = (n.tmpl + d + len(templates)) % len(templates)
		case 2:
			if ghLoggedInFast() {
				n.ghNow = !n.ghNow
			} else {
				m.say("◆ GitHub n'est pas connecté : c dans le menu, ou plus tard avec g", cWarn)
			}
		case 3:
			o := m.ownersOr()
			n.owner = (n.owner + d + len(o)) % len(o)
		case 4:
			n.private = !n.private
		}
	}
	switch k.Type {
	case tea.KeyEsc:
		m.scr = scrAdd
		return m, nil
	case tea.KeyTab, tea.KeyDown:
		move(1)
		return m, nil
	case tea.KeyShiftTab, tea.KeyUp:
		move(-1)
		return m, nil
	case tea.KeyEnter:
		if p := m.newProblem(); p != "" {
			m.say("◆ "+p, cWarn)
			n.field = 0
			return m, nil
		}
		var sess string
		name, dir := m.newName(), filepath.Join(Root, m.newName())
		list := m.newSteps(&sess)
		cmd := m.runSteps([]string{"Ajouter un projet", "Nouveau"}, list, scrNew,
			"Le dossier reste en place : tu peux réessayer, ou revenir au menu et donner le repo plus tard avec g.",
			func(m *model) tea.Cmd { return m.afterCreate(name, dir, sess, "nouveau projet") })
		return m, cmd
	}
	if n.field == 0 {
		if n.name.key(k) {
			v := strings.ReplaceAll(n.name.val(), " ", "-")
			pos := n.name.pos
			n.name.set(v)
			n.name.pos = min(pos, len(n.name.r))
		}
		return m, nil
	}
	switch k.String() {
	case "left", "h":
		change(-1)
	case "right", "l", " ":
		change(1)
	}
	return m, nil
}

func (m model) viewNew() []string {
	n := m.nw
	running := len(m.steps.list) > 0
	lab := func(f int, t string) seg {
		if !running && n.field == f {
			return B("  "+padRight(t, 11), cAcc)
		}
		if !m.newFieldOn(f) {
			return S("  "+padRight(t, 11), cDD)
		}
		return S("  "+padRight(t, 11), cD)
	}
	choice := func(f int, v string) []seg {
		ac, vc := cDD, cFg
		if !running && n.field == f {
			ac = cAcc
		}
		if !m.newFieldOn(f) {
			vc = cDD
		}
		return []seg{S("◂ ", ac), S(v, vc), S(" ▸", ac)}
	}
	name := m.newName()
	dirSeg := P(filepath.Join(Root, name))
	if p := m.newProblem(); p != "" {
		dirSeg = S(p, cErr)
		if name == "" {
			dirSeg = S(Root+"/…", cDD)
		}
	}
	ghl := Ln(lab(2, "GitHub"))
	on, off := S("◉", cAcc), S("○", cDD)
	if !n.ghNow {
		on, off = S("○", cDD), S("◉", cAcc)
	}
	ghl = ghl.Add(on, P(" maintenant   "), off, S(" plus tard", cD))
	if !ghLoggedInFast() {
		ghl = ghl.Add(S("  (non connecté)", cDD))
	}
	owners := m.ownersOr()
	vis := "privé"
	if !n.private {
		vis = "public"
	}
	btn := K("  Créer et lancer Claude  ")
	if !running && n.field == 5 {
		btn = Btn("  Créer et lancer Claude  ")
	}
	form := []Line{
		Ln(),
		Ln(lab(0, "Nom")).Add(n.name.segs(!running && n.field == 0, cFg, true)...),
		Ln(S("  Dossier    ", cD), dirSeg),
		Ln(),
		Ln(lab(1, "Modèle")).Add(choice(1, templates[n.tmpl])...),
		ghl,
		Ln(lab(3, "Compte")).Add(choice(3, owners[min(n.owner, len(owners)-1)])...),
		Ln(lab(4, "Visibilité")).Add(choice(4, vis)...),
		Ln(),
		Ln(Sp(2), btn),
		Ln(),
	}
	var sess string
	list := m.steps.list
	if !running {
		list = m.newSteps(&sess)
	}
	fw := 46
	pw := 31
	if m.w >= fw+1+38 {
		pw = 38
	}
	prog := append([]Line{Ln()}, stepLines(list, m.spinner(), pw-2)...)
	h := max(len(form), len(prog)) + 2
	ptitle := []seg{S("Après ↵", cD)}
	bc := cBd
	if running {
		ptitle = []seg{B("En cours", cFg)}
		if m.steps.err != nil {
			bc = cErr
		}
	}
	fbox := box(fw, h, form, []seg{B("Nouveau projet", cFg)}, nil, bRound, cBd)
	pbox := box(pw, h, prog, ptitle, nil, bRound, bc)
	out := []string{crumb("Ajouter un projet", "Nouveau").R(m.w, ""), Ln().R(m.w, "")}
	if m.w >= fw+1+pw {
		out = append(out, hcat(1, [][]string{fbox, pbox}, []int{fw, pw})...)
	} else {
		out = append(append(out, fbox...), pbox...)
	}
	out = append(out, Ln().R(m.w, ""))
	switch {
	case running && m.steps.err != nil:
		for _, w := range wrap(m.steps.hint, min(m.w, 78)-4) {
			out = append(out, Ln(S("  "+w, cD)).R(m.w, ""))
		}
		out = append(out, keys(" ", m.w, "↵", "réessayer", "échap", "retour au menu").R(m.w, ""))
	case running:
		out = append(out, Ln(S("  "+m.spinner()+" création en cours…", cD)).R(m.w, ""))
	default:
		if t, ok := m.toastLine(); ok {
			out = append(out, t.R(m.w, ""))
		}
		out = append(out, keys(" ", m.w, "tab", "champ suivant", "←→", "changer", "↵", "créer", "échap", "annuler").R(m.w, ""))
	}
	return out
}

func padRight(s string, n int) string {
	if r := []rune(s); len(r) < n {
		return s + strings.Repeat(" ", n-len(r))
	}
	return s
}

// ── Donner un repo GitHub à un projet ───────────────────────────────────

type linkState struct {
	p       *Project
	mode    int // 0 créer, 1 lier un repo existant
	field   int // 0 mode, 1 compte ou repo, 2 visibilité, 3 bouton
	owner   int
	private bool
	q       input
	sug     []GHRepo
	scur    int
}

func (m model) openLink(p *Project) (tea.Model, tea.Cmd) {
	if p == nil {
		return m, nil
	}
	if p.GitHub() {
		m.say("✓ "+p.Name+" est déjà lié à "+p.URL(), cOk)
		return m, nil
	}
	if !ghInstalled() {
		m.say("✖ gh (GitHub CLI) n'est pas installé sur le serveur", cErr)
		return m, nil
	}
	if !ghLoggedInFast() {
		m.say("◆ connecte d'abord ton compte GitHub", cWarn)
		return m.openSetup("github")
	}
	pp := *p
	m.link = linkState{p: &pp, private: true}
	m.link.q.ph = "owner/nom ou URL"
	m.steps = stepsState{}
	m.scr = scrLink
	if len(m.ghRepos) == 0 {
		m.ghRepos = cachedGHRepos()
	}
	m.linkSuggest()
	cmds := []tea.Cmd{ghReposCmd()}
	if len(m.owners) == 0 {
		cmds = append(cmds, ownersCmd())
	}
	return m, tea.Batch(cmds...)
}

func (m *model) linkSuggest() {
	l := &m.link
	q := strings.TrimSpace(l.q.val())
	if q == "" {
		q = l.p.Name
	}
	if strings.Contains(q, "/") {
		l.sug = nil
		return
	}
	names := make([]string, len(m.ghRepos))
	for i, r := range m.ghRepos {
		names[i] = r.Name
	}
	l.sug = l.sug[:0:0]
	for _, mt := range fuzzy.Find(q, names) {
		if r := m.ghRepos[mt.Index]; m.clonedProject(r) == nil {
			l.sug = append(l.sug, r)
		}
		if len(l.sug) == 4 {
			break
		}
	}
	l.scur = min(l.scur, max(0, len(l.sug)-1))
}

// Repo visé en mode « lier » : la saisie si elle a la forme owner/nom, sinon
// la suggestion choisie.
func (m model) linkTarget() (string, string) {
	l := m.link
	if o, n := parseRepoInput(l.q.val()); o != "" {
		return o, n
	}
	if l.scur < len(l.sug) {
		return l.sug[l.scur].Owner, l.sug[l.scur].Name
	}
	return "", ""
}

func (m model) updateLink(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case ghReposMsg:
		if msg.err == nil {
			m.ghRepos = msg.rs
			m.linkSuggest()
		}
		return m, nil
	case tea.KeyMsg:
		l := &m.link
		if len(m.steps.list) > 0 {
			switch msg.String() {
			case "enter", "r":
				if m.steps.err != nil {
					return m.retryStep()
				}
			case "esc", "q":
				if !m.steps.running() {
					m.scr = scrDash
					return m, loadProjectsCmd()
				}
			}
			return m, nil
		}
		last := 3
		next := func(d int) {
			f := l.field + d
			if l.mode == 1 && f == 2 {
				f += d
			}
			if f >= 0 && f <= last {
				l.field = f
			}
		}
		switch msg.Type {
		case tea.KeyEsc:
			m.scr = scrDash
			return m, nil
		case tea.KeyTab:
			next(1)
			return m, nil
		case tea.KeyShiftTab:
			next(-1)
			return m, nil
		case tea.KeyEnter:
			return m.linkGo()
		case tea.KeyUp:
			if l.field == 1 && l.mode == 1 && l.scur > 0 {
				l.scur--
				return m, nil
			}
			next(-1)
			return m, nil
		case tea.KeyDown:
			if l.field == 1 && l.mode == 1 && l.scur < len(l.sug)-1 {
				l.scur++
				return m, nil
			}
			next(1)
			return m, nil
		}
		if l.field == 1 && l.mode == 1 {
			if l.q.key(msg) {
				l.scur = 0
				m.linkSuggest()
			}
			return m, nil
		}
		d := 0
		switch msg.String() {
		case "left", "h":
			d = -1
		case "right", "l", " ":
			d = 1
		}
		if d == 0 {
			return m, nil
		}
		switch l.field {
		case 0:
			l.mode = 1 - l.mode
		case 1:
			o := m.ownersOr()
			l.owner = (l.owner + d + len(o)) % len(o)
		case 2:
			l.private = !l.private
		}
	}
	return m, nil
}

func (m model) linkGo() (tea.Model, tea.Cmd) {
	l := m.link
	p := l.p
	dir := p.Dir
	var list []*step
	if !p.Git || !p.HasCommits {
		if !p.Git {
			list = append(list, &step{label: "git init · main", run: func() (string, error) {
				ensureGitignore(dir)
				return "", gitInit(dir)
			}})
		}
		list = append(list, &step{label: "premier commit", sub: "tout le dossier, sauf ce que .gitignore exclut",
			run: func() (string, error) { return firstCommit(dir) }})
	}
	var target string
	if l.mode == 0 {
		owners := m.ownersOr()
		owner := owners[min(l.owner, len(owners)-1)]
		name, private := p.Name, l.private
		target = owner + "/" + name
		vis := "privé"
		if !private {
			vis = "public"
		}
		if p.Remote != "" {
			m.say("◆ origin pointe déjà vers "+p.Remote+" : retire-le d'abord (git remote remove origin)", cWarn)
			return m, nil
		}
		list = append(list,
			&step{label: "repo GitHub " + vis, sub: target, run: func() (string, error) {
				return "", ghCreate(owner, name, private, dir)
			}},
			&step{label: "premier push", run: func() (string, error) {
				b, _ := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD")
				return "", gitNet(dir, "push", "-q", "-u", "origin", strings.TrimSpace(b))
			}})
	} else {
		o, n := m.linkTarget()
		if o == "" {
			m.say("◆ choisis un repo dans la liste ou colle son URL", cWarn)
			return m, nil
		}
		target = o + "/" + n
		url := "https://github.com/" + target + ".git"
		hadRemote := p.Remote != ""
		list = append(list,
			&step{label: "remote origin", sub: "github.com/" + target, run: func() (string, error) {
				if hadRemote {
					return "", gitRun(dir, "remote", "set-url", "origin", url)
				}
				return "", gitRun(dir, "remote", "add", "origin", url)
			}},
			&step{label: "récupération (fetch)", run: func() (string, error) {
				return "", gitNet(dir, "fetch", "-q", "origin")
			}},
			&step{label: "suivi de branche", run: func() (string, error) { return trackRemote(dir) }})
	}
	cmd := m.runSteps([]string{"Projet " + p.Name, "GitHub"}, list, scrLink,
		"Rien n'est perdu : tu peux réessayer, ou revenir au menu.",
		func(m *model) tea.Cmd {
			m.say("✓ "+p.Name+" est lié à github.com/"+target, cOk)
			m.scr = scrDash
			m.selectDir = dir
			return loadProjectsCmd()
		})
	return m, cmd
}

// Après un fetch : brancher la branche locale sur celle du remote, ou la
// pousser si le remote ne l'a pas.
func trackRemote(dir string) (string, error) {
	b, err := gitOut(dir, "rev-parse", "--abbrev-ref", "HEAD")
	b = strings.TrimSpace(b)
	if err != nil || b == "HEAD" || b == "" {
		// Aucun commit local : on prend la branche par défaut du repo.
		def := "main"
		if out, err := gitOut(dir, "ls-remote", "--symref", "origin", "HEAD"); err == nil {
			if f := strings.Fields(firstLine(out)); len(f) >= 2 && f[0] == "ref:" {
				def = strings.TrimPrefix(f[1], "refs/heads/")
			}
		}
		return "branche " + def, gitRun(dir, "checkout", "-q", "-t", "origin/"+def)
	}
	if _, err := gitOut(dir, "rev-parse", "--verify", "-q", "refs/remotes/origin/"+b); err == nil {
		return b + " suit origin/" + b, gitRun(dir, "branch", "-q", "--set-upstream-to=origin/"+b)
	}
	return b + " poussée sur GitHub", gitNet(dir, "push", "-q", "-u", "origin", b)
}

func (m model) viewLink() []string {
	l := m.link
	p := l.p
	running := len(m.steps.list) > 0
	foc := func(f int) bool { return !running && l.field == f }
	lab := func(f int, t string) seg {
		if foc(f) {
			return B("  "+padRight(t, 11), cAcc)
		}
		return S("  "+padRight(t, 11), cD)
	}
	radio := func(on bool, t string) []seg {
		if on {
			return []seg{S("◉ ", cAcc), P(t)}
		}
		return []seg{S("○ ", cDD), S(t, cD)}
	}
	arrows := func(f int, v string) []seg {
		ac := cDD
		if foc(f) {
			ac = cAcc
		}
		return []seg{S("◂ ", ac), P(v), S(" ▸", ac)}
	}
	body := []Line{Ln(),
		Ln(S("  Projet     ", cD), B(p.Name, projColor(p.Name)), S("  "+p.Dir, cD)),
		Ln()}
	ml := Ln(lab(0, "GitHub")).Add(radio(l.mode == 0, "créer un repo")...).Add(Sp(4)).Add(radio(l.mode == 1, "lier un repo existant")...)
	body = append(body, ml, Ln())
	if l.mode == 0 {
		owners := m.ownersOr()
		vis := "privé"
		if !l.private {
			vis = "public"
		}
		body = append(body,
			Ln(lab(1, "Compte")).Add(arrows(1, owners[min(l.owner, len(owners)-1)])...),
			Ln(lab(2, "Visibilité")).Add(arrows(2, vis)...),
			Ln(S("  Nom        ", cD), P(p.Name)))
		if !p.Git {
			body = append(body, Ln(), Ln(S("  Pas encore de git ici : git init et premier commit d'abord (.gitignore de base).", cD)))
		} else if !p.HasCommits {
			body = append(body, Ln(), Ln(S("  Aucun commit : un premier commit de tout le dossier est fait d'abord.", cD)))
		}
	} else {
		body = append(body, Ln(lab(1, "Repo")).Add(l.q.segs(foc(1), cFg, true)...))
		o, n := parseRepoInput(l.q.val())
		switch {
		case o != "":
			body = append(body, Ln(S("             → github.com/"+o+"/"+n, cLnk)))
		case len(l.sug) == 0:
			body = append(body, Ln(S("             aucun repo à ce nom ; colle l'URL", cDD)))
		default:
			for i, r := range l.sug {
				ln := Ln(Sp(13), S(r.Owner+"/", cD), P(r.Name), S("  "+relDate(r.Pushed), cDD))
				if i == l.scur {
					ln = Ln(S("           ▌ ", cAcc), S(r.Owner+"/", cD), B(r.Name, cFg), S("  "+relDate(r.Pushed), cDD)).Sel()
				}
				body = append(body, ln)
			}
		}
	}
	label := "  Créer et pousser  "
	if l.mode == 1 {
		label = "  Lier  "
	}
	btn := K(label)
	if foc(3) {
		btn = Btn(label)
	}
	body = append(body, Ln(), Ln(Sp(2), btn), Ln())
	bw := min(m.w, 90)
	out := []string{crumb("Projet "+p.Name, "GitHub").R(m.w, ""), Ln().R(m.w, "")}
	if running {
		prog := append([]Line{Ln()}, stepLines(m.steps.list, m.spinner(), bw-2)...)
		prog = append(prog, Ln())
		bc := cBd
		if m.steps.err != nil {
			bc = cErr
		}
		out = append(out, box(bw, len(prog)+2, prog, []seg{B("En cours", cFg)}, nil, bRound, bc)...)
		out = append(out, Ln().R(m.w, ""))
		if m.steps.err != nil {
			for _, w := range wrap(m.steps.hint, bw-4) {
				out = append(out, Ln(S("  "+w, cD)).R(m.w, ""))
			}
			out = append(out, keys(" ", m.w, "↵", "réessayer", "échap", "retour au menu").R(m.w, ""))
		}
		return out
	}
	out = append(out, box(bw, len(body)+2, body, []seg{B("Donner un repo GitHub", cFg)}, nil, bRound, cBd)...)
	out = append(out, Ln().R(m.w, ""))
	if t, ok := m.toastLine(); ok {
		out = append(out, t.R(m.w, ""))
	}
	out = append(out, keys(" ", m.w, "tab", "champ suivant", "←→", "changer", "↵", "valider", "échap", "retour").R(m.w, ""))
	return out
}
