package main

// Les agents : Claude Code (par défaut), Codex ou Gemini (Antigravity, agy), au choix
// pour chaque session (n, N ou A dans le menu). La session garde son agent
// dans @four_agent et FOUR_AGENT ; les hooks des agents appellent tous le
// même four _notify.

import (
	"bufio"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

const (
	agentClaude = "claude"
	agentCodex  = "codex"
	agentAgy    = "agy"
)

// Une session sans agent noté date d'avant Codex : c'est Claude.
func normAgent(a string) string {
	switch a {
	case agentCodex, agentAgy:
		return a
	}
	return agentClaude
}

func agentLabel(a string) string {
	switch normAgent(a) {
	case agentCodex:
		return "Codex"
	case agentAgy:
		return "Gemini" // Antigravity (agy), l'agent Gemini de Google
	}
	return "Claude"
}

func findAgent(a string) string {
	switch normAgent(a) {
	case agentCodex:
		return findCodex()
	case agentAgy:
		return findAgy()
	}
	return findClaude()
}

func agentArgs(a string, auto bool) []string {
	switch normAgent(a) {
	case agentCodex:
		return codexArgs(auto)
	case agentAgy:
		return agyArgs(auto)
	}
	return claudeArgs(auto)
}

// Les commandes à taper dans le shell de la session, pour les messages.
func agentRelaunch(a string) string {
	switch normAgent(a) {
	case agentCodex:
		return "codex --no-daemon"
	case agentAgy:
		return "agy"
	}
	return "claude"
}

func agentResume(a string) string {
	switch normAgent(a) {
	case agentCodex:
		return "codex resume --no-daemon"
	case agentAgy:
		return "agy --continue"
	}
	return "claude --resume"
}

// Les arguments de lancement dans une session four. Codex rejoint sinon son
// serveur partagé en arrière-plan, qui exécute les hooks avec l'environnement
// de la première session lancée : les ✓ et ◆ d'une session b arriveraient
// sur la session a. --no-daemon garde les hooks dans la session.
func agentCmdArgs(a, bin string, auto bool) []string {
	args := agentArgs(a, auto)
	if normAgent(a) == agentCodex && codexHasFlag(bin, "--no-daemon") {
		args = append([]string{"--no-daemon"}, args...)
	}
	return args
}

// Les versions plus anciennes de Codex n'ont pas l'option (ni le serveur).
func codexHasFlag(bin, flag string) bool {
	out, _ := exec.Command(bin, "--help").Output()
	return strings.Contains(string(out), flag)
}

// ── Codex ───────────────────────────────────────────────────────────────

func findCodex() string {
	if p, err := exec.LookPath("codex"); err == nil {
		return p
	}
	for _, p := range []string{filepath.Join(home, ".local", "bin", "codex"), "/usr/local/bin/codex", "/usr/bin/codex"} {
		if exists(p) {
			return p
		}
	}
	return ""
}

func codexArgs(auto bool) []string {
	if !auto {
		return nil // par défaut, Codex demande avant d'agir et reste dans son bac à sable
	}
	return []string{"--dangerously-bypass-approvals-and-sandbox"}
}

func codexHome() string { return envOr("CODEX_HOME", filepath.Join(home, ".codex")) }

func codexHooksPath() string { return filepath.Join(codexHome(), "hooks.json") }

func codexCreds() bool { return exists(filepath.Join(codexHome(), "auth.json")) } // sans lire les jetons

const codexMissing = "facultatif · pas installé · ↵ l'installe"

// Installeur officiel (chatgpt.com/codex), pour le compte en cours
// (~/.local/bin), sans ses questions : la connexion suit.
const codexInstall = `set -o pipefail
printf '\n  Installation de Codex pour %s (installeur officiel chatgpt.com)\n\n' "$USER"
if curl -fsSL https://chatgpt.com/codex/install.sh | CODEX_NON_INTERACTIVE=1 sh; then
  printf '\n  ✓ Codex installé. Connexion à ton compte ChatGPT…\n\n'
  sleep 1
else
  printf '\n  ✖ Installation interrompue. Entrée pour revenir à four. '
  read -r _
fi
`

// Connecté ? codex login status répond 0 si oui.
func codexLoggedIn(c string) bool {
	if c == "" {
		return false
	}
	return exec.Command(c, "login", "status").Run() == nil
}

// Les hooks de four dans ~/.codex/hooks.json, sans toucher aux autres. Codex
// les ignore tant qu'ils ne sont pas approuvés : trustCodexHooks s'en charge.
const codexHookMerge = `
import json, os, sys
p, bin = sys.argv[1], sys.argv[2]
d = {}
if os.path.exists(p):
    with open(p, encoding="utf-8") as f:
        d = json.load(f)
hooks = d.setdefault("hooks", {})
for ev, arg in (("Stop", "done"), ("PermissionRequest", "wait"), ("UserPromptSubmit", "clear")):
    lst = hooks.setdefault(ev, [])
    cmd = bin + " _notify " + arg
    if any(h.get("command") == cmd for e in lst for h in e.get("hooks", [])):
        continue
    lst.append({"hooks": [{"type": "command", "command": cmd}]})
tmp = p + ".four.tmp"
with open(tmp, "w", encoding="utf-8") as f:
    json.dump(d, f, indent=2, ensure_ascii=False)
    f.write("\n")
os.replace(tmp, p)
`

func installCodexHooks(c string) error {
	_ = os.MkdirAll(codexHome(), 0o700)
	if b, err := os.ReadFile(codexHooksPath()); err == nil {
		_ = os.WriteFile(codexHooksPath()+".avant-four", b, 0o600)
	}
	out, err := exec.Command("python3", "-I", "-c", codexHookMerge, codexHooksPath(), Bin).CombinedOutput()
	if err != nil {
		return errors.New("hooks.json : " + lastLine(string(out)))
	}
	return trustCodexHooks(c)
}

// ── Approbation des hooks, par l'app-server de Codex ───────────────────
//
// Codex n'exécute un hook que si son empreinte est notée dans config.toml
// (ce que fait /hooks dans Codex). Plutôt que de recalculer cette empreinte,
// four la demande à Codex (hooks/list) et l'enregistre par la même voie que
// Codex (config/batchWrite), une clé à la fois : les autres hooks gardent
// leur état.

type codexHook struct {
	Key         string `json:"key"`
	Command     string `json:"command"`
	SourcePath  string `json:"sourcePath"`
	CurrentHash string `json:"currentHash"`
	TrustStatus string `json:"trustStatus"`
}

func isFourCodexHook(h codexHook) bool {
	return strings.HasPrefix(h.Command, Bin+" _notify ")
}

type codexRPC struct {
	cmd *exec.Cmd
	enc *json.Encoder
	sc  *bufio.Scanner
	id  int
}

func startCodexRPC(c string) (*codexRPC, error) {
	cmd := exec.Command(c, "app-server")
	cmd.Dir = home
	in, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	out, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	r := &codexRPC{cmd: cmd, enc: json.NewEncoder(in), sc: bufio.NewScanner(out)}
	r.sc.Buffer(make([]byte, 64*1024), 16<<20)
	return r, nil
}

func (r *codexRPC) init() error {
	if _, err := r.call("initialize", map[string]any{"clientInfo": map[string]string{"name": "four", "version": version}}); err != nil {
		return err
	}
	return r.enc.Encode(map[string]any{"method": "initialized"})
}

func (r *codexRPC) call(method string, params any) (json.RawMessage, error) {
	r.id++
	id := r.id
	if err := r.enc.Encode(map[string]any{"id": id, "method": method, "params": params}); err != nil {
		return nil, err
	}
	for r.sc.Scan() {
		var m struct {
			ID     *int            `json:"id"`
			Method string          `json:"method"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(r.sc.Bytes(), &m) != nil || m.ID == nil || *m.ID != id || m.Method != "" {
			continue // notifications et requêtes du serveur : rien à répondre ici
		}
		if m.Error != nil {
			return nil, errors.New(method + " : " + m.Error.Message)
		}
		return m.Result, nil
	}
	return nil, errors.New("codex app-server s'est arrêté")
}

func (r *codexRPC) close() {
	if r.cmd.Process != nil {
		_ = r.cmd.Process.Kill()
	}
	_ = r.cmd.Wait()
}

// Délai pour toute la conversation avec l'app-server : passé ce délai, le
// processus est tué, ce qui débloque la lecture.
func withCodexRPC(c string, f func(*codexRPC) error) error {
	r, err := startCodexRPC(c)
	if err != nil {
		return err
	}
	defer r.close()
	done := make(chan error, 1)
	go func() {
		err := r.init()
		if err == nil {
			err = f(r)
		}
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-time.After(20 * time.Second):
		return errors.New("codex app-server ne répond pas")
	}
}

func listCodexHooks(r *codexRPC) ([]codexHook, error) {
	res, err := r.call("hooks/list", map[string]any{"cwds": []string{home}})
	if err != nil {
		return nil, err
	}
	var lr struct {
		Data []struct {
			Hooks []codexHook `json:"hooks"`
		} `json:"data"`
	}
	if err := json.Unmarshal(res, &lr); err != nil {
		return nil, err
	}
	var out []codexHook
	for _, d := range lr.Data {
		for _, h := range d.Hooks {
			if isFourCodexHook(h) {
				out = append(out, h)
			}
		}
	}
	return out, nil
}

// Les trois hooks de four sont là et approuvés ?
func codexHooksTrusted(c string) bool {
	ok := false
	_ = withCodexRPC(c, func(r *codexRPC) error {
		hs, err := listCodexHooks(r)
		if err != nil {
			return err
		}
		ok = codexHooksReady(hs)
		return nil
	})
	return ok
}

func codexHooksReady(hs []codexHook) bool {
	need := map[string]bool{"done": true, "wait": true, "clear": true}
	for _, h := range hs {
		if h.TrustStatus == "trusted" {
			delete(need, strings.TrimPrefix(h.Command, Bin+" _notify "))
		}
	}
	return len(need) == 0
}

func trustCodexHooks(c string) error {
	return withCodexRPC(c, func(r *codexRPC) error {
		hs, err := listCodexHooks(r)
		if err != nil {
			return err
		}
		var edits []map[string]any
		for _, h := range hs {
			if h.TrustStatus == "trusted" || h.Key == "" || h.CurrentHash == "" {
				continue
			}
			edits = append(edits, map[string]any{
				"keyPath":       `hooks.state."` + h.Key + `".trusted_hash`,
				"value":         h.CurrentHash,
				"mergeStrategy": "upsert",
			})
		}
		if len(edits) > 0 {
			if _, err := r.call("config/batchWrite", map[string]any{"edits": edits, "reloadUserConfig": true}); err != nil {
				return err
			}
			if hs, err = listCodexHooks(r); err != nil {
				return err
			}
		}
		if !codexHooksReady(hs) {
			return errors.New("Codex n'a pas pris les hooks de four (" + codexHooksPath() + ")")
		}
		return nil
	})
}

// Pour four setup : installé, connecté, hooks approuvés.
func codexStatus() (int, string) {
	c := findCodex()
	switch {
	case c == "":
		return stSkip, codexMissing
	case !codexLoggedIn(c):
		return stTodo, "installé · pas encore connecté"
	case !codexHooksTrusted(c):
		return stTodo, "connecté · notifications à activer"
	}
	return stOK, "connecté · ✓ cuit et ◆ attend"
}
