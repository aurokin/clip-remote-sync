package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigValidatesTaskModeFields(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	configJSON := `{
  "destination": { "name": "bront" },
  "sources": {
    "haste": {
      "ssh_target": "auro@haste.home.arpa",
      "launch_mode": "task",
      "remote_bin": "C:\\Program Files\\clip-remote-sync\\crs.exe"
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := loadConfig(configPath)
	if err == nil {
		t.Fatal("expected validation error for missing task names")
	}
	if !strings.Contains(err.Error(), "capture_task_name") {
		t.Fatalf("expected capture_task_name error, got %v", err)
	}
}

func TestLoadConfigAllowsDirectMode(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.json")
	configJSON := `{
  "destination": { "name": "bront" },
  "sources": {
    "luma": {
      "ssh_target": "auro@luma.home.arpa",
      "launch_mode": "direct",
      "remote_bin": "crs"
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Sources["luma"].LaunchMode != "direct" {
		t.Fatalf("expected direct launch mode, got %q", cfg.Sources["luma"].LaunchMode)
	}
}
