package config

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/pelletier/go-toml/v2"
	"github.com/ridhomujizat/lana-harness/core"
)

// Options controls how config is read.
type Options struct {
	Home    string
	Profile string
	Patches []string
	Layers  []Layer
	Env     func(name string) (string, bool)
}

// Layer is an in-memory layer. Set exactly one of TOML or Value.
type Layer struct {
	Name  string
	TOML  []byte
	Value any
}

// Result is the valid result of Load.
type Result struct {
	Profile  string
	Home     string
	Config   core.Config
	Origins  Origins
	Layers   []LayerInfo
	Warnings []Warning
	Secrets  core.Secrets
}

// LayerInfo describes a single layer.
type LayerInfo struct {
	Name    string
	Path    string
	Present bool
}

type pendingLayer struct {
	info  LayerInfo
	raw   []byte
	value any
}

// Load builds the effective config.
func Load(ctx context.Context, opts Options) (*Result, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	env := opts.Env
	if env == nil {
		env = os.LookupEnv
	}
	profile := opts.Profile
	if profile == "" {
		profile = "minimal"
	}
	if _, ok := builtinProfiles[profile]; !ok {
		return nil, Errors{{Msg: fmt.Sprintf("unknown profile: %s (available: %s)", profile, strings.Join(ProfileNames(), ", "))}}
	}
	home := opts.Home
	if home != "" {
		if abs, err := filepath.Abs(home); err == nil {
			home = abs
		}
	}
	var pending []pendingLayer
	pending = append(pending, pendingLayer{info: LayerInfo{Name: "base", Present: true}, raw: []byte(baseTOML)})
	pending = append(pending, pendingLayer{info: LayerInfo{Name: "profile:" + profile, Present: true}, raw: []byte(builtinProfiles[profile])})
	if home != "" {
		profilePath := filepath.Join(home, "profiles", profile+".toml")
		pending = append(pending, fileLayer(profilePath))
		pending = append(pending, fileLayer(filepath.Join(home, "config.toml")))
	}
	for _, patch := range opts.Patches {
		path := patch
		if abs, err := filepath.Abs(path); err == nil {
			path = abs
		}
		data, err := os.ReadFile(path)
		if err != nil {
			pending = append(pending, pendingLayer{info: LayerInfo{Name: path, Path: path}, value: err})
			continue
		}
		pending = append(pending, pendingLayer{info: LayerInfo{Name: path, Path: path, Present: true}, raw: data})
	}
	for _, layer := range opts.Layers {
		info := LayerInfo{Name: layer.Name, Present: true}
		pl := pendingLayer{info: info}
		switch {
		case layer.Name == "":
			return nil, Errors{{Msg: "in-memory layer name is required"}}
		case layer.TOML != nil && layer.Value != nil:
			pl.value = fmt.Errorf("in-memory layer must use TOML or Value, not both")
		case layer.TOML == nil && layer.Value == nil:
			pl.value = fmt.Errorf("in-memory layer must use TOML or Value")
		case layer.TOML != nil:
			pl.raw = layer.TOML
		default:
			b, err := toml.Marshal(layer.Value)
			if err != nil {
				pl.value = err
			} else {
				pl.raw = b
			}
		}
		pending = append(pending, pl)
	}

	rank := map[string]int{}
	layers := make([]LayerInfo, 0, len(pending))
	var errs Errors
	var parsed []loadedLayer
	for i, pl := range pending {
		rank[pl.info.Name] = i
		layers = append(layers, pl.info)
		if err, ok := pl.value.(error); ok {
			if os.IsNotExist(err) {
				errs = append(errs, Error{File: pl.info.Name, Msg: "patch file not found: " + pl.info.Name})
			} else {
				errs = append(errs, Error{File: pl.info.Name, Msg: err.Error()})
			}
			continue
		}
		ld, perr := parseTOML(pl.raw, pl.info.Name)
		if perr != nil {
			errs = append(errs, *perr)
			continue
		}
		parsed = append(parsed, loadedLayer{info: pl.info, data: ld, rank: i})
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	secretSrc, warnings, secretErrs := loadSecretSource(home, env)
	errs = append(errs, secretErrs...)
	for _, warning := range warnings {
		slog.Warn(warning.Msg, "file", warning.File)
	}

	moduleNames := core.ModuleNames()
	for _, layer := range parsed {
		layerErrs := validateLayer(layer.data, layer.info.Name, moduleNames)
		errs = append(errs, layerErrs...)
	}

	st := mergeLayers(parsed)
	errs = append(errs, interpolateMCP(st.value, secretSrc, st.positions)...)
	validateEffective(st.value, st.positions, func(key, msg string) {
		p := st.positions[key]
		errs = append(errs, Error{File: st.origins[key], Line: p.Line, Col: p.Col, Key: key, Msg: msg})
	})

	if len(errs) > 0 {
		sort.SliceStable(errs, func(i, j int) bool {
			ri, rj := rank[errs[i].File], rank[errs[j].File]
			if ri != rj {
				return ri < rj
			}
			if errs[i].Line != errs[j].Line {
				return errs[i].Line < errs[j].Line
			}
			return errs[i].Key < errs[j].Key
		})
		return nil, errs
	}

	cfg, err := decodeConfig(st.value)
	if err != nil {
		return nil, Errors{{Msg: err.Error()}}
	}
	return &Result{
		Profile:  profile,
		Home:     home,
		Config:   cfg,
		Origins:  st.origins,
		Layers:   layers,
		Warnings: warnings,
		Secrets:  secretSrc,
	}, nil
}

func fileLayer(path string) pendingLayer {
	info := LayerInfo{Name: path, Path: path}
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return pendingLayer{info: info}
		}
		return pendingLayer{info: info, value: err}
	}
	info.Present = true
	return pendingLayer{info: info, raw: data}
}

func decodeConfig(value map[string]any) (core.Config, error) {
	if len(value) == 0 {
		return core.Config{Version: maxSupportedVersion}, nil
	}
	if _, ok := value["version"]; !ok {
		value["version"] = int64(maxSupportedVersion)
	}
	b, err := toml.Marshal(value)
	if err != nil {
		return core.Config{}, err
	}
	var cfg core.Config
	if err := toml.Unmarshal(b, &cfg); err != nil {
		return core.Config{}, err
	}
	if cfg.Version == 0 {
		cfg.Version = maxSupportedVersion
	}
	return cfg, nil
}

// HomeDir returns LANA_HOME or ~/.lana.
func HomeDir(env func(name string) (string, bool)) (string, error) {
	if env == nil {
		env = os.LookupEnv
	}
	if v, ok := env("LANA_HOME"); ok && strings.TrimSpace(v) != "" {
		return v, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".lana"), nil
}
