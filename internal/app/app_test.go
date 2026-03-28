package app

import (
	"bufio"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/aurokin/clip-remote-sync/internal/protocol"
	"github.com/aurokin/clip-remote-sync/internal/remote"
)

const (
	testDestinationName    = "test-destination"
	testTaskSourceName     = "task-source"
	testDirectSourceName   = "direct-source"
	testTaskSSHTarget      = "task-source.example.test"
	testDirectSSHTarget    = "direct-source.example.test"
	testLocalClipboardText = "hello from test destination"
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
	exitCode := app.run([]string{"--config", configPath, testTaskSourceName}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}

	wantOrder := []string{"local-image:png", "remote-text:/tmp/clip/test-image.png"}
	if !reflect.DeepEqual(order, wantOrder) {
		t.Fatalf("unexpected operation order: got %v want %v", order, wantOrder)
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("Image captured from %s and saved to /tmp/clip/test-image.png", testTaskSourceName)) {
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
	exitCode := app.run([]string{"--config", configPath, testTaskSourceName}, &stdout, &stderr)
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
	exitCode := app.run([]string{"--config", configPath, testTaskSourceName}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
	if localText != "hello" {
		t.Fatalf("expected local clipboard text to be set, got %q", localText)
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("Clipboard synced from %s to %s", testTaskSourceName, testDestinationName)) {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunReverseTextFlowSetsRemoteClipboard(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: testLocalClipboardText}, nil
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
	exitCode := app.run([]string{"--config", configPath, "-r", testTaskSourceName}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
	if remoteText != testLocalClipboardText {
		t.Fatalf("expected remote clipboard text to be set, got %q", remoteText)
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("Clipboard synced from %s to %s", testDestinationName, testTaskSourceName)) {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunReverseTextFlowReportsRemoteWriteFailure(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: testLocalClipboardText}, nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		return errors.New("ssh failed")
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "-r", testTaskSourceName}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure, got exit code %d", exitCode)
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("Failed to set %s clipboard", testTaskSourceName)) {
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
	exitCode := app.run([]string{"--config", configPath, "--reverse", testTaskSourceName}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
	if remoteText != "/tmp/clip/reverse-image.png" {
		t.Fatalf("expected remote clipboard path to be set, got %q", remoteText)
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("Local image saved to /tmp/clip/reverse-image.png and synced to %s as text", testTaskSourceName)) {
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
	exitCode := app.run([]string{"--config", configPath, "-r", testTaskSourceName}, &stdout, &stderr)
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
	exitCode := app.run([]string{"--config", configPath, "-r", testTaskSourceName}, &stdout, &stderr)
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
	exitCode := app.run([]string{"--config", configPath, "-r", testTaskSourceName}, &stdout, &stderr)
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
	exitCode := app.run([]string{"--config", configPath, "-r", testTaskSourceName}, &stdout, &stderr)
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
	exitCode := app.run([]string{"--config", configPath, "-r", testTaskSourceName}, &stdout, &stderr)
	if exitCode != 1 {
		t.Fatalf("expected failure, got exit code %d", exitCode)
	}
	if !strings.Contains(stderr.String(), `Unsupported local capture kind "weird"`) {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunWithoutArgsPromptsForHostThenAction(t *testing.T) {
	configPath := writeMultiSourceConfig(t)
	app := testApplication()
	app.in = strings.NewReader("1\n1\n")

	var capturedTarget string
	app.deps.defaultConfigPath = func() (string, error) {
		return configPath, nil
	}
	app.deps.remoteCapture = func(source remote.SourceOptions) (remote.CapturedData, error) {
		capturedTarget = source.SSHTarget
		return remote.CapturedData{Kind: protocol.KindText, Text: "hello"}, nil
	}
	app.deps.setLocalClipboard = func(text string) error {
		if text != "hello" {
			t.Fatalf("unexpected local clipboard text: %q", text)
		}
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run(nil, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
	if capturedTarget != testDirectSSHTarget {
		t.Fatalf("expected direct target %q, got %q", testDirectSSHTarget, capturedTarget)
	}
	if !strings.Contains(stdout.String(), "Select source host:") {
		t.Fatalf("missing host prompt: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[1] "+testDirectSourceName) {
		t.Fatalf("missing direct host option: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[2] "+testTaskSourceName) {
		t.Fatalf("missing task host option: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("Select action for %s:", testDirectSourceName)) {
		t.Fatalf("missing direct source action prompt: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("[1] Pull from %s (crs %s)", testDirectSourceName, testDirectSourceName)) {
		t.Fatalf("missing direct source pull option: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("Clipboard synced from %s to %s", testDirectSourceName, testDestinationName)) {
		t.Fatalf("unexpected stdout: %q", stdout.String())
	}
}

func TestRunWithoutArgsRetriesAfterInvalidHostSelection(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()
	app.in = strings.NewReader("9\n1\n2\n")

	app.deps.defaultConfigPath = func() (string, error) {
		return configPath, nil
	}
	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: testLocalClipboardText}, nil
	}

	var remoteText string
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		remoteText = text
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run(nil, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
	if remoteText != testLocalClipboardText {
		t.Fatalf("expected remote clipboard text to be set, got %q", remoteText)
	}
	if !strings.Contains(stderr.String(), `Key "9" not found. Try again.`) {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
	if strings.Count(stdout.String(), "> ") != 3 {
		t.Fatalf("expected reprompt, stdout=%q", stdout.String())
	}
}

func TestRunWithoutArgsOnlyShowsValidActionsForSelectedHost(t *testing.T) {
	configPath := writeReverseOnlyTaskConfig(t)
	app := testApplication()
	app.in = strings.NewReader("1\n1\n")

	app.deps.defaultConfigPath = func() (string, error) {
		return configPath, nil
	}
	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: testLocalClipboardText}, nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run(nil, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Select source host:") {
		t.Fatalf("missing host prompt: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "Press the key shown in brackets, or q to quit.") {
		t.Fatalf("missing key hint: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("Select action for %s:", testTaskSourceName)) {
		t.Fatalf("missing action prompt: %q", stdout.String())
	}
	if strings.Contains(stdout.String(), fmt.Sprintf("[1] Pull from %s (crs %s)", testTaskSourceName, testTaskSourceName)) {
		t.Fatalf("did not expect invalid forward action in menu: %q", stdout.String())
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("[1] Push to %s (crs -r %s)", testTaskSourceName, testTaskSourceName)) {
		t.Fatalf("expected reverse action in menu: %q", stdout.String())
	}
}

func TestRunWithoutArgsQCancelsInteractiveSelection(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()
	app.in = strings.NewReader("q")

	app.deps.defaultConfigPath = func() (string, error) {
		return configPath, nil
	}
	app.deps.remoteCapture = func(source remote.SourceOptions) (remote.CapturedData, error) {
		t.Fatal("unexpected remote capture")
		return remote.CapturedData{}, nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		t.Fatal("unexpected remote clipboard write")
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run(nil, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected cancel exit code 0, got %d", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), "Cancelled.") {
		t.Fatalf("expected cancel message, stdout=%q", stdout.String())
	}
}

func TestRunWithoutArgsCtrlCInterruptsInteractiveSelection(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()
	app.in = strings.NewReader(string([]byte{3}))

	app.deps.defaultConfigPath = func() (string, error) {
		return configPath, nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run(nil, &stdout, &stderr)
	if exitCode != 130 {
		t.Fatalf("expected interrupt exit code 130, got %d", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
}

func TestRunWithoutArgsCtrlCInterruptsActionSelection(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()
	app.in = strings.NewReader("1" + string([]byte{3}))

	app.deps.defaultConfigPath = func() (string, error) {
		return configPath, nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run(nil, &stdout, &stderr)
	if exitCode != 130 {
		t.Fatalf("expected interrupt exit code 130, got %d", exitCode)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %q", stderr.String())
	}
	if !strings.Contains(stdout.String(), fmt.Sprintf("Select action for %s:", testTaskSourceName)) {
		t.Fatalf("expected action prompt before interrupt, stdout=%q", stdout.String())
	}
}

func TestInteractiveOutputTranslatesNewlinesInRawMode(t *testing.T) {
	var output strings.Builder

	writer := interactiveOutput(&output, true)
	if _, err := io.WriteString(writer, "line one\nline two\n"); err != nil {
		t.Fatalf("write interactive output: %v", err)
	}

	if output.String() != "line one\r\nline two\r\n" {
		t.Fatalf("unexpected translated output: %q", output.String())
	}
}

func TestPromptInteractiveSelectionAcceptsLetterKeysAfterNineOptions(t *testing.T) {
	labels := []string{"one", "two", "three", "four", "five", "six", "seven", "eight", "nine", "ten"}

	var stdout strings.Builder
	var stderr strings.Builder
	index, exitCode, ok := promptInteractiveSelection(
		interactiveInput{reader: bufio.NewReader(strings.NewReader("a"))},
		&stdout,
		&stderr,
		"Select option:",
		labels,
	)
	if !ok {
		t.Fatalf("expected success, got exit code %d stderr=%q", exitCode, stderr.String())
	}
	if index != 9 {
		t.Fatalf("expected index 9 for key a, got %d", index)
	}
}

func TestPromptInteractiveSelectionReservesQForQuit(t *testing.T) {
	labels := make([]string, 26)
	for i := range labels {
		labels[i] = fmt.Sprintf("option-%d", i)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	index, exitCode, ok := promptInteractiveSelection(
		interactiveInput{reader: bufio.NewReader(strings.NewReader("r"))},
		&stdout,
		&stderr,
		"Select option:",
		labels,
	)
	if !ok {
		t.Fatalf("expected success, got exit code %d stderr=%q", exitCode, stderr.String())
	}
	if index != 25 {
		t.Fatalf("expected index 25 for key r, got %d", index)
	}
	if strings.Contains(stdout.String(), "[q]") {
		t.Fatalf("q should be reserved for quit, stdout=%q", stdout.String())
	}
	if !strings.Contains(stdout.String(), "[r] option-25") {
		t.Fatalf("expected option 25 to use key r, stdout=%q", stdout.String())
	}
}

func TestPromptInteractiveSelectionRejectsTooManyOptions(t *testing.T) {
	labels := make([]string, len(interactiveSelectionKeys)+1)
	for i := range labels {
		labels[i] = fmt.Sprintf("option-%d", i)
	}

	var stdout strings.Builder
	var stderr strings.Builder
	_, exitCode, ok := promptInteractiveSelection(
		interactiveInput{reader: bufio.NewReader(strings.NewReader(""))},
		&stdout,
		&stderr,
		"Select option:",
		labels,
	)
	if ok {
		t.Fatal("expected selection to fail")
	}
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), fmt.Sprintf("supports at most %d options", len(interactiveSelectionKeys))) {
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
	if !strings.Contains(stdout.String(), "host-first interactive menu") || !strings.Contains(stdout.String(), "key shown in brackets") || !strings.Contains(stdout.String(), "With -r, local text is pushed") {
		t.Fatalf("unexpected help output: %q", stdout.String())
	}
}

func TestReverseLongFlagIsAccepted(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: testLocalClipboardText}, nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "--reverse", testTaskSourceName}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
}

func TestRunWithConfigButNoSourceRemainsNonInteractive(t *testing.T) {
	configPath := writeTestConfig(t)
	app := testApplication()
	app.in = strings.NewReader("1\n")

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath}, &stdout, &stderr)
	if exitCode != 2 {
		t.Fatalf("expected usage error, got exit code %d", exitCode)
	}
	if strings.Contains(stdout.String(), "Select clipboard sync action:") {
		t.Fatalf("expected no interactive menu, stdout=%q", stdout.String())
	}
	if !strings.Contains(stderr.String(), "Usage: crs") {
		t.Fatalf("expected usage output, stderr=%q", stderr.String())
	}
}

func TestRunReverseTaskModeAllowsMissingCaptureTaskName(t *testing.T) {
	configPath := writeReverseOnlyTaskConfig(t)
	app := testApplication()

	app.deps.localCapturePreferText = func() (protocol.CaptureEnvelope, error) {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: testLocalClipboardText}, nil
	}
	app.deps.remoteSetClipboardText = func(source remote.SourceOptions, text string) error {
		return nil
	}

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, "-r", testTaskSourceName}, &stdout, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}
}

func TestForwardTaskModeStillRequiresCaptureTaskName(t *testing.T) {
	configPath := writeReverseOnlyTaskConfig(t)
	app := testApplication()

	var stdout strings.Builder
	var stderr strings.Builder
	exitCode := app.run([]string{"--config", configPath, testTaskSourceName}, &stdout, &stderr)
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
	configJSON := fmt.Sprintf(`{
  "destination": { "name": %q },
  "sources": {
    %q: {
      "ssh_target": %q,
      "launch_mode": "task",
      "remote_bin": "crs.exe",
      "capture_task_name": "crs_capture",
      "set_text_task_name": "crs_set_clipboard_text"
    }
  }
}`, testDestinationName, testTaskSourceName, testTaskSSHTarget)
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

func writeReverseOnlyTaskConfig(t *testing.T) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{
  "destination": { "name": %q },
  "sources": {
    %q: {
      "ssh_target": %q,
      "launch_mode": "task",
      "remote_bin": "crs.exe",
      "set_text_task_name": "crs_set_clipboard_text"
    }
  }
}`, testDestinationName, testTaskSourceName, testTaskSSHTarget)
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

func writeMultiSourceConfig(t *testing.T) string {
	t.Helper()

	configPath := filepath.Join(t.TempDir(), "config.json")
	configJSON := fmt.Sprintf(`{
  "destination": { "name": %q },
  "sources": {
    %q: {
      "ssh_target": %q,
      "launch_mode": "task",
      "remote_bin": "crs.exe",
      "capture_task_name": "crs_capture",
      "set_text_task_name": "crs_set_clipboard_text"
    },
    %q: {
      "ssh_target": %q,
      "launch_mode": "direct",
      "remote_bin": "crs"
    }
  }
}`, testDestinationName, testTaskSourceName, testTaskSSHTarget, testDirectSourceName, testDirectSSHTarget)
	if err := os.WriteFile(configPath, []byte(configJSON), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}
