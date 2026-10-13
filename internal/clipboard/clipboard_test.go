package clipboard

import (
	"testing"
)

func TestCleanText(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"", ""},
		{"   ", ""},
		{"[BLANK_AUDIO]", ""},
		{"(silence)", ""},
		{"[music]", ""},
		{"... ", ""},
		{" . . . ", ""},
		{"  Hello World  ", "Hello World"},
		{"Hello   World!", "Hello World!"},
		{"[BLANK_AUDIO] Incoming attack on left flank!", "Incoming attack on left flank!"},
		{"Heal me please! (music)", "Heal me please!"},
		{"Taunt boss now, swap on 3 stacks.", "Taunt boss now, swap on 3 stacks."},
	}

	for _, tc := range tests {
		got := CleanText(tc.input)
		if got != tc.expected {
			t.Errorf("CleanText(%q) = %q, expected %q", tc.input, got, tc.expected)
		}
	}
}

func TestWriteEmpty(t *testing.T) {
	_, err := Write("   [BLANK_AUDIO]   ")
	if err != ErrEmptyText {
		t.Errorf("expected ErrEmptyText, got %v", err)
	}
}
