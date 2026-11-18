package main

import (
	"strings"
	"testing"
)

func names(ss []Session) string {
	var out []string
	for _, s := range ss {
		out = append(out, s.Name)
	}
	return strings.Join(out, " ")
}

// Dans l'ordre de listSessions : projet, puis lettre.
func sampleSessions() []Session {
	return []Session{
		{Name: "api_a", Project: "api", Letter: "a", Cmd: "claude", Mark: "wait", MarkTime: 300},
		{Name: "api_b", Project: "api", Letter: "b", Cmd: "claude", Mark: "busy", MarkTime: 100},
		{Name: "boutique_a", Project: "boutique", Letter: "a", Cmd: "claude", Mark: "busy", MarkTime: 50},
		{Name: "boutique_b", Project: "boutique", Letter: "b", Cmd: "claude", Mark: "done", MarkTime: 200},
		{Name: "docs_a", Project: "docs", Letter: "a", Cmd: "claude", Mark: "done", MarkTime: 10, Seen: true},
		{Name: "scripts_a", Project: "scripts", Letter: "a", Cmd: "bash"},
		{Name: "site_a", Project: "site", Letter: "a", Cmd: "claude", Mark: "wait", MarkTime: 400, Seen: true},
		{Name: "vieux_a", Project: "vieux", Letter: "a", Cmd: "bash", Mark: "busy", MarkTime: 5},
	}
}

func TestGroupSessions(t *testing.T) {
	g := groupSessionsAt(sampleSessions(), 1000)
	cases := []struct{ name, got, want string }{
		// Les attentes d'abord (même vues), la plus ancienne en tête, puis les « cuit » non vus.
		{"à traiter", names(g.todo), "api_a site_a boutique_b"},
		{"au four", names(g.busy), "api_b boutique_a"},
		// Cuit déjà vu, shell, et « busy » sans Claude (marque périmée) : au repos.
		{"au repos", names(g.idle), "docs_a scripts_a vieux_a"},
	}
	for _, c := range cases {
		if c.got != c.want {
			t.Errorf("%s = %q, attendu %q", c.name, c.got, c.want)
		}
	}
	// Une heure plus tard, l'attente vue (site_a) passe au repos ; pas celle
	// qu'on n'a pas encore vue (api_a).
	g = groupSessionsAt(sampleSessions(), 400+staleWait)
	if got := names(g.todo); got != "api_a boutique_b" {
		t.Errorf("à traiter après une heure = %q", got)
	}
	if got := names(g.idle); got != "docs_a scripts_a site_a vieux_a" {
		t.Errorf("au repos après une heure = %q", got)
	}
}

func TestNumbered(t *testing.T) {
	order := groupSessionsAt(sampleSessions(), 1000).order()
	n := numbered(order, "api_b")
	if _, ok := n["api_b"]; ok {
		t.Errorf("la session courante ne doit pas avoir de numéro")
	}
	want := map[string]int{"api_a": 1, "site_a": 2, "boutique_b": 3, "boutique_a": 4, "docs_a": 5}
	for name, k := range want {
		if n[name] != k {
			t.Errorf("numéro de %s = %d, attendu %d", name, n[name], k)
		}
	}
	var many []Session
	for _, c := range "abcdefghijkl" {
		many = append(many, Session{Name: "p_" + string(c), Cmd: "claude"})
	}
	if n := numbered(many, ""); len(n) != 9 || n["p_i"] != 9 {
		t.Errorf("au plus 9 numéros, de 1 à 9 : %v", n)
	}
}

func TestNextTodo(t *testing.T) {
	ss := sampleSessions()
	if s, ok := nextTodo(ss, "boutique_a"); !ok || s.Name != "api_a" {
		t.Errorf("prochaine = %q, attendu api_a", s.Name)
	}
	// site_a attend depuis longtemps et a été vue : on passe à boutique_b.
	if s, ok := nextTodo(ss, "api_a"); !ok || s.Name != "boutique_b" {
		t.Errorf("depuis api_a, prochaine = %q, attendu boutique_b", s.Name)
	}
	if _, ok := nextTodo(ss[1:3], "api_b"); ok {
		t.Errorf("rien à traiter attendu")
	}
}

func TestWaitText(t *testing.T) {
	cases := map[string]string{
		"Claude needs your permission to use Bash":   "Autoriser Bash ?",
		"Claude is waiting for your input":           "attend ta réponse",
		"  Une question\nsur deux lignes ":           "Une question sur deux lignes",
		"":                                           "",
		"Claude needs your permission to use  Write": "Autoriser Write ?",
	}
	for in, want := range cases {
		if got := waitText(in); got != want {
			t.Errorf("waitText(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestIsPanelCmd(t *testing.T) {
	if !isPanelCmd("'/usr/local/bin/four' _panel") {
		t.Error("le panneau n'est pas reconnu")
	}
	if isPanelCmd("'/usr/local/bin/four' _run") || isPanelCmd("") {
		t.Error("le pane de Claude est pris pour le panneau")
	}
}

// Le panneau tient toujours dans sa hauteur, et chaque clic vise une session.
func TestPanelLinesFit(t *testing.T) {
	ss := sampleSessions()
	for i := range ss {
		ss[i].Pane = "%" + itoa(i)
	}
	for _, h := range []int{3, 8, 14, 20, 40} {
		m := panelModel{w: panelW, h: h, sess: "boutique_a", ss: ss,
			msgs: map[string]string{"api_a": "Autoriser Bash ?"}}
		ls, hits := m.lines()
		if len(ls) != h {
			t.Errorf("h=%d : %d lignes", h, len(ls))
		}
		for y, name := range hits {
			if y < 0 || y >= h || name == "" {
				t.Errorf("h=%d : clic invalide %d → %q", h, y, name)
			}
		}
		if h == 40 {
			text := ""
			for _, l := range ls {
				for _, s := range l.segs {
					text += s.t
				}
				text += "\n"
			}
			for _, want := range []string{"À TRAITER", "AU FOUR", "AU REPOS", "▣ ici", "Autoriser Bash ?", "ctrl-b tab"} {
				if !strings.Contains(text, want) {
					t.Errorf("h=40 : %q absent du panneau\n%s", want, text)
				}
			}
		}
	}
}

// Au clavier : le curseur part sur la n° 1, reste dans la liste, et la
// surbrillance quitte la session courante tant que le panneau a le focus.
func TestPanelCursor(t *testing.T) {
	m := panelModel{sess: "api_b"}
	order := groupSessionsAt(sampleSessions(), 1000).order()
	if got := m.highlight(order); got != "api_b" {
		t.Errorf("sans focus, surbrillance sur %q, attendu api_b", got)
	}
	m.focused = true
	if got := m.cursorIn(order); got != "api_a" {
		t.Errorf("curseur de départ %q, attendu api_a", got)
	}
	if m.cursor = m.step(order, -1); m.cursor != "api_a" {
		t.Errorf("↑ en haut de liste : %q", m.cursor)
	}
	m.cursor = m.step(order, 1)
	m.cursor = m.step(order, 1)
	if m.cursor != "boutique_b" {
		t.Errorf("↓↓ : %q, attendu boutique_b", m.cursor)
	}
	m.cursor = "disparue"
	if got := m.cursorIn(order); got != "api_a" {
		t.Errorf("session disparue : curseur %q, attendu api_a", got)
	}
}
