package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
)

type Config struct {
	Destination DestinationConfig       `json:"destination"`
	Sources     map[string]SourceConfig `json:"sources"`
}

type DestinationConfig struct {
	Name string `json:"name"`
}

type SourceConfig struct {
	SSHTarget       string `json:"ssh_target"`
	RemoteBin       string `json:"remote_bin,omitempty"`
	LaunchMode      string `json:"launch_mode,omitempty"`
	TaskBridgeDir   string `json:"task_bridge_dir,omitempty"`
	CaptureTaskName string `json:"capture_task_name,omitempty"`
	SetTextTaskName string `json:"set_text_task_name,omitempty"`
}

func defaultConfigPath() (string, error) {
	if override := os.Getenv("CRS_CONFIG"); override != "" {
		return override, nil
	}

	if xdg := os.Getenv("XDG_CONFIG_HOME"); xdg != "" {
		return filepath.Join(xdg, "clip-remote-sync", "config.json"), nil
	}

	home, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("resolve home directory: %w", err)
	}

	return filepath.Join(home, ".config", "clip-remote-sync", "config.json"), nil
}

func loadConfig(configPath string) (Config, error) {
	var cfg Config

	data, err := os.ReadFile(configPath)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return cfg, fmt.Errorf("config not found at %s", configPath)
		}
		return cfg, fmt.Errorf("read config: %w", err)
	}

	if err := json.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}

	if len(cfg.Sources) == 0 {
		return cfg, errors.New("config must define at least one source")
	}

	for name, source := range cfg.Sources {
		if source.SSHTarget == "" {
			return cfg, fmt.Errorf("source %q is missing ssh_target", name)
		}
		if source.LaunchMode != "" && source.LaunchMode != "direct" && source.LaunchMode != "task" {
			return cfg, fmt.Errorf("source %q has unsupported launch_mode %q", name, source.LaunchMode)
		}
	}

	return cfg, nil
}

func configuredSources(cfg Config) []string {
	names := make([]string, 0, len(cfg.Sources))
	for name := range cfg.Sources {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}
