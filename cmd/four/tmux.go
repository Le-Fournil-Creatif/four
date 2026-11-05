package main

// four a son propre serveur tmux (tmux -L four) : ses sessions ne croisent
// jamais celles d'un autre tmux, il a sa configuration (barre, Ctrl-b Espace)
// sans toucher ~/.tmux.conf, et il démarre avec le groupe devs.

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unicode"
)

const letters = "abcdefghijklmoprstuvwyz" // ni n, ni q, ni x : touches du menu

type Session struct {
	Name, Project, Dir, Letter string
	Cmd, Title                 string
	Created, Activity          int64
	Attached                   int
	Mark                       string // busy | done | wait
	MarkTime                   int64
	Seen                       bool
	Remote                     bool // Remote Control connecté
}

func (s Session) Running() bool {
	switch s.Cmd {
	case "four", "claude", "node":
		return true
	}
	return false
}

func (s Session) Shell() bool {
	switch s.Cmd {
	case "bash", "zsh", "sh", "fish", "dash":
		return true
	}
	return false
}

func confPath() string { return filepath.Join(stateDir(), "tmux.conf") }

func tmuxArgs(args ...string) []string {
	return append([]string{"-L", Socket, "-f", confPath()}, args...)
}

func tmuxCmd(args ...string) *exec.Cmd {
	c := exec.Command("tmux", tmuxArgs(args...)...)
	c.Env = envWithout(os.Environ(), "TMUX", "TMUX_PANE")
	return c
}

func tmuxOut(args ...string) (string, error) {
	out, err := tmuxCmd(args...).Output()
	return strings.TrimRight(string(out), "\n"), err
}

func envWithout(env []string, keys ...string) []string {
	out := env[:0:0]
outer:
	for _, e := range env {
		for _, k := range keys {
			if strings.HasPrefix(e, k+"=") {
				continue outer
			}
		}
		out = append(out, e)
	}
	return out
}

// Dans une session four (Ctrl-b Espace ou un shell d'une session) ?
func insideFour() bool {
	t := os.Getenv("TMUX")
	if t == "" {
		return false
	}
	sock := strings.Split(t, ",")[0]
	return filepath.Base(sock) == Socket
}

func insideOtherTmux() bool { return os.Getenv("TMUX") != "" && !insideFour() }

func tmuxConf() string {
	b := Bin
	return fmt.Sprintf(`# four : serveur tmux à part (tmux -L %[2]s). Fichier régénéré par four,
# mets tes réglages dans ~/.tmux.conf (lu en premier).
source-file -q ~/.tmux.conf
set -g mouse on
set -g history-limit 50000
set -sg escape-time 10
set -g focus-events on
set -s extended-keys on
set -as terminal-features 'xterm*:RGB:extkeys'
set -g default-terminal tmux-256color
set -g set-titles on
set -g set-titles-string '#{@four_project} · #{@four_letter} — four'
set -g status on
set -g status-interval 5
set -g status-position bottom
set -g window-status-format ''
set -g window-status-current-format ''
set -g status-left-length 90
set -g status-right-length 150
set -g status-style 'bg=#30271f,fg=#ece3d6'
set -g message-style 'bg=#f2a541,fg=#1b1713,bold'
set -g mode-style 'bg=#30271f,fg=#f2a541'
set -g popup-border-style 'fg=#4a3f35'
set -g popup-border-lines rounded
set -g display-time 4000
set -g status-left '#[bg=#{@four_dark},fg=#fff5e6,bold] #{@four_project} · #{@four_letter} #[bg=#{@four_color},fg=#1a1510,nobold]#{?#{==:#{pane_title},#{host}},, #{=/48/…:pane_title}} '
set -g status-right '#(%[1]s _status right #{q:session_name} #{q:pane_current_path} #{q:@four_project})#[bg=#{@four_dark},fg=#fff5e6,bold] %%H:%%M '
bind Space display-popup -E -w 90%% -h 85%% -T ' four ' '%[1]s --popup'
bind C-Space display-popup -E -w 90%% -h 85%% -T ' four ' '%[1]s --popup'
bind x confirm-before -p 'fermer la session #S ? (y/n)' kill-session
`, b, Socket)
}

