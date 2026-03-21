package app

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aurokin/clip-remote-sync/internal/protocol"
	"github.com/aurokin/clip-remote-sync/internal/remote"
)

func TestRunImageFlowUpdatesLocalImageBeforeRemoteText(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	var order []string
	app.deps.remoteCapture = func(source remote.SourceOptions) (remote.CapturedData, error) {
		return remote.CapturedData{Kind: protocol.KindImage, ImagePNG: []byte("png")}, nil
	}
	app.deps.saveImage = func(imagePNG []byte) (string, error) {
		return "/tmp/clip/test-image.png", nil
	}
	app.deps.setLocalImageClipboard = func(imagePNG []byte) error {
		order = append(order, "local-image:"+string(imagePNG))
		return nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		order = append(order, "remote-text:"+text)
		return nil
	}
	app.deps.setLocalClipboard = func(text string) error {
		t.Fatalf("unexpected local text clipboard write: %q", text)
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "haste"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}

	wantOrder := []string{"local-image:png", "remote-text:/tmp/clip/test-image.png"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("unexpected operation order: got %v want %v", order, wantOrder)
	}
	if !strings.Contains(stdout.String(), "Image captured from haste and saved to /tmp/clip/test-image.png") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunImageFlowKeepsLocalImageClipboardWhenRemoteUpdateFails(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	imagePath := filepath.Join(t.TempDir(), "clipboard.png")
	if err := os.WriteFile(imagePath, []byte("png"), 0o644); err != nil {
		t.Fatalf("write image file: %v", err)
	}

	var localImageWrites [][]byte
	app.deps.remoteCapture = func(source remote.SourceOptions) (remote.CapturedData, error) {
		return remote.CapturedData{Kind: protocol.KindImage, ImagePNG: []byte("png")}, nil
	}
	app.deps.saveImage = func(imagePNG []byte) (string, error) {
		return imagePath, nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		return errors.New("task failed")
	}
	app.deps.setLocalImageClipboard = func(imagePNG []byte) error {
		localImageWrites = append(localImageWrites, append([]byte(nil), imagePNG...))
		return nil
	}
	app.deps.setLocalClipboard = func(text string) error {
		t.Fatalf("unexpected local text clipboard write: %q", text)
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "haste"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure, got exit code %d", exitCode)
	}

	wantWrites := [][]byte{[]byte("png")}
	if !reflect.DeepEqual(localImageWrites, wantWrites) {
		t.Fatalf("unexpected local image clipboard writes: got %v want %v", localImageWrites, wantWrites)
	}
	if !strings.Contains(stderr.String(), "Remote clipboard state is unknown") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
	if _, err := os.Stat(imagePath); err != nil {
		t.Fatalf("expected saved image to remain available locally, stat err=%v", err)
	}
}

func TestRunTextFlowSetsLocalClipboard(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	localText := ""
	app.deps.remoteCapture = func(source remote.SourceOptions) (remote.CapturedData, error) {
		return remote.CapturedData{Kind: protocol.KindText, Text: "hello"}, nil
	}
	app.deps.setLocalClipboard = func(text string) error {
		localText = text
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "haste"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
	if localText != "hello" {
		t.Fatalf("expected local clipboard text to be set, got %q", localText)
	}
	if !strings.Contains(stdout.String(), "Clipboard synced from haste to bront") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func testApplication() application {
	return application{deps: defaultDependencies()}
}

func writeTestConfig(t *testing.T) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.json")
	configJSON := `{
  "destination": { "name": "bront" },
  "sources": {
    "haste": {
      "ssh_target": "auro@haste.home.arpa",
      "launch_mode": "task",
      "remote_bin": "crs.exe",
      "capture_task_name": "crs_capture",
      "set_text_task_name": "crs_set_clipboard_text"
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}
