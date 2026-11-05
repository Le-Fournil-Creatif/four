package main

import (
	"os"
	"path/filepath"
	"testing"
)

// Le mode sans confirmation ne s'active que sur demande, jamais sur un projet prod.
func TestAutoMode(t *testing.T) {
	cases := []struct {
		name, prod, auto, project string
		want                      bool
	}{
		{"aucun fichier", "", "", "app", false},
		{"non listé", "", "autre\n", "app", false},
		{"listé", "", "app\n", "app", true},
		{"tous", "", "*\n", "app", true},
		{"commentaire ignoré", "", "# app\n", "app", false},
		{"prod l'emporte sur le nom", "app\n", "app\n", "app", false},
		{"prod l'emporte sur *", "app\n", "*\n", "app", false},
		{"* hors prod", "app\n", "*\n", "autre", true},
	}
	old := Root
	defer func() { Root = old }()
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			Root = t.TempDir()
			if err := os.MkdirAll(shareDir(), 0o755); err != nil {
				t.Fatal(err)
			}
			for f, v := range map[string]string{"prod": c.prod, "auto": c.auto} {
				if v == "" {
					continue
				}
				if err := os.WriteFile(filepath.Join(shareDir(), f), []byte(v), 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if got := autoMode(c.project); got != c.want {
				t.Errorf("autoMode(%q) = %v, attendu %v", c.project, got, c.want)
			}
		})
	}
}
