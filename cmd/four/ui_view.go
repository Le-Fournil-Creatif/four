package main

import (
	"strings"
	"time"
)

var spinFrames = []string{"◐", "◓", "◑", "◒"}

func (m model) spinner() string { return spinFrames[m.spin%len(spinFrames)] }

func (m model) View() string {
	if m.w == 0 || m.h == 0 {
		return ""
	}
	var rs []string
	switch m.scr {
	case scrHelp:
		rs = m.viewHelp()
	case scrConfirm:
		rs = m.viewConfirm()
	case scrAdd:
		rs = m.viewAdd()
	case scrGitHub:
		rs = m.viewGitHub()
	case scrNew:
		rs = m.viewNew()
	case scrLink:
		rs = m.viewLink()
	case scrSteps:
		rs = m.viewSteps()
	case scrSetup:
		rs = m.viewSetup()
	default:
		rs = m.viewDash()
	}
	return frame(rs, m.w, m.h)
}

// Fil d'Ariane des écrans secondaires : « four › Ajouter un projet › GitHub ».
func crumb(parts ...string) Line {
	l := Ln(B(" four", cAcc))
	for i, p := range parts {
		l = l.Add(S(" › ", cD))
		if i == len(parts)-1 {
			l = l.Add(B(p, cFg))
		} else {
			l = l.Add(S(p, cD))
		}
	}
	return l
}

// Bandeau de message (toast) ou d'activité, à la place d'une ligne vide.
func (m model) toastLine() (Line, bool) {
	if m.busy != "" {
		return Ln(S(" "+m.spinner()+" ", cAcc), S(m.busy+"…", cD)), true
	}
	if m.toast != "" && time.Since(m.toastAt) < 6*time.Second {
		return Ln(S(" "+m.toast, m.toastC)), true
	}
	return Ln(), false
}

// ── Tableau de bord ─────────────────────────────────────────────────────

func (m model) header() Line {
	left := []seg{B(" four", cAcc), S("  "+Root, cD)}
	if m.missing > 0 {
		left = append(left, S("   ◆ config à finir · c", cWarn))
	}
	var right []seg
	now := time.Now()
	for _, t := range teammates() {
		if firstActive(m.presence, t, now) != nil {
			right = append(right, S("●", cOk), S(" "+t+" en ligne", cD))
		} else {
			right = append(right, S("○", cDD), S(" "+t+" hors ligne", cDD))
		}
		right = append(right, Sp(5))
	}
	right = append(right, B(me, userColor(me)), Sp(5))
	right = append(right, usageSegs(m.usage)...)
	right = append(right, Sp(1))
	// Étroit : on lâche d'abord les coéquipiers, puis les jauges, puis l'usage.
	if segsW(left)+segsW(right)+1 > m.w {
		right = append(append([]seg{B(me, userColor(me)), Sp(3)}, usageSegs(m.usage)...), Sp(1))
	}
	if segsW(left)+segsW(right)+1 > m.w {
		right = append(append([]seg{B(me, userColor(me)), Sp(3)}, usageCompact(m.usage)...), Sp(1))
	}
	if segsW(left)+segsW(right)+1 > m.w {
		right = []seg{B(me, userColor(me)), Sp(1)}
	}
	return lr(left, right, m.w)
}

func (m model) viewDash() []string {
	l := m.lay()
	out := []string{m.header().R(m.w, "")}
	if t, ok := m.toastLine(); ok {
		out = append(out, t.R(m.w, ""))
	} else {
		out = append(out, Ln().R(m.w, ""))
	}
	p := m.sel()
	var body []string
	switch {
	case l.wide:
		lb := m.leftBox(l.lw, l.bh)
		rb := m.rightBox(p, l.rw, l.bh)
		body = hcat(0, [][]string{lb, rb}, []int{l.lw, l.rw})
	case m.focus == 1:
		body = m.rightBox(p, l.rw, l.bh)
	default:
		body = m.leftBox(l.lw, l.bh)
	}
	out = append(out, body...)
	out = append(out, Ln().R(m.w, ""))
	out = append(out, m.footer().R(m.w, ""))
	return out
}

func (m model) footer() Line {
	if m.filtring {
		return keys(" ", m.w, "↑↓", "choisir", "↵", "ouvrir", "tab", "garder le filtre", "échap", "effacer")
	}
	if m.focus == 1 {
		return keys(" ", m.w, "↵", "rejoindre", "a…z", "session", "n", "nouvelle", "x", "fermer", "←", "projets", "q", "quitter")
	}
	q := "quitter"
	if m.popup {
		q = "fermer"
	}
	return keys(" ", m.w, "↵", "ouvrir", "n", "session", "a", "ajouter", "g", "GitHub", "/", "chercher", "?", "aide", "q", q)
}

