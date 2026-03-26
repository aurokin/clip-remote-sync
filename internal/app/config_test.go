package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoadConfigAllowsTaskModeWithoutEagerTaskValidation(t *testing.T) {
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

	cfg, err := loadConfig(configPath)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	if cfg.Sources["haste"].LaunchMode != "task" {
		t.Fatalf("expected task launch mode, got %q", cfg.Sources["haste"].LaunchMode)
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

func TestValidateSourceUsageRequiresOnlySetTextTaskForReverse(t *testing.T) {
	t.Parallel()

	err := validateSourceUsage(SourceConfig{LaunchMode: "task", SetTextTaskName: "crs_set_text"}, true)
	if err != nil {
		t.Fatalf("expected reverse validation to pass, got %v", err)
	}
}

func TestValidateSourceUsageRequiresCaptureTaskForForward(t *testing.T) {
	t.Parallel()

	err := validateSourceUsage(SourceConfig{LaunchMode: "task", SetTextTaskName: "crs_set_text"}, false)
	if err == nil {
		t.Fatal("expected forward validation to fail")
	}
	if !strings.Contains(err.Error(), "capture_task_name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSourceUsageRequiresSetTextTaskForReverse(t *testing.T) {
	t.Parallel()

	err := validateSourceUsage(SourceConfig{LaunchMode: "task"}, true)
	if err == nil {
		t.Fatal("expected reverse validation to fail")
	}
	if !strings.Contains(err.Error(), "set_text_task_name") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSourceUsageRequiresSetTextTaskForForward(t *testing.T) {
	t.Parallel()

	err := validateSourceUsage(SourceConfig{LaunchMode: "task", CaptureTaskName: "crs_capture"}, false)
	if err == nil {
		t.Fatal("expected forward validation to fail")
	}
	if !strings.Contains(err.Error(), "set_text_task_name") {
		t.Fatalf("unexpected error: %v", err)
	}
}
