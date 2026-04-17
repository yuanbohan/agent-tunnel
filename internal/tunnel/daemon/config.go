package daemon

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
	"strings"
)

var defaultAllowedCommands = []string{"codex", "claude", "gemini"}

type Config struct {
	AllowedCommands []string `json:"allowed_commands"`
}

func LoadConfig(paths Paths) (Config, error) {
	payload, err := os.ReadFile(paths.ConfigFile)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultConfig(), nil
		}
		return Config{}, err
	}

	var config Config
	if err := json.Unmarshal(payload, &config); err != nil {
		return Config{}, err
	}
	config.Normalize()
	if len(config.AllowedCommands) == 0 {
		config = DefaultConfig()
	}
	return config, nil
}

func DefaultConfig() Config {
	return Config{AllowedCommands: append([]string(nil), defaultAllowedCommands...)}
}

func (c *Config) Normalize() {
	seen := make(map[string]struct{}, len(c.AllowedCommands))
	normalized := make([]string, 0, len(c.AllowedCommands))
	for _, command := range c.AllowedCommands {
		trimmed := strings.TrimSpace(command)
		if trimmed == "" {
			continue
		}
		key := strings.ToLower(trimmed)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		normalized = append(normalized, trimmed)
	}
	slices.Sort(normalized)
	c.AllowedCommands = normalized
}

func (c Config) Allows(command string) bool {
	trimmed := strings.TrimSpace(command)
	if trimmed == "" {
		return false
	}
	for _, allowed := range c.AllowedCommands {
		if strings.EqualFold(allowed, trimmed) {
			return true
		}
	}
	return false
}
