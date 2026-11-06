package main

// TeamNames returns the registered teams for the Telegram /agent command.
func (b *daemonBackend) TeamNames() []string { return b.rt.TeamNames() }
