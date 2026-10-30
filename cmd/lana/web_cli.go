package main

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/ridhomujizat/lana-harness/internal/webfiles"
	"github.com/ridhomujizat/lana-harness/web"
)

const (
	// daemonSecretFileName is the daemon local control file under $LANA_HOME
	// (API-WLG-008).
	daemonSecretFileName = "daemon.secret"
	// daemonSecretFileMode keeps the secret readable only by the owner.
	daemonSecretFileMode os.FileMode = 0o600
	// webResetTimeout bounds the local control request of REQ-WLG-006.
	webResetTimeout = 5 * time.Second
	// webResetNoDaemonMessage is the exact hint printed without a daemon.
	webResetNoDaemonMessage = "lana is not running. Start it and a browser window will open to set a new password."
)

// webResetRequest posts to the daemon local control endpoint and returns the
// sign-in link. It is a variable so tests can replace it.
var webResetRequest = postLocalSignInLink

// daemonSecretFile is the JSON of $LANA_HOME/daemon.secret (API-WLG-008).
type daemonSecretFile struct {
	Addr   string `json:"addr"`
	Secret string `json:"secret"`
}

// daemonSecretPath returns the local control file path for a home.
func daemonSecretPath(home string) string {
	return filepath.Join(home, daemonSecretFileName)
}

// newLocalSecret returns 32 random bytes as base64url without padding. The
// daemon and the CLI share it through daemon.secret (API-WLG-008).
func newLocalSecret() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// writeDaemonSecret writes $LANA_HOME/daemon.secret atomically with mode 0600.
// The daemon writes it after the web listener is up so the CLI reaches the bound
// address (REQ-WLG-006).
func writeDaemonSecret(home, addr, secret string) error {
	body, err := json.Marshal(daemonSecretFile{Addr: addr, Secret: secret})
	if err != nil {
		return err
	}
	return webfiles.AtomicWriteMode(daemonSecretPath(home), append(body, '\n'), daemonSecretFileMode)
}

// readDaemonSecret reads $LANA_HOME/daemon.secret. It reports false when the
// file is missing or unusable, so the caller treats the daemon as not running.
func readDaemonSecret(home string) (daemonSecretFile, bool) {
	data, err := os.ReadFile(daemonSecretPath(home))
	if err != nil {
		return daemonSecretFile{}, false
	}
	var stored daemonSecretFile
	if err := json.Unmarshal(data, &stored); err != nil {
		return daemonSecretFile{}, false
	}
	if stored.Addr == "" || stored.Secret == "" {
		return daemonSecretFile{}, false
	}
	return stored, true
}

// removeDaemonSecret deletes $LANA_HOME/daemon.secret on a daemon stop. A
// missing file is not an error.
func removeDaemonSecret(home string) {
	if err := os.Remove(daemonSecretPath(home)); err != nil && !errors.Is(err, os.ErrNotExist) {
		slog.Warn("daemon: remove daemon.secret", "error", err)
	}
}

// localControlError reports a non-200 answer from the local control endpoint.
type localControlError struct {
	Status int
	Body   string
}

func (e *localControlError) Error() string {
	if message := strings.TrimSpace(e.Body); message != "" {
		return fmt.Sprintf("daemon returned status %d: %s", e.Status, message)
	}
	return fmt.Sprintf("daemon returned status %d", e.Status)
}

// postLocalSignInLink calls the daemon local control endpoint (API-WLG-008).
func postLocalSignInLink(addr, secret string, timeout time.Duration) (string, error) {
	client := &http.Client{Timeout: timeout}
	req, err := http.NewRequest(http.MethodPost, "http://"+addr+web.LocalSignInPath, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set(web.LocalSecretHeader, secret)
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer func() { _ = resp.Body.Close() }()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64<<10))
	if resp.StatusCode != http.StatusOK {
		return "", &localControlError{Status: resp.StatusCode, Body: string(body)}
	}
	var payload struct {
		Link string `json:"link"`
	}
	if err := json.Unmarshal(body, &payload); err != nil || payload.Link == "" {
		return "", errors.New("daemon returned an invalid sign-in link")
	}
	return payload.Link, nil
}

// cmdWeb dispatches the `lana web` subcommands (REQ-WLG-006).
func cmdWeb(args []string) int {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "error: use lana web reset-login")
		return 2
	}
	switch args[0] {
	case "reset-login":
		return cmdWebResetLogin(args[1:])
	}
	fmt.Fprintf(os.Stderr, "error: unknown web command %q (use reset-login)\n", args[0])
	return 2
}

// cmdWebResetLogin implements `lana web reset-login` (REQ-WLG-006, AC-WLG-011):
// with a running daemon it prints and opens a sign-in link; without one it marks
// reset_requested and prints the restart hint.
func cmdWebResetLogin(args []string) int {
	if len(args) > 0 {
		fmt.Fprintln(os.Stderr, "error: use lana web reset-login")
		return 2
	}
	home, err := homeDir()
	if err != nil {
		return fail(err, 1)
	}
	if stored, ok := readDaemonSecret(home); ok {
		link, err := webResetRequest(stored.Addr, stored.Secret, webResetTimeout)
		switch {
		case err == nil:
			fmt.Printf("Open this link to sign in: %s\n", link)
			_ = openBrowser(link)
			return 0
		case errors.Is(err, syscall.ECONNREFUSED):
			// A refused connection means the file is stale: fall through to the
			// no-daemon path.
		default:
			fmt.Fprintf(os.Stderr, "error: %v\n", err)
			return 1
		}
	}
	if err := web.RequestReset(home); err != nil {
		return fail(err, 1)
	}
	fmt.Println(webResetNoDaemonMessage)
	return 0
}
