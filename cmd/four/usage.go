package main

// Part utilisée des fenêtres de 5 h et de 7 j de l'abonnement Claude. Claude
// Code les transmet à la ligne d'état (rate_limits, champ documenté) : four
// _statusline les garde en cache pour le menu, sans jeton ni appel réseau.

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Usage struct {
	OK      bool      `json:"ok"`
	Has5h   bool      `json:"has5h"`
	Pct5h   float64   `json:"pct5h"`
	Reset5h time.Time `json:"reset5h"`
	Has7d   bool      `json:"has7d"`
	Pct7d   float64   `json:"pct7d"`
	Reset7d time.Time `json:"reset7d"`
}

// Les quotas des trois agents, pour le menu et la ligne d'état de tmux.
type Quotas struct{ Claude, Codex, Gemini Usage }

func cachedQuotas() Quotas {
	return Quotas{Claude: cachedUsage(), Codex: codexUsage(), Gemini: cachedGemini()}
}

// Un agent sans chiffres cette fois garde les précédents.
func (q Quotas) merge(n Quotas) Quotas {
	if n.Claude.OK {
		q.Claude = n.Claude
	}
	if n.Codex.OK {
		q.Codex = n.Codex
	}
	if n.Gemini.OK {
		q.Gemini = n.Gemini
	}
	return q
}

// La fenêtre la plus entamée d'un agent, pour la ligne d'état de tmux.
func (u Usage) worst() float64 {
	p := 0.0
	if u.Has5h {
		p = u.Pct5h
	}
	if u.Has7d && u.Pct7d > p {
		p = u.Pct7d
	}
	return p
}

// En bas de chaque session : « Claude 42 % · Codex 10 % · Gemini 5 % », la
// fenêtre la plus entamée de chacun (rouge à partir de 85 %).
func quotaStatus(q Quotas) string {
	var parts []string
	for _, a := range []struct {
		name string
		u    Usage
	}{{"Claude", q.Claude}, {"Codex", q.Codex}, {"Gemini", q.Gemini}} {
		if !a.u.OK {
			continue
		}
		p := a.u.worst()
		parts = append(parts, "#[fg="+cD+"]"+a.name+" #[fg="+usageColor(p)+"]"+itoa(int(p+0.5))+" %")
	}
	if len(parts) == 0 {
		return ""
	}
	return "#[bg=" + cBar2Bg + "] " + strings.Join(parts, "#[fg="+cDD+"] · ") + " #[default]"
}

func usageCache() string { return filepath.Join(stateDir(), "usage.json") }

func cachedUsage() Usage {
	var u Usage
	if b, err := os.ReadFile(usageCache()); err == nil {
		_ = json.Unmarshal(b, &u)
	}
	return u.current(time.Now())
}

// Une fenêtre dont la date de remise à zéro est passée repart de 0.
func (u Usage) current(now time.Time) Usage {
	if !u.Reset5h.IsZero() && now.After(u.Reset5h) {
		u.Pct5h = 0
	}
	if !u.Reset7d.IsZero() && now.After(u.Reset7d) {
		u.Pct7d = 0
	}
	return u
}

// Une fenêtre peut manquer dans une mise à jour : on garde alors la précédente.
func (u Usage) merge(status []byte) Usage {
	type window struct {
		Used   *float64 `json:"used_percentage"`
		Resets int64    `json:"resets_at"`
	}
	var in struct {
		Limits struct {
			Five  window `json:"five_hour"`
			Seven window `json:"seven_day"`
		} `json:"rate_limits"`
	}
	if json.Unmarshal(status, &in) != nil {
		return u
	}
	if w := in.Limits.Five; w.Used != nil {
		u.OK, u.Has5h, u.Pct5h, u.Reset5h = true, true, *w.Used, unixOrZero(w.Resets)
	}
	if w := in.Limits.Seven; w.Used != nil {
		u.OK, u.Has7d, u.Pct7d, u.Reset7d = true, true, *w.Used, unixOrZero(w.Resets)
	}
	return u
}

func unixOrZero(s int64) time.Time {
	if s <= 0 {
		return time.Time{}
	}
	return time.Unix(s, 0).UTC()
}

func saveUsage(u Usage) {
	b, err := json.Marshal(u)
	if err != nil {
		return
	}
	// Plusieurs sessions écrivent ici : on remplace le fichier d'un coup.
	tmp := fmt.Sprintf("%s.%d.tmp", usageCache(), os.Getpid())
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, usageCache())
	}
}

// four _statusline : la commande de ligne d'état de Claude Code. Elle reçoit
// l'état de la session sur stdin et n'affiche rien : les limites sont dans le
// menu.
func statusLineCmd(in io.Reader) {
	b, err := io.ReadAll(in)
	if err != nil {
		return
	}
	var cached Usage
	if c, err := os.ReadFile(usageCache()); err == nil {
		_ = json.Unmarshal(c, &cached)
	}
	if u := cached.merge(b); u != cached {
		saveUsage(u)
	}
}
