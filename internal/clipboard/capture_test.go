package clipboard

import (
	"encoding/base64"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"unicode/utf16"

	"github.com/aurokin/clip-remote-sync/internal/protocol"
)

func TestNormalizeCapturedTextTrimsTrailingLineEndings(t *testing.T) {
	t.Parallel()

	got, ok := normalizeCapturedText("hello world\r\n\n")
	if !ok {
		t.Fatal("expected normalized text")
	}
	if got != "hello world" {
		t.Fatalf("expected trimmed text, got %q", got)
	}
}

func TestNormalizeCapturedTextRejectsWhitespaceOnly(t *testing.T) {
	t.Parallel()

	if _, ok := normalizeCapturedText(" \t\r\n"); ok {
		t.Fatal("expected whitespace-only clipboard text to be rejected")
	}
}

func TestNormalizeCapturedTextPreservesInteriorNewlines(t *testing.T) {
	t.Parallel()

	got, ok := normalizeCapturedText("first line\nsecond line\r\n")
	if !ok {
		t.Fatal("expected normalized text")
	}
	if got != "first line\nsecond line" {
		t.Fatalf("expected interior newline to be preserved, got %q", got)
	}
}

func TestBuildWindowsImageCaptureScriptSeparatesStatements(t *testing.T) {
	t.Parallel()

	script := buildWindowsImageCaptureScript()
	if !strings.Contains(script, "\nif ($null -eq $img)") {
		t.Fatalf("expected Get-Clipboard assignment to be followed by a statement separator, got %s", script)
	}
	if !strings.Contains(script, "\n[Console]::Out.Write(") {
		t.Fatalf("expected image write call to be on its own statement, got %s", script)
	}
}

func TestEncodePowerShellScriptRoundTripsUnicode(t *testing.T) {
	t.Parallel()

	script := "$text = 'héllo 👋 你好'"
	encoded := encodePowerShellScript(script)
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatalf("DecodeString: %v", err)
	}
	if len(raw)%2 != 0 {
		t.Fatalf("expected utf16 byte length, got %d", len(raw))
	}
	units := make([]uint16, 0, len(raw)/2)
	for i := 0; i < len(raw); i += 2 {
		units = append(units, uint16(raw[i])|uint16(raw[i+1])<<8)
	}
	if got := string(utf16.Decode(units)); got != script {
		t.Fatalf("unexpected decoded script: %q", got)
	}
}

func TestCapturePreferTextPrefersTextWhenBothPresent(t *testing.T) {
	t.Parallel()

	var order []string
	got, err := capturePreferText(
		func() (string, bool, error) {
			order = append(order, "text")
			return "hello", true, nil
		},
		func() ([]byte, bool, error) {
			order = append(order, "image")
			return []byte("png"), true, nil
		},
	)
	if err != nil {
		t.Fatalf("capturePreferText: %v", err)
	}
	if got.Kind != protocol.KindText || got.Text != "hello" {
		t.Fatalf("unexpected capture: %#v", got)
	}
	if strings.Join(order, ",") != "text" {
		t.Fatalf("unexpected read order: %v", order)
	}
}

func TestCapturePreferTextFallsBackToImage(t *testing.T) {
	t.Parallel()

	var order []string
	got, err := capturePreferText(
		func() (string, bool, error) {
			order = append(order, "text")
			return "", false, nil
		},
		func() ([]byte, bool, error) {
			order = append(order, "image")
			return []byte("png"), true, nil
		},
	)
	if err != nil {
		t.Fatalf("capturePreferText: %v", err)
	}
	if got.Kind != protocol.KindImage || got.ImagePNGBase64 == "" {
		t.Fatalf("unexpected capture: %#v", got)
	}
	if strings.Join(order, ",") != "text,image" {
		t.Fatalf("unexpected read order: %v", order)
	}
}

func TestCapturePreferTextPropagatesImageErrorsAfterTextMiss(t *testing.T) {
	t.Parallel()

	_, err := capturePreferText(
		func() (string, bool, error) {
			return "", false, nil
		},
		func() ([]byte, bool, error) {
			return nil, false, errors.New("image failed")
		},
	)
	if err == nil || !strings.Contains(err.Error(), "image failed") {
		t.Fatalf("expected image failure, got %v", err)
	}
}

