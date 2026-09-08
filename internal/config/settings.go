package config

import (
	"fmt"
	"net/url"
	"sort"
	"strings"
	"time"

	"github.com/BurntSushi/toml"
)

// Setting is an effective value with its origin, safe for diagnostic display.
type Setting struct {
	Key    string
	Value  any
	Source string
}

// Settings returns effective settings in a stable order, redacting credentials.
func (c *Config) Settings() []Setting {
	settings := []Setting{
		{Key: "db.url", Value: redactDatabaseURL(c.DB.URL)},
		{Key: "source.remote", Value: redactURL(c.Source.Remote)},
		{Key: "worker.workdir", Value: c.Worker.Workdir},
		{Key: "worker.heartbeat_interval", Value: c.Worker.HeartbeatInterval.String()},
		{Key: "worker.stale_threshold", Value: c.Worker.StaleThreshold},
		{Key: "worker.log_flush_lines", Value: c.Worker.LogFlushLines},
		{Key: "worker.log_flush_interval", Value: c.Worker.LogFlushInterval.String()},
		{Key: "worker.log_buffer_size", Value: c.Worker.LogBufferSize},
		{Key: "retry.initial_interval", Value: c.Retry.InitialInterval.String()},
		{Key: "retry.max_interval", Value: c.Retry.MaxInterval.String()},
	}
	names := []string{}
	for name := range c.Integrations {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		keys := []string{}
		for key := range c.Integrations[name] {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			value := c.Integrations[name][key]
			if text, ok := value.(string); ok {
				value = redactURL(text)
			}
			lower := strings.ToLower(key)
			if strings.Contains(lower, "password") || strings.Contains(lower, "token") || strings.Contains(lower, "secret") {
				value = "REDACTED"
			}
			settings = append(settings, Setting{Key: "integrations." + name + "." + key, Value: value})
		}
	}
	for i := range settings {
		settings[i].Source = c.SourceFor(settings[i].Key)
	}
	return settings
}

// SourceFor reports the winning file/key, environment variable, flag, or default.
func (c *Config) SourceFor(key string) string {
	if source := c.sources[key]; source != "" {
		return source
	}
	return "default"
}

func (c *Config) setSource(key, source string) {
	if c.sources == nil {
		c.sources = make(map[string]string)
	}
	c.sources[key] = source
}

func (c *Config) recordSources(meta toml.MetaData, path string, prefix []string) {
	for _, setting := range c.Settings() {
		key := append(append([]string{}, prefix...), strings.Split(setting.Key, ".")...)
		if meta.IsDefined(key...) {
			c.setSource(setting.Key, path+" ["+strings.Join(key, ".")+"]")
		}
	}
}

func (c *Config) validate() error {
	for _, entry := range []struct {
		key   string
		value time.Duration
	}{
		{"worker.heartbeat_interval", c.Worker.HeartbeatInterval},
		{"worker.log_flush_interval", c.Worker.LogFlushInterval},
		{"retry.initial_interval", c.Retry.InitialInterval},
		{"retry.max_interval", c.Retry.MaxInterval},
	} {
		if entry.value <= 0 {
			return fmt.Errorf("%s: %s must be positive", c.SourceFor(entry.key), entry.key)
		}
	}
	for _, entry := range []struct {
		key   string
		value int
	}{
		{"worker.stale_threshold", c.Worker.StaleThreshold},
		{"worker.log_flush_lines", c.Worker.LogFlushLines},
		{"worker.log_buffer_size", c.Worker.LogBufferSize},
	} {
		if entry.value <= 0 {
			return fmt.Errorf("%s: %s must be positive", c.SourceFor(entry.key), entry.key)
		}
	}
	if c.Retry.MaxInterval < c.Retry.InitialInterval {
		return fmt.Errorf("%s: retry.max_interval must be at least retry.initial_interval", c.SourceFor("retry.max_interval"))
	}
	return nil
}

func redactDatabaseURL(raw string) string {
	// pgx also accepts keyword connection strings. Hide those in their entirety.
	if raw != "" && !strings.Contains(raw, "://") {
		return "[redacted connection string]"
	}
	return redactURL(raw)
}

func redactURL(raw string) string {
	if !strings.Contains(raw, "://") {
		return raw
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "[redacted URL]"
	}
	if parsed.User != nil {
		parsed.User = url.User("REDACTED")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil {
		parsed.RawQuery = "REDACTED"
	} else {
		for key := range query {
			query.Set(key, "REDACTED")
		}
		parsed.RawQuery = query.Encode()
	}
	if parsed.Fragment != "" {
		parsed.Fragment = "REDACTED"
	}
	return parsed.String()
}
