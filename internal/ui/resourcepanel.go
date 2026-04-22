package ui

import (
	"fmt"
	"github.com/ourstudio-se/devtui/internal/msgs"
	"sort"
	"strings"
)

const (
	resourcePanelBorders  = 2
	resourcePanelMaxItems = 5
)

type rankedService struct {
	name  string
	stats msgs.ResourceStats
}

// resourcePanelContentHeight returns the number of content lines for n services.
func resourcePanelContentHeight(n int) int {
	if n == 0 {
		return 0
	}
	shown := n
	others := 0
	if shown > resourcePanelMaxItems {
		shown = resourcePanelMaxItems
		others = 1
	}
	return shown + others // individual lines + optional others
}

func renderResourcePanel(stats map[string]msgs.ResourceStats, width, height int) string {
	contentHeight := height - resourcePanelBorders
	if contentHeight < 1 {
		contentHeight = 1
	}

	innerWidth := width - 4 // border chars + padding

	// Collect and sort by CPU descending
	ranked := make([]rankedService, 0, len(stats))
	var totalCPU, totalMem float64
	for name, s := range stats {
		ranked = append(ranked, rankedService{name: name, stats: s})
		totalCPU += s.CPUPercent
		totalMem += s.MemUsageMB
	}
	sort.Slice(ranked, func(i, j int) bool {
		return ranked[i].stats.CPUPercent > ranked[j].stats.CPUPercent
	})

	var lines []string

	// Top N individual services
	shown := len(ranked)
	if shown > resourcePanelMaxItems {
		shown = resourcePanelMaxItems
	}
	for i := 0; i < shown; i++ {
		r := ranked[i]
		cpuStr := formatCPU(r.stats.CPUPercent)
		lines = append(lines, formatResourceLine(" "+r.name, cpuStr, r.stats.MemUsageMB, innerWidth))
	}

	// Others aggregated line
	if len(ranked) > resourcePanelMaxItems {
		var otherCPU, otherMem float64
		for i := resourcePanelMaxItems; i < len(ranked); i++ {
			otherCPU += ranked[i].stats.CPUPercent
			otherMem += ranked[i].stats.MemUsageMB
		}
		othersLabel := fmt.Sprintf(" [%d others]", len(ranked)-resourcePanelMaxItems)
		cpuStr := formatCPU(otherCPU)
		lines = append(lines, formatResourceLine(othersLabel, cpuStr, otherMem, innerWidth))
	}

	for len(lines) < contentHeight {
		lines = append(lines, "")
	}

	content := strings.Join(lines[:contentHeight], "\n")

	panel := BorderStyle.
		Width(width).
		Height(height).
		Render(content)

	panelLines := strings.Split(panel, "\n")
	if len(panelLines) > 0 {
		panelLines[0] = injectBorderTitle(panelLines[0], " Resources ", 2)
		totalLabel := fmt.Sprintf(" %s %s ", formatCPU(totalCPU), formatMem(totalMem))
		panelLines[0] = injectBorderTitleRight(panelLines[0], totalLabel, 1)
	}

	return strings.Join(panelLines, "\n")
}

func formatCPU(cpuPercent float64) string {
	m := cpuPercent * 10 // 1% = 10m, 100% = 1000m
	if m >= 1000 {
		return fmt.Sprintf("%.1f", m/1000)
	}
	return fmt.Sprintf("%.0fm", m)
}

func formatResourceLine(name string, cpuStr string, memMB float64, width int) string {
	memStr := fmt.Sprintf("%6s", formatMem(memMB))
	rightPart := fmt.Sprintf("%7s %s", cpuStr, memStr)

	maxName := width - len(rightPart) - 1
	if maxName < 5 {
		maxName = 5
	}
	displayName := name
	if len(displayName) > maxName {
		displayName = displayName[:maxName-1] + "~"
	}

	padding := width - len(displayName) - len(rightPart)
	if padding < 1 {
		padding = 1
	}

	nameStyled := ServiceNameStyle.Render(displayName)
	rightStyled := PortStyle.Render(rightPart)

	return nameStyled + strings.Repeat(" ", padding) + rightStyled
}

func formatMem(mb float64) string {
	if mb >= 1024 {
		return fmt.Sprintf("%.1fG", mb/1024)
	}
	if mb >= 1 {
		return fmt.Sprintf("%.0fM", mb)
	}
	if mb > 0 {
		return fmt.Sprintf("%.1fM", mb)
	}
	return "0M"
}
