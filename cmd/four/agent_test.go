package main

import (
	"reflect"
	"testing"
)

func TestNormAgent(t *testing.T) {
	for in, want := range map[string]string{"": agentClaude, "claude": agentClaude, "codex": agentCodex, "agy": agentAgy, "autre": agentClaude} {
		if got := normAgent(in); got != want {
			t.Errorf("normAgent(%q) = %q, attendu %q", in, got, want)
		}
	}
}

func TestAgentArgs(t *testing.T) {
	cases := []struct {
		agent string
		auto  bool
		want  []string
	}{
		{agentClaude, false, nil},
		{agentClaude, true, []string{"--dangerously-skip-permissions"}},
		{agentCodex, false, nil},
		{agentCodex, true, []string{"--dangerously-bypass-approvals-and-sandbox"}},
		{agentAgy, false, nil},
		{agentAgy, true, []string{"--dangerously-skip-permissions"}},
		{"", true, []string{"--dangerously-skip-permissions"}}, // session d'avant Codex
	}
	for _, c := range cases {
		if got := agentArgs(c.agent, c.auto); !reflect.DeepEqual(got, c.want) {
			t.Errorf("agentArgs(%q, %v) = %v, attendu %v", c.agent, c.auto, got, c.want)
		}
	}
}

func TestCodexHooksReady(t *testing.T) {
	h := func(arg, trust string) codexHook {
		return codexHook{Command: Bin + " _notify " + arg, TrustStatus: trust}
	}
	all := []codexHook{h("done", "trusted"), h("wait", "trusted"), h("clear", "trusted")}
	if !codexHooksReady(all) {
		t.Error("les trois hooks approuvés : prêt attendu")
	}
	if codexHooksReady([]codexHook{h("done", "trusted"), h("wait", "modified"), h("clear", "trusted")}) {
		t.Error("un hook modifié depuis son approbation : pas prêt")
	}
	if codexHooksReady(all[:2]) {
		t.Error("un hook manquant : pas prêt")
	}
	// Un ancien chemin de four, encore approuvé, ne remplace pas le bon.
	old := codexHook{Command: "/ancien/four _notify clear", TrustStatus: "trusted"}
	if isFourCodexHook(old) || codexHooksReady(append(all[:2:2], old)) {
		t.Error("hook d'un autre binaire compté")
	}
}