func (m model) leftBox(w, h int) []string {
	iw := w - 2
	l := m.lay()
	var body []Line
	fl := Ln(S(" › ", cAcc)).Add(m.filter.segs(m.filtring, cFg, true)...)
	if q := strings.TrimSpace(m.filter.val()); q != "" && !m.filtring {
		fl = fl.Add(S("   échap efface", cDD))
	}
	body = append(body, fl, Ln())
	rs := m.listRows()
	for i := 0; i < l.listH; i++ {
		ri := m.off + i
		if ri >= len(rs) {
			body = append(body, Ln())
			continue
		}
		r := rs[ri]
		if r.vi < 0 {
			body = append(body, Ln(S(" "+r.header, cDD)))
			continue
		}
		body = append(body, m.projectRow(m.projects[m.view[r.vi]], r.vi == m.cur, iw))
	}
	if len(m.view) == 0 {
		msg := " aucun projet dans " + Root
		if strings.TrimSpace(m.filter.val()) != "" {
			msg = " aucun projet ne correspond"
		}
		body[2] = Ln(S(msg, cD))
	}
	body = append(body, Ln(), lr([]seg{S(" + ", cAcc), S("Ajouter un projet", cAcc)}, []seg{S("a ", cD)}, iw))

	bc := cBd
	if m.focus == 0 && l.wide {
		bc = cDD
	}
	title := []seg{B("Projets", cFg), S(" "+itoa(len(m.projects)), cD)}
	if q := strings.TrimSpace(m.filter.val()); q != "" {
		title = []seg{B("Projets", cFg), S(" "+itoa(len(m.view))+" sur "+itoa(len(m.projects)), cD)}
	}
	var rt []seg
	if !l.wide {
		rt = []seg{S("→ détail", cDD)}
	}
	return box(w, h, body, title, rt, bRound, bc)
}

func (m model) projectRow(p *Project, selected bool, iw int) Line {
	pc := projColor(p.Name)
	mark := S(" ", cAcc)
	if selected {
		mark = S("▌", cAcc)
	}
	dot := S("● ", pc)
	if !p.GitHub() {
		dot = S("○ ", cD)
	}
	nameW := 19
	if iw >= 46 {
		nameW = 22
	}
	nm := append(padSegs([]seg{P(p.Name)}, nameW-1), Sp(1))
	if selected {
		nm = append(padSegs([]seg{B(p.Name, pc)}, nameW-1), Sp(1))
	}
	left := append([]seg{mark, dot}, nm...)
	left = append(left, m.badges(p)...)
	var right []seg
	if p.Branch != "" {
		right = append(right, S(p.Branch, cD))
		if p.Dirty+p.Untracked > 0 {
			right = append(right, S("*", cWarn))
		}
		right = append(right, Sp(1))
	}
	// La branche passe après les badges si la place manque.
	if segsW(left)+segsW(right)+2 > iw {
		right = nil
	}
	ln := lr(left, right, iw)
	if selected {
		ln = ln.Sel()
	}
	return ln
}

func (m model) badges(p *Project) []seg {
	if m.busyFor == p.Dir {
		return []seg{S(m.spinner()+" démarrage", cAcc)}
	}
	ss := m.sessionsOf(p)
	var wait, done, remote bool
	for _, s := range ss {
		remote = remote || s.Remote
		switch s.Mark {
		case "wait":
			wait = true
		case "done":
			if !s.Seen {
				done = true
			}
		}
	}
	var out []seg
	add := func(s ...seg) {
		if len(out) > 0 {
			out = append(out, Sp(2))
		}
		out = append(out, s...)
	}
	switch {
	case wait:
		add(S("◆", cWarn), S(" attend", cWarn))
	case done:
		add(S("✓", cOk), S(" cuit", cOk))
	case len(ss) > 1:
		add(S("●●", cOk), S(" "+itoa(len(ss))+" sessions", cD))
	case len(ss) == 1:
		add(S("●", cOk), S(" allumé", cD))
	}
	if remote {
		add(S("⇄ remote", cOk))
	}
	if m.prod[p.Name] {
		add(S("▲ prod", cErr))
	}
	seen := map[string]bool{}
	for _, o := range m.othersOn(p) {
		if !seen[o.User] {
			seen[o.User] = true
			add(S(o.User, userColor(o.User)))
		}
	}
	if len(out) == 0 {
		switch {
		case p.Loaded && !p.Git:
			add(S("◌ pas de git", cDD))
		case p.Loaded && !p.GitHub():
			add(S("◌ sans GitHub", cDD))
		}
	}
	return out
}

