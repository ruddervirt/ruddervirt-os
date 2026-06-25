package main

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/bubbles/textinput"
	tea "github.com/charmbracelet/bubbletea"
)

type screen int

const (
	screenMenu screen = iota
	screenResult
	screenNetwork
)

type model struct {
	input         string
	result        string
	current       screen
	networkInputs [2]textinput.Model
	networkFocus  int
	resultSource  string
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
	ifaceInput := textinput.New()
	ifaceInput.Placeholder = "e.g. eno1"
	ifaceInput.Focus()

	ipInput := textinput.New()
	ipInput.Placeholder = "e.g. 192.168.10.2"

	return model{
		current:       screenMenu,
		networkInputs: [2]textinput.Model{ifaceInput, ipInput},
		networkFocus:  0,
	}
}

func (m model) Init() tea.Cmd {
	return textinput.Blink
}

func resolveInput(input string) (string, bool) {
	input = strings.ToLower(strings.TrimSpace(input))
	if label, ok := menuOptions[input]; ok {
		return label, true
	}
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
			m.resultSource = ""
			m.networkFocus = 0
			m.networkInputs[0].SetValue("")
			m.networkInputs[1].SetValue("")
			m.networkInputs[0].Focus()
			m.networkInputs[1].Blur()
			return m, nil

		case tea.KeyTab:
			if m.current == screenNetwork {
				m.networkFocus = (m.networkFocus + 1) % 2
				if m.networkFocus == 0 {
					m.networkInputs[0].Focus()
					m.networkInputs[1].Blur()
				} else {
					m.networkInputs[1].Focus()
					m.networkInputs[0].Blur()
				}
				return m, nil
			}

		case tea.KeyEnter:
			if m.current == screenMenu {
				if label, ok := resolveInput(m.input); ok {
					if label == "logout" {
						return m, tea.Quit
					}
					if label == "network" {
						m.current = screenNetwork
						m.input = ""
						return m, nil
					}
					m.result = label
					m.current = screenResult
				}
				m.input = ""
			} else if m.current == screenNetwork {
				iface := m.networkInputs[0].Value()
				ip := m.networkInputs[1].Value()
				if iface == "" || ip == "" {
					return m, nil
				}

				m.result = fmt.Sprintf("network: interface=%s ip=%s", iface, ip)
				m.resultSource = "network"
				m.current = screenResult
			}
			return m, nil

		case tea.KeyBackspace:
			if m.current == screenMenu && len(m.input) > 0 {
				m.input = m.input[:len(m.input)-1]
				return m, nil
			}
		}
	}

	if m.current == screenNetwork {
		var cmd tea.Cmd
		m.networkInputs[m.networkFocus], cmd = m.networkInputs[m.networkFocus].Update(msg)
		return m, cmd
	}

	if m.current == screenMenu {
		if keyMsg, ok := msg.(tea.KeyMsg); ok {
			if keyMsg.Type == tea.KeyRunes {
				m.input += keyMsg.String()
			}
		}
	}

	return m, nil
}

func (m model) View() string {
	switch m.current {
	case screenNetwork:
		s := "\nNetwork Configuration\n\n"
		s += fmt.Sprintf("  Interface name: %s\n", m.networkInputs[0].View())
		s += fmt.Sprintf("  IP address:     %s\n", m.networkInputs[1].View())
		s += "\nTab to switch fields, Enter to confirm, Esc to go back.\n"
		return s

	case screenResult:
		return fmt.Sprintf("\n%s\n\nPress Esc to go back, Ctrl+C to quit.\n", m.result)

	default:
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
}

func main() {
	p := tea.NewProgram(initialModel())
	if _, err := p.Run(); err != nil {
		fmt.Printf("Error: %v\n", err)
	}
}
