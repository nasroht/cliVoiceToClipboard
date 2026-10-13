package hotkey

import (
	"context"
	"fmt"
	"strconv"
	"strings"
	"syscall"
	"time"
)

var (
	user32               = syscall.NewLazyDLL("user32.dll")
	procGetAsyncKeyState = user32.NewProc("GetAsyncKeyState")
	procMessageBeep      = user32.NewProc("MessageBeep")
)

const (
	// DefaultHotkeyF12 is 0x7B (VK_F12).
	DefaultHotkeyF12 = 0x7B
	// DefaultPollInterval is 10ms (100 Hz), providing sub-10ms responsiveness with ~0% CPU.
	DefaultPollInterval = 10 * time.Millisecond

	// MB_ICONASTERISK is the standard Windows information notification sound.
	MB_ICONASTERISK = 0x00000040
	// MB_OK is standard default sound.
	MB_OK = 0x00000000
)

// Common Windows Virtual Key mapping for human-friendly CLI flag inputs.
var keyNameToVK = map[string]int{
	"LBUTTON":    0x01,
	"RBUTTON":    0x02,
	"MBUTTON":    0x04,
	"XBUTTON1":   0x05,
	"XBUTTON2":   0x06,
	"BACK":       0x08,
	"TAB":        0x09,
	"RETURN":     0x0D,
	"ENTER":      0x0D,
	"SHIFT":      0x10,
	"CONTROL":    0x11,
	"CTRL":       0x11,
	"ALT":        0x12,
	"PAUSE":      0x13,
	"CAPITAL":    0x14,
	"CAPSLOCK":   0x14,
	"ESCAPE":     0x1B,
	"ESC":        0x1B,
	"SPACE":      0x20,
	"PAGEUP":     0x21,
	"PAGEDOWN":   0x22,
	"END":        0x23,
	"HOME":       0x24,
	"LEFT":       0x25,
	"UP":         0x26,
	"RIGHT":      0x27,
	"DOWN":       0x28,
	"SNAPSHOT":   0x2C,
	"PRINTSCRN":  0x2C,
	"INSERT":     0x2D,
	"DELETE":     0x2E,
	"LSHIFT":     0xA0,
	"RSHIFT":     0xA1,
	"LCONTROL":   0xA2,
	"LCTRL":      0xA2,
	"RCONTROL":   0xA3,
	"RCTRL":      0xA3,
	"LMENU":      0xA4,
	"LALT":       0xA4,
	"RMENU":      0xA5,
	"RALT":       0xA5,
	"SCROLL":     0x91,
	"SCROLLLOCK": 0x91,
	"TILDE":      0xC0,
	"GRAVE":      0xC0,
}

func init() {
	// Populate F1 through F24
	for i := 1; i <= 24; i++ {
		keyNameToVK[fmt.Sprintf("F%d", i)] = 0x70 + (i - 1)
	}
	// Populate Numpad 0 through 9
	for i := 0; i <= 9; i++ {
		keyNameToVK[fmt.Sprintf("NUMPAD%d", i)] = 0x60 + i
	}
}

// ParseHotkey converts a hex (0x7B), decimal (123), or friendly key name (F12) to virtual key code.
func ParseHotkey(input string) (int, error) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return DefaultHotkeyF12, nil
	}

	upper := strings.ToUpper(trimmed)
	if vk, ok := keyNameToVK[upper]; ok {
		return vk, nil
	}

	// Try hex with 0x prefix
	if strings.HasPrefix(upper, "0X") {
		val, err := strconv.ParseUint(trimmed[2:], 16, 32)
		if err != nil {
			return 0, fmt.Errorf("invalid hex hotkey %q: %w", input, err)
		}
		return int(val), nil
	}

	// Try decimal
	val, err := strconv.ParseUint(trimmed, 10, 32)
	if err == nil {
		return int(val), nil
	}

	// Try raw hex without 0x prefix if valid hex chars
	valHex, errHex := strconv.ParseUint(trimmed, 16, 32)
	if errHex == nil {
		return int(valHex), nil
	}

	return 0, fmt.Errorf("unrecognized hotkey %q (use hex like 0x7B or key name like F12)", input)
}

// KeyName returns a human-readable representation of a virtual key code.
func KeyName(vk int) string {
	for name, code := range keyNameToVK {
		if code == vk {
			return fmt.Sprintf("%s (0x%02X)", name, vk)
		}
	}
	return fmt.Sprintf("0x%02X", vk)
}

// IsKeyDown queries the Windows global keyboard state for the virtual key using GetAsyncKeyState.
// This is a passive, read-only system call that never simulates input, blocks keys, or interacts with game hooks.
func IsKeyDown(vKey int) bool {
	ret, _, _ := procGetAsyncKeyState.Call(uintptr(vKey))
	// If the most significant bit is set, the key is currently down.
	return (int16(ret) & int16(-0x8000)) != 0
}

// PlayChime emits a subtle Windows notification beep via user32.dll MessageBeep.
func PlayChime() {
	_, _, _ = procMessageBeep.Call(uintptr(MB_ICONASTERISK))
}

// Listener monitors the push-to-talk hotkey using GetAsyncKeyState in a low-overhead loop.
type Listener struct {
	vKey         int
	pollInterval time.Duration
}

// NewListener creates a new hotkey listener for the specified virtual key code.
func NewListener(vKey int, pollInterval time.Duration) *Listener {
	if pollInterval <= 0 {
		pollInterval = DefaultPollInterval
	}
	return &Listener{
		vKey:         vKey,
		pollInterval: pollInterval,
	}
}

// Listen starts the polling loop and invokes callbacks on key down and key up edge transitions.
// Exits cleanly when ctx is cancelled.
func (l *Listener) Listen(ctx context.Context, onKeyDown func(), onKeyUp func()) error {
	ticker := time.NewTicker(l.pollInterval)
	defer ticker.Stop()

	wasPressed := false

	for {
		select {
		case <-ctx.Done():
			// If key was held when context was cancelled, fire onKeyUp to clean up
			if wasPressed && onKeyUp != nil {
				onKeyUp()
			}
			return ctx.Err()
		case <-ticker.C:
			isPressed := IsKeyDown(l.vKey)

			if isPressed && !wasPressed {
				// Transition: Idle -> Held
				wasPressed = true
				if onKeyDown != nil {
					onKeyDown()
				}
			} else if !isPressed && wasPressed {
				// Transition: Held -> Released
				wasPressed = false
				if onKeyUp != nil {
					onKeyUp()
				}
			}
		}
	}
}
