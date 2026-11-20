//go:build darwin

package app

import (
	"context"
	"os"
	"os/exec"
	"time"

	"golang.org/x/sys/unix"
)

// platformHostNameValue is the machine's name as the person set it in
// System Settings ("ridho's MacBook Pro"). It is read once, at startup,
// so that drawing the chip never waits on scutil (spec 58, window design).
var platformHostNameValue = hostNameFrom(
	scutilName("ComputerName"),
	scutilName("LocalHostName"),
	os.Hostname,
)

// platformHostName is the name cached at startup, where the system gives
// one, and the hostname otherwise.
func platformHostName() string { return platformHostNameValue }

// scutilName is one of the names scutil keeps, read with a timeout so
// that a wedged scutil cannot hold the app.
func scutilName(key string) func() (string, error) {
	return func() (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		out, err := exec.CommandContext(ctx, "/usr/sbin/scutil", "--get", key).Output()
		if err != nil {
			return "", err
		}
		return string(out), nil
	}
}

// platformChip is the machine's processor, shortened, as the host chip
// shows it (spec 58, window design): "Apple M4 Pro" becomes "M4 Pro".
func platformChip() string {
	brand, err := unix.Sysctl("machdep.cpu.brand_string")
	if err != nil {
		return ""
	}
	return shortenChip(brand)
}

// platformMemoryGB is the machine's memory in whole gigabytes, 0 where
// the system does not tell.
func platformMemoryGB() int {
	n, err := unix.SysctlUint64("hw.memsize")
	if err != nil || n == 0 {
		return 0
	}
	return int(n / (1 << 30))
}
