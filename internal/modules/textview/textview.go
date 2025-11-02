package textview

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
)

type TextView struct {
	content string
	title   string
}

func New(title, content string) *TextView {
	return &TextView{
		title:   title,
		content: content,
	}
}

type model struct {
	content string
	title   string
	width   int
	height  int
}

func (m model) Init() tea.Cmd {
	return nil
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		return m, nil

	case tea.KeyMsg:
		switch msg.String() {
		case "q", "esc", "enter", " ":
			return m, tea.Quit
		}
	}
	return m, nil
}

func (m model) View() string {
	// Create a centered box style
	boxStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("62")).
		Padding(1, 2).
		Margin(1).
		Width(m.width - 4).
		Height(m.height - 4)

	titleStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color("62")).
		MarginBottom(1)

	contentStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("252"))

	// Build the view
	title := titleStyle.Render(m.title)
	content := contentStyle.Render(m.content)

	helpText := lipgloss.NewStyle().
		Foreground(lipgloss.Color("240")).
		MarginTop(1).
		Render("Press 'q', 'esc', 'enter', or space to return to menu")

	fullContent := lipgloss.JoinVertical(lipgloss.Left,
		title,
		content,
		"",
		helpText,
	)

	return lipgloss.Place(
		m.width,
		m.height,
		lipgloss.Center,
		lipgloss.Center,
		boxStyle.Render(fullContent),
	)
}

func (tv *TextView) Start(ctx context.Context) error {
	m := model{
		content: tv.content,
		title:   tv.title,
		width:   80,
		height:  24,
	}
	p := tea.NewProgram(m, tea.WithAltScreen())
	_, err := p.Run()
	return err
}
