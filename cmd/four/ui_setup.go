package main

// Assistant de configuration (première connexion, puis touche c) : clé SSH,
// groupe devs, GitHub, identité git, Claude, notifications.

import (
	"bufio"
	"encoding/json"
	"errors"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	tea "github.com/charmbracelet/bubbletea"
)

const (
	stUnknown = iota
	stOK
	stTodo
	stRun
	stErr
	stSkip
)

type sItem struct {
	key, label string
	state      int
	info       string // à droite du libellé
	err        string
}

type setupState struct {
	items    []sItem
	cur      int
	checked  bool
	focus    string
	sshIn    input
	sshEdit  bool
	code     string
	codeURL  string
	flow     *exec.Cmd
	flowCh   chan tea.Msg
	flowDone bool
}

type (
	setupCheckMsg []sItem
	setupActMsg   struct {
		key  string
		err  error
		info string
	}
	ghCodeMsg          struct{ code, url string }
	ghFlowMsg          struct{ err error }
	claudeInstalledMsg struct{}
)

const claudeMissing = "pas installé pour ce compte · ↵ l'installe"

// Installeur officiel, pour le compte en cours (~/.local), dans le terminal :
// on voit la progression. La connexion au compte Claude suit.
const claudeInstall = `set -o pipefail
printf '\n  Installation de Claude Code pour %s (installeur officiel claude.ai)\n\n' "$USER"
if curl -fsSL https://claude.ai/install.sh | bash; then
  printf '\n  ✓ Claude Code installé. Connexion à ton compte Claude…\n\n'
  sleep 1
else
  printf '\n  ✖ Installation interrompue. Entrée pour revenir à four. '
  read -r _
fi
`

func claudeLoginCmd(c string) tea.Cmd {
	return tea.ExecProcess(exec.Command(c, "auth", "login"), func(err error) tea.Msg {
		ok, info := claudeStatus()
		if !ok {
			return setupActMsg{key: "claude", err: errors.New("pas encore connecté : ↵ pour recommencer")}
		}
		return setupActMsg{key: "claude", info: info}
	})
}

var setupLabels = []struct{ key, label, todo string }{
	{"ssh", "Compte SSH", "ta clé publique, pour te connecter sans mot de passe"},
	{"group", "Accès projets", "groupe devs : écrire dans " + Root},
	{"github", "GitHub", "ton compte, pour cloner et créer les repos"},
	{"ident", "Identité git", "remplie depuis ton compte GitHub"},
	{"claude", "Claude", "connexion à ton abonnement"},
	{"notif", "Notifications", "quand Claude a fini ou attend, et limites de l'abonnement"},
}

func newSetupState() setupState {
	s := setupState{}
	for _, l := range setupLabels {
		s.items = append(s.items, sItem{key: l.key, label: l.label, info: l.todo})
	}
	s.sshIn.ph = "ssh-ed25519 AAAA… nom@machine"
	return s
}

func (s setupState) waiting() bool {
	if !s.checked || s.flow != nil {
		return true
	}
	for _, it := range s.items {
		if it.state == stRun {
			return true
		}
	}
	return false
}

func dismissFile() string { return filepath.Join(stateDir(), "setup-plus-tard") }

// ── Vérifications ───────────────────────────────────────────────────────

func authorizedKeys() []string {
	f, err := os.Open(filepath.Join(home, ".ssh", "authorized_keys"))
	if err != nil {
		return nil
	}
	defer f.Close()
	var out []string
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 64*1024), 64*1024)
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" || strings.HasPrefix(l, "#") {
			continue
		}
		f := strings.Fields(l)
		// options éventuelles devant le type de clé
		for i, x := range f {
			if isKeyType(x) {
				c := "clé " + strings.TrimPrefix(strings.TrimPrefix(x, "ssh-"), "sk-")
				if i+2 < len(f) {
					c += " « " + strings.Join(f[i+2:], " ") + " »"
				}
				out = append(out, c)
				break
			}
		}
	}
	return out
}

func isKeyType(s string) bool {
	return strings.HasPrefix(s, "ssh-") || strings.HasPrefix(s, "ecdsa-sha2-") || strings.HasPrefix(s, "sk-")
}

func gitGlobal(k string) string {
	out, _ := exec.Command("git", "config", "--global", k).Output()
	return strings.TrimSpace(string(out))
}

func claudeCreds() bool {
	return exists(filepath.Join(home, ".claude", ".credentials.json")) // sans lire les jetons
}

