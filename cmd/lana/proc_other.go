//go:build !darwin && !linux

package main

// processZombie cannot tell a zombie on this platform; kill(pid, 0) decides.
func processZombie(int) bool { return false }
