package main

import (
	tea "charm.land/bubbletea/v2"
	"github.com/hexadecimaltime/pingo-tui/app"
)

func HandleKey(m *app.Model, msg tea.KeyMsg) (bool, tea.Cmd) {
	var noCmd tea.Cmd

	if msg.String() == "ctrl+p" {
		// Safely manipulate a component on the current screen
		m.StatusMsg = "Yaegi intercepted your keystroke!"

		return true, app.LoadHistoryCmd(m.Store)
	}

	return false, noCmd
}
