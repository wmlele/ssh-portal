package sender

import (
	"fmt"
	"io"
	"os"
	"regexp"

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

func (p profileMenuItem) Title() string       { return p.name }
func (p profileMenuItem) Description() string { return p.description }
func (p profileMenuItem) FilterValue() string { return p.name + " " + p.description }

type codeFormData struct {
	Code string
}

type profileMenuModel struct {
	list         list.Model
	del          *markedDelegate
	form         *huh.Form
	codeFormData codeFormData // Store code value for form
	keys         profileMenuKeyMap
	selected     string
	code         string
	quitting     bool
	needsCode    bool
	formActive   bool
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

// markedDelegate renders like the default delegate, but:
// - keeps default selected/normal coloring (cursor-based)
// - shows a "• " marker when the row's ID == selectedID (model-chosen)
type markedDelegate struct {
	styles          list.DefaultItemStyles
	showDescription bool
	selectedID      string
}

func newMarkedDelegate(showDescription bool) *markedDelegate {
	d := list.NewDefaultDelegate()
	return &markedDelegate{
		styles:          d.Styles,
		showDescription: showDescription,
	}
}

func (d *markedDelegate) SetSelectedID(id string) { d.selectedID = id }

func (d *markedDelegate) Height() int {
	if d.showDescription {
		return 2
	}
	return 1
}

// IMPORTANT: restore the space between items like the default delegate
func (d *markedDelegate) Spacing() int { return 1 }

func (d *markedDelegate) Update(msg tea.Msg, m *list.Model) tea.Cmd { return nil }

func (d *markedDelegate) Render(w io.Writer, m list.Model, index int, listItem list.Item) {
	it, ok := listItem.(profileMenuItem)
	if !ok {
		return
	}

	// cursor-driven styling stays (color, etc.)
	selectedCursor := index == m.Index()
	titleStyle := d.styles.NormalTitle
	descStyle := d.styles.NormalDesc
	if selectedCursor {
		titleStyle = d.styles.SelectedTitle
		descStyle = d.styles.SelectedDesc
	}

	// dot is driven by your model's chosen item, not the cursor
	mark := ""
	if it.id == d.selectedID {
		mark = lipgloss.NewStyle().Foreground(lipgloss.Color("201")).Render("• ")
	}

	// Create a style that only applies the color from titleStyle
	titleColorStyle := lipgloss.NewStyle().Foreground(titleStyle.GetForeground())
	title := titleStyle.Render(mark + titleColorStyle.Render(it.Title()))

	var row string
	if d.showDescription && it.Description() != "" {
		desc := descStyle.Render(it.Description())
		row = lipgloss.JoinVertical(lipgloss.Left, title, desc)
	} else {
		row = title
	}

	io.WriteString(w, zone.Mark(it.id, row))
}

func newProfileMenuModel(profiles []Profile, needsCode bool) *profileMenuModel {
	items := make([]list.Item, 0, len(profiles)+1)

	// Add "none" option first
	items = append(items, profileMenuItem{
		name:        "none",
		description: "Use top-level configuration only (no profile)",
		id:          "profile-none",
	})

	// Add all profiles
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

	//l := list.New(items, list.NewDefaultDelegate(), 40, 14)
	del := newMarkedDelegate(true) // true = show description (same as default)
	l := list.New(items, del, 40, 14)

	l.Title = "Select Profile"
	l.SetShowStatusBar(false)
	l.SetFilteringEnabled(true)

	// Build model first so we can bind form input directly to the model field
	pm := &profileMenuModel{
		list:         l,
		del:          del,
		form:         nil,
		codeFormData: codeFormData{Code: ""},
		keys: profileMenuKeyMap{
			Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "move up")),
			Down:  key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "move down")),
			Enter: key.NewBinding(key.WithKeys("enter", " "), key.WithHelp("enter", "select/confirm")),
			Quit:  key.NewBinding(key.WithKeys("q", "ctrl+c", "esc"), key.WithHelp("q/esc", "cancel")),
		},
		selected:   "none",
		code:       "",
		quitting:   false,
		needsCode:  needsCode,
		formActive: false, // Start with list focused, user can tab to form
	}

	// Set "none" as the default selected item
	del.SetSelectedID("profile-none")

	if needsCode {
		pm.form = huh.NewForm(
			huh.NewGroup(
				huh.NewInput().
					Title("Connection Code").
					Description("Enter the connection code").
					Placeholder("series-spell-lava-then-038-8307").
					Value(&pm.codeFormData.Code).
					Validate(func(s string) error {
						if s == "" {
							return fmt.Errorf("code is required")
						}
						// Validate format: word-word-word-word-123-4567
						pattern := `^[a-zA-Z0-9]+-[a-zA-Z0-9]+-[a-zA-Z0-9]+-[a-zA-Z0-9]+-\d{3}-\d{4}$`
						matched, err := regexp.MatchString(pattern, s)
						if err != nil {
							return fmt.Errorf("invalid code format: %v", err)
						}
						if !matched {
							return fmt.Errorf("code must be in format: word-word-word-word-123-4567")
						}
						return nil
					}),
			),
		).WithWidth(80)
	}

	return pm
}

