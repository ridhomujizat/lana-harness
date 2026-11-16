package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/workbench"
)

// workbenchStopTimeout bounds how long `lana workbench stop` waits for the core
// to answer.
const workbenchStopTimeout = 10 * time.Second

// cmdWorkbench runs the Workbench terminal core: `serve` owns the sessions in
// the foreground, `stop` asks a running core to shut down.
func cmdWorkbench(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: use lana workbench serve|stop")
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	switch args[0] {
	case "serve":
		fmt.Fprintf(os.Stderr, "workbench: serving on %s\n", filepath.Join(workbench.Dir(home), "core.sock"))
		if err := workbench.Serve(context.Background(), home); err != nil {
			return fail(err, 1)
		}
		return 0
	case "stop":
		ctx, cancel := context.WithTimeout(context.Background(), workbenchStopTimeout)
		defer cancel()
		c, err := workbench.Dial(ctx, home)
		if err != nil {
			return fail(errors.New("workbench core is not running"), 1)
		}
		defer func() { _ = c.Close() }()
		if err := c.Shutdown(ctx); err != nil {
			return fail(err, 1)
		}
		return 0
	default:
		fmt.Fprintf(os.Stderr, "error: unknown workbench command: %s\n", args[0])
		return 2
	}
}