func settingsPath() string { return filepath.Join(home, ".claude", "settings.json") }

// Les hooks et une ligne d'état sont en place ? Une ligne d'état déjà choisie
// par l'utilisateur compte : four ne la remplace pas.
func hooksInstalled() bool {
	b, err := os.ReadFile(settingsPath())
	if err != nil || !strings.Contains(string(b), "four _notify") {
		return false
	}
	var d struct {
		StatusLine json.RawMessage `json:"statusLine"`
	}
	return json.Unmarshal(b, &d) == nil && d.StatusLine != nil
}

func rootWritable() bool { return syscall.Access(Root, 2) == nil }

// Sans réseau : ce qu'il reste à régler, pour l'en-tête et le premier lancement.
func quickSetupMissing() int {
	n := 0
	if len(authorizedKeys()) == 0 {
		n++
	}
	if !lookupGroup(GroupName).has(me) {
		n++
	}
	if !ghLoggedInFast() {
		n++
	}
	if gitGlobal("user.email") == "" {
		n++
	}
	if !claudeCreds() {
		n++
	}
	if !hooksInstalled() {
		n++
	}
	return n
}

func setupCheckCmd() tea.Cmd {
	return func() tea.Msg {
		items := newSetupState().items
		set := func(k string, state int, info string) {
			for i := range items {
				if items[i].key == k {
					items[i].state = state
					if info != "" {
						items[i].info = info
					}
				}
			}
		}
		if ks := authorizedKeys(); len(ks) > 0 {
			info := ks[len(ks)-1]
			if len(ks) > 1 {
				info = plural(len(ks), "clé", "clés") + " · dernière : " + strings.TrimPrefix(info, "clé ")
			}
			set("ssh", stOK, info)
		} else {
			set("ssh", stTodo, "")
		}
		g := lookupGroup(GroupName)
		switch {
		case g.has(me) && rootWritable():
			set("group", stOK, "groupe devs · "+Root)
		case g.has(me):
			set("group", stOK, "groupe devs · actif à ta prochaine connexion SSH")
		default:
			set("group", stTodo, "")
		}
		if ghLoggedInFast() {
			if u, err := ghUser(); err == nil {
				set("github", stOK, "connecté : "+u.Login)
			} else {
				set("github", stTodo, "connexion à refaire ("+firstLine(err.Error())+")")
			}
		} else {
			set("github", stTodo, "")
		}
		if n, e := gitGlobal("user.name"), gitGlobal("user.email"); n != "" && e != "" {
			set("ident", stOK, n+" <"+e+">")
		} else {
			set("ident", stTodo, "")
		}
		if ok, info := claudeStatus(); ok {
			set("claude", stOK, info)
		} else {
			set("claude", stTodo, info)
		}
		if hooksInstalled() {
			set("notif", stOK, "✓ cuit et ◆ attend dans la barre et le menu")
		} else {
			set("notif", stTodo, "")
		}
		return setupCheckMsg(items)
	}
}

func claudeStatus() (bool, string) {
	c := findClaude()
	if c == "" {
		return false, claudeMissing
	}
	out, err := exec.Command(c, "auth", "status").Output()
	if err != nil && len(out) == 0 {
		if claudeCreds() {
			return true, "connecté"
		}
		return false, ""
	}
	var st struct {
		LoggedIn bool   `json:"loggedIn"`
		Email    string `json:"email"`
		Sub      string `json:"subscriptionType"`
	}
	if json.Unmarshal(out, &st) != nil {
		return claudeCreds(), ""
	}
	if !st.LoggedIn {
		return false, ""
	}
	info := "connecté"
	if st.Email != "" {
		info += " : " + st.Email
	}
	if st.Sub != "" {
		info += " · " + st.Sub
	}
	return true, info
}

// ── Actions ─────────────────────────────────────────────────────────────

func addAuthorizedKey(k string) error {
	k = strings.TrimSpace(k)
	f := strings.Fields(k)
	if len(f) < 2 || !isKeyType(f[0]) {
		return errors.New("ça ne ressemble pas à une clé publique (ssh-ed25519 AAAA…)")
	}
	if strings.Contains(k, "PRIVATE KEY") {
		return errors.New("c'est une clé privée : colle le fichier .pub")
	}
	dir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	_ = os.Chmod(dir, 0o700)
	p := filepath.Join(dir, "authorized_keys")
	if b, err := os.ReadFile(p); err == nil && strings.Contains(string(b), f[1]) {
		return errors.New("cette clé est déjà installée")
	}
	fh, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o600)
	if err != nil {
		return err
	}
	defer fh.Close()
	_ = os.Chmod(p, 0o600)
	_, err = fh.WriteString(k + "\n")
	return err
}

