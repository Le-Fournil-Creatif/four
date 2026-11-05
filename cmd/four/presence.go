package main

// Présence : chaque session four écrit une fiche dans /srv/repos/.four/presence
// (qui, quel projet, état de Claude). Les sessions vivent dans le tmux de
// chaque utilisateur, invisibles pour l'autre : ces fiches font le lien. Une
// fiche dont le processus n'existe plus est ignorée puis effacée.

import (
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"
)

type Presence struct {
	User    string `json:"user"`
	Project string `json:"project"`
	Dir     string `json:"dir"`
	Session string `json:"session"`
	Pid     int    `json:"pid"`
	Start   uint64 `json:"start"`
	State   string `json:"state"` // open | busy | done | wait
	Since   int64  `json:"since"`
	Started int64  `json:"started"`
}

func presencePath(user, session string) string {
	return filepath.Join(presenceDir(), user+"__"+session+".json")
}

func writePresence(p Presence) {
	_ = os.MkdirAll(presenceDir(), 0o2775)
	b, _ := json.Marshal(p)
	f := presencePath(p.User, p.Session)
	tmp := f + ".tmp"
	if os.WriteFile(tmp, b, 0o664) == nil {
		_ = os.Chmod(tmp, 0o664)
		_ = os.Rename(tmp, f)
	}
}

func setPresenceState(user, session, state string) {
	f := presencePath(user, session)
	b, err := os.ReadFile(f)
	if err != nil {
		return
	}
	var p Presence
	if json.Unmarshal(b, &p) != nil {
		return
	}
	p.State, p.Since = state, time.Now().Unix()
	writePresence(p)
}

func removePresence(user, session string) { _ = os.Remove(presencePath(user, session)) }

// Heure de démarrage du processus (champ 22 de /proc/<pid>/stat) : protège
// contre un pid réutilisé.
func procStart(pid int) uint64 {
	b, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return 0
	}
	s := string(b)
	i := strings.LastIndexByte(s, ')')
	if i < 0 {
		return 0
	}
	f := strings.Fields(s[i+1:])
	if len(f) < 20 {
		return 0
	}
	v, _ := strconv.ParseUint(f[19], 10, 64)
	return v
}

func alive(pid int, start uint64) bool {
	if pid <= 0 {
		return false
	}
	err := syscall.Kill(pid, 0)
	if err != nil && !errors.Is(err, syscall.EPERM) {
		return false
	}
	return start == 0 || procStart(pid) == start
}

// Toutes les fiches valides (y compris les miennes).
func readPresence() []Presence {
	ents, err := os.ReadDir(presenceDir())
	if err != nil {
		return nil
	}
	var out []Presence
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		f := filepath.Join(presenceDir(), e.Name())
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var p Presence
		if json.Unmarshal(b, &p) != nil {
			continue
		}
		if !alive(p.Pid, p.Start) {
			_ = os.Remove(f)
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].User != out[j].User {
			return out[i].User < out[j].User
		}
		return out[i].Since > out[j].Since
	})
	return out
}

// Qui est connecté en SSH en ce moment (commande users).
func onlineUsers() map[string]bool {
	out, err := exec.Command("users").Output()
	m := map[string]bool{}
	if err != nil {
		return m
	}
	for _, u := range strings.Fields(string(out)) {
		m[u] = true
	}
	return m
}