// Écrit la configuration si elle a changé ; la recharge si le serveur tourne.
func writeConf() {
	want := tmuxConf()
	if cur, err := os.ReadFile(confPath()); err == nil && string(cur) == want {
		return
	}
	if os.WriteFile(confPath(), []byte(want), 0o644) == nil && serverRunning() {
		_ = tmuxCmd("source-file", confPath()).Run()
	}
}

func serverRunning() bool { return tmuxCmd("has-session").Run() == nil }

const fmtSess = "#{session_name}\x1f#{@four_project}\x1f#{@four_dir}\x1f#{@four_letter}\x1f" +
	"#{session_created}\x1f#{session_activity}\x1f#{session_attached}\x1f#{pane_current_command}\x1f#{pane_title}\x1f#{pane_pid}"

func listSessions() []Session {
	out, err := tmuxOut("list-sessions", "-F", fmtSess)
	if err != nil || out == "" {
		return nil
	}
	var ss []Session
	remote := remoteParents(filepath.Join(home, ".claude", "sessions"))
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\x1f")
		if len(f) < 10 || f[1] == "" {
			continue
		}
		s := Session{Name: f[0], Project: f[1], Dir: f[2], Letter: f[3], Cmd: f[7], Title: cleanTitle(f[8])}
		s.Created, _ = strconv.ParseInt(f[4], 10, 64)
		s.Activity, _ = strconv.ParseInt(f[5], 10, 64)
		s.Attached, _ = strconv.Atoi(f[6])
		s.Mark, s.MarkTime, s.Seen = readMark(s.Name)
		panePid, _ := strconv.Atoi(f[9])
		s.Remote = remote[panePid]
		ss = append(ss, s)
	}
	sort.Slice(ss, func(i, j int) bool {
		if ss[i].Project != ss[j].Project {
			return ss[i].Project < ss[j].Project
		}
		return strings.Index(letters, ss[i].Letter) < strings.Index(letters, ss[j].Letter)
	})
	return ss
}

// Titre que Claude donne au terminal (« ✳ Refonte du panier ») sans l'icône.
func cleanTitle(t string) string {
	t = strings.TrimLeftFunc(t, func(r rune) bool { return !unicode.IsLetter(r) && !unicode.IsDigit(r) })
	t = strings.TrimSpace(t)
	h, _ := os.Hostname()
	switch strings.ToLower(t) {
	case "", "claude", "claude code", strings.ToLower(h), "bash", "four":
		return ""
	}
	return t
}

func sessName(project, letter string) string {
	r := strings.NewReplacer(".", "_", ":", "_", " ", "_")
	return r.Replace(project) + "_" + letter
}

func freeLetter(ss []Session, project string) string {
	used := map[string]bool{}
	for _, s := range ss {
		if s.Project == project {
			used[s.Letter] = true
		}
	}
	for _, c := range letters {
		if !used[string(c)] {
			return string(c)
		}
	}
	return ""
}

// Nouvelle session : Claude démarre dans le projet (via four _run, qui tient
// la présence à jour et laisse un shell quand Claude s'arrête).
func newSession(p *Project, letter string, prod bool) (string, error) {
	name := sessName(p.Name, letter)
	writeConf()
	args := tmuxArgs("new-session", "-d", "-s", name, "-c", p.Dir,
		"-e", "FOUR_PROJECT="+p.Name, "-e", "FOUR_DIR="+p.Dir,
		"-e", "FOUR_SESSION="+name, "-e", "FOUR_LETTER="+letter,
		shQuote(Bin)+" _run")
	var c *exec.Cmd
	g := lookupGroup(GroupName)
	if g.ok && g.has(me) && !procHasGid(g.gid) && !serverRunning() {
		// Ajouté au groupe devs depuis la connexion : le serveur tmux le prend
		// quand même, sinon Claude ne pourrait pas écrire dans /srv/repos.
		q := make([]string, len(args)+1)
		q[0] = "tmux"
		for i, a := range args {
			q[i+1] = shQuote(a)
		}
		c = exec.Command("sg", GroupName, "-c", strings.Join(q, " "))
	} else {
		c = exec.Command("tmux", args...)
	}
	c.Env = envWithout(os.Environ(), "TMUX", "TMUX_PANE")
	if out, err := c.CombinedOutput(); err != nil {
		return "", fmt.Errorf("tmux : %s", firstLine(string(out)))
	}
	if err := decorate(name, p.Name, p.Dir, letter, prod); err != nil {
		_ = tmuxCmd("kill-session", "-t", "="+name).Run()
		return "", fmt.Errorf("tmux : %v", err)
	}
	return name, nil
}

