package main

import (
	"strings"
	"unicode"

	tea "github.com/charmbracelet/bubbletea"
)

// Champ de saisie minimal, rendu en segments pour garder le fond de l'écran.
type input struct {
	r   []rune
	pos int
	ph  string // texte d'attente
}

func (in *input) val() string { return string(in.r) }

func (in *input) set(s string) {
	in.r = []rune(s)
	in.pos = len(in.r)
}

// Retourne vrai si la touche a été consommée.
func (in *input) key(k tea.KeyMsg) bool {
	switch k.Type {
	case tea.KeyRunes:
		rs := make([]rune, 0, len(k.Runes))
		for _, r := range k.Runes {
			if r == '\n' || r == '\r' || r == '\t' {
				r = ' '
			}
			if unicode.IsPrint(r) {
				rs = append(rs, r)
			}
		}
		in.r = append(in.r[:in.pos], append(rs, in.r[in.pos:]...)...)
		in.pos += len(rs)
	case tea.KeySpace:
		in.r = append(in.r[:in.pos], append([]rune{' '}, in.r[in.pos:]...)...)
		in.pos++
	case tea.KeyBackspace:
		if in.pos > 0 {
			in.r = append(in.r[:in.pos-1], in.r[in.pos:]...)
			in.pos--
		}
	case tea.KeyDelete:
		if in.pos < len(in.r) {
			in.r = append(in.r[:in.pos], in.r[in.pos+1:]...)
		}
	case tea.KeyLeft:
		if in.pos > 0 {
			in.pos--
		}
	case tea.KeyRight:
		if in.pos < len(in.r) {
			in.pos++
		}
	case tea.KeyHome, tea.KeyCtrlA:
		in.pos = 0
	case tea.KeyEnd, tea.KeyCtrlE:
		in.pos = len(in.r)
	case tea.KeyCtrlU:
		in.r, in.pos = in.r[:0], 0
	case tea.KeyCtrlW:
		i := in.pos
		for i > 0 && in.r[i-1] == ' ' {
			i--
		}
		for i > 0 && in.r[i-1] != ' ' {
			i--
		}
		in.r = append(in.r[:i], in.r[in.pos:]...)
		in.pos = i
	default:
		return false
	}
	return true
}

func (in input) segs(focused bool, fg string, bold bool) []seg {
	cur := S("▏", cAcc)
	if len(in.r) == 0 {
		if !focused {
			return []seg{S(in.ph, cD)}
		}
		if in.ph != "" {
			return []seg{cur, S(in.ph, cD)}
		}
		return []seg{cur}
	}
	mk := func(s string) seg { return seg{t: s, fg: fg, bold: bold} }
	if !focused {
		return []seg{mk(string(in.r))}
	}
	before, after := string(in.r[:in.pos]), string(in.r[in.pos:])
	out := []seg{mk(before), cur}
	if after != "" {
		out = append(out, mk(after))
	}
	return out
}

func trimLower(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
