package sender

import (
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
)

var menuDocStyle = lipgloss.NewStyle().Margin(1, 2)

type profileMenuItem struct {
	name        string
	description string
	id          string
}

func (p profileMenuItem) Title() string       { return zone.Mark(p.id, p.name) }
func (p profileMenuItem) Description() string { return p.description }
func (p profileMenuItem) FilterValue() string  { return zone.Mark(p.id, p.name) }

type profileMenuModel struct {
	list      list.Model
	form      *huh.Form
	formCode  string // Store code value for form
	keys      profileMenuKeyMap
	selected  string
	code      string
	quitting  bool
	needsCode bool
	formActive bool
}

type profileMenuKeyMap struct {
	Up    key.Binding
	Down  key.Binding
	Enter key.Binding
	Quit  key.Binding
}

func (k profileMenuKeyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Quit}
}

func newProfileMenuModel(profiles []Profile, needsCode bool) profileMenuModel {
    items := make([]list.Item, 0, len(profiles))
    for _, p := range profiles {
        desc := p.Description
        if desc == "" {
            desc = fmt.Sprintf("Relay: %s", p.Relay)
        }
        items = append(items, profileMenuItem{
            name:        p.Name,
            description: desc,
            id:          fmt.Sprintf("profile-%s", p.Name),
        })
    }

    l := list.New(items, list.NewDefaultDelegate(), 40, 14)
    l.Title = "Select Profile"
    l.SetShowStatusBar(false)
    l.SetFilteringEnabled(false)

    // Build model first so we can bind form input directly to the model field
    pm := profileMenuModel{
        list:       l,
        form:       nil,
        formCode:   "",
        keys: profileMenuKeyMap{
            Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "move up")),
            Down:  key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "move down")),
            Enter: key.NewBinding(key.WithKeys("enter", " "), key.WithHelp("enter", "select/confirm")),
            Quit:  key.NewBinding(key.WithKeys("q", "ctrl+c", "esc"), key.WithHelp("q/esc", "cancel")),
        },
        selected:   "",
        code:       "",
        quitting:   false,
        needsCode:  needsCode,
        formActive: needsCode, // When code is required, start with form focused so the cursor is visible
    }

    if needsCode {
        pm.form = huh.NewForm(
            huh.NewGroup(
                huh.NewInput().
                    Title("Connection Code").
                    Description("Enter the connection code (eg: series-spell-lava-then-038-8307)").
                    Placeholder("series-spell-lava-then-038-8307").
                    Value(&pm.formCode).
                    Validate(func(s string) error {
                        if s == "" {
                            return fmt.Errorf("code is required")
                        }
                        return nil
                    }),
            ),
        ).WithWidth(50)
    }

    return pm
}

func (m profileMenuModel) Init() tea.Cmd {
	if m.form != nil {
		return m.form.Init()
	}
	return nil
}