func decorate(name, project, dir, letter string, prod bool) error {
	pc := projColor(project)
	pr := ""
	if prod {
		pr = "1"
	}
	t := "=" + name + ":"
	out, err := tmuxCmd(
		"set-option", "-t", t, "@four_project", project, ";",
		"set-option", "-t", t, "@four_dir", dir, ";",
		"set-option", "-t", t, "@four_letter", letter, ";",
		"set-option", "-t", t, "@four_prod", pr, ";",
		"set-option", "-t", t, "@four_color", pc, ";",
		"set-option", "-t", t, "@four_dark", darken(pc, 0.72), ";",
		"set-option", "-t", t, "status-style", "bg="+pc+",fg="+cBarFg,
	).CombinedOutput()
	if err != nil {
		return errors.New(firstLine(string(out)))
	}
	return nil
}

// Commande pour rejoindre une session. Hors tmux : attach (le menu revient
// au détachement). Dans four : on bascule le terminal. Dans un autre tmux :
// attach imbriqué.
func attachCmd(name, msg string) (*exec.Cmd, bool) {
	if insideFour() {
		cl := currentClient()
		sw := []string{"switch-client"}
		dm := []string{"display-message"}
		if cl != "" {
			sw = append(sw, "-c", cl)
			dm = append(dm, "-c", cl)
		}
		args := append(append(sw, "-t", "="+name, ";"), append(dm, "-d", "4000", msg)...)
		return tmuxCmd(args...), true
	}
	return tmuxCmd("attach-session", "-t", "="+name, ";", "display-message", "-d", "4000", msg), false
}

// Le terminal qui a ouvert ce menu (Ctrl-b Espace) : TMUX est gardé pour que
// tmux le retrouve, même depuis une popup.
func currentClient() string {
	c := exec.Command("tmux", tmuxArgs("display-message", "-p", "#{client_name}")...)
	out, err := c.Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func killSession(name string) error {
	return tmuxCmd("kill-session", "-t", "="+name).Run()
}

// Claude a été quitté et le panneau est revenu au shell : on le relance.
func relaunch(name string) error {
	return tmuxCmd("send-keys", "-t", "="+name+":", "exec "+shQuote(Bin)+" _run", "Enter").Run()
}

// ── Marques : où en est Claude dans chaque session ─────────────────────

func markFile(s string) string { return filepath.Join(markDir(), s) }

func readMark(s string) (state string, t int64, seen bool) {
	f := markFile(s)
	b, err := os.ReadFile(f)
	if err != nil {
		return "", 0, false
	}
	fi, err := os.Stat(f)
	if err == nil {
		t = fi.ModTime().Unix()
	}
	p := strings.Fields(string(b))
	if len(p) == 0 {
		return "", t, false
	}
	return p[0], t, len(p) > 1 && p[1] == "seen"
}

func writeMark(s, state string) {
	_ = os.WriteFile(markFile(s), []byte(state+"\n"), 0o644)
}

// Vu : la notification s'éteint, l'état reste affiché.
func markSeen(s string) {
	st, t, seen := readMark(s)
	if st == "" || seen || st == "busy" {
		return
	}
	_ = os.WriteFile(markFile(s), []byte(st+" seen\n"), 0o644)
	if t > 0 {
		tm := time.Unix(t, 0)
		_ = os.Chtimes(markFile(s), tm, tm)
	}
}

func clearMark(s string) { _ = os.Remove(markFile(s)) }

// Ajouté au groupe devs depuis la connexion SSH : le processus n'a pas encore
// le groupe, donc /srv/repos est en lecture seule. On se relance via sg, qui
// garde l'environnement et le terminal.
func regainGroup() {
	if os.Getenv("FOUR_SG") != "" {
		return
	}
	g := lookupGroup(GroupName)
	if !g.ok || !g.has(me) || procHasGid(g.gid) {
		return
	}
	sg, err := exec.LookPath("sg")
	if err != nil {
		return
	}
	q := []string{"exec", shQuote(Bin)}
	for _, a := range os.Args[1:] {
		q = append(q, shQuote(a))
	}
	_ = syscall.Exec(sg, []string{"sg", GroupName, "-c", strings.Join(q, " ")}, append(os.Environ(), "FOUR_SG=1"))
}
