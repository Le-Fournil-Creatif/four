package main

// Rendu : palette « Atelier » (direction A de la planche), lignes composées de
// segments colorés, boîtes à bord arrondi, assemblage en colonnes. Les séquences
// ANSI sont écrites à la main (cache par couleur) : c'est ce qui garde le rendu
// instantané, même plein écran à chaque touche.

import (
	"os"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"github.com/muesli/termenv"
)

const (
	cBg      = "#1b1713"
	cFg      = "#ece3d6"
	cD       = "#9b8f81"
	cDD      = "#5c5249"
	cBd      = "#4a3f35"
	cAcc     = "#f2a541"
	cSel     = "#30271f"
	cOk      = "#98c66e"
	cWarn    = "#e9c45c"
	cErr     = "#f07f5a"
	cLnk     = "#dcbc8c"
	cKbg     = "#3b3229"
	cKfg     = "#f5ebde"
	cBarFg   = "#1a1510"
	cBarHiFg = "#fff5e6"
	cBar2Bg  = "#2a231c"
)

var projColors = [...]string{"#e9b35f", "#f07f5a", "#72b6e3", "#c59ee3", "#6dc8a2", "#d9c86a",
	"#e891b2", "#d1a874", "#a6c189", "#92aad8", "#e49d7f"}

// Couleur d'un projet : dérivée du nom, donc la même pour tout le groupe et
// dans la barre tmux.
func projColor(name string) string {
	h := uint32(2166136261)
	for i := 0; i < len(name); i++ {
		h ^= uint32(name[i])
		h *= 16777619
	}
	return projColors[h%uint32(len(projColors))]
}

func userColor(u string) string { return projColor(u) }

// Assombrit une couleur #rrggbb (fond du libellé dans la barre tmux).
func darken(hex string, f float64) string {
	if len(hex) != 7 {
		return hex
	}
	var out [3]byte
	for i := 0; i < 3; i++ {
		v := hexByte(hex[1+2*i : 3+2*i])
		out[i] = byte(float64(v) * f)
	}
	const digits = "0123456789abcdef"
	b := []byte{'#'}
	for _, v := range out {
		b = append(b, digits[v>>4], digits[v&15])
	}
	return string(b)
}

func hexByte(s string) byte {
	var v byte
	for _, c := range []byte(s) {
		v <<= 4
		switch {
		case c >= '0' && c <= '9':
			v |= c - '0'
		case c >= 'a' && c <= 'f':
			v |= c - 'a' + 10
		case c >= 'A' && c <= 'F':
			v |= c - 'A' + 10
		}
	}
	return v
}

// ── Profil de couleurs ──────────────────────────────────────────────────
// Vraies couleurs par défaut (Windows Terminal, iTerm2, Ghostty, Kitty… et
// tmux les transmet) ; FOUR_COLOR=256 pour les terminaux qui ne savent pas.
var prof = pickProfile()

func pickProfile() termenv.Profile {
	switch strings.ToLower(os.Getenv("FOUR_COLOR")) {
	case "256":
		return termenv.ANSI256
	case "16":
		return termenv.ANSI
	case "truecolor", "24bit", "rgb":
		return termenv.TrueColor
	}
	if os.Getenv("NO_COLOR") != "" {
		return termenv.Ascii
	}
	if os.Getenv("TERM_PROGRAM") == "Apple_Terminal" {
		return termenv.ANSI256
	}
	return termenv.TrueColor
}

var sgrCache sync.Map

func sgr(fg, bg string, bold bool) string {
	key := fg + bg
	if bold {
		key += "!"
	}
	if v, ok := sgrCache.Load(key); ok {
		return v.(string)
	}
	parts := []string{"0"}
	if bold {
		parts = append(parts, "1")
	}
	if fg != "" {
		if c := prof.Color(fg); c != nil {
			if s := c.Sequence(false); s != "" {
				parts = append(parts, s)
			}
		}
	}
	if bg != "" {
		if c := prof.Color(bg); c != nil {
			if s := c.Sequence(true); s != "" {
				parts = append(parts, s)
			}
		}
	}
	s := "\x1b[" + strings.Join(parts, ";") + "m"
	sgrCache.Store(key, s)
	return s
}

// ── Segments et lignes ──────────────────────────────────────────────────

type seg struct {
	t, fg, bg string
	bold      bool
}

