package main

// Remote Control : Claude Code note bridgeSessionId dans
// ~/.claude/sessions/<pid>.json tant qu'une session est connectée. Ce format
// n'est pas documenté : s'il change, l'indicateur affichera « remote off ».

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Pid des parents (les panneaux tmux) dont le Claude a Remote Control connecté.
func remoteParents(dir string) map[int]bool {
	out := map[int]bool{}
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return out
	}
	for _, f := range files {
		b, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var s struct {
			Pid    int    `json:"pid"`
			Bridge string `json:"bridgeSessionId"`
		}
		if json.Unmarshal(b, &s) != nil || s.Bridge == "" || !alive(s.Pid, 0) {
			continue
		}
		if ppid := procParent(s.Pid); ppid > 0 {
			out[ppid] = true
		}
	}
	return out
}

// Parent d'un processus (champ 4 de /proc/<pid>/stat).
func procParent(pid int) int {
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
	if len(f) < 2 {
		return 0
	}
	ppid, _ := strconv.Atoi(f[1])
	return ppid
}
