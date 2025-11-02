package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/charmbracelet/bubbles/key"
	"github.com/charmbracelet/bubbles/list"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/lipgloss"
	zone "github.com/lrstanley/bubblezone"
	"github.com/spf13/cobra"

	"ssh-portal/internal/modules"
	"ssh-portal/internal/modules/textview"
)

var docStyle = lipgloss.NewStyle().Margin(1, 2)

type menuItem struct {
	title, desc string
	id          string
}

func (m menuItem) Title() string       { return zone.Mark(m.id, m.title) }
func (m menuItem) Description() string { return m.desc }
func (m menuItem) FilterValue() string { return zone.Mark(m.id, m.title) }

type menuModel struct {
	list list.Model
	keys keyMap

	moduleRegistry map[string]modules.Module
	runningModule  bool
}

type keyMap struct {
	Up    key.Binding
	Down  key.Binding
	Enter key.Binding
	Quit  key.Binding
}

func (k keyMap) ShortHelp() []key.Binding {
	return []key.Binding{k.Up, k.Down, k.Enter, k.Quit}
}
func (k keyMap) FullHelp() [][]key.Binding {
	return [][]key.Binding{{k.Up, k.Down, k.Enter, k.Quit}}
}

func initialModel() menuModel {
	items := []list.Item{
		menuItem{"Generic Option 1", "Execute module 1", "module-1"},
		menuItem{"Generic Option 2", "Execute module 2", "module-2"},
		menuItem{"Generic Option 3", "Execute module 3", "module-3"},
		menuItem{"Generic Option 4", "Execute module 4", "module-4"},
		menuItem{"Exit", "Exit the program", "exit"},
	}
	l := list.New(items, list.NewDefaultDelegate(), 40, 10) // temporary height, will update on WindowSizeMsg
	l.Title = "Module Menu"

	// Register modules here
	registry := make(map[string]modules.Module)
	registry["module-1"] = textview.New("Generic Option 1", "This is the content for Generic Option 1.\n\nYou can put any text here that you want to display in a full-screen Bubble Tea view.\n\nThe view supports multiple lines and will be centered and styled nicely.")
	registry["module-2"] = textview.New("Generic Option 2", "This is Generic Option 2.\n\nEach module can have different content and will display in its own full-screen TUI.\n\nWhen you're done viewing, press 'q', 'esc', 'enter', or space to return to the menu.")
	registry["module-3"] = textview.New("Generic Option 3", "Generic Option 3 content here.\n\nThis demonstrates how modules can run their own TUIs and return control to the menu when finished.")
	registry["module-4"] = textview.New("Generic Option 4", "This is Generic Option 4.\n\nAll modules implement the modules.Module interface with a Start() method that may run a Bubble Tea TUI.")

	return menuModel{
		list: l,
		keys: keyMap{
			Up:    key.NewBinding(key.WithKeys("up", "k"), key.WithHelp("↑/k", "move up")),
			Down:  key.NewBinding(key.WithKeys("down", "j"), key.WithHelp("↓/j", "move down")),
			Enter: key.NewBinding(key.WithKeys("enter", " "), key.WithHelp("enter", "select")),
			Quit:  key.NewBinding(key.WithKeys("q", "ctrl+c"), key.WithHelp("q", "quit")),
		},
		moduleRegistry: registry,
		runningModule:  false,
	}
}

func (m menuModel) Init() tea.Cmd {
	return nil // list.Model does not have Init()
}

// moduleDoneMsg is sent when a module completes execution
type moduleDoneMsg struct{}

func (m menuModel) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	// When a module is running, ignore most messages except module completion
	if m.runningModule {
		switch msg := msg.(type) {
		case moduleDoneMsg:
			m.runningModule = false
			return m, tea.ClearScreen
		case tea.WindowSizeMsg:
			// Still handle window resize even when module is running
			h, v := docStyle.GetFrameSize()
			m.list.SetSize(msg.Width-h, msg.Height-v)
			return m, nil
		}
		return m, nil
	}

	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		h, v := docStyle.GetFrameSize()
		m.list.SetSize(msg.Width-h, msg.Height-v)

	case tea.MouseMsg:
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
				v, _ := listItem.(menuItem)
				idStr := v.id
				if zone.Get(idStr).InBounds(msg) {
					// If already selected, execute; otherwise select it
					if m.list.Index() == i {
						return m.executeCurrentSelection()
					} else {
						m.list.Select(i)
					}
					break
				}
			}
		}

	case tea.KeyMsg:
		switch {
		case key.Matches(msg, m.keys.Quit):
			return m, tea.Quit
		case key.Matches(msg, m.keys.Enter):
			return m.executeCurrentSelection()
		}
	}
	var cmd tea.Cmd
	m.list, cmd = m.list.Update(msg)
	return m, cmd
}

func (m menuModel) View() string {
	if m.runningModule {
		return zone.Scan(docStyle.Render("Running module..."))
	}
	return zone.Scan(docStyle.Render(m.list.View()))
}

// executeModule runs a module and returns a command that signals completion
func executeModule(mod modules.Module) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		err := mod.Start(ctx)
		if err != nil {
			// Log error but still signal completion
			fmt.Fprintf(os.Stderr, "Module error: %v\n", err)
		}
		return moduleDoneMsg{}
	}
}

// execute the currently selected list item; quits if the id is "exit"
func (m menuModel) executeCurrentSelection() (menuModel, tea.Cmd) {
	selected := m.list.SelectedItem()
	if it, ok := selected.(menuItem); ok {
		if it.id == "exit" {
			return m, tea.Quit
		}

		// Look up the module in the registry
		mod, exists := m.moduleRegistry[it.id]
		if !exists {
			// Module not registered - return without error
			// In production, you might want to show an error message
			return m, nil
		}

		// Mark that we're running a module
		m.runningModule = true

		// Execute the module in a goroutine and signal completion
		return m, executeModule(mod)
	}
	return m, nil
}

var testmenuCmd = &cobra.Command{
	Use:   "testmenu",
	Short: "Run the test menu",
	RunE: func(cmd *cobra.Command, args []string) error {
		p := tea.NewProgram(initialModel(), tea.WithAltScreen(), tea.WithMouseCellMotion())
		_, err := p.Run()
		return err
	},
}