func (m model) rightBox(p *Project, w, h int) []string {
	if p == nil {
		body := []Line{Ln(), Ln(S(" Aucun projet pour l'instant.", cD)), Ln(),
			Ln(S(" a ", cAcc), P("ajoute un repo GitHub ou crée un nouveau projet"))}
		return box(w, h, body, []seg{B("four", cAcc)}, nil, bRound, cBd)
	}
	pc := projColor(p.Name)
	body, _ := m.rightLines(p, w-2, h-2)
	var rt []seg
	if p.Owner != "" {
		rt = []seg{S(p.Owner, cD)}
	} else if p.Legacy {
		rt = []seg{S(Legacy, cDD)}
	}
	bc := cBd
	if m.focus == 1 {
		bc = cDD
	}
	return box(w, h, body, []seg{S("● ", pc), B(p.Name, pc)}, rt, bRound, bc)
}

func sectionTitle(t string) Line { return Ln(B(" "+t, cD)) }

// Corps du panneau de droite ; hits : ligne (dans la boîte) → index de session
// (len(sessions) pour « nouvelle session »).
func (m model) rightLines(p *Project, iw, ih int) ([]Line, map[int]int) {
	hits := map[int]int{}
	if p == nil {
		return nil, hits
	}
	var ls []Line
	// URL, état git
	switch {
	case p.GitHub():
		ls = append(ls, Ln(S(" "+p.URL(), cLnk)))
	case p.Git && p.Remote != "":
		ls = append(ls, Ln(S(" "+p.Remote, cLnk)))
	case p.Git:
		ls = append(ls, Ln(S(" pas encore de repo GitHub · ", cD), K(" g "), S(" lier ou créer", cD)))
	case p.Loaded:
		ls = append(ls, Ln(S(" pas de git ici · ", cD), K(" g "), S(" créer le repo", cD)))
	default:
		ls = append(ls, Ln(S(" "+p.Dir, cD)))
	}
	if p.Git {
		st := Ln(S(" ", cFg))
		sep := S("  ·  ", cD)
		parts := 0
		addp := func(s ...seg) {
			if parts > 0 {
				st = st.Add(sep)
			}
			st = st.Add(s...)
			parts++
		}
		if p.Branch != "" {
			addp(P(p.Branch))
		}
		switch {
		case !p.Loaded:
			addp(S("lecture…", cDD))
		case !p.HasCommits:
			addp(S("aucun commit", cD))
		case p.Dirty > 0 && p.Untracked > 0:
			addp(S(plural(p.Dirty, "fichier modifié", "fichiers modifiés"), cWarn), S(" · "+plural(p.Untracked, "nouveau", "nouveaux"), cD))
		case p.Dirty > 0:
			addp(S(plural(p.Dirty, "fichier modifié", "fichiers modifiés"), cWarn))
		case p.Untracked > 0:
			addp(S(plural(p.Untracked, "nouveau fichier", "nouveaux fichiers"), cD))
		default:
			addp(S("propre", cOk))
		}
		if p.Ahead > 0 {
			addp(S(plural(p.Ahead, "commit à pousser", "commits à pousser"), cD))
		}
		if p.Behind > 0 {
			addp(S(plural(p.Behind, "commit à récupérer", "commits à récupérer"), cWarn))
		}
		if p.Loaded && p.HasCommits && !p.Upstream && p.GitHub() {
			addp(S("branche non suivie", cDD))
		}
		if m.prod[p.Name] {
			addp(S("▲ prod : Claude demande avant d'agir", cErr))
		}
		ls = append(ls, st)
	} else if m.prod[p.Name] {
		ls = append(ls, Ln(S(" ▲ prod : Claude demande avant d'agir", cErr)))
	}
	if p.Legacy {
		ls = append(ls, Ln(S(" "+p.Dir+" · ancien emplacement", cDD)))
	}

	// Sessions
	ls = append(ls, Ln(), sectionTitle("SESSIONS"))
	ss := m.sessionsOf(p)
	for i, s := range ss {
		hits[len(ls)] = i
		title := s.Title
		if title == "" {
			switch {
			case s.Running():
				title = "Claude"
			default:
				title = "shell"
			}
		}
		var right []seg
		switch {
		case s.Mark == "wait":
			right = []seg{S("◆", cWarn), S(" attend", cWarn), S(" · "+dur(s.MarkTime)+" ", cD)}
		case s.Mark == "done":
			c := cOk
			if s.Seen {
				c = cD
			}
			right = []seg{S("✓", c), S(" cuit · "+dur(s.MarkTime)+" ", cD)}
		case s.Running() && s.Mark == "busy":
			right = []seg{S("●", cAcc), S(" au four", cAcc), S(" · "+dur(s.MarkTime)+" ", cD)}
		case s.Running():
			right = []seg{S("●", cOk), S(" allumé · "+dur(s.Created)+" ", cD)}
		default:
			right = []seg{S("○", cDD), S(" éteint ", cD)}
		}
		if s.Remote {
			right = append([]seg{S("⇄ remote  ", cOk)}, right...)
		} else if s.Running() {
			right = append([]seg{S("remote off  ", cDD)}, right...)
		}
		if s.Attached > 0 {
			right = append([]seg{S("▣ ", cD)}, right...)
		}
		left := []seg{B(" "+s.Letter+"  ", cAcc), P(title)}
		ln := lr(left, right, iw)
		if m.focus == 1 && m.scur == i {
			ln = lr(append([]seg{S("▌", cAcc), B(s.Letter+"  ", cAcc)}, B(title, cFg)), right, iw).Sel()
		}
		ls = append(ls, ln)
	}
	hits[len(ls)] = len(ss)
	nl := lr([]seg{S(" +  ", cAcc), S("nouvelle session Claude", cD)}, []seg{S("n ", cD)}, iw)
	if m.focus == 1 && m.scur == len(ss) {
		nl = lr([]seg{S("▌", cAcc), S("+  ", cAcc), P("nouvelle session Claude")}, []seg{S("n ", cD)}, iw).Sel()
	}
	ls = append(ls, nl)

	// Équipe
	others := m.othersOn(p)
	team := teammates()
	if len(team) > 0 {
		ls = append(ls, Ln(), sectionTitle("ÉQUIPE"))
		for _, t := range team {
			var mine []Presence
			for _, o := range others {
				if o.User == t {
					mine = append(mine, o)
				}
			}
			uc := userColor(t)
			if len(mine) > 0 {
				o, on := mine[0], firstActive(mine, t, time.Now())
				if on != nil {
					o = *on
				}
				st := map[string]string{"busy": "au four", "done": "cuit", "wait": "attend"}[o.State]
				if st == "" {
					st = "allumé"
				}
				dot, c := "●", cOk
				if on == nil {
					dot, c = "○", cDD
				} else if o.State == "wait" {
					c = cWarn
				}
				extra := ""
				if len(mine) > 1 {
					extra = " (" + itoa(len(mine)) + " sessions)"
				}
				ls = append(ls, lr([]seg{S(" "+t, uc), S("  sur ce projet"+extra, cD)},
					[]seg{S(dot, c), S(" "+st+" · "+dur(o.Since)+" ", cD)}, iw))
				continue
			}
			// Ailleurs ?
			if e := firstActive(m.presence, t, time.Now()); e != nil {
				ls = append(ls, lr([]seg{S(" "+t, uc), S("  sur "+e.Project, cD)},
					[]seg{S("●", cOk), S(" Claude · "+dur(e.Since)+" ", cD)}, iw))
			} else {
				ls = append(ls, lr([]seg{S(" "+t, cD)}, []seg{S("○", cDD), S(" hors ligne ", cDD)}, iw))
			}
		}
	}

	// Commits et fichiers : ce qui tient dans la hauteur restante.
	d, ok := m.details[p.Dir]
	if p.Git && ok {
		room := ih - len(ls) - 2
		nc := len(d.Commits)
		nf := len(d.Changes)
		if nf > 0 {
			// Laisser de la place aux fichiers : au moins 3 commits, puis partage.
			maxC := room - 2 - min(nf, 6)
			if maxC < 3 {
				maxC = 3
			}
			nc = min(nc, maxC)
		}
		nc = min(nc, room)
		if nc > 0 {
			ls = append(ls, Ln(), sectionTitle("DERNIERS COMMITS"))
			for _, c := range d.Commits[:nc] {
				who := shortAuthor(c.Author, c.Email)
				cl := Ln(S(" "+c.Hash, cAcc), S("  ", cD))
				cl = cl.Add(padSegs([]seg{S(when(c.When), cD)}, 11)...).Add(Sp(2))
				cl = cl.Add(padSegs([]seg{S(who, userColor(who))}, 8)...).Add(Sp(2))
				ls = append(ls, cl.Add(P(c.Subject)))
			}
		} else if len(d.Commits) == 0 && p.HasCommits == false && p.Loaded {
			ls = append(ls, Ln(), Ln(S(" Pas encore de commit. ", cD), S("Claude peut faire le premier.", cDD)))
		}
		room = ih - len(ls) - 2
		if nf > 0 && room > 0 {
			ls = append(ls, Ln(), sectionTitle("FICHIERS MODIFIÉS"))
			show := min(nf, room)
			if show < nf {
				show--
			}
			for _, c := range d.Changes[:max(show, 0)] {
				col := cWarn
				switch c.Code {
				case "?":
					col = cD
				case "D":
					col = cErr
				case "A":
					col = cOk
				}
				ls = append(ls, Ln(S("  "+padCode(c.Code)+" ", col), P(c.Path)))
			}
			if show < nf {
				ls = append(ls, Ln(S("  … et "+itoa(nf-max(show, 0))+" autres", cDD)))
			}
		}
	}
	if len(ls) > ih {
		ls = ls[:ih]
	}
	return ls, hits
}