func joinGroup() error {
	out, err := exec.Command("sudo", "-n", "usermod", "-aG", GroupName, me).CombinedOutput()
	if err != nil {
		return errors.New("sans sudo : demande « sudo usermod -aG " + GroupName + " " + me + " » (" + firstLine(string(out)) + ")")
	}
	return nil
}

func setIdentity() (string, error) {
	u, err := ghUser()
	if err != nil {
		return "", errors.New("GitHub d'abord : " + err.Error())
	}
	name := u.Name
	if name == "" {
		name = u.Login
	}
	email := u.ID + "+" + u.Login + "@users.noreply.github.com"
	if err := exec.Command("git", "config", "--global", "user.name", name).Run(); err != nil {
		return "", err
	}
	if err := exec.Command("git", "config", "--global", "user.email", email).Run(); err != nil {
		return "", err
	}
	return name + " <" + email + ">", nil
}

const hookMerge = `
import json, os, sys
p, bin = sys.argv[1], sys.argv[2]
d = {}
if os.path.exists(p):
    with open(p, encoding="utf-8") as f:
        d = json.load(f)
hooks = d.setdefault("hooks", {})
for ev, arg in (("Stop", "done"), ("Notification", "wait"), ("UserPromptSubmit", "clear")):
    lst = hooks.setdefault(ev, [])
    cmd = bin + " _notify " + arg
    if any(h.get("command") == cmd for e in lst for h in e.get("hooks", [])):
        continue
    lst.append({"hooks": [{"type": "command", "command": cmd}]})
d.setdefault("statusLine", {"type": "command", "command": bin + " _statusline"})
tmp = p + ".four.tmp"
with open(tmp, "w", encoding="utf-8") as f:
    json.dump(d, f, indent=2, ensure_ascii=False)
    f.write("\n")
os.replace(tmp, p)
`

// Ajoute les hooks de four à ~/.claude/settings.json, sans toucher aux autres
// (chacun ne réagit que dans ses propres sessions).
func installHooks() error {
	_ = os.MkdirAll(filepath.Join(home, ".claude"), 0o700)
	if b, err := os.ReadFile(settingsPath()); err == nil {
		_ = os.WriteFile(settingsPath()+".avant-four", b, 0o600)
	}
	out, err := exec.Command("python3", "-I", "-c", hookMerge, settingsPath(), Bin).CombinedOutput()
	if err != nil {
		return errors.New("settings.json : " + lastLine(string(out)))
	}
	return nil
}

var reCode = regexp.MustCompile(`([A-Z0-9]{4}-[A-Z0-9]{4})`)

// Connexion GitHub par code (github.com/login/device) : gh tourne sans
// terminal, on lit le code dans sa sortie.
func (m *model) startGHFlow() tea.Cmd {
	s := &m.setup
	c := ghCmd("auth", "login", "-h", "github.com", "-p", "https", "-w", "-s", "workflow")
	c.Stdin = nil
	pr, pw := io.Pipe()
	c.Stdout, c.Stderr = pw, pw
	ch := make(chan tea.Msg, 4)
	if err := c.Start(); err != nil {
		return func() tea.Msg { return ghFlowMsg{err} }
	}
	s.flow, s.flowCh, s.code, s.codeURL = c, ch, "", "github.com/login/device"
	go func() {
		sc := bufio.NewScanner(pr)
		var last string
		sent := false
		for sc.Scan() {
			l := sc.Text()
			if strings.TrimSpace(l) != "" {
				last = strings.TrimSpace(l)
			}
			if !sent {
				if mm := reCode.FindStringSubmatch(l); mm != nil {
					ch <- ghCodeMsg{code: mm[1], url: "github.com/login/device"}
					sent = true
				}
			}
		}
		_ = last
	}()
	go func() {
		err := c.Wait()
		pw.Close()
		if err == nil {
			_ = ghCmd("auth", "setup-git", "-h", "github.com").Run()
		}
		ch <- ghFlowMsg{err}
		close(ch)
	}()
	return waitCh(ch)
}

