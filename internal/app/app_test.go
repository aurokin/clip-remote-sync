package app

import (
	"encoding/base64"
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

func TestRunReverseTextFlowSetsRemoteClipboard(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: "hello from bront"}, nil
	}

	remoteText := ""
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		remoteText = text
		return nil
	}
	app.deps.remoteCapture = func(source remote.SourceOptions) (remote.CapturedData, error) {
		t.Fatal("unexpected remote capture")
		return remote.CapturedData{}, nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "-r", "haste"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
	if remoteText != "hello from bront" {
		t.Fatalf("expected remote clipboard text to be set, got %q", remoteText)
	}
	if !strings.Contains(stdout.String(), "Clipboard synced from bront to haste") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunReverseTextFlowReportsRemoteWriteFailure(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: "hello from bront"}, nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		return errors.New("ssh failed")
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "-r", "haste"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure, got exit code %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "Failed to set haste clipboard") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunReverseImageFlowSavesImageAndSetsRemotePath(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{
			Kind:           protocol.KindImage,
			ImagePNGBase64: base64.StdEncoding.EncodeToString([]byte("png")),
		}, nil
	}
	app.deps.saveImage = func(imagePNG []byte) (string, error) {
		if string(imagePNG) != "png" {
			t.Fatalf("unexpected image payload: %q", string(imagePNG))
		}
		return "/tmp/clip/reverse-image.png", nil
	}

	remoteText := ""
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		remoteText = text
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "--reverse", "haste"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
	if remoteText != "/tmp/clip/reverse-image.png" {
		t.Fatalf("expected remote clipboard path to be set, got %q", remoteText)
	}
	if !strings.Contains(stdout.String(), "Local image saved to /tmp/clip/reverse-image.png and synced to haste as text") {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunReverseImageFlowKeepsSavedImageWhenRemoteUpdateFails(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{
			Kind:           protocol.KindImage,
			ImagePNGBase64: base64.StdEncoding.EncodeToString([]byte("png")),
		}, nil
	}
	app.deps.saveImage = func(imagePNG []byte) (string, error) {
		return "/tmp/clip/reverse-image.png", nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		return errors.New("task failed")
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "-r", "haste"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure, got exit code %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "Remote clipboard state is unknown") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunReverseImageFlowRejectsMalformedImagePayload(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindImage, ImagePNGBase64: "not-base64"}, nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "-r", "haste"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure, got exit code %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "Failed to decode local image clipboard") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunReverseImageFlowReportsSaveFailure(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{
			Kind:           protocol.KindImage,
			ImagePNGBase64: base64.StdEncoding.EncodeToString([]byte("png")),
		}, nil
	}
	app.deps.saveImage = func(imagePNG []byte) (string, error) {
		return "", errors.New("disk full")
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "-r", "haste"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure, got exit code %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "Failed to save local image") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunReverseReportsLocalCaptureFailure(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{}, errors.New("clipboard is empty or unsupported")
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "-r", "haste"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure, got exit code %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "Failed to capture local clipboard") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunReverseRejectsUnsupportedCaptureKind(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.Kind("weird")}, nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "-r", "haste"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure, got exit code %d", exitCode)
	}
	if !strings.Contains(stderr.String(), `Unsupported local capture kind "weird"`) {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestHelpIncludesReverseMode(t *testing.T) {
	app := testApplication()

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--help"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected help success, got %d", exitCode)
	}
	if !strings.Contains(stdout.String(), "[-r]") || !strings.Contains(stdout.String(), "With -r, local text is pushed") {
		t.Fatalf("unexpected help output: %q", stdout.String())
	}
}

func TestReverseLongFlagIsAccepted(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: "hello from bront"}, nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "--reverse", "haste"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
}

func TestRunReverseTaskModeAllowsMissingCaptureTaskName(t *testing.T) {
	configPath := writeReverseOnlyTaskConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: "hello from bront"}, nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "-r", "haste"}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
}

func TestForwardTaskModeStillRequiresCaptureTaskName(t *testing.T) {
	configPath := writeReverseOnlyTaskConfig(t)
	app := testApplication()

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "haste"}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure, got exit code %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "capture_task_name") {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestImageSaveDirForPOSIX(t *testing.T) {
	t.Parallel()

	if got := imageSaveDirFor('/', "/ignored"); got != "/tmp/clip" {
		t.Fatalf("unexpected posix image save dir: %q", got)
	}
}

func TestImageSaveDirForWindows(t *testing.T) {
	t.Parallel()

	if got := imageSaveDirFor('\\', `C:\Temp`); got != `C:\Temp\clip` {
		t.Fatalf("unexpected windows image save dir: %q", got)
	}
}

func TestSaveImageToDirWritesPNG(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	path, err := saveImageToDir([]byte("png"), dir)
	if err != nil {
		t.Fatalf("saveImageToDir: %v", err)
	}
	if !strings.HasPrefix(path, dir+string(filepath.Separator)) {
		t.Fatalf("expected image path under temp dir, got %q", path)
	}
	if !strings.HasSuffix(path, ".png") {
		t.Fatalf("expected png suffix, got %q", path)
	}
	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read saved image: %v", err)
	}
	if string(got) != "png" {
		t.Fatalf("unexpected saved image contents: %q", string(got))
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

func writeReverseOnlyTaskConfig(t *testing.T) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.json")
	configJSON := `{
  "destination": { "name": "bront" },
  "sources": {
    "haste": {
      "ssh_target": "auro@haste.home.arpa",
      "launch_mode": "task",
      "remote_bin": "crs.exe",
      "set_text_task_name": "crs_set_clipboard_text"
    }
  }
}`
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}
