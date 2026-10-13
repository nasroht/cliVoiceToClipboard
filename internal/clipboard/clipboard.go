package clipboard

import (
	"errors"
	"regexp"
	"strings"
	"syscall"

	atottoclipboard "github.com/atotto/clipboard"
)

var (
	user32          = syscall.NewLazyDLL("user32.dll")
	procMessageBeep = user32.NewProc("MessageBeep")
)

var (
	// ErrEmptyText is returned when text is empty after cleaning.
	ErrEmptyText = errors.New("transcribed text is empty after cleaning")
)

var (
	// Regex to remove Whisper noise / artifact tags like [BLANK_AUDIO], [music], (silence)
	artifactPattern = regexp.MustCompile(`(?i)\[(blank_audio|silence|music|applause|laughter|noise|\s*)\]|\((blank_audio|silence|music|applause|laughter|noise|\s*)\)`)
	// Regex to collapse consecutive whitespace into a single space
	multipleSpaces = regexp.MustCompile(`[ \t]+`)
)

// CleanText sanitizes whisper transcription output.
// It trims whitespace, strips hallucinated bracket tokens, collapses internal spaces,
// and removes trailing artifact ellipsis.
func CleanText(raw string) string {
	cleaned := artifactPattern.ReplaceAllString(raw, " ")
	cleaned = multipleSpaces.ReplaceAllString(cleaned, " ")
	cleaned = strings.TrimSpace(cleaned)

	// If the entire text consists only of dots/punctuation/whitespace (e.g. "...", " . ")
	if isOnlyPunctuationOrEmpty(cleaned) {
		return ""
	}

	return cleaned
}

func isOnlyPunctuationOrEmpty(s string) bool {
	if s == "" {
		return true
	}
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r > 127 {
			return false
		}
	}
	return true
}

// Write cleans the provided text and copies it to the Windows clipboard.
// Returns the cleaned text written, or an error.
func Write(raw string) (string, error) {
	cleaned := CleanText(raw)
	if cleaned == "" {
		return "", ErrEmptyText
	}

	if err := atottoclipboard.WriteAll(cleaned); err != nil {
		return "", err
	}

	return cleaned, nil
}

// Read reads the current text from the clipboard.
func Read() (string, error) {
	return atottoclipboard.ReadAll()
}

// PlayChime triggers a short, non-intrusive native Windows chime via user32.dll MessageBeep(0).
func PlayChime() {
	// 0 corresponds to MB_OK: default notification sound
	_, _, _ = procMessageBeep.Call(0)
}
