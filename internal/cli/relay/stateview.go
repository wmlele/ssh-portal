package relay

import (
	"fmt"
	"sort"
	"time"

	"github.com/charmbracelet/bubbles/table"
	"github.com/charmbracelet/lipgloss"
)

// NewInvitesTable creates and returns a table.Model configured for invites
func NewInvitesTable(width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4
	// Four columns: Code, RID, Receiver Addr, Expires
	colWidth := availableWidth / 4

	columns := []table.Column{
		{Title: "Code", Width: colWidth},
		{Title: "RID", Width: colWidth},
		{Title: "Receiver Addr", Width: colWidth},
		{Title: "Expires", Width: colWidth},
	}

	rows := []table.Row{}
	invites := GetOutstandingInvites()
	// Sort invites by expiration (earliest first)
	sort.Slice(invites, func(i, j int) bool {
		return invites[i].ExpiresAt.Before(invites[j].ExpiresAt)
	})
	for _, inv := range invites {
		expiresIn := time.Until(inv.ExpiresAt)
		expiresStr := expiresIn.Round(time.Second).String()
		if len(expiresStr) > 12 {
			expiresStr = expiresStr[:12]
		}

		code := inv.Code
		if len(code) > colWidth {
			code = code[:colWidth]
		}
		rid := inv.RID
		if len(rid) > colWidth {
			rid = rid[:colWidth]
		}

		// Get receiver address if receiver has connected
		receiverAddr := "-"
		if inv.ReceiverConn != nil {
			receiverAddr = inv.ReceiverConn.RemoteAddr().String()
			if len(receiverAddr) > colWidth {
				receiverAddr = receiverAddr[:colWidth]
			}
		}

		rows = append(rows, table.Row{code, rid, receiverAddr, expiresStr})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
		table.WithWidth(width),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true).
		Foreground(lipgloss.Color("62"))
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("62")).
		Bold(false)
	t.SetStyles(s)

	return t
}

// UpdateInvitesTable updates the table with current invites data
func UpdateInvitesTable(t table.Model, width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4
	colWidth := availableWidth / 4

	columns := []table.Column{
		{Title: "Code", Width: colWidth},
		{Title: "RID", Width: colWidth},
		{Title: "Receiver Addr", Width: colWidth},
		{Title: "Expires", Width: colWidth},
	}

	rows := []table.Row{}
	invites := GetOutstandingInvites()
	// Sort invites by expiration (earliest first)
	sort.Slice(invites, func(i, j int) bool {
		return invites[i].ExpiresAt.Before(invites[j].ExpiresAt)
	})
	for _, inv := range invites {
		expiresIn := time.Until(inv.ExpiresAt)
		expiresStr := expiresIn.Round(time.Second).String()
		if len(expiresStr) > 12 {
			expiresStr = expiresStr[:12]
		}

		code := inv.Code
		if len(code) > colWidth {
			code = code[:colWidth]
		}
		rid := inv.RID
		if len(rid) > colWidth {
			rid = rid[:colWidth]
		}

		// Get receiver address if receiver has connected
		receiverAddr := "-"
		if inv.ReceiverConn != nil {
			receiverAddr = inv.ReceiverConn.RemoteAddr().String()
			if len(receiverAddr) > colWidth {
				receiverAddr = receiverAddr[:colWidth]
			}
		}

		rows = append(rows, table.Row{code, rid, receiverAddr, expiresStr})
	}

	t.SetColumns(columns)
	t.SetRows(rows)
	t.SetWidth(width)
	t.SetHeight(height)

	return t
}

