package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestStatusLineCmd(t *testing.T) {
	old := home
	defer func() { home = old }()
	home = t.TempDir()
	future := time.Now().Add(time.Hour).Unix()

	run := func(in string) Usage {
		statusLineCmd(strings.NewReader(in))
		return cachedUsage()
	}

	if run(`{"model":{"display_name":"x"}}`).OK {
		t.Error("sans rate_limits, le cache ne doit pas être valide")
	}
	in := `{"rate_limits":{"five_hour":{"used_percentage":23.5,"resets_at":` + itoa(int(future)) + `},` +
		`"seven_day":{"used_percentage":41.2,"resets_at":` + itoa(int(future)) + `}}}`
	if u := run(in); !u.Has5h || u.Pct5h != 23.5 || !u.Has7d || u.Pct7d != 41.2 {
		t.Errorf("les deux fenêtres : %+v", u)
	}
	// Une fenêtre absente garde sa dernière valeur.
	u := run(`{"rate_limits":{"seven_day":{"used_percentage":50,"resets_at":` + itoa(int(future)) + `}}}`)
	if !u.Has5h || u.Pct5h != 23.5 || u.Pct7d != 50 {
		t.Errorf("fenêtre absente : %+v", u)
	}
	// Une entrée invalide ne touche pas au cache.
	if got := run(`pas du json`); got != u {
		t.Errorf("entrée invalide : %+v", got)
	}
}

func TestUsageCurrent(t *testing.T) {
	now := time.Now()
	u := Usage{OK: true, Has5h: true, Pct5h: 80, Reset5h: now.Add(-time.Minute),
		Has7d: true, Pct7d: 40, Reset7d: now.Add(time.Hour)}.current(now)
	if u.Pct5h != 0 {
		t.Errorf("5h expiré : %v", u.Pct5h)
	}
	if u.Pct7d != 40 {
		t.Errorf("7j en cours : %v", u.Pct7d)
	}
}

func TestHookMergeStatusLine(t *testing.T) {
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 absent")
	}
	merge := func(start string) map[string]any {
		p := filepath.Join(t.TempDir(), "settings.json")
		if err := os.WriteFile(p, []byte(start), 0o600); err != nil {
			t.Fatal(err)
		}
		if out, err := exec.Command("python3", "-I", "-c", hookMerge, p, "/bin/four").CombinedOutput(); err != nil {
			t.Fatalf("%v : %s", err, out)
		}
		b, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		var d map[string]any
		if err := json.Unmarshal(b, &d); err != nil {
			t.Fatal(err)
		}
		return d
	}
	line := func(d map[string]any) any { return d["statusLine"].(map[string]any)["command"] }
	if got := line(merge(`{}`)); got != "/bin/four _statusline" {
		t.Errorf("ajout : %v", got)
	}
	if got := line(merge(`{"statusLine":{"type":"command","command":"le-mien"}}`)); got != "le-mien" {
		t.Errorf("une ligne d'état existante a été remplacée : %v", got)
	}
}
