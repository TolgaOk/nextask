package config

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/BurntSushi/toml"
	"github.com/TolgaOk/nextask/internal/endpoint"
	"github.com/TolgaOk/nextask/internal/integrations"
)

// decodeFile validates each file before merging later layers, so an override
// cannot hide literal credentials in a shareable configuration file.
func decodeFile(file configFile, cfg *Config) error {
	var document toml.Primitive
	meta, err := toml.DecodeFile(file.path, &document)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return configFileError(file.path, err)
	}
	for _, key := range meta.Keys() {
		name := strings.ToLower(strings.Join(key, "."))
		localKey := key
		if file.shared {
			localKey = nil
			if strings.EqualFold(key[0], "nextask") {
				localKey = key[1:]
			}
		}
		if len(localKey) > 0 && len(localKey) <= 2 && strings.EqualFold(localKey[0], "integrations") && meta.Type(key...) != "Hash" {
			return fmt.Errorf("%s: %s must be a table", file.path, strings.Join(key, "."))
		}
		switch name {
		case "defaults.db_url":
			return fmt.Errorf("%s: defaults.db_url is unsupported; use nextask.db.url with environment references", file.path)
		}
	}

	previous := cfg.Integrations
	cfg.Integrations = nil
	var prefix []string
	if file.shared {
		var shared struct {
			Defaults struct{} `toml:"defaults"`
			Nextask  *Config  `toml:"nextask"`
		}
		shared.Nextask = cfg
		err = meta.PrimitiveDecode(document, &shared)
		prefix = []string{"nextask"}
	} else {
		err = meta.PrimitiveDecode(document, cfg)
	}
	if err != nil {
		return configFileError(file.path, err)
	}
	if err := endpoint.Validate(cfg.DB.Endpoint, endpoint.Database); err != nil {
		return fmt.Errorf("%s: db.url: %w", file.path, err)
	}
	selectionKey := append(append([]string{}, prefix...), "enqueue", "with")
	if meta.IsDefined(selectionKey...) {
		return fmt.Errorf("%s: select integrations explicitly with --with; enqueue.with is unsupported", file.path)
	}
	for _, key := range meta.Undecoded() {
		if file.shared && !strings.EqualFold(key[0], "nextask") {
			continue
		}
		return fmt.Errorf("%s: unknown config setting %s", file.path, strings.Join(key, "."))
	}
	// Validate each file before merging another layer over it. Unsafe values
	// must be removed from the file even if an override would hide them.
	if _, err := cfg.resolveIntegrations(); err != nil {
		return fmt.Errorf("%s: %w", file.path, err)
	}
	cfg.Integrations = mergeIntegrationOptions(previous, cfg.Integrations)
	cfg.LoadedFiles = append(cfg.LoadedFiles, file.path)
	cfg.recordSources(meta, file.path, prefix)
	cfg.applySourceAlias(meta, file.path, prefix)
	return nil
}

// TOML errors may quote a source value. Report its location without echoing it.
func configFileError(path string, err error) error {
	var parseError toml.ParseError
	if errors.As(err, &parseError) {
		return fmt.Errorf("%s: invalid TOML at line %d", path, parseError.Position.Line)
	}
	return fmt.Errorf("%s: %w", path, err)
}

// resolveIntegrations applies the same connection constraints to files and env
// values, including the deprecated source.remote alias.
func (cfg *Config) resolveIntegrations() (map[string]map[string]any, error) {
	if _, err := (integrations.Git{}).Options().Resolve(map[string]any{"remote": cfg.Source.Remote}); err != nil {
		return nil, fmt.Errorf("source.%w", err)
	}
	return integrations.Builtins().Configure(cfg.Integrations)
}