func padCode(c string) string {
	if len([]rune(c)) < 2 {
		return c + " "
	}
	return c
}

// ── Aide ────────────────────────────────────────────────────────────────

func (m model) viewHelp() []string {
	k := func(key, txt string) Line {
		return Ln(Sp(4), K(" "+key+" "), Sp(max(1, 8-len([]rune(key)))), P(txt))
	}
	h := func(t string) Line { return Ln(B("   "+t, cD)) }
	ls := []Line{crumb("Aide"), Ln(),
		h("PROJETS"),
		k("↵", "ouvrir : rejoint la dernière session, sinon lance Claude"),
		k("n", "nouvelle session Claude sur le projet (a, b, c…)"),
		k("→", "sessions du projet : ↵ rejoindre, lettre directe, x fermer"),
		k("/", "chercher (approximatif : « pgn » trouve pogne)"),
		k("a", "ajouter un projet : repo GitHub existant ou nouveau"),
		k("g", "donner un repo GitHub au projet (lier ou créer)"),
		k("r", "actualiser · c configuration (GitHub, Claude, clé SSH)"),
		Ln(),
		h("DANS UNE SESSION"),
		k("Ctrl-b Espace", "ce menu par-dessus la session, pour basculer"),
		k("Ctrl-b d", "revenir au menu (Claude continue de tourner)"),
		k("Ctrl-b x", "fermer la session"),
		Ln(),
		h("BON À SAVOIR"),
		Ln(S("    Les projets sont dans "+Root+", partagés par le groupe "+GroupName+".", cD)),
		Ln(S("    Chacun a ses sessions, son compte Claude et son GitHub. ✓ cuit et ◆ attend", cD)),
		Ln(S("    s'affichent dans la barre du bas, même depuis un autre projet.", cD)),
		Ln(S("    Claude demande avant d'agir, sauf pour les projets listés dans "+shareDir()+"/auto", cD)),
		Ln(S("    (* pour tous). ▲ prod (liste dans "+shareDir()+"/prod) l'emporte toujours.", cD)),
		Ln(S("    ⇄ remote : Remote Control est connecté sur cette session (/remote-control).", cD)),
		Ln(S("    four a son propre tmux (tmux -L "+Socket+").", cD)),
		Ln(),
		Ln(S("   four "+version+" · une touche pour revenir", cDD)),
	}
	return rows(ls, m.w)
}

// ── Confirmation ────────────────────────────────────────────────────────

func (m model) viewConfirm() []string {
	c := m.confirm
	body := []Line{Ln()}
	for _, l := range c.lines {
		body = append(body, Ln(S("  "+l, cD)))
	}
	body = append(body, Ln(), Ln(Sp(2), Btn("  o  oui  "), Sp(3), K("  n  non  ")), Ln())
	w := 0
	for _, l := range c.lines {
		w = max(w, len([]rune(l))+6)
	}
	w = max(w, len([]rune(c.title))+8, 44)
	w = min(w, m.w-6)
	out := []string{crumb("Confirmer").R(m.w, ""), Ln().R(m.w, "")}
	out = append(out, indent(3, box(w, len(body)+2, body, []seg{B(c.title, cWarn)}, nil, bRound, cWarn))...)
	out = append(out, Ln().R(m.w, ""), keys("   ", m.w, "o", "oui", "n", "non", "échap", "retour").R(m.w, ""))
	return out
}
