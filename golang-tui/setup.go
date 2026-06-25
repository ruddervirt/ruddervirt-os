package main

import (
	"fmt"
	"strings" // <-- CHANGED

	tea "github.com/charmbracelet/bubbletea"
)

type screen int

const (
	screenMenu screen = iota
	screenResult
)

type model struct {
	input   string
	result  string
	current screen
}

var menuOptions = map[string]string{
	"1": "install",
	"2": "update",
	"3": "network",
	"4": "purge",
	"5": "shell",
	"6": "logout",
}

func initialModel() model {
	return model{current: screenMenu}
}

func (m model) Init() tea.Cmd {
	return nil
}

func resolveInput(input string) (string, bool) {
	input = strings.ToLower(strings.TrimSpace(input))
	// check number keys first
	if label, ok := menuOptions[input]; ok {
		return label, true
	}
	// check if input matches any value directly
	for _, label := range menuOptions {
		if input == label {
			return label, true
		}
	}
	return "", false
}

func (m model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {

		case tea.KeyCtrlC:
			return m, tea.Quit

		case tea.KeyEsc:
			m.current = screenMenu
			m.input = ""
			m.result = ""
			return m, nil

		case tea.KeyEnter:
			if m.current == screenMenu {
				if label, ok := resolveInput(m.input); ok { // <-- CHANGED
					if label == "logout" {
						return m, tea.Quit
					}
					m.result = label
					m.current = screenResult
				}
				m.input = ""
			}
			return m, nil

		case tea.KeyBackspace:
			if len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
			}
			return m, nil

		default:
			if m.current == screenMenu {
				m.input += msg.String()
			}
			return m, nil
		}
	}

	return m, nil
}

func (m model) View() string {
	if m.current == screenResult {
		return fmt.Sprintf("\n%s\n\nPress esc to go back, ctrl+c to quit.\n", m.result)
	}

	s := "\nRudderVirt Setup\n\n"
	s += "  1. install\n"
	s += "  2. update\n"
	s += "  3. network\n"
	s += "  4. purge\n"
	s += "  5. shell\n"
	s += "  6. logout\n"
	s += fmt.Sprintf("\n> %s_\n\n", m.input)
	s += "Press ctrl+c to quit.\n"
	return s
}

func main() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}
