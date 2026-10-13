package hotkey

import (
	"testing"
)

func TestParseHotkey(t *testing.T) {
	tests := []struct {
		input    string
		expected int
		wantErr  bool
	}{
		{"", DefaultHotkeyF12, false},
		{"F12", 0x7B, false},
		{"f12", 0x7B, false},
		{"F1", 0x70, false},
		{"0x7B", 0x7B, false},
		{"0x7b", 0x7B, false},
		{"123", 123, false},
		{"CAPSLOCK", 0x14, false},
		{"LALT", 0xA4, false},
		{"RALT", 0xA5, false},
		{"SPACE", 0x20, false},
		{"NUMPAD0", 0x60, false},
		{"XBUTTON1", 0x05, false},
		{"INVALID_KEY_NAME_XYZ", 0, true},
	}

	for _, tc := range tests {
		got, err := ParseHotkey(tc.input)
		if (err != nil) != tc.wantErr {
			t.Errorf("ParseHotkey(%q) err = %v, wantErr = %v", tc.input, err, tc.wantErr)
			continue
		}
		if !tc.wantErr && got != tc.expected {
			t.Errorf("ParseHotkey(%q) = 0x%02X, expected 0x%02X", tc.input, got, tc.expected)
		}
	}
}

func TestKeyName(t *testing.T) {
	name := KeyName(0x7B)
	if name != "F12 (0x7B)" {
		t.Errorf("expected F12 (0x7B), got %s", name)
	}

	name = KeyName(0x9999)
	if name != "0x9999" {
		t.Errorf("expected 0x9999, got %s", name)
	}
}
