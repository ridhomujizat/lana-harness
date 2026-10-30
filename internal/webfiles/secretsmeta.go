package webfiles

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

const (
	// SecretsMetaFileName is the secret update-time file under $LANA_HOME.
	SecretsMetaFileName = "secrets.meta.json"
	// SecretsMetaFileMode is the only accepted mode for secrets.meta.json.
	SecretsMetaFileMode os.FileMode = 0o600
	// SecretsMetaVersion is the on-disk schema version.
	SecretsMetaVersion = 1
)

// SecretsMeta is the on-disk form of $LANA_HOME/secrets.meta.json. It holds
// names and set times only, never a secret value.
type SecretsMeta struct {
	Version   int              `json:"version"`
	UpdatedMS map[string]int64 `json:"updated_ms"`
}

// ReadSecretsMeta parses path. A missing file is an empty version 1 file: the
// caller shows no update times. Any other read or parse problem is an error
// that names the file, so the caller can log it and keep the secrets API
// working.
func ReadSecretsMeta(path string) (SecretsMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return SecretsMeta{Version: SecretsMetaVersion, UpdatedMS: map[string]int64{}}, nil
		}
		return SecretsMeta{}, fmt.Errorf("webfiles: read %s: %w", path, err)
	}
	var meta SecretsMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return SecretsMeta{}, fmt.Errorf("webfiles: parse %s: %w", path, err)
	}
	if meta.Version != SecretsMetaVersion {
		return SecretsMeta{}, fmt.Errorf("webfiles: %s: unsupported version %d", path, meta.Version)
	}
	if meta.UpdatedMS == nil {
		meta.UpdatedMS = map[string]int64{}
	}
	return meta, nil
}

// SetSecretMeta records updatedMS for name and writes the file atomically with
// mode 0600. Every other name keeps its time.
func SetSecretMeta(path, name string, updatedMS int64) error {
	if err := ValidateSecretName(name); err != nil {
		return err
	}
	meta, err := ReadSecretsMeta(path)
	if err != nil {
		return err
	}
	meta.UpdatedMS[name] = updatedMS
	return writeSecretsMeta(path, meta)
}

// RemoveSecretMeta drops name from the file and writes it atomically with mode
// 0600. A name with no entry is not an error.
func RemoveSecretMeta(path, name string) error {
	if err := ValidateSecretName(name); err != nil {
		return err
	}
	meta, err := ReadSecretsMeta(path)
	if err != nil {
		return err
	}
	delete(meta.UpdatedMS, name)
	return writeSecretsMeta(path, meta)
}

func writeSecretsMeta(path string, meta SecretsMeta) error {
	meta.Version = SecretsMetaVersion
	if meta.UpdatedMS == nil {
		meta.UpdatedMS = map[string]int64{}
	}
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("webfiles: encode %s: %w", path, err)
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("webfiles: create directory for %s: %w", path, err)
	}
	return AtomicWriteMode(path, data, SecretsMetaFileMode)
}
