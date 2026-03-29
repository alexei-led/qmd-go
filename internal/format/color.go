package format

import (
	"fmt"
	"os"
)

var colorEnabled bool

func init() {
	colorEnabled = detectColor()
}

func detectColor() bool {
	if os.Getenv("NO_COLOR") != "" || os.Getenv("QMD_NO_COLOR") != "" {
		return false
	}
	fi, err := os.Stdout.Stat()
	if err != nil {
		return false
	}
	return (fi.Mode() & os.ModeCharDevice) != 0
}

// ColorEnabled returns whether ANSI color output is active.
func ColorEnabled() bool { return colorEnabled }

// SetColor overrides color detection (useful for testing).
func SetColor(enabled bool) { colorEnabled = enabled }

func ansi(code, s string) string {
	if !colorEnabled {
		return s
	}
	return fmt.Sprintf("\033[%sm%s\033[0m", code, s)
}

// Bold renders text in bold.
func Bold(s string) string { return ansi("1", s) }

// Dim renders text in dim.
func Dim(s string) string { return ansi("2", s) }

// Green renders text in green.
func Green(s string) string { return ansi("32", s) }

// Yellow renders text in yellow.
func Yellow(s string) string { return ansi("33", s) }

// Cyan renders text in cyan.
func Cyan(s string) string { return ansi("36", s) }