// NewSplicesTable creates and returns a table.Model configured for splices
func NewSplicesTable(width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4
	// Five columns: Code, Up, Down, Sender Addr, Receiver Addr
	colWidth := availableWidth / 5

	columns := []table.Column{
		{Title: "Code", Width: colWidth},
		{Title: "Up", Width: colWidth},
		{Title: "Down", Width: colWidth},
		{Title: "Sender Addr", Width: colWidth},
		{Title: "Receiver Addr", Width: colWidth},
	}

	rows := []table.Row{}
	splices := GetActiveSplices()
	// Sort splices by code (alphabetically)
	sort.Slice(splices, func(i, j int) bool {
		return splices[i].Code < splices[j].Code
	})
	for _, s := range splices {
		bytesUpStr := formatBytes(s.BytesUp)
		bytesDownStr := formatBytes(s.BytesDown)

		code := s.Code
		if len(code) > colWidth {
			code = code[:colWidth]
		}
		up := bytesUpStr
		if len(up) > colWidth {
			up = up[:colWidth]
		}
		down := bytesDownStr
		if len(down) > colWidth {
			down = down[:colWidth]
		}
		senderAddr := s.SenderAddr
		if len(senderAddr) > colWidth {
			senderAddr = senderAddr[:colWidth]
		}
		receiverAddr := s.ReceiverAddr
		if len(receiverAddr) > colWidth {
			receiverAddr = receiverAddr[:colWidth]
		}

		rows = append(rows, table.Row{code, up, down, senderAddr, receiverAddr})
	}

	t := table.New(
		table.WithColumns(columns),
		table.WithRows(rows),
		table.WithFocused(true),
		table.WithHeight(height),
		table.WithWidth(width),
	)

	s := table.DefaultStyles()
	s.Header = s.Header.
		BorderStyle(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("240")).
		BorderBottom(true).
		Bold(true).
		Foreground(lipgloss.Color("62"))
	s.Selected = s.Selected.
		Foreground(lipgloss.Color("230")).
		Background(lipgloss.Color("62")).
		Bold(false)
	t.SetStyles(s)

	return t
}

// UpdateSplicesTable updates the table with current splices data
func UpdateSplicesTable(t table.Model, width, height int) table.Model {
	if width < 20 {
		width = 20
	}
	if height < 3 {
		height = 3
	}
	availableWidth := width - 4
	colWidth := availableWidth / 5

	columns := []table.Column{
		{Title: "Code", Width: colWidth},
		{Title: "Up", Width: colWidth},
		{Title: "Down", Width: colWidth},
		{Title: "Sender Addr", Width: colWidth},
		{Title: "Receiver Addr", Width: colWidth},
	}

	rows := []table.Row{}
	splices := GetActiveSplices()
	// Sort splices by code (alphabetically)
	sort.Slice(splices, func(i, j int) bool {
		return splices[i].Code < splices[j].Code
	})
	for _, s := range splices {
		bytesUpStr := formatBytes(s.BytesUp)
		bytesDownStr := formatBytes(s.BytesDown)

		code := s.Code
		if len(code) > colWidth {
			code = code[:colWidth]
		}
		up := bytesUpStr
		if len(up) > colWidth {
			up = up[:colWidth]
		}
		down := bytesDownStr
		if len(down) > colWidth {
			down = down[:colWidth]
		}
		senderAddr := s.SenderAddr
		if len(senderAddr) > colWidth {
			senderAddr = senderAddr[:colWidth]
		}
		receiverAddr := s.ReceiverAddr
		if len(receiverAddr) > colWidth {
			receiverAddr = receiverAddr[:colWidth]
		}

		rows = append(rows, table.Row{code, up, down, senderAddr, receiverAddr})
	}

	t.SetColumns(columns)
	t.SetRows(rows)
	t.SetWidth(width)
	t.SetHeight(height)

	return t
}

// RenderLeftPaneContent renders the invites table with header
func RenderLeftPaneContent(width int, invitesTable table.Model) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		MarginBottom(1)

	title := titleStyle.Render("Outstanding Invites")

	invites := GetOutstandingInvites()
	info := infoStyle.Render(fmt.Sprintf("Active: %d", len(invites)))

	tableView := invitesTable.View()
	if tableView == "" {
		tableView = "  No outstanding invites"
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		info,
		tableView,
	)

	return content
}

// RenderRightPaneContent renders the splices table with header
func RenderRightPaneContent(width int, splicesTable table.Model) string {
	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		MarginBottom(1)

	title := titleStyle.Render("Active Splices")

	splices := GetActiveSplices()
	info := infoStyle.Render(fmt.Sprintf("Active: %d", len(splices)))

	tableView := splicesTable.View()
	if tableView == "" {
		tableView = "  No active splices"
	}

	content := lipgloss.JoinVertical(
		lipgloss.Left,
		title,
		info,
		tableView,
	)

	return content
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
