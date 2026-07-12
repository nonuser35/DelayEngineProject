package hotkey

import "testing"

func TestParseSupportsOneTwoOrThreeKeys(t *testing.T) {
	tests := map[string]string{
		"f7":           "F7",
		"ctrl+f7":      "Ctrl+F7",
		"Ctrl+Alt+d":   "Ctrl+Alt+D",
		"shift + 9":    "Shift+9",
		"win+pageDown": "Win+PageDown",
	}
	for input, expected := range tests {
		binding, err := Parse(input)
		if err != nil {
			t.Fatalf("Parse(%q): %v", input, err)
		}
		if binding.Canonical != expected {
			t.Fatalf("Parse(%q) = %q, want %q", input, binding.Canonical, expected)
		}
	}
}

func TestParseRejectsInvalidBindings(t *testing.T) {
	for _, input := range []string{"Ctrl", "Ctrl+Alt+Shift+D", "Ctrl+D+A", "F12", "Ctrl+?"} {
		if _, err := Parse(input); err == nil {
			t.Fatalf("Parse(%q) should fail", input)
		}
	}
}

func TestValidatePairRejectsDuplicate(t *testing.T) {
	if _, err := ValidatePair("F7", "f7"); err == nil {
		t.Fatal("expected duplicate shortcut error")
	}
}
