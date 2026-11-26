package main

// Antigravity (agy), l'agent de Google. Ses hooks globaux sont dans
// ~/.gemini/config/hooks.json, sous un nom de hook à lui : four y ajoute le
// sien (« four ») sans toucher aux autres. Antigravity n'a pas d'événement
// « demande d'autorisation » : ✓ cuit et « au four », mais pas de ◆ attend.

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
)

func findAgy() string {
	if p, err := exec.LookPath("agy"); err == nil {
		return p
	}
	for _, p := range []string{filepath.Join(home, ".local", "bin", "agy"), "/usr/local/bin/agy", "/usr/bin/agy"} {
		if exists(p) {
			return p
		}
	}
	return ""
}

func agyArgs(auto bool) []string {
	if !auto {
		return nil
	}
	return []string{"--dangerously-skip-permissions"}
}

func agyHome() string { return filepath.Join(home, ".gemini") }

func agyHooksPath() string { return filepath.Join(agyHome(), "config", "hooks.json") }

// Connecté ? Le jeton OAuth est là (sans le lire).
func agyLoggedIn() bool {
	return exists(filepath.Join(agyHome(), "antigravity-cli", "antigravity-oauth-token"))
}

const agyMissing = "facultatif · pas installé"

const agyHookName = "four"

// Stop : le tour est fini. PreInvocation : avant chaque appel du modèle, donc
// dès que la demande part (et à chaque étape ensuite, sans effet de plus).
func agyFourHook() map[string]any {
	h := func(arg string) []any {
		return []any{map[string]any{"type": "command", "command": Bin + " _notify " + arg + " agy", "timeout": 10}}
	}
	return map[string]any{"Stop": h("done"), "PreInvocation": h("clear")}
}

func agyHooksInstalled() bool {
	b, err := os.ReadFile(agyHooksPath())
	if err != nil {
		return false
	}
	var d map[string]any
	if json.Unmarshal(b, &d) != nil || d[agyHookName] == nil {
		return false
	}
	// Comparer après un aller-retour JSON : mêmes types des deux côtés.
	var want any
	w, _ := json.Marshal(agyFourHook())
	_ = json.Unmarshal(w, &want)
	return reflect.DeepEqual(d[agyHookName], want)
}

func installAgyHooks() error {
	p := agyHooksPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	d := map[string]json.RawMessage{}
	if b, err := os.ReadFile(p); err == nil {
		if err := json.Unmarshal(b, &d); err != nil {
			return errors.New("hooks.json illisible : " + err.Error())
		}
		_ = os.WriteFile(p+".avant-four", b, 0o600)
	}
	h, _ := json.Marshal(agyFourHook())
	d[agyHookName] = h
	out, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := p + ".four.tmp"
	if err := os.WriteFile(tmp, append(out, '\n'), 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, p)
}

// Pour four setup : installé, connecté, hooks posés.
func agyStatus() (int, string) {
	switch {
	case findAgy() == "":
		return stSkip, agyMissing
	case !agyLoggedIn():
		return stTodo, "installé · pas encore connecté"
	case !agyHooksInstalled():
		return stTodo, "connecté · notifications à activer"
	}
	return stOK, "connecté · ✓ cuit"
}