func waitCh(ch chan tea.Msg) tea.Cmd {
	return func() tea.Msg {
		v, ok := <-ch
		if !ok {
			return nil
		}
		return v
	}
}

func (m *model) stopGHFlow() {
	if f := m.setup.flow; f != nil && f.Process != nil {
		_ = f.Process.Kill()
	}
	m.setup.flow = nil
	m.setup.code = ""
}

// ── Écran ───────────────────────────────────────────────────────────────

func (m model) openSetup(focus string) (tea.Model, tea.Cmd) {
	m.setup = newSetupState()
	m.setup.focus = focus
	m.scr = scrSetup
	return m, tea.Batch(setupCheckCmd(), spinTick())
}

func (s *setupState) item(k string) *sItem {
	for i := range s.items {
		if s.items[i].key == k {
			return &s.items[i]
		}
	}
	return &sItem{}
}

func (s *setupState) nextTodo(from int) {
	for i := from; i < len(s.items); i++ {
		if st := s.items[i].state; st == stTodo || st == stErr {
			s.cur = i
			return
		}
	}
	for i := 0; i < from && i < len(s.items); i++ {
		if st := s.items[i].state; st == stTodo || st == stErr {
			s.cur = i
			return
		}
	}
	s.cur = len(s.items) // tout est réglé
}

func (s setupState) doneCount() int {
	n := 0
	for _, it := range s.items {
		if it.state == stOK {
			n++
		}
	}
	return n
}

func actCmd(key string, f func() (string, error)) tea.Cmd {
	return func() tea.Msg { info, err := f(); return setupActMsg{key: key, err: err, info: info} }
}

func (m model) updateSetup(msg tea.Msg) (tea.Model, tea.Cmd) {
	s := &m.setup
	switch msg := msg.(type) {
	case setupCheckMsg:
		cur := s.cur
		s.items = msg
		first := !s.checked
		s.checked = true
		m.missing = 0
		for _, it := range s.items {
			if it.state != stOK {
				m.missing++
			}
		}
		if first {
			s.nextTodo(0)
			if s.focus != "" {
				for i, it := range s.items {
					if it.key == s.focus {
						s.cur = i
					}
				}
			}
		} else {
			s.cur = cur
			if s.cur < len(s.items) && s.items[s.cur].state == stOK {
				s.nextTodo(s.cur)
			}
		}
		if s.cur < len(s.items) && s.items[s.cur].key == "ssh" && s.items[s.cur].state == stTodo {
			s.sshEdit = true
		}
		return m, nil
	case setupActMsg:
		it := s.item(msg.key)
		if msg.err != nil {
			it.state, it.err = stErr, msg.err.Error()
			return m, nil
		}
		it.state, it.err = stOK, ""
		if msg.info != "" {
			it.info = msg.info
		}
		s.sshEdit = false
		s.nextTodo(s.cur)
		return m, tea.Batch(setupCheckCmd(), spinTick())
	case ghCodeMsg:
		s.code, s.codeURL = msg.code, msg.url
		return m, tea.Batch(waitCh(s.flowCh), spinTick())
	case ghFlowMsg:
		s.flow = nil
		s.code = ""
		it := s.item("github")
		if msg.err != nil {
			it.state, it.err = stErr, "connexion GitHub interrompue"
			return m, nil
		}
		it.state, it.err = stOK, ""
		// L'identité git suit directement.
		if id := s.item("ident"); id.state != stOK {
			id.state = stRun
			return m, tea.Batch(actCmd("ident", setIdentity), spinTick())
		}
		s.nextTodo(s.cur)
		return m, setupCheckCmd()
	case claudeInstalledMsg:
		it := s.item("claude")
		c := findClaude()
		if c == "" {
			it.state, it.err = stErr, "installation interrompue : ↵ pour réessayer"
			return m, nil
		}
		it.info = "installé"
		return m, claudeLoginCmd(c)
	case attachedMsg:
		return m, nil
	case tea.KeyMsg:
		return m.keySetup(msg)
	}
	return m, nil
}

