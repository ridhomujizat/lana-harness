package main

import (
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/egoist/mygo"
)

// snapshot is a development hook for screenshots, like Lana.app's
// --window-snapshot: with LANA_WB_SNAPSHOT set to a file path it waits
// LANA_WB_SNAPSHOT_DELAY seconds (default 4; LANA_WB_SNAPSHOT_SIZE "WxH"
// sizes the window first), writes a PNG of the window there and quits.
func snapshot(w *mygo.Window) {
	path := os.Getenv("LANA_WB_SNAPSHOT")
	if path == "" {
		return
	}
	delay := 4.0
	if v := os.Getenv("LANA_WB_SNAPSHOT_DELAY"); v != "" {
		d, err := strconv.ParseFloat(v, 64)
		if err != nil {
			log.Printf("snapshot: LANA_WB_SNAPSHOT_DELAY: %v", err)
		} else {
			delay = d
		}
	}
	size := os.Getenv("LANA_WB_SNAPSHOT_SIZE")
	go func() {
		if size != "" {
			width, height, ok := strings.Cut(size, "x")
			nw, errW := strconv.Atoi(width)
			nh, errH := strconv.Atoi(height)
			if !ok || errW != nil || errH != nil {
				log.Printf("snapshot: LANA_WB_SNAPSHOT_SIZE %q is not WxH", size)
			} else {
				w.SetSize(nw, nh)
			}
		}
		time.Sleep(time.Duration(delay * float64(time.Second)))
		// A snapshot can show a dialog over the window: with
		// LANA_WB_SNAPSHOT_DIALOG set to "rename-space" or "rename-tab",
		// the rename dialog of the active space or tab opens just before
		// the capture, so the design can be checked headlessly (spec 58,
		// spaces).
		if name := os.Getenv("LANA_WB_SNAPSHOT_DIALOG"); name != "" {
			mygo.RunOnMain(func() { workbench.SnapshotDialog(name) })
			time.Sleep(600 * time.Millisecond) // let the dialog fade in
		}
		// LANA_WB_SNAPSHOT_PREVIEW opens a file in the preview panel before
		// the capture, as a ⌘+click on its path would (REQ-DW-080). The link
		// is an absolute path and there is no session, which the daemon
		// resolves on its own (REQ-WP-040).
		if p := os.Getenv("LANA_WB_SNAPSHOT_PREVIEW"); p != "" {
			mygo.RunOnMain(func() { workbench.OpenPreview("", p, "") })
			time.Sleep(1500 * time.Millisecond) // let the page load
		}
		png, err := w.CapturePage()
		if err == nil {
			err = os.WriteFile(path, png, 0o644)
		}
		if err != nil {
			log.Printf("snapshot: %v", err)
			mygo.App.Exit(1)
			return
		}
		log.Printf("snapshot: wrote %s", path)
		mygo.App.Quit()
	}()
}
