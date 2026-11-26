package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestInstallAgyHooks(t *testing.T) {
	old := home
	home = t.TempDir()
	defer func() { home = old }()
	if agyHooksInstalled() {
		t.Fatal("hooks vus sans fichier")
	}
	p := agyHooksPath()
	_ = os.MkdirAll(filepath.Dir(p), 0o700)
	autre := `{"lint": {"Stop": [{"command": "./lint.sh"}]}}`
	if err := os.WriteFile(p, []byte(autre), 0o600); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ { // deux fois : pas de doublon
		if err := installAgyHooks(); err != nil {
			t.Fatal(err)
		}
	}
	if !agyHooksInstalled() {
		t.Fatal("hooks de four absents après installation")
	}
	var d map[string]any
	b, _ := os.ReadFile(p)
	if err := json.Unmarshal(b, &d); err != nil || d["lint"] == nil || len(d) != 2 {
		t.Fatalf("les autres hooks doivent rester : %s", b)
	}
}