func (m *profileMenuModel) Init() tea.Cmd {
	if m.form != nil {
		return m.form.Init()
	}
	return nil
}

func (m *profileMenuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	// 1) Global / sizing first
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := menuDocStyle.GetFrameSize()
		availableWidth := msg.Width - h
		availableHeight := msg.Height - v
		// Allocate space: list gets most, form gets bottom portion if needed
		// Reduce by 2 lines to ensure border is visible and account for spacing
		if m.needsCode && m.form != nil {
			listHeight := availableHeight - 10 // Reserve space for form + 2 lines for borders/spacing
			if listHeight < 5 {
				listHeight = 5
			}
			m.list.SetSize(availableWidth, listHeight)
			// Update form width to match available width (accounting for border padding)
			// Border has 1 char padding on each side, so subtract 2
			formWidth := availableWidth - 2
			if formWidth < 20 {
				formWidth = 20
			}
			m.form = m.form.WithWidth(formWidth)
		} else {
			// Reduce by 2 lines even when no form to ensure border visibility
			listHeight := availableHeight - 2
			if listHeight < 5 {
				listHeight = 5
			}
			m.list.SetSize(availableWidth, listHeight)
		}
		return m, nil

	case tea.KeyMsg:
		// Global quit handling regardless of focus
		if key.Matches(msg, m.keys.Quit) || msg.String() == "esc" || msg.String() == "ctrl+c" {
			m.quitting = true
			return m, tea.Quit
		}

		// Toggle focus with tab, regardless of who’s focused
		if msg.String() == "tab" && m.form != nil {
			m.formActive = !m.formActive
			if m.formActive {
				return m, m.form.Init()
			}
			return m, nil
		}
	}

	// 2) If the form has focus, forward *every* msg to it (keys, mouse, internal)
	if m.form != nil && m.formActive {
		form, cmd := m.form.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.form = f
		}
		if cmd != nil {
			cmds = append(cmds, cmd)
		}

		// Check completion after the form processes the msg
		if m.form.State == huh.StateCompleted {
			m.code = m.codeFormData.Code
			return m, tea.Quit
		}
		return m, tea.Batch(cmds...)
	}

	// 3) Otherwise, list has focus → do list mouse/keys
	switch msg := msg.(type) {
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
							m.del.SetSelectedID(v.id)
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

		// If list is filtering and list has focus, let list consume keys
		if m.list.FilterState() == list.Filtering && !m.formActive {
			var cmd tea.Cmd
			m.list, cmd = m.list.Update(msg)
			if cmd != nil {
				cmds = append(cmds, cmd)
			}
			return m, tea.Batch(cmds...)
		}

		// // If form is active, handle form first
		// if m.form != nil && m.formActive {

		// 	form, cmd := m.form.Update(msg)
		// 	if f, ok := form.(*huh.Form); ok {
		// 		m.form = f

		// 	}
		// 	if cmd != nil {
		// 		cmds = append(cmds, cmd)
		// 	}

		// 	// After form update, update code value from form
		// 	if m.form.State == huh.StateCompleted {
		// 		m.code = m.codeFormData.Code
		// 	}

		// 	fmt.Println("Form updated, form state:", m.form.State, "code:", m.codeFormData.Code)
		// 	// If form is complete and we have both profile and code, proceed
		// 	if m.form.State == huh.StateCompleted && m.codeFormData.Code != "" {
		// 		m.code = m.codeFormData.Code
		// 		return m, tea.Quit
		// 	}

		// 	return m, tea.Batch(cmds...)
		// }

		// Handle list navigation
		switch {
		case key.Matches(msg, m.keys.Quit):
			m.quitting = true
			return m, tea.Quit
		case key.Matches(msg, m.keys.Enter):
			// Enter handling for list (when form is not active)
			if !m.formActive {
				if selected := m.list.SelectedItem(); selected != nil {
					if item, ok := selected.(profileMenuItem); ok {
						m.selected = item.name
						m.del.SetSelectedID(item.id)
						if m.needsCode && m.form != nil {
							// If code is needed, check if form is already filled
							if m.form.State == huh.StateCompleted && m.codeFormData.Code != "" {
								m.code = m.codeFormData.Code
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

func (m *profileMenuModel) View() string {
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

	if m, ok := finalModel.(*profileMenuModel); ok {
		if m.quitting && (m.selected == "" && m.code == "") {
			fmt.Fprintf(os.Stderr, "Profile selection cancelled\n")
			os.Exit(1)
		}
		// If "none" was selected, set profile to empty string
		profile := m.selected
		if profile == "none" {
			profile = ""
		}
		// If code is required but not provided, that's an error
		if needsCode && m.code == "" {
			fmt.Fprintf(os.Stderr, "Code is required\n")
			os.Exit(1)
		}
		return &SelectProfileResult{
			Profile: profile,
			Code:    m.code,
		}, nil
	}

	return nil, fmt.Errorf("unexpected error in profile selection")
}