func S(t, fg string) seg { return seg{t: t, fg: fg} }
func B(t, fg string) seg { return seg{t: t, fg: fg, bold: true} }
func P(t string) seg     { return seg{t: t, fg: cFg} }
func K(t string) seg     { return seg{t: t, fg: cKfg, bg: cKbg} }
func Btn(t string) seg   { return seg{t: t, fg: cBg, bg: cAcc, bold: true} }
func Sp(n int) seg {
	if n < 0 {
		n = 0
	}
	return seg{t: strings.Repeat(" ", n)}
}

type Line struct {
	segs []seg
	bg   string // vide : fond de l'écran (ou de la boîte)
}

func Ln(s ...seg) Line { return Line{segs: s} }

func (l Line) Add(s ...seg) Line {
	n := make([]seg, 0, len(l.segs)+len(s))
	n = append(append(n, l.segs...), s...)
	l.segs = n
	return l
}

func (l Line) Sel() Line { l.bg = cSel; return l }

func segsW(ss []seg) int {
	w := 0
	for _, s := range ss {
		w += ansi.StringWidth(s.t)
	}
	return w
}

func (l Line) W() int { return segsW(l.segs) }

// Coupe une suite de segments à w colonnes (avec « … » si coupée).
func truncSegs(ss []seg, w int) []seg {
	if segsW(ss) <= w {
		return ss
	}
	out := make([]seg, 0, len(ss))
	used := 0
	for _, s := range ss {
		sw := ansi.StringWidth(s.t)
		if used+sw <= w {
			out = append(out, s)
			used += sw
			continue
		}
		if rest := w - used; rest > 0 {
			s.t = ansi.Truncate(s.t, rest, "…")
			out = append(out, s)
		}
		break
	}
	return out
}

// Rend la ligne sur exactement w colonnes.
func (l Line) R(w int, bg string) string {
	if l.bg != "" {
		bg = l.bg
	}
	if bg == "" {
		bg = cBg
	}
	var b strings.Builder
	used := 0
	for _, s := range truncSegs(l.segs, w) {
		if s.t == "" {
			continue
		}
		sb := bg
		if s.bg != "" {
			sb = s.bg
		}
		b.WriteString(sgr(s.fg, sb, s.bold))
		b.WriteString(s.t)
		used += ansi.StringWidth(s.t)
	}
	if used < w {
		b.WriteString(sgr(cFg, bg, false))
		b.WriteString(strings.Repeat(" ", w-used))
	}
	b.WriteString("\x1b[0m")
	return b.String()
}

// Gauche et droite sur w colonnes ; la gauche est coupée si ça déborde.
func lr(left, right []seg, w int) Line {
	rw := segsW(right)
	if rw > w {
		right, rw = nil, 0
	}
	lw := w - rw
	if rw > 0 {
		lw-- // au moins une espace entre les deux
	}
	left = truncSegs(left, lw)
	gap := w - segsW(left) - rw
	out := make([]seg, 0, len(left)+len(right)+1)
	out = append(append(append(out, left...), Sp(gap)), right...)
	return Line{segs: out}
}

func padSegs(ss []seg, w int) []seg {
	ss = truncSegs(ss, w)
	return append(append([]seg{}, ss...), Sp(w-segsW(ss)))
}

// Raccourcis en bas d'écran : on retire les derniers s'il manque de la place.
func keys(lead string, w int, pairs ...string) Line {
	for n := len(pairs); n >= 2; n -= 2 {
		l := Ln(Sp(len(lead)))
		for i := 0; i < n; i += 2 {
			l = l.Add(K(" "+pairs[i]+" "), S(" "+pairs[i+1], cD))
			if i+2 < n {
				l = l.Add(Sp(3))
			}
		}
		if l.W() <= w {
			return l
		}
	}
	return Ln()
}

// ── Boîtes et colonnes ──────────────────────────────────────────────────

const (
	bRound = "╭╮╰╯─│"
	bHeavy = "┏┓┗┛━┃"
)