func (m model) keySetup(k tea.KeyMsg) (tea.Model, tea.Cmd) {
	s := &m.setup
	leave := func() (tea.Model, tea.Cmd) {
		m.stopGHFlow()
		m.scr = scrDash
		m.missing = quickSetupMissing()
		return m, loadProjectsCmd()
	}
	if s.sshEdit && s.cur < len(s.items) && s.items[s.cur].key == "ssh" {
		switch k.Type {
		case tea.KeyEnter:
			v := s.sshIn.val()
			if strings.TrimSpace(v) == "" {
				return m, nil
			}
			s.items[s.cur].state = stRun
			return m, tea.Batch(actCmd("ssh", func() (string, error) {
				if err := addAuthorizedKey(v); err != nil {
					return "", err
				}
				ks := authorizedKeys()
				return ks[len(ks)-1] + " · teste une connexion avant de fermer celle-ci", nil
			}), spinTick())
		case tea.KeyEsc:
			s.sshEdit = false
			return m, nil
		case tea.KeyTab, tea.KeyDown:
			s.sshEdit = false
		default:
			s.sshIn.key(k)
			return m, nil
		}
	}
	switch k.String() {
	case "q", "esc":
		_ = os.WriteFile(dismissFile(), []byte("1\n"), 0o644)
		return leave()
	case "up", "k":
		if s.cur > 0 {
			s.cur--
		}
		m.stopGHFlow()
	case "down", "j", "tab":
		if s.cur < len(s.items) {
			s.cur++
		}
		m.stopGHFlow()
	case "s":
		if s.cur < len(s.items) {
			if s.items[s.cur].state != stOK {
				s.items[s.cur].state = stSkip
			}
			m.stopGHFlow()
			s.nextTodo(s.cur + 1)
		}
	case "a":
		if s.cur < len(s.items) && s.items[s.cur].key == "ssh" {
			s.sshEdit = true
		}
	case "r":
		return m, tea.Batch(setupCheckCmd(), spinTick())
	case "enter":
		if s.cur >= len(s.items) {
			_ = os.Remove(dismissFile())
			return leave()
		}
		it := &s.items[s.cur]
		if it.state == stOK || it.state == stRun {
			if it.state == stOK {
				s.nextTodo(s.cur + 1)
			}
			return m, nil
		}
		it.err = ""
		switch it.key {
		case "ssh":
			s.sshEdit = true
		case "group":
			it.state = stRun
			return m, tea.Batch(actCmd("group", func() (string, error) {
				return "groupe devs · actif à ta prochaine connexion SSH", joinGroup()
			}), spinTick())
		case "github":
			if !ghInstalled() {
				it.state, it.err = stErr, "gh (GitHub CLI) n'est pas installé"
				return m, nil
			}
			if s.flow != nil {
				return m, nil
			}
			it.state = stRun
			cmd := m.startGHFlow()
			return m, tea.Batch(cmd, spinTick())
		case "ident":
			it.state = stRun
			return m, tea.Batch(actCmd("ident", setIdentity), spinTick())
		case "claude":
			it.state = stRun
			if c := findClaude(); c != "" {
				return m, claudeLoginCmd(c)
			}
			return m, tea.ExecProcess(exec.Command("bash", "-c", claudeInstall),
				func(error) tea.Msg { return claudeInstalledMsg{} })
		case "notif":
			it.state = stRun
			return m, tea.Batch(actCmd("notif", func() (string, error) {
				return "✓ cuit et ◆ attend dans la barre et le menu", installHooks()
			}), spinTick())
		}
	}
	return m, nil
}

