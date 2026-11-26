package main

// Part utilisée du quota Gemini d'Antigravity (agy). Rien de documenté : agy
// le lit dans une API interne de Google (v1internal:fetchAvailableModels, un
// quotaInfo par modèle). On l'interroge comme agy, avec son jeton OAuth (lu,
// jamais rafraîchi ni réécrit : agy s'en charge), au plus toutes les 3 min,
// et on garde le résultat en cache. Si Google change l'API, la jauge reste
// simplement vide.

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

const (
	geminiAPI      = "https://daily-cloudcode-pa.googleapis.com/v1internal:fetchAvailableModels"
	geminiEvery    = 3 * time.Minute
	geminiFallback = "1.5.18" // version d'agy si --version ne répond pas
)

// Gemini n'a qu'une fenêtre : elle va dans les champs « 5h » de Usage.
func geminiCache() string { return filepath.Join(stateDir(), "gemini-usage.json") }

func cachedGemini() Usage {
	var u Usage
	if b, err := os.ReadFile(geminiCache()); err == nil {
		_ = json.Unmarshal(b, &u)
	}
	return u.current(time.Now())
}

// Le cache est-il à refaire ? Sa date sert aussi de verrou : on la pose avant
// d'appeler, pour que les autres sessions n'appellent pas en même temps.
func geminiDue() bool {
	if findAgy() == "" || !agyLoggedIn() {
		return false
	}
	st, err := os.Stat(geminiCache())
	return err != nil || time.Since(st.ModTime()) > geminiEvery
}

func claimGemini() {
	now := time.Now()
	if os.Chtimes(geminiCache(), now, now) != nil {
		saveGemini(cachedGemini())
	}
}

// Pour la ligne d'état de tmux : l'appel part dans un processus à part.
func refreshGeminiBackground() {
	if !geminiDue() {
		return
	}
	claimGemini()
	c := exec.Command(Bin, "_gemini-usage")
	c.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if c.Start() == nil {
		_ = c.Process.Release()
	}
}

// Pour le menu, déjà dans sa propre goroutine.
func refreshGemini() {
	if !geminiDue() {
		return
	}
	claimGemini()
	geminiUsageCmd()
}

// four _gemini-usage
func geminiUsageCmd() {
	if u, err := fetchGemini(); err == nil {
		saveGemini(u)
	}
}

func saveGemini(u Usage) {
	b, err := json.Marshal(u)
	if err != nil {
		return
	}
	tmp := geminiCache() + "." + itoa(os.Getpid()) + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, geminiCache())
	}
}

func agyToken() (string, error) {
	b, err := os.ReadFile(filepath.Join(agyHome(), "antigravity-cli", "antigravity-oauth-token"))
	if err != nil {
		return "", err
	}
	var t struct {
		Token struct {
			Access string    `json:"access_token"`
			Expiry time.Time `json:"expiry"`
		} `json:"token"`
	}
	if json.Unmarshal(b, &t) != nil || t.Token.Access == "" {
		return "", errors.New("jeton illisible")
	}
	if !t.Token.Expiry.IsZero() && time.Now().After(t.Token.Expiry.Add(-time.Minute)) {
		return "", errors.New("jeton expiré") // agy le renouvellera à son prochain lancement
	}
	return t.Token.Access, nil
}

func agyVersion() string {
	out, _ := exec.Command(findAgy(), "--version").Output()
	if f := strings.Fields(string(out)); len(f) > 0 {
		return f[len(f)-1]
	}
	return geminiFallback
}

func fetchGemini() (Usage, error) {
	tok, err := agyToken()
	if err != nil {
		return Usage{}, err
	}
	req, _ := http.NewRequest("POST", geminiAPI, bytes.NewReader([]byte("{}")))
	req.Header.Set("Authorization", "Bearer "+tok)
	req.Header.Set("Content-Type", "application/json")
	// Sans l'User-Agent d'agy, l'API refuse (403).
	req.Header.Set("User-Agent", "antigravity/"+agyVersion()+" linux/amd64")
	resp, err := (&http.Client{Timeout: 15 * time.Second}).Do(req)
	if err != nil {
		return Usage{}, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return Usage{}, errors.New(resp.Status)
	}
	var body bytes.Buffer
	if _, err := body.ReadFrom(http.MaxBytesReader(nil, resp.Body, 4<<20)); err != nil {
		return Usage{}, err
	}
	return parseGemini(body.Bytes())
}

// Les modèles de Google partagent un même quota ; ceux des autres (Claude,
// GPT-OSS) en ont un autre, qui n'est pas celui de Gemini. On garde le plus
// entamé des modèles Google qui ont une date de remise à zéro.
func parseGemini(b []byte) (Usage, error) {
	var in struct {
		Models map[string]struct {
			Provider string `json:"modelProvider"`
			Quota    *struct {
				Remaining *float64  `json:"remainingFraction"`
				Reset     time.Time `json:"resetTime"`
			} `json:"quotaInfo"`
		} `json:"models"`
	}
	if err := json.Unmarshal(b, &in); err != nil {
		return Usage{}, err
	}
	var u Usage
	for name, m := range in.Models {
		q := m.Quota
		if q == nil || q.Remaining == nil || q.Reset.IsZero() {
			continue
		}
		if m.Provider != "MODEL_PROVIDER_GOOGLE" && !strings.HasPrefix(name, "gemini-") {
			continue
		}
		if pct := (1 - *q.Remaining) * 100; !u.OK || pct > u.Pct5h {
			u.OK, u.Has5h, u.Pct5h, u.Reset5h = true, true, pct, q.Reset.UTC()
		}
	}
	if !u.OK {
		return u, errors.New("pas de quota Gemini dans la réponse")
	}
	return u, nil
}
