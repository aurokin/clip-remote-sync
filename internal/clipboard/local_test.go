package clipboard

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunPowerShellWithInputPipesSTDIN(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	stdinPath := filepath.Join(dir, "stdin.txt")
	argsPath := filepath.Join(dir, "args.txt")
	scriptPath := filepath.Join(dir, "fake-powershell.sh")

	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" > %q
cat > %q
`, argsPath, stdinPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake powershell script: %v", err)
	}

	command := func(name string, args ...string) *exec.Cmd {
		cmdArgs := append([]string{scriptPath, name}, args...)
		return exec.Command("sh", cmdArgs...)
	}

	if err := runPowerShellWithInput(command, "$text = [Console]::In.ReadToEnd(); Set-Clipboard -Value $text", "/tmp/clip/test.png"); err != nil {
		t.Fatalf("runPowerShellWithInput: %v", err)
	}

	stdinBytes, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read stdin log: %v", err)
	}
	if string(stdinBytes) != "/tmp/clip/test.png" {
		t.Fatalf("unexpected stdin payload: %q", string(stdinBytes))
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	if got := string(argsBytes); got != "powershell -NoProfile -NonInteractive -Command $text = [Console]::In.ReadToEnd(); Set-Clipboard -Value $text\n" {
		t.Fatalf("unexpected command args: %q", got)
	}
}

func TestRunDetachedXclipUsesTempFileAndCleansUp(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	payloadPath := filepath.Join(dir, "payload.txt")
	scriptPath := filepath.Join(dir, "fake-sh.sh")

	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" > %q
cat "$5" > %q
`, argsPath, payloadPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake shell script: %v", err)
	}

	command := func(name string, args ...string) *exec.Cmd {
		cmdArgs := append([]string{scriptPath, name}, args...)
		return exec.Command("sh", cmdArgs...)
	}

	if err := runDetachedXclip(command, "/tmp/clip/test.png", "clipboard"); err != nil {
		t.Fatalf("runDetachedXclip: %v", err)
	}

	payloadBytes, err := os.ReadFile(payloadPath)
	if err != nil {
		t.Fatalf("read payload log: %v", err)
	}
	if string(payloadBytes) != "/tmp/clip/test.png" {
		t.Fatalf("unexpected payload: %q", string(payloadBytes))
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	gotArgs := string(argsBytes)
	prefix := "sh -c xclip -selection clipboard -i < $1 >/dev/null 2>&1 & sh "
	if !strings.HasPrefix(gotArgs, prefix) {
		t.Fatalf("unexpected shell args: %q", gotArgs)
	}
	tempPath := strings.TrimSuffix(strings.TrimPrefix(gotArgs, prefix), "\n")
	if tempPath == "" {
		t.Fatal("expected temp payload path in shell args")
	}
	if _, err := os.Stat(tempPath); !os.IsNotExist(err) {
		t.Fatalf("expected temp payload file to be removed, stat err=%v", err)
	}
}

func TestRunDetachedXclipBytesAddsImageTarget(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args.txt")
	scriptPath := filepath.Join(dir, "fake-sh.sh")

	script := fmt.Sprintf(`#!/bin/sh
printf '%%s\n' "$*" > %q
`, argsPath)
	if err := os.WriteFile(scriptPath, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake shell script: %v", err)
	}

	command := func(name string, args ...string) *exec.Cmd {
		cmdArgs := append([]string{scriptPath, name}, args...)
		return exec.Command("sh", cmdArgs...)
	}

	if err := runDetachedXclipBytes(command, []byte("png"), "clipboard", "image/png"); err != nil {
		t.Fatalf("runDetachedXclipBytes: %v", err)
	}

	argsBytes, err := os.ReadFile(argsPath)
	if err != nil {
		t.Fatalf("read args log: %v", err)
	}
	if got := string(argsBytes); !strings.Contains(got, `xclip -selection clipboard -t image/png -i < $1 >/dev/null 2>&1 &`) {
		t.Fatalf("unexpected command args: %q", got)
	}
}

func TestBuildWindowsSetTextScriptClearsClipboardBeforeSettingText(t *testing.T) {
	t.Parallel()

	script := buildWindowsSetTextScript()
	if !strings.Contains(script, `System.Text.UTF8Encoding($false)`) {
		t.Fatalf("expected windows set-text script to use utf8 stdin decoding, got %s", script)
	}
	if !strings.Contains(script, `System.IO.StreamReader([Console]::OpenStandardInput(), $utf8NoBom)`) {
		t.Fatalf("expected windows set-text script to read stdin via stream reader, got %s", script)
	}
	if !strings.Contains(script, `[System.Windows.Forms.Clipboard]::Clear()`) {
		t.Fatalf("expected windows set-text script to clear clipboard first, got %s", script)
	}
	if !strings.Contains(script, `[System.Windows.Forms.Clipboard]::SetText($text)`) {
		t.Fatalf("expected windows set-text script to set text explicitly, got %s", script)
	}
}