func (m model) viewSetup() []string {
	s := m.setup
	title := "Configuration"
	sub := "   Chaque compte est à toi : ta clé, ton GitHub, ton abonnement Claude."
	if !exists(dismissFile()) && m.missing >= 3 {
		title = "Bienvenue " + meName
		sub = "   Première connexion : on règle tout une fois, ensuite c'est le menu."
	}
	bw := min(m.w, 86)
	iw := bw - 2
	var body []Line
	body = append(body, Ln())
	for i, it := range s.items {
		cur := i == s.cur
		var mk seg
		lc := cFg
		switch it.state {
		case stOK:
			mk = S("✓", cOk)
		case stRun:
			mk = S(m.spinner(), cAcc)
		case stErr:
			mk = S("✖", cErr)
		case stSkip:
			mk, lc = S("–", cDD), cD
		case stUnknown:
			mk, lc = S("·", cDD), cD
		default:
			mk, lc = S("○", cDD), cD
		}
		if cur && it.state != stOK && it.state != stRun && it.state != stErr {
			mk = S("◆", cAcc)
		}
		lab := S(padRight(it.label, 16), lc)
		if cur {
			lab = B(padRight(it.label, 16), cFg)
		}
		info := S(it.info, cD)
		if it.state == stTodo || it.state == stSkip || it.state == stUnknown {
			info = S(it.info, cDD)
		}
		mark := S("  ", cAcc)
		if cur {
			mark = S(" ▌", cAcc)
		}
		ln := Ln(mark, mk, Sp(1), lab, info)
		if cur {
			ln = ln.Sel()
		}
		body = append(body, ln)
		if cur {
			body = append(body, m.setupDetail(it, iw)...)
		}
	}
	body = append(body, Ln())
	n := s.doneCount()
	barW := iw - 14
	fill := barW * n / len(s.items)
	body = append(body, Ln(Sp(3), S(strings.Repeat("━", fill), cAcc), S(strings.Repeat("━", barW-fill), cDD),
		S("  "+itoa(n)+" / "+itoa(len(s.items)), cD)), Ln())
	out := []string{crumb(title).R(m.w, ""), Ln(S(sub, cD)).R(m.w, ""), Ln().R(m.w, "")}
	rt := []seg{}
	if !s.checked {
		rt = []seg{S(m.spinner()+" vérification", cD)}
	}
	out = append(out, box(bw, len(body)+2, body, nil, rt, bRound, cBd)...)
	out = append(out, Ln().R(m.w, ""))
	switch {
	case s.cur >= len(s.items) && n == len(s.items):
		out = append(out, Ln(S("  ✓ tout est prêt.", cOk)).R(m.w, ""),
			keys(" ", m.w, "↵", "aller au menu").R(m.w, ""))
	case s.cur >= len(s.items):
		out = append(out, keys(" ", m.w, "↵", "aller au menu", "↑", "revenir sur une étape", "r", "revérifier").R(m.w, ""))
	case s.sshEdit:
		out = append(out, keys(" ", m.w, "↵", "ajouter la clé", "tab", "étape suivante", "échap", "annuler").R(m.w, ""))
	default:
		out = append(out, keys(" ", m.w, "↵", "continuer", "s", "passer l'étape", "↑↓", "étapes", "q", "plus tard").R(m.w, ""))
	}
	return out
}

// Lignes sous l'étape en cours : ce qui va se passer, le code GitHub, la
// saisie de clé, l'erreur.
func (m model) setupDetail(it sItem, iw int) []Line {
	s := m.setup
	pad := Sp(21)
	var ls []Line
	txt := func(t, c string) {
		for _, w := range wrap(t, iw-23) {
			ls = append(ls, Ln(pad, S(w, c)))
		}
	}
	if it.err != "" {
		txt(it.err, cErr)
	}
	switch it.key {
	case "ssh":
		if s.sshEdit || it.state != stOK {
			ls = append(ls, Ln(pad, S("› ", cAcc)).Add(s.sshIn.segs(s.sshEdit, cFg, false)...))
			txt("sur ton PC : cat ~/.ssh/id_ed25519.pub, puis colle la ligne ici", cDD)
		} else {
			txt("a pour ajouter une autre clé", cDD)
		}
	case "group":
		if it.state != stOK {
			txt("↵ t'ajoute au groupe devs (sudo)", cDD)
		}
	case "github":
		switch {
		case s.code != "":
			ls = append(ls, Ln(pad, S("ouvre ", cFg), S(s.codeURL, cLnk), S(" et tape", cFg)),
				Ln(pad, Btn(" "+s.code+" "), Sp(3), S(m.spinner(), cAcc), S(" en attente…", cD)))
		case s.flow != nil:
			ls = append(ls, Ln(pad, S(m.spinner()+" demande d'un code à GitHub…", cD)))
		case it.state != stOK:
			txt("↵ affiche un code à saisir sur github.com, depuis n'importe quel navigateur", cDD)
		}
	case "ident":
		if it.state != stOK {
			txt("↵ prend ton nom GitHub et ton adresse noreply (tes commits sont signés de ton compte)", cDD)
		}
	case "claude":
		switch {
		case it.state == stOK:
		case it.info == claudeMissing:
			txt("↵ installe Claude Code pour ce compte (installeur officiel, ~350 Mo), puis te connecte à ton compte Claude", cDD)
		default:
			txt("↵ lance « claude auth login » : un lien à ouvrir, puis un code à recopier ici", cDD)
		}
	case "notif":
		if it.state != stOK {
			txt("↵ ajoute les hooks et la ligne d'état de four dans ~/.claude/settings.json (les autres restent)", cDD)
		}
	}
	return ls
}
