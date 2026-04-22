package ui

import (
	"fmt"
	"github.com/ourstudio-se/devtui/internal/service"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

func renderLogViewer(title string, logBuffer *service.RingBuffer, width, height int, focused bool, scrollOffset int, autoScroll bool) string {
	borderStyle := BorderStyle
	if focused {
		borderStyle = FocusedBorderStyle
	}

	headerTitle := " Logs "
	if title != "" {
		headerTitle = fmt.Sprintf(" Logs: %s ", title)
	}

	contentHeight := height - 2 // borders
	if contentHeight < 1 {
		contentHeight = 1
	}

	contentWidth := width - 4
	if contentWidth < 10 {
		contentWidth = 10
	}

	var rawLines []string
	if logBuffer != nil {
		rawLines = logBuffer.Lines()
	}

	if len(rawLines) == 0 {
		rawLines = []string{lipgloss.NewStyle().Foreground(lipgloss.Color("240")).Render("No logs yet...")}
	}

	// Wrap long lines (ANSI-aware -- preserves color codes)
	var lines []string
	for _, line := range rawLines {
		wrapped := ansi.Hardwrap(line, contentWidth, false)
		lines = append(lines, strings.Split(wrapped, "\n")...)
	}

	// Apply scroll offset
	totalLines := len(lines)
	if autoScroll || scrollOffset < 0 {
		scrollOffset = totalLines - contentHeight
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	if scrollOffset > totalLines-contentHeight {
		scrollOffset = totalLines - contentHeight
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}

	end := scrollOffset + contentHeight
	if end > totalLines {
		end = totalLines
	}
	visibleLines := lines[scrollOffset:end]

	// Pad to fill height
	for len(visibleLines) < contentHeight {
		visibleLines = append(visibleLines, "")
	}

	content := strings.Join(visibleLines, "\n")

	// Scroll indicator
	scrollInfo := ""
	if totalLines > contentHeight {
		if autoScroll {
			scrollInfo = " [follow] "
		} else {
			pct := 100
			if totalLines-contentHeight > 0 {
				pct = scrollOffset * 100 / (totalLines - contentHeight)
			}
			scrollInfo = fmt.Sprintf(" %d%% ", pct)
		}
	}

	panel := borderStyle.
		Width(width).
		Height(height).
		Render(content)

	// Inject title and scroll info into borders (ANSI-aware)
	panelLines := strings.Split(panel, "\n")
	if len(panelLines) > 0 {
		panelLines[0] = injectBorderTitle(panelLines[0], headerTitle, 2)
	}
	if scrollInfo != "" && len(panelLines) > 0 {
		panelLines[len(panelLines)-1] = injectBorderTitleRight(panelLines[len(panelLines)-1], scrollInfo, 2)
	}

	return strings.Join(panelLines, "\n")
}
