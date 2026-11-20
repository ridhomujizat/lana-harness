package lana

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
)

// SecretFileName is the daemon local control file under a Lana home
// (API-WLG-008).
const SecretFileName = "daemon.secret"

// ErrNoSecret reports that $LANA_HOME/daemon.secret is missing, unreadable, or
// unusable, so the daemon is not running or was started with another home
// (REQ-DW-013).
var ErrNoSecret = errors.New("lana: daemon.secret is missing or invalid")

// Secret is the JSON of $LANA_HOME/daemon.secret: the address the web listener
// is bound to and the local control secret, the file Lana.app reads.
type Secret struct {
	Addr   string `json:"addr"`
	Secret string `json:"secret"`
}

// ReadSecret reads <home>/daemon.secret. A missing file, bad JSON, or an empty
// addr or secret is ErrNoSecret with the reason in the message.
func ReadSecret(home string) (Secret, error) {
	path := filepath.Join(home, SecretFileName)
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Secret{}, fmt.Errorf("%w: %s: the file does not exist", ErrNoSecret, path)
	}
	if err != nil {
		return Secret{}, fmt.Errorf("%w: %s: %v", ErrNoSecret, path, err)
	}
	var secret Secret
	if err := json.Unmarshal(data, &secret); err != nil {
		return Secret{}, fmt.Errorf("%w: %s: invalid JSON: %v", ErrNoSecret, path, err)
	}
	if secret.Addr == "" || secret.Secret == "" {
		return Secret{}, fmt.Errorf("%w: %s: addr and secret must both be set", ErrNoSecret, path)
	}
	return secret, nil
}
