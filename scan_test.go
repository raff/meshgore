package meshgore

import "testing"

func TestNameMatches(t *testing.T) {
	for _, c := range []struct {
		adv, want string
		ok        bool
	}{
		{"MeshCore-KO6IYC-SC", "KO6IYC-SC", true},          // short name, name has a dash
		{"MeshCore-KO6IYC-SC", "MeshCore-KO6IYC-SC", true}, // full name
		{"Other-Node", "Node", true},                       // another prefix
		{"MeshCore-Node", "Nod", false},
		{"MeshCore-Node", "", false},
		{"Node", "Node", true}, // no prefix at all
		{"Node", "ode", false},
	} {
		if got := nameMatches(c.adv, c.want); got != c.ok {
			t.Errorf("nameMatches(%q, %q) = %v, want %v", c.adv, c.want, got, c.ok)
		}
	}
}

func TestIsCompanion(t *testing.T) {
	if !isCompanion(true, "") || !isCompanion(false, "MeshCore-X") || isCompanion(false, "LYWSD02") {
		t.Error("wrong classification")
	}
}
