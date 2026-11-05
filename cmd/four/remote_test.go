package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRemoteParents(t *testing.T) {
	// Un pid qui n'existe plus : celui d'une commande déjà terminée.
	c := exec.Command("true")
	if err := c.Run(); err != nil {
		t.Skip(err)
	}
	dead := c.Process.Pid
	self, parent := os.Getpid(), os.Getppid()

	cases := []struct {
		name, body string
		want       bool
	}{
		{"connecté", fmt.Sprintf(`{"pid":%d,"bridgeSessionId":"abc"}`, self), true},
		{"déconnecté", fmt.Sprintf(`{"pid":%d,"bridgeSessionId":null}`, self), false},
		{"champ absent", fmt.Sprintf(`{"pid":%d}`, self), false},
		{"processus mort", fmt.Sprintf(`{"pid":%d,"bridgeSessionId":"abc"}`, dead), false},
		{"fichier illisible", `pas du json`, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := os.WriteFile(filepath.Join(dir, "1.json"), []byte(c.body), 0o600); err != nil {
				t.Fatal(err)
			}
			if got := remoteParents(dir)[parent]; got != c.want {
				t.Errorf("remoteParents = %v, attendu %v", got, c.want)
			}
		})
	}
}
