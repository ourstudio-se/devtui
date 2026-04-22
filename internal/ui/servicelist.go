package ui

import (
	"fmt"
	"github.com/ourstudio-se/devtui/internal/service"
	"strings"

	"charm.land/lipgloss/v2"
)

type GroupInfo struct {
	Name       string
	StartIndex int
	Count      int
	Collapsed  bool
}

// renderServiceList renders the left panel. scrollOffset should already be
// adjusted by the caller (adjustListScroll).
func renderServiceList(services []*service.Service, groups []GroupInfo, cursor int, scrollOffset, width, height int, focused bool) string {
	title := " Services "
	borderStyle := BorderStyle
	if focused {
		borderStyle = FocusedBorderStyle
	}

	lines := make([]string, 0, len(services)+len(groups))

	for gi := range groups {
		g := &groups[gi]
		arrow := "▾"
		if g.Collapsed {
			arrow = "▸"
		}
		header := GroupHeaderStyle.Render(fmt.Sprintf(" %s %s", arrow, g.Name))
		lines = append(lines, header)

		if !g.Collapsed {
			for i := 0; i < g.Count; i++ {
				idx := g.StartIndex + i
				if idx >= len(services) {
					break
				}
				lines = append(lines, renderServiceLine(services[idx], idx, cursor, width-4))
			}
		}
	}

	contentHeight := height - 2 // borders
	if contentHeight < 1 {
		contentHeight = 1
	}

	visibleLines := lines
	if len(lines) > contentHeight {
		end := scrollOffset + contentHeight
		if end > len(lines) {
			end = len(lines)
		}
		visibleLines = lines[scrollOffset:end]
	}

	// Pad to fill height
	for len(visibleLines) < contentHeight {
		visibleLines = append(visibleLines, "")
	}

	content := strings.Join(visibleLines, "\n")

	panel := borderStyle.
		Width(width).
		Height(height).
		Render(content)

	// Inject title into top border (ANSI-aware)
	panelLines := strings.Split(panel, "\n")
	if len(panelLines) > 0 {
		panelLines[0] = injectBorderTitle(panelLines[0], title, 2)
	}

	return strings.Join(panelLines, "\n")
}

func renderServiceLine(svc *service.Service, index, cursor, maxWidth int) string {
	isCurrent := index == cursor

	icon := lipgloss.NewStyle().
		Foreground(StateColor(svc.State)).
		Render(svc.State.Icon())

	name := svc.Name
	portStr := ""
	if svc.Port > 0 {
		portStr = fmt.Sprintf(":%d", svc.Port)
	}

	// Truncate name if needed
	maxName := maxWidth - 8 - len(portStr) // icon + spaces + port
	if maxName < 10 {
		maxName = 10
	}
	if len(name) > maxName {
		name = name[:maxName-3] + "..."
	}

	// Build the line
	prefix := "  "
	if isCurrent {
		prefix = CursorStyle.Render("> ")
	}

	nameStyled := ServiceNameStyle.Render(name)
	if isCurrent {
		nameStyled = CursorStyle.Render(name)
	}

	portStyled := PortStyle.Render(portStr)

	// Right-align port
	padding := maxWidth - 4 - len(name) - len(portStr)
	if padding < 1 {
		padding = 1
	}

	return fmt.Sprintf("%s %s %s%s%s", prefix, icon, nameStyled, strings.Repeat(" ", padding), portStyled)
}

func findCursorLine(groups []GroupInfo, cursor int) int {
	line := 0
	for _, g := range groups {
		line++ // group header
		if !g.Collapsed {
			for i := 0; i < g.Count; i++ {
				if g.StartIndex+i == cursor {
					return line
				}
				line++
			}
		}
	}
	return 0
}
