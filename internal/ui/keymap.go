package ui

import "charm.land/bubbles/v2/key"

type KeyMap struct {
	Up         key.Binding
	Down       key.Binding
	PageUp     key.Binding
	PageDown   key.Binding
	Home       key.Binding
	End        key.Binding
	Toggle     key.Binding
	Tab        key.Binding
	StartGroup key.Binding
	StopGroup  key.Binding
	StartAll   key.Binding
	StopAll    key.Binding
	Build      key.Binding
	StopDocker key.Binding
	Rebuild    key.Binding
	Follow     key.Binding
	OpenPager  key.Binding
	Help       key.Binding
	Detach     key.Binding
	Quit       key.Binding
}

func DefaultKeyMap() KeyMap {
	return KeyMap{
		Up: key.NewBinding(
			key.WithKeys("up", "k"),
			key.WithHelp("↑/k", "up"),
		),
		Down: key.NewBinding(
			key.WithKeys("down", "j"),
			key.WithHelp("↓/j", "down"),
		),
		PageUp: key.NewBinding(
			key.WithKeys("pgup"),
			key.WithHelp("PgUp", "page up"),
		),
		PageDown: key.NewBinding(
			key.WithKeys("pgdown"),
			key.WithHelp("PgDn", "page down"),
		),
		Home: key.NewBinding(
			key.WithKeys("home"),
			key.WithHelp("Home", "scroll to top"),
		),
		End: key.NewBinding(
			key.WithKeys("end"),
			key.WithHelp("End", "scroll to bottom"),
		),
		Toggle: key.NewBinding(
			key.WithKeys("enter", "space"),
			key.WithHelp("⏎/space", "start/stop"),
		),
		Tab: key.NewBinding(
			key.WithKeys("tab"),
			key.WithHelp("tab", "switch panel"),
		),
		StartGroup: key.NewBinding(
			key.WithKeys("g"),
			key.WithHelp("g", "start group"),
		),
		StopGroup: key.NewBinding(
			key.WithKeys("G"),
			key.WithHelp("G", "stop group"),
		),
		StartAll: key.NewBinding(
			key.WithKeys("a"),
			key.WithHelp("a", "start all"),
		),
		StopAll: key.NewBinding(
			key.WithKeys("A"),
			key.WithHelp("A", "stop non-docker"),
		),
		Build: key.NewBinding(
			key.WithKeys("b"),
			key.WithHelp("b", "build"),
		),
		StopDocker: key.NewBinding(
			key.WithKeys("D"),
			key.WithHelp("D", "stop docker"),
		),
		Rebuild: key.NewBinding(
			key.WithKeys("r"),
			key.WithHelp("r", "rebuild service"),
		),
		Follow: key.NewBinding(
			key.WithKeys("f"),
			key.WithHelp("f", "follow logs"),
		),
		OpenPager: key.NewBinding(
			key.WithKeys("o"),
			key.WithHelp("o", "open logs in pager"),
		),
		Help: key.NewBinding(
			key.WithKeys("?"),
			key.WithHelp("?", "help"),
		),
		Detach: key.NewBinding(
			key.WithKeys("q"),
			key.WithHelp("q", "detach"),
		),
		Quit: key.NewBinding(
			key.WithKeys("Q", "ctrl+c"),
			key.WithHelp("Q", "quit (stop all)"),
		),
	}
}
