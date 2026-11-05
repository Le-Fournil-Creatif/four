package main

// Commandes internes : four _run (le processus d'une session), four _notify
// (hooks de Claude), four _status (barre tmux).

import (
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

func findClaude() string {
	if p, err := exec.LookPath("claude"); err == nil {
		return p
	}
	for _, p := range []string{filepath.Join(home, ".local", "bin", "claude"), "/usr/local/bin/claude", "/usr/bin/claude"} {
		if exists(p) {
			return p
		}
	}
	return ""
}

func claudeArgs(auto bool) []string {
	if !auto {
		return nil // par défaut, Claude demande avant d'agir
	}
	return []string{"--dangerously-skip-permissions"}
}

func execShell(dir string) int {
	sh := os.Getenv("SHELL")
	if sh == "" {
		sh = "/bin/bash"
	}
	_ = os.Chdir(dir)
	err := syscall.Exec(sh, []string{filepath.Base(sh)}, os.Environ())
	fmt.Fprintln(os.Stderr, "four : shell impossible :", err)
	return 1
}

func runSession() int {
	proj := os.Getenv("FOUR_PROJECT")
	dir := os.Getenv("FOUR_DIR")
	sess := os.Getenv("FOUR_SESSION")
	letter := os.Getenv("FOUR_LETTER")
	if dir == "" {
		dir, _ = os.Getwd()
	}
	if proj == "" {
		proj = filepath.Base(dir)
	}
	auto := autoMode(proj)
	dim, rst := "\x1b[2m", "\x1b[0m"
	note := ""
	switch {
	case prodSet()[proj]:
		note = " · \x1b[31m▲ prod : Claude en mode normal, il demande avant d'agir" + rst + dim
	case auto:
		note = " · Claude agit sans demander de confirmation"
	}
	fmt.Printf("%s── %s · session %s%s%s\n", dim, proj, letter, note, rst)
	fmt.Print("\x1b]2;Claude\x1b\\") // titre du pane, en attendant celui de Claude

	claude := findClaude()
	if claude == "" {
		fmt.Println("Claude Code n'est pas installé pour ce compte : four setup l'installe, puis relance : four _run")
		return execShell(dir)
	}
	now := time.Now().Unix()
	if sess != "" {
		writePresence(Presence{User: me, Project: proj, Dir: dir, Session: sess, Pid: os.Getpid(),
			Start: procStart(os.Getpid()), State: "open", Since: now, Started: now})
		clearMark(sess)
	}
	cleanup := func() {
		if sess != "" {
			removePresence(me, sess)
			clearMark(sess)
		}
	}

	cmd := exec.Command(claude, claudeArgs(auto)...)
	cmd.Dir = dir
	cmd.Stdin, cmd.Stdout, cmd.Stderr = os.Stdin, os.Stdout, os.Stderr
	// Sans TMUX_PANE, les hooks d'autres outils basés sur tmux ne réagissent pas
	// dans les sessions four (sinon chaque notification arriverait en double).
	cmd.Env = envWithout(os.Environ(), "TMUX_PANE")

	sigs := make(chan os.Signal, 4)
	signal.Notify(sigs, syscall.SIGINT, syscall.SIGQUIT, syscall.SIGTSTP, syscall.SIGHUP, syscall.SIGTERM)
	if err := cmd.Start(); err != nil {
		cleanup()
		fmt.Println("Claude n'a pas démarré :", err)
		return execShell(dir)
	}
	go func() {
		for s := range sigs {
			if s == syscall.SIGHUP || s == syscall.SIGTERM {
				cleanup()
				_ = cmd.Process.Signal(s)
				os.Exit(0)
			}
		}
	}()
	_ = cmd.Wait()
	signal.Stop(sigs)
	cleanup()
	fmt.Printf("\n%s── Claude s'est arrêté. Shell dans %s · « claude » ou Ctrl-b Espace ↵ pour le relancer.%s\n", dim, dir, rst)
	return execShell(dir)
}

// ── Hooks de Claude ─────────────────────────────────────────────────────

func notify(kind string) {
	// L'entrée JSON du hook : la lire sans jamais bloquer.
	go func() { _, _ = io.Copy(io.Discard, os.Stdin) }()
	sess := os.Getenv("FOUR_SESSION")
	if sess == "" || !insideFour() {
		return // pas une session four : rien à faire
	}
	switch kind {
	case "clear":
		writeMark(sess, "busy")
		setPresenceState(me, sess, "busy")
	case "done", "wait":
		writeMark(sess, kind)
		setPresenceState(me, sess, kind)
		alertOthers(sess, kind)
	default:
		return
	}
	refreshClients()
}

type client struct{ name, session, tty string }

func clients() []client {
	out, err := tmuxOut("list-clients", "-F", "#{client_name}\x1f#{session_name}\x1f#{client_tty}")
	if err != nil || out == "" {
		return nil
	}
	var cs []client
	for _, l := range strings.Split(out, "\n") {
		f := strings.Split(l, "\x1f")
		if len(f) == 3 {
			cs = append(cs, client{f[0], f[1], f[2]})
		}
	}
	return cs
}

func sessLabel(s string) string {
	out, err := tmuxOut("display-message", "-p", "-t", "="+s+":", "#{@four_project}\x1f#{@four_letter}")
	if err != nil {
		return s
	}
	f := strings.Split(out, "\x1f")
	if len(f) < 2 || f[0] == "" {
		return s
	}
	if f[1] == "a" || f[1] == "" {
		return f[0]
	}
	return f[0] + " · " + f[1]
}

// Prévenir les terminaux qui regardent une autre session ; celui qui regarde
// celle-ci voit déjà, la marque passe directement en « vue ».
func alertOthers(sess, kind string) {
	label := sessLabel(sess)
	msg := "✓ " + label + " : c'est cuit · Ctrl-b Espace"
	if kind == "wait" {
		msg = "◆ " + label + " : Claude attend ta réponse · Ctrl-b Espace"
	}
	watched := false
	for _, c := range clients() {
		if c.session == sess {
			watched = true
			continue
		}
		_ = tmuxCmd("display-message", "-c", c.name, "-d", "6000", msg).Run()
		if f, err := os.OpenFile(c.tty, os.O_WRONLY, 0); err == nil {
			_, _ = f.WriteString("\a")
			f.Close()
		}
	}
	if watched {
		markSeen(sess)
	}
}

func refreshClients() {
	for _, c := range clients() {
		_ = tmuxCmd("refresh-client", "-t", c.name, "-S").Run()
	}
}

// ── Barre tmux ──────────────────────────────────────────────────────────

func statusLine(args []string) string {
	if len(args) < 2 || args[0] != "right" {
		return ""
	}
	sess := args[1]
	dir := ""
	if len(args) > 2 {
		dir = args[2]
	}
	project := ""
	if len(args) > 3 {
		project = args[3]
	}
	var b strings.Builder
	if dir != "" {
		p := &Project{Dir: dir, Git: true}
		loadGit(p)
		if p.Branch != "" {
			b.WriteString(" " + p.Branch)
			if p.Dirty > 0 {
				b.WriteString(" *" + itoa(p.Dirty))
			}
			if p.Ahead > 0 {
				b.WriteString(" ↑" + itoa(p.Ahead))
			}
			if p.Behind > 0 {
				b.WriteString(" ↓" + itoa(p.Behind))
			}
			b.WriteString(" ")
		}
	}
	if project != "" && prodSet()[project] {
		b.WriteString("#[fg=#5a1405,bold]▲ prod #[default]")
	}
	// Cette session est regardée : sa notification est vue.
	markSeen(sess)
	var notes []string
	for _, s := range listSessions() {
		if s.Name == sess || s.Seen {
			continue
		}
		label := s.Project
		if s.Letter != "a" {
			label += " " + s.Letter
		}
		switch s.Mark {
		case "done":
			notes = append(notes, "#[bg="+cBar2Bg+",fg="+cOk+"] ✓ "+label+" est cuit #[default]")
		case "wait":
			notes = append(notes, "#[bg="+cBar2Bg+",fg="+cWarn+"] ◆ "+label+" attend #[default]")
		}
	}
	if len(notes) > 0 {
		b.WriteString(notes[0])
		if len(notes) > 1 {
			b.WriteString("#[bg=" + cBar2Bg + ",fg=" + cD + "]+" + itoa(len(notes)-1) + " #[default]")
		}
	}
	b.WriteString(" ctrl-b espace = menu ")
	return b.String()
}
