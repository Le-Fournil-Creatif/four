package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestCodexUsage(t *testing.T) {
	old := home
	defer func() { home = old }()
	home = t.TempDir()
	t.Setenv("CODEX_HOME", "")
	now := time.Now()
	day := filepath.Join(home, ".codex", "sessions", now.Format("2006/01/02"))
	_ = os.MkdirAll(day, 0o755)
	ts := now.UTC().Format(time.RFC3339)
	at := itoa(int(now.Add(2 * time.Hour).Unix()))
	lines := `{"timestamp":"` + ts + `","type":"session_meta","payload":{}}
{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":10,"window_minutes":300,"resets_in_seconds":3600},"secondary":{"used_percent":20,"window_minutes":10080,"resets_in_seconds":7200}}}}
{"timestamp":"` + ts + `","type":"event_msg","payload":{"type":"token_count","rate_limits":{"primary":{"used_percent":12.5,"window_minutes":300,"resets_at":` + at + `}}}}
`
	_ = os.WriteFile(filepath.Join(day, "rollout-x.jsonl"), []byte(lines), 0o600)
	u := codexUsage()
	if !u.OK || u.Pct5h != 12.5 || u.Pct7d != 20 || u.Reset5h.Unix() != now.Add(2*time.Hour).Unix() {
		t.Errorf("codex : %+v", u)
	}
	if u.Reset7d.Before(now.Add(time.Hour)) {
		t.Errorf("resets_in_seconds : %v", u.Reset7d)
	}
}

func TestParseGemini(t *testing.T) {
	future := time.Now().Add(time.Hour).UTC().Format(time.RFC3339)
	b := `{"models":{
	 "gemini-3-flash":{"modelProvider":"MODEL_PROVIDER_GOOGLE","quotaInfo":{"remainingFraction":0.9,"resetTime":"` + future + `"}},
	 "gemini-2.5-flash":{"quotaInfo":{"remainingFraction":0.75,"resetTime":"` + future + `"}},
	 "claude-opus-5-5-high":{"modelProvider":"MODEL_PROVIDER_ANTHROPIC","quotaInfo":{"remainingFraction":0.1,"resetTime":"` + future + `"}},
	 "tab_flash_lite_preview":{"quotaInfo":{"remainingFraction":1}}}}`
	u, err := parseGemini([]byte(b))
	if err != nil || !u.OK || u.Pct5h != 25 {
		t.Errorf("gemini : %+v %v", u, err)
	}
	if _, err := parseGemini([]byte(`{"models":{}}`)); err == nil {
		t.Error("sans quota : erreur attendue")
	}
}

func TestQuotaStatus(t *testing.T) {
	q := Quotas{Claude: Usage{OK: true, Has5h: true, Pct5h: 42, Has7d: true, Pct7d: 90}, Gemini: Usage{OK: true, Has5h: true, Pct5h: 5}}
	s := quotaStatus(q)
	if !strings.Contains(s, "Claude #[fg="+cErr+"]90 %") || !strings.Contains(s, "Gemini #[fg="+cAcc+"]5 %") || strings.Contains(s, "Codex") {
		t.Errorf("ligne d'état : %q", s)
	}
	if quotaStatus(Quotas{}) != "" {
		t.Error("sans chiffres : rien")
	}
}
