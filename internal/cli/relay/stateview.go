package relay

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"ssh-portal/internal/cli/tui"
)

// RenderStateView renders the invites and splices in a two-column layout
func RenderStateView(width int) string {
	// Header with software name and colored bar
	header := tui.RenderTitleBar("Relay", width)

	invites := GetOutstandingInvites()
	splices := GetActiveSplices()

	// Format invites column
	invitesCol := formatInvites(invites, width/2-2)

	// Format splices column
	splicesCol := formatSplices(splices, width/2-2)

	// Join columns side by side
	columns := lipgloss.JoinHorizontal(lipgloss.Top, invitesCol, splicesCol)

	// Wrap to fit viewport width
	wrapped := lipgloss.NewStyle().
		Width(width).
		Render(columns)

	// Join header and content
	return lipgloss.JoinVertical(lipgloss.Left, header, wrapped)
}

func formatInvites(invites []*Invite, width int) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	containerStyle := lipgloss.NewStyle().
		Padding(1, 2).
		Width(width)

	title := titleStyle.Render("Outstanding Invites")

	if len(invites) == 0 {
		content := "No outstanding invites"
		return containerStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, content))
	}

	// Table header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("240")).
		Padding(0, 1)

	header := headerStyle.Render("Code          RID           Expires")
	divider := strings.Repeat("─", width-6)

	var rows []string
	rows = append(rows, title)
	rows = append(rows, header)
	rows = append(rows, divider)

	rowStyle := lipgloss.NewStyle().Padding(0, 1)
	for _, inv := range invites {
		expiresIn := time.Until(inv.ExpiresAt)
		expiresStr := expiresIn.Round(time.Second).String()
		if len(expiresStr) > 12 {
			expiresStr = expiresStr[:12]
		}

		// Truncate if too long
		code := inv.Code
		if len(code) > 12 {
			code = code[:12]
		}
		rid := inv.RID
		if len(rid) > 12 {
			rid = rid[:12]
		}

		row := fmt.Sprintf("%-12s %-12s %-12s", code, rid, expiresStr)
		rows = append(rows, rowStyle.Render(row))
	}

	content := strings.Join(rows, "\n")
	return containerStyle.Render(content)
}

func formatSplices(splices []*Splice, width int) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	containerStyle := lipgloss.NewStyle().
		Padding(1, 2).
		Width(width)

	title := titleStyle.Render("Active Splices")

	if len(splices) == 0 {
		content := "No active splices"
		return containerStyle.Render(lipgloss.JoinVertical(lipgloss.Left, title, content))
	}

	// Table header
	headerStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("240")).
		Padding(0, 1)

	header := headerStyle.Render("Code          Up       Down     Sender")
	divider := strings.Repeat("─", width-6)

	var rows []string
	rows = append(rows, title)
	rows = append(rows, header)
	rows = append(rows, divider)

	rowStyle := lipgloss.NewStyle().Padding(0, 1)
	for _, s := range splices {
		bytesUpStr := formatBytes(s.BytesUp)
		bytesDownStr := formatBytes(s.BytesDown)

		// Truncate if too long
		code := s.Code
		if len(code) > 12 {
			code = code[:12]
		}
		up := bytesUpStr
		if len(up) > 8 {
			up = up[:8]
		}
		down := bytesDownStr
		if len(down) > 8 {
			down = down[:8]
		}
		sender := s.SenderAddr
		// Truncate sender address if too long
		if len(sender) > 15 {
			sender = sender[:15]
		}

		row := fmt.Sprintf("%-12s %-8s %-8s %-15s", code, up, down, sender)
		rows = append(rows, rowStyle.Render(row))
	}

	content := strings.Join(rows, "\n")
	return containerStyle.Render(content)
}

func formatBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%d B", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(b)/float64(div), "KMGTPE"[exp])
}
