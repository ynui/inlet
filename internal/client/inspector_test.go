package client

import "testing"

func TestInspectorReset(t *testing.T) {
	ins := NewInspector("", "")
	ins.Add(Event{Status: 200})
	ins.Add(Event{Status: 500})
	ins.Reset()
	if total, s2, _, _, s5 := ins.Stats(); total+s2+s5 != 0 {
		t.Fatalf("stats after reset = %d %d %d", total, s2, s5)
	}
	if len(ins.Events()) != 0 {
		t.Fatalf("events after reset = %d", len(ins.Events()))
	}
}
