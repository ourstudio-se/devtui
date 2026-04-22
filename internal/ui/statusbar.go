package ui

import (
	"fmt"
	"github.com/ourstudio-se/devtui/internal/service"
	"strings"

	"charm.land/bubbles/v2/key"
	"charm.land/lipgloss/v2"
)

func renderStatusBar(services []*service.Service, keys KeyMap, width int, focus FocusPanel, confirmStop string) string {
	if confirmStop != "" {
		prompt := fmt.Sprintf(" Stop external service %q? (y/n)", confirmStop)
		return lipgloss.NewStyle().
			Foreground(lipgloss.Color("208")).
			Width(width).
			Padding(0, 1).
			Render(prompt)
	}

	running := 0
	total := len(services)
	for _, svc := range services {
		if svc.State == service.StateRunning {
			running++
		}
	}

	status := fmt.Sprintf(" %d/%d running", running, total)

	var hints []string
	if focus == PanelServiceList {
		hints = append(hints,
			formatNavHint(keys.Up, keys.Down),
			formatSingleHint(keys.Toggle),
			formatSingleHint(keys.Tab),
			formatSingleHint(keys.StartGroup),
			formatSingleHint(keys.Help),
			formatSingleHint(keys.Detach),
		)
	} else {
		hints = append(hints,
			formatNavHint(keys.Up, keys.Down),
			formatSingleHint(keys.PageUp),
			formatSingleHint(keys.Tab),
			formatSingleHint(keys.Follow),
			formatSingleHint(keys.Help),
			formatSingleHint(keys.Detach),
		)
	}

	hintsStr := strings.Join(hints, "  ")

	padding := width - len(status) - len(hintsStr) - 2
	if padding < 1 {
		padding = 1
	}

	return StatusBarStyle.Width(width).Render(
		status + strings.Repeat(" ", padding) + hintsStr,
	)
}

func formatNavHint(bindings ...key.Binding) string {
	var keys []string
	for _, b := range bindings {
		help := b.Help()
		keys = append(keys, help.Key)
	}
	return HelpKeyStyle.Render(strings.Join(keys, "/")) + " " + HelpDescStyle.Render("navigate")
}

func formatSingleHint(b key.Binding) string {
	help := b.Help()
	return HelpKeyStyle.Render(help.Key) + " " + HelpDescStyle.Render(help.Desc)
}
