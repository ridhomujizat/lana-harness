package main

import (
	"os"
	"strconv"
	"strings"
)

// processZombie reports whether pid has exited but its parent has not reaped
// it yet. A zombie still answers kill(pid, 0).
func processZombie(pid int) bool {
	data, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/stat")
	if err != nil {
		return false
	}
	// The state follows the command name, which is in parentheses and may
	// contain spaces.
	text := string(data)
	end := strings.LastIndexByte(text, ')')
	if end < 0 || end+2 >= len(text) {
		return false
	}
	return text[end+2] == 'Z' || text[end+2] == 'X'
}
