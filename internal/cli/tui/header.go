package tui

import (
	"fmt"

	"github.com/charmbracelet/lipgloss"
)

// RenderDirectionalHeader renders a header with two colored boxes and info text
// Format: [ leftText ] -> [ rightText ] (infoText)
// leftText: text for the left box (e.g., "R")
// leftColor: background color for the left box (e.g., "21" for blue)
// rightText: text for the right box (e.g., "L")
// rightColor: background color for the right box (e.g., "62" for purple)
// infoText: free-form text to display after the boxes (e.g., "2 active")
func RenderDirectionalHeader(leftText, leftColor, rightText, rightColor, infoText string) string {
	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240"))

	// Left box style
	leftStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color(leftColor)).
		Padding(0, 1)

	// Right box style
	rightStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color(rightColor)).
		Padding(0, 1)

	leftBox := leftStyle.Render(leftText)
	rightBox := rightStyle.Render(rightText)
	info := infoStyle.Render(fmt.Sprintf("(%s)", infoText))

	return fmt.Sprintf("%s -> %s %s", leftBox, rightBox, info)
}