func TestCapturePreferImagePrefersImageWhenBothPresent(t *testing.T) {
	t.Parallel()

	var order []string
	got, err := capturePreferImage(
		func() (string, bool, error) {
			order = append(order, "text")
			return "hello", true, nil
		},
		func() ([]byte, bool, error) {
			order = append(order, "image")
			return []byte("png"), true, nil
		},
	)
	if err != nil {
		t.Fatalf("capturePreferImage: %v", err)
	}
	if got.Kind != protocol.KindImage || got.ImagePNGBase64 == "" {
		t.Fatalf("unexpected capture: %#v", got)
	}
	if strings.Join(order, ",") != "image" {
		t.Fatalf("unexpected read order: %v", order)
	}
}

func TestCapturePreferImageFallsBackToText(t *testing.T) {
	t.Parallel()

	var order []string
	got, err := capturePreferImage(
		func() (string, bool, error) {
			order = append(order, "text")
			return "hello", true, nil
		},
		func() ([]byte, bool, error) {
			order = append(order, "image")
			return nil, false, nil
		},
	)
	if err != nil {
		t.Fatalf("capturePreferImage: %v", err)
	}
	if got.Kind != protocol.KindText || got.Text != "hello" {
		t.Fatalf("unexpected capture: %#v", got)
	}
	if strings.Join(order, ",") != "image,text" {
		t.Fatalf("unexpected read order: %v", order)
	}
}

func TestReadXclipClipboardTextUsesExplicitTextTargets(t *testing.T) {
	logPath := installFakeXclip(t, `#!/bin/sh
printf '%s\n' "$*" >>"$XCLIP_LOG"
case "$*" in
  "-selection clipboard -t TARGETS -o")
    printf 'TARGETS\nUTF8_STRING\n'
    ;;
  "-selection clipboard -t UTF8_STRING -o")
    printf 'hello world\n'
    ;;
  *)
    exit 1
    ;;
esac
`)

	got, ok := readXclipClipboardText(execCommand)
	if !ok {
		t.Fatal("expected xclip text capture to succeed")
	}
	if got != "hello world" {
		t.Fatalf("unexpected captured text: %q", got)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if !strings.Contains(string(logBytes), "-selection clipboard -t UTF8_STRING -o") {
		t.Fatalf("expected explicit UTF8_STRING target probe, got %s", string(logBytes))
	}
	if strings.Contains(string(logBytes), "-selection clipboard -o") {
		t.Fatalf("did not expect fallback to raw xclip -o, got %s", string(logBytes))
	}
}

func TestReadXclipClipboardTextRejectsImageOnlyClipboard(t *testing.T) {
	logPath := installFakeXclip(t, `#!/bin/sh
printf '%s\n' "$*" >>"$XCLIP_LOG"
case "$*" in
  "-selection clipboard -t TARGETS -o")
    printf 'TARGETS\nimage/png\n'
    ;;
  "-selection clipboard -o")
    printf '\211PNG\r\n\032\n'
    ;;
  *)
    exit 1
    ;;
esac
`)

	got, ok := readXclipClipboardText(execCommand)
	if ok {
		t.Fatalf("expected image-only clipboard to be rejected, got %q", got)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read log: %v", err)
	}
	if strings.Contains(string(logBytes), "-selection clipboard -o") {
		t.Fatalf("did not expect raw xclip -o fallback, got %s", string(logBytes))
	}
}

func installFakeXclip(t *testing.T, script string) string {
	t.Helper()

	dir := t.TempDir()
	logPath := filepath.Join(dir, "xclip.log")
	scriptPath := filepath.Join(dir, "xclip")
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake xclip: %v", err)
	}

	t.Setenv("XCLIP_LOG", logPath)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func execCommand(name string, arg ...string) *exec.Cmd {
	return exec.Command(name, arg...)
}