func (m profileMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := menuDocStyle.GetFrameSize()
		availableWidth := msg.Width - h
		availableHeight := msg.Height - v
		// Allocate space: list gets most, form gets bottom portion if needed
		if m.needsCode && m.form != nil {
			listHeight := availableHeight - 8 // Reserve space for form
			if listHeight < 5 {
				listHeight = 5
			}
			m.list.SetSize(availableWidth, listHeight)
		} else {
			m.list.SetSize(availableWidth, availableHeight)
		}
		return m, nil

	case tea.MouseMsg:
		// Only handle mouse if form is not active
		if m.form == nil || !m.formActive {
			if msg.Button == tea.MouseButtonWheelUp {
				m.list.CursorUp()
				return m, nil
			}
			if msg.Button == tea.MouseButtonWheelDown {
				m.list.CursorDown()
				return m, nil
			}
			if msg.Action == tea.MouseActionRelease && msg.Button == tea.MouseButtonLeft {
				for i, listItem := range m.list.VisibleItems() {
					v, _ := listItem.(profileMenuItem)
					if zone.Get(v.id).InBounds(msg) {
						if m.list.Index() == i {
							m.selected = v.name
							if m.needsCode && m.form != nil {
								// If we need code, don't quit yet, switch to form
								m.formActive = true
								cmds = append(cmds, m.form.Init())
							} else {
								return m, tea.Quit
							}
						} else {
							m.list.Select(i)
						}
						break
					}
				}
			}
		}

    case tea.KeyMsg:
        // Global quit handling regardless of focus
        if key.Matches(msg, m.keys.Quit) || msg.String() == "esc" || msg.String() == "ctrl+c" {
            m.quitting = true
            return m, tea.Quit
        }
		// If form is active, handle form first
		if m.form != nil && m.formActive {
			form, cmd := m.form.Update(msg)
			if f, ok := form.(*huh.Form); ok {
				m.form = f
				cmds = append(cmds, cmd)
			}
			// If form is complete, check if we have profile and code
			if m.form.State == huh.StateCompleted {
				if m.selected != "" {
					// Extract code from form field
					m.code = m.formCode
					return m, tea.Quit
				}
			}
			// Handle tab to switch between list and form
			if msg.String() == "tab" {
				m.formActive = false
				return m, nil
			}
			return m, tea.Batch(cmds...)
		}

		// Handle list navigation
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			return m, tea.Quit
		case key.Matches(msg, m.keys.Enter):
			if m.formActive && m.form != nil {
				// If form is active and Enter is pressed, check if form is complete
				if m.form.State == huh.StateCompleted {
					if m.selected != "" {
						m.code = m.formCode
						return m, tea.Quit
					}
				}
			} else if selected := m.list.SelectedItem(); selected != nil {
				if item, ok := selected.(profileMenuItem); ok {
					m.selected = item.name
					if m.needsCode && m.form != nil {
						// If code is needed, check if form is already filled
						if m.form.State == huh.StateCompleted && m.formCode != "" {
							m.code = m.formCode
							return m, tea.Quit
						}
						// Otherwise switch to form for code input
						m.formActive = true
						cmds = append(cmds, m.form.Init())
					} else {
						return m, tea.Quit
					}
				}
			}
		case msg.String() == "tab" && m.form != nil:
			// Switch between list and form
			m.formActive = !m.formActive
			if m.formActive {
				cmds = append(cmds, m.form.Init())
			}
		}
	}

	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	return m, tea.Batch(cmds...)
}

func (m profileMenuModel) View() string {
	if m.quitting && m.selected == "" {
		return menuDocStyle.Render("\n\nCancelled.\n")
	}

	// Get terminal width from list (it should have been set via WindowSizeMsg)
	width := m.list.Width()
	if width == 0 {
		width = 80 // fallback
	}

	// Base border style for focused view - colored border
	focusBorderStyle := lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(lipgloss.Color("201")).
		Padding(0, 1).
		Width(width)

	// Unfocused border style - invisible border (same size, uses HiddenBorder)
	unfocusBorderStyle := lipgloss.NewStyle().
		Border(lipgloss.HiddenBorder()).
		Padding(0, 1).
		Width(width)

	var views []string

	// Render list with border
	listView := m.list.View()
	if m.form != nil && m.formActive {
		// List is not focused
		views = append(views, unfocusBorderStyle.Render(listView))
	} else {
		// List is focused
		views = append(views, focusBorderStyle.Render(listView))
	}

	// Always show form below if code is needed
	if m.form != nil {
		formView := m.form.View()
		if m.formActive {
			// Form is focused
			views = append(views, focusBorderStyle.Render(formView))
		} else {
			// Form is not focused
			views = append(views, unfocusBorderStyle.Render(formView))
		}
		// Help hint
		hint := lipgloss.NewStyle().Faint(true).Render("(tab to switch focus between list and code form)")
		views = append(views, "", hint)
	}

	return zone.Scan(menuDocStyle.Render(lipgloss.JoinVertical(lipgloss.Left, views...)))
}

// SelectProfileResult contains both the selected profile and code
type SelectProfileResult struct {
	Profile string
	Code    string
}

// SelectProfile shows a menu to select a profile from the given profiles
// If needsCode is true, also shows a form to enter the connection code
// Returns the selected profile name and code, or empty strings if cancelled/no profiles
func SelectProfile(profiles []Profile, needsCode bool) (*SelectProfileResult, error) {
	if len(profiles) == 0 {
		return nil, fmt.Errorf("no profiles available")
	}

	zone.NewGlobal()
	p := tea.NewProgram(newProfileMenuModel(profiles, needsCode), tea.WithAltScreen(), tea.WithMouseCellMotion())
	finalModel, err := p.Run()
	if err != nil {
		return nil, err
	}

	if m, ok := finalModel.(profileMenuModel); ok {
		if m.quitting && (m.selected == "" || (needsCode && m.code == "")) {
			fmt.Fprintf(os.Stderr, "Profile selection cancelled\n")
			os.Exit(1)
		}
		return &SelectProfileResult{
			Profile: m.selected,
			Code:    m.code,
		}, nil
	}

	return nil, fmt.Errorf("unexpected error in profile selection")
}

