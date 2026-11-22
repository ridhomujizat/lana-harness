package figcraft

import (
	"context"
	"errors"
	"net"
	"os/exec"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"
)

// discoverTimeout is how long the port auto-detection may take (REQ-FV-010).
const discoverTimeout = 3 * time.Second

// defaultDiscoverPorts finds the FigCraft apps' control ports (REQ-FV-010). On
// macOS it asks lsof for every TCP port a process named FigCraft listens on and
// keeps the loopback ones; elsewhere there is no auto-detection.
func defaultDiscoverPorts(ctx context.Context) ([]int, error) {
	if runtime.GOOS != "darwin" {
		return nil, nil
	}
	runCtx, cancel := context.WithTimeout(ctx, discoverTimeout)
	defer cancel()

	cmd := exec.CommandContext(runCtx, "lsof", "-nP", "-a", "-iTCP", "-sTCP:LISTEN", "-c", "FigCraft", "-Fn")
	out, err := cmd.Output()
	if err != nil {
		// lsof exits 1 when nothing matches, which is not a failure.
		var exit *exec.ExitError
		if errors.As(err, &exit) && exit.ExitCode() == 1 {
			return parseLsofPorts(string(out)), nil
		}
		if errors.Is(err, exec.ErrNotFound) {
			return nil, nil
		}
		return nil, err
	}
	return parseLsofPorts(string(out)), nil
}

// parseLsofPorts reads the "n" (name) lines of lsof -Fn output and returns the
// ports bound to 127.0.0.1, localhost, ::1 or *, sorted and deduplicated
// (REQ-FV-010). A name bound to another address is ignored.
func parseLsofPorts(out string) []int {
	seen := make(map[int]struct{})
	for _, line := range strings.Split(out, "\n") {
		line = strings.TrimSpace(line)
		if len(line) < 2 || line[0] != 'n' {
			continue
		}
		host, portStr, err := net.SplitHostPort(line[1:])
		if err != nil {
			continue
		}
		switch host {
		case "127.0.0.1", "localhost", "::1", "*":
		default:
			continue
		}
		port, err := strconv.Atoi(portStr)
		if err != nil || port < 1 || port > 65535 {
			continue
		}
		seen[port] = struct{}{}
	}

	ports := make([]int, 0, len(seen))
	for p := range seen {
		ports = append(ports, p)
	}
	sort.Ints(ports)
	return ports
}
