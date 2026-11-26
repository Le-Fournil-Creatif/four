package main

// Part utilisée des fenêtres de 5 h et de 7 j de l'abonnement ChatGPT, pour
// Codex. Codex l'écrit dans ses journaux de session (événements token_count,
// champ rate_limits) : on lit la fin du journal le plus récent, sans réseau.

import (
	"bufio"
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"time"
)

// La fin du journal suffit : Codex note les limites à chaque tour.
const codexTail = 512 << 10

func codexUsage() Usage {
	f := latestCodexRollout(time.Now())
	if f == "" {
		return Usage{}
	}
	return codexUsageFrom(f).current(time.Now())
}

// Le journal le plus récent : sessions/AAAA/MM/JJ/rollout-*.jsonl, sur les
// derniers jours (une fenêtre de 7 j peut dater d'une session ancienne).
func latestCodexRollout(now time.Time) string {
	var best string
	var bestT time.Time
	for d := 0; d < 8; d++ {
		day := filepath.Join(codexHome(), "sessions", now.AddDate(0, 0, -d).Format("2006/01/02"))
		es, _ := os.ReadDir(day)
		for _, e := range es {
			if e.IsDir() || filepath.Ext(e.Name()) != ".jsonl" {
				continue
			}
			if i, err := e.Info(); err == nil && i.ModTime().After(bestT) {
				best, bestT = filepath.Join(day, e.Name()), i.ModTime()
			}
		}
		if best != "" {
			return best
		}
	}
	return ""
}

func codexUsageFrom(path string) Usage {
	f, err := os.Open(path)
	if err != nil {
		return Usage{}
	}
	defer f.Close()
	if st, err := f.Stat(); err == nil && st.Size() > codexTail {
		_, _ = f.Seek(st.Size()-codexTail, io.SeekStart)
	}
	var u Usage
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), 8<<20)
	for sc.Scan() {
		if l := sc.Bytes(); bytes.Contains(l, []byte(`"rate_limits"`)) {
			u = u.mergeCodex(l)
		}
	}
	return u
}

// Deux formes selon la version de Codex : resets_at (date Unix) ou
// resets_in_seconds (à compter de l'horodatage de la ligne).
func (u Usage) mergeCodex(line []byte) Usage {
	type window struct {
		Used    *float64 `json:"used_percent"`
		Minutes int      `json:"window_minutes"`
		At      int64    `json:"resets_at"`
		In      int64    `json:"resets_in_seconds"`
	}
	var in struct {
		Timestamp time.Time `json:"timestamp"`
		Payload   struct {
			Limits *struct {
				Primary   *window `json:"primary"`
				Secondary *window `json:"secondary"`
			} `json:"rate_limits"`
		} `json:"payload"`
	}
	if json.Unmarshal(line, &in) != nil || in.Payload.Limits == nil {
		return u
	}
	set := func(w *window, long bool) {
		if w == nil || w.Used == nil {
			return
		}
		if w.Minutes > 0 {
			long = w.Minutes > 24*60
		}
		reset := unixOrZero(w.At)
		if reset.IsZero() && w.In > 0 && !in.Timestamp.IsZero() {
			reset = in.Timestamp.Add(time.Duration(w.In) * time.Second).UTC()
		}
		if long {
			u.OK, u.Has7d, u.Pct7d, u.Reset7d = true, true, *w.Used, reset
		} else {
			u.OK, u.Has5h, u.Pct5h, u.Reset5h = true, true, *w.Used, reset
		}
	}
	set(in.Payload.Limits.Primary, false)
	set(in.Payload.Limits.Secondary, true)
	return u
}
