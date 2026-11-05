package main

import (
	"testing"
	"time"
)

func TestPresenceActive(t *testing.T) {
	now := time.Unix(100000, 0)
	cases := []struct {
		name, state string
		age         time.Duration
		want        bool
	}{
		{"tâche en cours", "busy", 3 * time.Hour, true},
		{"terminée à l'instant", "done", 0, true},
		{"terminée juste avant 5 min", "done", onlineGrace - time.Second, true},
		{"terminée depuis 5 min", "done", onlineGrace, false},
		{"en attente récente", "wait", time.Minute, true},
		{"en attente ancienne", "wait", time.Hour, false},
		{"session ouverte sans tâche", "open", 0, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := Presence{State: c.state, Since: now.Add(-c.age).Unix()}
			if got := p.active(now); got != c.want {
				t.Errorf("active = %v, attendu %v", got, c.want)
			}
		})
	}
}

func TestFirstActive(t *testing.T) {
	now := time.Unix(100000, 0)
	list := []Presence{
		{User: "marie", State: "done", Since: now.Add(-time.Hour).Unix()},
		{User: "paul", State: "busy", Project: "autre"},
		{User: "marie", State: "busy", Project: "bon"},
	}
	if p := firstActive(list, "marie", now); p == nil || p.Project != "bon" {
		t.Errorf("firstActive(marie) = %+v, attendu le projet bon", p)
	}
	if p := firstActive(list, "lea", now); p != nil {
		t.Errorf("firstActive(lea) = %+v, attendu aucune fiche", p)
	}
}
