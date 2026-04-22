package ui

import (
	"github.com/ourstudio-se/devtui/internal/service"
	"image/color"
	"strings"
	"unicode/utf8"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

var (
	// Panel borders
	BorderStyle = lipgloss.NewStyle().
			Border(lipgloss.RoundedBorder()).
			BorderForeground(lipgloss.Color("240"))

	FocusedBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(lipgloss.Color("62"))

	// Service list
	GroupHeaderStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(lipgloss.Color("254"))

	CursorStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86"))

	ServiceNameStyle = lipgloss.NewStyle().
				Foreground(lipgloss.Color("252"))

	PortStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))

	// Status bar
	StatusBarStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240")).
			Padding(0, 1)

	// Help overlay
	HelpKeyStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(lipgloss.Color("86"))

	HelpDescStyle = lipgloss.NewStyle().
			Foreground(lipgloss.Color("240"))
)

// injectBorderTitle replaces visible border characters in a rendered border
// line with the given title, starting at visible position `offset`. It walks
// the string tracking only visible characters so that ANSI escape sequences
// (from BorderForeground etc.) are preserved untouched.
func injectBorderTitle(borderLine, title string, offset int) string {
	titleRunes := []rune(title)
	if len(titleRunes) == 0 {
		return borderLine
	}

	lineWidth := ansi.StringWidth(borderLine)
	if offset+len(titleRunes) > lineWidth-1 {
		return borderLine
	}

	var buf strings.Builder
	visiblePos := 0
	titleIdx := 0
	i := 0
	bytes := []byte(borderLine)

	for i < len(bytes) {
		b := bytes[i]

		// Start of an ANSI escape sequence -- copy it verbatim
		if b == '\x1b' && i+1 < len(bytes) && bytes[i+1] == '[' {
			buf.WriteByte(b)
			i++
			for i < len(bytes) {
				buf.WriteByte(bytes[i])
				if (bytes[i] >= 'A' && bytes[i] <= 'Z') || (bytes[i] >= 'a' && bytes[i] <= 'z') {
					i++
					break
				}
				i++
			}
			continue
		}

		// Decode a UTF-8 character
		r, size := utf8.DecodeRune(bytes[i:])

		if visiblePos >= offset && titleIdx < len(titleRunes) {
			// Replace this visible character with next title rune
			buf.WriteRune(titleRunes[titleIdx])
			titleIdx++
		} else {
			// Keep the original character
			for j := 0; j < size; j++ {
				buf.WriteByte(bytes[i+j])
			}
		}
		_ = r
		i += size
		visiblePos++
	}

	return buf.String()
}

// injectBorderTitleRight injects text right-aligned into a border line,
// ending `offset` visible characters from the right edge.
func injectBorderTitleRight(borderLine, text string, offset int) string {
	lineWidth := ansi.StringWidth(borderLine)
	textWidth := ansi.StringWidth(text)
	pos := lineWidth - textWidth - offset
	if pos < 2 {
		return borderLine
	}
	return injectBorderTitle(borderLine, text, pos)
}

func StateColor(s service.State) color.Color {
	switch s {
	case service.StateStopped:
		return lipgloss.Color("240")
	case service.StateStarting, service.StateStopping, service.StateBuilding:
		return lipgloss.Color("220")
	case service.StateRunning:
		return lipgloss.Color("82")
	case service.StateError:
		return lipgloss.Color("196")
	case service.StateExternal:
		return lipgloss.Color("208")
	default:
		return lipgloss.Color("240")
	}
}