func box(w, h int, body []Line, title, rtitle []seg, border, bc string) []string {
	if w < 4 {
		w = 4
	}
	r := []rune(border)
	tl, tr, bl, br, hz, vt := string(r[0]), string(r[1]), string(r[2]), string(r[3]), string(r[4]), string(r[5])
	top := []seg{S(tl+hz, bc)}
	if len(title) > 0 {
		top = append(top, Sp(1))
		top = append(top, title...)
		top = append(top, Sp(1))
	}
	var right []seg
	if len(rtitle) > 0 {
		right = append([]seg{Sp(1)}, rtitle...)
		right = append(right, Sp(1), S(hz, bc))
	}
	fill := w - segsW(top) - segsW(right) - 1
	if fill < 1 {
		right = nil
		fill = w - segsW(top) - 1
	}
	if fill < 0 {
		top = truncSegs(top, w-1)
		fill = w - segsW(top) - 1
	}
	top = append(top, S(strings.Repeat(hz, fill), bc))
	top = append(top, right...)
	top = append(top, S(tr, bc))
	out := []string{Line{segs: top}.R(w, "")}
	inner := w - 2
	for i := 0; i < h-2; i++ {
		var l Line
		if i < len(body) {
			l = body[i]
		}
		out = append(out, Ln(S(vt, bc)).R(1, "")+l.R(inner, "")+Ln(S(vt, bc)).R(1, ""))
	}
	out = append(out, Ln(S(bl+strings.Repeat(hz, w-2)+br, bc)).R(w, ""))
	return out
}

// Colonnes côte à côte ; chaque bloc fait déjà sa largeur.
func hcat(gap int, blocks [][]string, widths []int) []string {
	n := 0
	for _, b := range blocks {
		if len(b) > n {
			n = len(b)
		}
	}
	empty := make([]string, len(widths))
	for i, w := range widths {
		empty[i] = Ln().R(w, "")
	}
	sp := Ln().R(gap, "")
	out := make([]string, n)
	for i := 0; i < n; i++ {
		var b strings.Builder
		for j, blk := range blocks {
			if j > 0 && gap > 0 {
				b.WriteString(sp)
			}
			if i < len(blk) {
				b.WriteString(blk[i])
			} else {
				b.WriteString(empty[j])
			}
		}
		out[i] = b.String()
	}
	return out
}

func indent(n int, rows []string) []string {
	p := Ln().R(n, "")
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = p + r
	}
	return out
}

// Écran complet : chaque ligne remplie jusqu'au bord, fond peint partout.
func frame(rows []string, w, h int) string {
	var b strings.Builder
	blank := Ln().R(w, "")
	for i := 0; i < h; i++ {
		if i > 0 {
			b.WriteByte('\n')
		}
		if i >= len(rows) {
			b.WriteString(blank)
			continue
		}
		r := rows[i]
		rw := ansi.StringWidth(r)
		switch {
		case rw < w:
			b.WriteString(r)
			b.WriteString(Ln().R(w-rw, ""))
		case rw > w:
			b.WriteString(ansi.Truncate(r, w, ""))
			b.WriteString("\x1b[0m")
		default:
			b.WriteString(r)
		}
	}
	return b.String()
}

func rows(lines []Line, w int) []string {
	out := make([]string, len(lines))
	for i, l := range lines {
		out[i] = l.R(w, "")
	}
	return out
}

func usageColor(pct float64) string {
	if pct >= 85 {
		return cErr
	}
	return cAcc
}

// usageBar : une jauge de 10 crans suivie du pourcentage.
func usageBar(label string, pct float64, ok bool) []seg {
	if !ok {
		return []seg{S(label+" ", cD), S(strings.Repeat("━", 10), cDD), S("  – %", cDD)}
	}
	n := int(pct/10 + 0.5)
	if n > 10 {
		n = 10
	}
	return []seg{S(label+" ", cD), S(strings.Repeat("━", n), usageColor(pct)), S(strings.Repeat("━", 10-n), cDD),
		P(" " + itoa(int(pct+0.5)) + " %")}
}

func usageSegs(u Usage) []seg {
	return append(append(usageBar("Claude 5h", u.Pct5h, u.Has5h), Sp(3)), usageBar("7j", u.Pct7d, u.Has7d)...)
}

// usageNum : un pourcentage seul, pour les en-têtes étroits.
func usageNum(label string, pct float64, ok bool) []seg {
	if !ok {
		return []seg{S(label+" ", cD), S("– %", cDD)}
	}
	return []seg{S(label+" ", cD), S(itoa(int(pct+0.5))+" %", usageColor(pct))}
}

func usageCompact(u Usage) []seg {
	return append(append(usageNum("Claude 5h", u.Pct5h, u.Has5h), Sp(2)), usageNum("7j", u.Pct7d, u.Has7d)...)
}
