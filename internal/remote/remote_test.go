package remote

import (
	"encoding/base64"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"

	"github.com/aurokin/clip-remote-sync/internal/protocol"
)

type fakeCommandStep struct {
	stdout   string
	stderr   string
	exitCode int
}

func TestParseCaptureImagePayload(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"kind":"image","image_png_base64":"` + base64.StdEncoding.EncodeToString([]byte("png-bytes")) + `"}`)
	captured, err := parseCapture(payload)
	if err != nil {
		t.Fatalf("parseCapture: %v", err)
	}
	if captured.Kind != protocol.KindImage {
		t.Fatalf("expected image kind, got %q", captured.Kind)
	}
	if string(captured.ImagePNG) != "png-bytes" {
		t.Fatalf("unexpected image payload: %q", string(captured.ImagePNG))
	}
}

func TestBridgePathsDefaults(t *testing.T) {
	t.Parallel()

	paths := bridgePaths(SourceOptions{})
	if paths.rootDir != `C:\ProgramData\clip-remote-sync` {
		t.Fatalf("unexpected root dir: %q", paths.rootDir)
	}
	if paths.requestDir != `C:\ProgramData\clip-remote-sync\requests` {
		t.Fatalf("unexpected request dir: %q", paths.requestDir)
	}
	if paths.resultDir != `C:\ProgramData\clip-remote-sync\results` {
		t.Fatalf("unexpected result dir: %q", paths.resultDir)
	}

	requestID := "abc123"
	if got := paths.captureRequestPath(requestID); got != `C:\ProgramData\clip-remote-sync\requests\capture-abc123.json` {
		t.Fatalf("unexpected capture request path: %q", got)
	}
	if got := paths.captureResultPath(requestID); got != `C:\ProgramData\clip-remote-sync\results\capture-abc123.json` {
		t.Fatalf("unexpected capture result path: %q", got)
	}
	if got := paths.setTextRequestPath(requestID); got != `C:\ProgramData\clip-remote-sync\requests\set-text-abc123.json` {
		t.Fatalf("unexpected set-text request path: %q", got)
	}
	if got := paths.setTextInputPath(requestID); got != `C:\ProgramData\clip-remote-sync\requests\set-text-abc123.txt` {
		t.Fatalf("unexpected set-text input path: %q", got)
	}
	if got := paths.setTextResultPath(requestID); got != `C:\ProgramData\clip-remote-sync\results\set-text-abc123.json` {
		t.Fatalf("unexpected set-text result path: %q", got)
	}
}

func TestWaitForRemoteJSONFileIgnoresPartialJSONUntilValid(t *testing.T) {
	command, _ := scriptedCommand(t, []fakeCommandStep{
		{stdout: `{"request_id":`, exitCode: 0},
		{stdout: `{"request_id":"abc123","ok":true}`, exitCode: 0},
	})

	result, err := waitForRemoteJSONFile(command, "ignored", `C:\ProgramData\clip-remote-sync\results\capture-abc123.json`, 700*time.Millisecond)
	if err != nil {
		t.Fatalf("waitForRemoteJSONFile: %v", err)
	}
	if strings.TrimSpace(result) != `{"request_id":"abc123","ok":true}` {
		t.Fatalf("unexpected result payload: %q", result)
	}
}

func TestWaitForRemoteJSONFileReturnsIncompleteJSONErrorAfterTimeout(t *testing.T) {
	command, _ := scriptedCommand(t, []fakeCommandStep{{stdout: `{"request_id":`, exitCode: 0}})

	_, err := waitForRemoteJSONFile(command, "ignored", `C:\ProgramData\clip-remote-sync\results\capture-abc123.json`, 350*time.Millisecond)
	if err == nil {
		t.Fatal("expected incomplete JSON error")
	}
	if !strings.Contains(err.Error(), "JSON is incomplete") {
		t.Fatalf("expected incomplete JSON error, got %v", err)
	}
}

func TestWaitForCaptureResultRejectsRequestIDMismatch(t *testing.T) {
	command, _ := scriptedCommand(t, []fakeCommandStep{{
		stdout:   `{"request_id":"wrong","ok":true,"capture":{"kind":"text","text":"hello"}}`,
		exitCode: 0,
	}})

	_, err := waitForCaptureResult(command, "ignored", `C:\ProgramData\clip-remote-sync\results\capture-abc123.json`, "abc123", 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected request id mismatch error")
	}
	if !strings.Contains(err.Error(), `got "wrong" want "abc123"`) {
		t.Fatalf("unexpected mismatch error: %v", err)
	}
}

func TestWaitForSetTextResultRejectsRequestIDMismatch(t *testing.T) {
	command, _ := scriptedCommand(t, []fakeCommandStep{{
		stdout:   `{"request_id":"wrong","ok":true}`,
		exitCode: 0,
	}})

	_, err := waitForSetTextResult(command, "ignored", `C:\ProgramData\clip-remote-sync\results\set-text-abc123.json`, "abc123", 200*time.Millisecond)
	if err == nil {
		t.Fatal("expected request id mismatch error")
	}
	if !strings.Contains(err.Error(), `got "wrong" want "abc123"`) {
		t.Fatalf("unexpected mismatch error: %v", err)
	}
}

func TestCaptureDirectReturnsText(t *testing.T) {
	t.Parallel()

	command, _ := scriptedCommand(t, []fakeCommandStep{{
		stdout:   `{"kind":"text","text":"hello"}`,
		exitCode: 0,
	}})

	captured, err := Capture(command, SourceOptions{SSHTarget: "ignored", RemoteBin: "crs", LaunchMode: "direct"})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}
	if captured.Kind != protocol.KindText || captured.Text != "hello" {
		t.Fatalf("unexpected capture: %#v", captured)
	}
}

func TestSetClipboardTextDirectWritesSTDIN(t *testing.T) {
	command, stdinPath := stdinCommand(t, []fakeCommandStep{{exitCode: 0}})

	if err := SetClipboardText(command, SourceOptions{SSHTarget: "ignored", RemoteBin: "crs", LaunchMode: "direct"}, "/tmp/clip/test.png"); err != nil {
		t.Fatalf("SetClipboardText: %v", err)
	}

	stdinBytes, err := os.ReadFile(stdinPath)
	if err != nil {
		t.Fatalf("read stdin log: %v", err)
	}
	if string(stdinBytes) != "/tmp/clip/test.png" {
		t.Fatalf("unexpected stdin payload: %q", string(stdinBytes))
	}
}

func TestCaptureDirectQuotesPOSIXRemoteBinWithSpaces(t *testing.T) {
	command, logPath := scriptedCommand(t, []fakeCommandStep{{
		stdout:   `{"kind":"text","text":"hello"}`,
		exitCode: 0,
	}})

	_, err := Capture(command, SourceOptions{SSHTarget: "ignored", RemoteBin: "/opt/clip remote/crs", LaunchMode: "direct"})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	if !strings.Contains(string(logBytes), `'/opt/clip remote/crs' '_capture'`) {
		t.Fatalf("expected quoted POSIX remote command, got %s", string(logBytes))
	}
}

func TestCaptureDirectUsesPowerShellForWindowsRemoteBinWithSpaces(t *testing.T) {
	command, logPath := scriptedCommand(t, []fakeCommandStep{{
		stdout:   `{"kind":"text","text":"hello"}`,
		exitCode: 0,
	}})

	_, err := Capture(command, SourceOptions{SSHTarget: "ignored", RemoteBin: `C:\\Program Files\\clip-remote-sync\\crs.exe`, LaunchMode: "direct"})
	if err != nil {
		t.Fatalf("Capture: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "powershell -NoProfile -NonInteractive -EncodedCommand") {
		t.Fatalf("expected powershell direct invocation, got %s", log)
	}
	decoded := decodeLoggedEncodedCommand(t, log)
	if !strings.Contains(decoded, `& 'C:\\Program Files\\clip-remote-sync\\crs.exe' _capture`) {
		t.Fatalf("expected quoted windows remote bin in script, got %s", decoded)
	}
}

func TestSetClipboardTextDirectUsesPowerShellForWindowsRemoteBinWithSpaces(t *testing.T) {
	command, logPath := scriptedCommand(t, []fakeCommandStep{{exitCode: 0}})

	if err := SetClipboardText(command, SourceOptions{SSHTarget: "ignored", RemoteBin: `C:\\Program Files\\clip-remote-sync\\crs.exe`, LaunchMode: "direct"}, "/tmp/clip/test.png"); err != nil {
		t.Fatalf("SetClipboardText: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, "powershell -NoProfile -NonInteractive -EncodedCommand") {
		t.Fatalf("expected powershell direct invocation, got %s", log)
	}
	decoded := decodeLoggedEncodedCommand(t, log)
	if !strings.Contains(decoded, `$psi.FileName='C:\\Program Files\\clip-remote-sync\\crs.exe'`) {
		t.Fatalf("expected windows direct script to quote remote bin, got %s", decoded)
	}
}

func TestCaptureViaTaskSuccess(t *testing.T) {
	command, logPath := scriptedCommand(t, []fakeCommandStep{
		{exitCode: 0},
		{exitCode: 0},
		{exitCode: 0},
		{stdout: `{"request_id":"abc123","ok":true,"capture":{"kind":"text","text":"hello"}}`, exitCode: 0},
		{exitCode: 0},
	})

	originalRequestID := newRequestIDFunc
	newRequestIDFunc = func() (string, error) { return "abc123", nil }
	defer func() { newRequestIDFunc = originalRequestID }()

	captured, err := captureViaTask(command, SourceOptions{SSHTarget: "ignored", LaunchMode: "task", CaptureTaskName: "crs_capture"})
	if err != nil {
		t.Fatalf("captureViaTask: %v", err)
	}
	if captured.Kind != protocol.KindText || captured.Text != "hello" {
		t.Fatalf("unexpected capture: %#v", captured)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, `schtasks /Run /TN crs_capture`) {
		t.Fatalf("expected capture task launch in log, got %s", log)
	}
	if !logContainsEncodedScript(log, `Remove-Item -LiteralPath 'C:\ProgramData\clip-remote-sync\results\capture-abc123.json'`) {
		t.Fatalf("expected capture result cleanup in log, got %s", log)
	}
}

func TestSetClipboardTextViaTaskSuccess(t *testing.T) {
	command, logPath := scriptedCommand(t, []fakeCommandStep{
		{exitCode: 0},
		{exitCode: 0},
		{exitCode: 0},
		{exitCode: 0},
		{stdout: `{"request_id":"abc123","ok":true}`, exitCode: 0},
		{exitCode: 0},
	})

	originalRequestID := newRequestIDFunc
	newRequestIDFunc = func() (string, error) { return "abc123", nil }
	defer func() { newRequestIDFunc = originalRequestID }()

	if err := setClipboardTextViaTask(command, SourceOptions{SSHTarget: "ignored", LaunchMode: "task", SetTextTaskName: "crs_set_text"}, "/tmp/clip/test.png"); err != nil {
		t.Fatalf("setClipboardTextViaTask: %v", err)
	}

	logBytes, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read command log: %v", err)
	}
	log := string(logBytes)
	if !strings.Contains(log, `schtasks /Run /TN crs_set_text`) {
		t.Fatalf("expected set-text task launch in log, got %s", log)
	}
	if !logContainsEncodedScript(log, `Remove-Item -LiteralPath 'C:\ProgramData\clip-remote-sync\results\set-text-abc123.json'`) {
		t.Fatalf("expected set-text result cleanup in log, got %s", log)
	}
}

func TestCaptureViaTaskCleansQueuedArtifactsOnTimeout(t *testing.T) {
	originalTimeout := taskResultTimeout
	originalPollInterval := taskPollInterval
	taskResultTimeout = 40 * time.Millisecond
	taskPollInterval = 10 * time.Millisecond
	defer func() {
		taskResultTimeout = originalTimeout
		taskPollInterval = originalPollInterval
	}()

	command, logPath := scriptedCommand(t, []fakeCommandStep{
		{exitCode: 0},
		{exitCode: 0},
		{exitCode: 0},
		{stderr: "missing", exitCode: 3},
	})

	_, err := captureViaTask(command, SourceOptions{SSHTarget: "ignored", LaunchMode: "task", CaptureTaskName: "crs_capture"})
	if err == nil {
		t.Fatal("expected captureViaTask to fail")
	}
	if !strings.Contains(err.Error(), "wait for capture result") {
		t.Fatalf("unexpected error: %v", err)
	}

	logBytes, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read command log: %v", readErr)
	}
	log := string(logBytes)
	if !logContainsEncodedScript(log, `Remove-Item -LiteralPath 'C:\ProgramData\clip-remote-sync\requests\capture-`) {
		t.Fatalf("expected capture request cleanup in log, got %s", log)
	}
	if !logContainsEncodedScript(log, `Remove-Item -LiteralPath 'C:\ProgramData\clip-remote-sync\results\capture-`) {
		t.Fatalf("expected capture result cleanup in log, got %s", log)
	}
}

func TestSetClipboardTextViaTaskCleansQueuedArtifactsOnLaunchFailure(t *testing.T) {
	command, logPath := scriptedCommand(t, []fakeCommandStep{
		{exitCode: 0},
		{exitCode: 0},
		{exitCode: 0},
		{stderr: "task failed", exitCode: 1},
		{exitCode: 0},
		{exitCode: 0},
		{exitCode: 0},
	})

	err := setClipboardTextViaTask(command, SourceOptions{SSHTarget: "ignored", LaunchMode: "task", SetTextTaskName: "crs_set_text"}, "/tmp/clip/test.png")
	if err == nil {
		t.Fatal("expected setClipboardTextViaTask to fail")
	}
	if !strings.Contains(err.Error(), "run set-text task") {
		t.Fatalf("unexpected error: %v", err)
	}

	logBytes, readErr := os.ReadFile(logPath)
	if readErr != nil {
		t.Fatalf("read command log: %v", readErr)
	}
	log := string(logBytes)
	if !logContainsEncodedScript(log, `Remove-Item -LiteralPath 'C:\ProgramData\clip-remote-sync\requests\set-text-`) || !logContainsEncodedScript(log, `.txt'`) {
		t.Fatalf("expected set-text input cleanup in log, got %s", log)
	}
	if !logContainsEncodedScript(log, `Remove-Item -LiteralPath 'C:\ProgramData\clip-remote-sync\requests\set-text-`) || !logContainsEncodedScript(log, `.json'`) {
		t.Fatalf("expected set-text request cleanup in log, got %s", log)
	}
	if !logContainsEncodedScript(log, `Remove-Item -LiteralPath 'C:\ProgramData\clip-remote-sync\results\set-text-`) {
		t.Fatalf("expected set-text result cleanup in log, got %s", log)
	}
}

func TestEscapeSingleQuotes(t *testing.T) {
	t.Parallel()

	got := escapeSingleQuotes(`C:\it's\clip`)
	want := `C:\it''s\clip`
	if got != want {
		t.Fatalf("expected %q, got %q", want, got)
	}
}

func TestBuildEncodedPowerShellCommand(t *testing.T) {
	t.Parallel()

	command := buildEncodedPowerShellCommand("Write-Output 'hello'")
	if !strings.HasPrefix(command, "powershell -NoProfile -NonInteractive -EncodedCommand ") {
		t.Fatalf("unexpected encoded powershell command: %s", command)
	}
	decoded, err := decodeEncodedPowerShellCommand(strings.TrimPrefix(command, "powershell -NoProfile -NonInteractive -EncodedCommand "))
	if err != nil {
		t.Fatalf("decodeEncodedPowerShellCommand: %v", err)
	}
	if !strings.HasPrefix(decoded, "$ProgressPreference = 'SilentlyContinue'; ") {
		t.Fatalf("expected encoded powershell command to silence progress, got %s", decoded)
	}
}

func TestWriteUTF8FileScriptContainsBase64Payload(t *testing.T) {
	t.Parallel()

	script := writeUTF8FileScript(`C:\ProgramData\clip-remote-sync\requests\set-text-abc123.txt`, "hello world")
	encoded := base64.StdEncoding.EncodeToString([]byte("hello world"))
	if !strings.Contains(script, encoded) {
		t.Fatalf("script missing base64 payload: %s", script)
	}
	if !strings.Contains(script, "WriteAllText") {
		t.Fatalf("script missing WriteAllText call: %s", script)
	}
}

func logContainsEncodedScript(log, needle string) bool {
	for _, decoded := range decodeLoggedEncodedCommands(log) {
		if strings.Contains(decoded, needle) {
			return true
		}
	}
	return false
}

func decodeLoggedEncodedCommand(t *testing.T, log string) string {
	t.Helper()

	decoded := decodeLoggedEncodedCommands(log)
	if len(decoded) == 0 {
		t.Fatalf("expected encoded PowerShell command in log, got %s", log)
	}
	return decoded[len(decoded)-1]
}

func decodeLoggedEncodedCommands(log string) []string {
	lines := strings.Split(log, "\n")
	decoded := make([]string, 0, len(lines))
	for _, line := range lines {
		idx := strings.Index(line, " -EncodedCommand ")
		if idx == -1 {
			continue
		}
		encoded := strings.TrimSpace(line[idx+len(" -EncodedCommand "):])
		script, err := decodeEncodedPowerShellCommand(encoded)
		if err == nil {
			decoded = append(decoded, script)
		}
	}
	return decoded
}

func decodeEncodedPowerShellCommand(encoded string) (string, error) {
	raw, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		return "", err
	}
	if len(raw)%2 != 0 {
		return "", fmt.Errorf("odd utf16 length")
	}
	units := make([]uint16, 0, len(raw)/2)
	for idx := 0; idx < len(raw); idx += 2 {
		units = append(units, uint16(raw[idx])|uint16(raw[idx+1])<<8)
	}
	return string(utf16.Decode(units)), nil
}

func scriptedCommand(t *testing.T, steps []fakeCommandStep) (commandFunc, string) {
	t.Helper()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.txt")
	logPath := filepath.Join(dir, "commands.log")
	scriptPath := filepath.Join(dir, "fake-ssh.sh")

	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	script.WriteString(fmt.Sprintf("state_path=%q\n", statePath))
	script.WriteString(fmt.Sprintf("log_path=%q\n", logPath))
	script.WriteString("printf '%s\n' \"$*\" >> \"$log_path\"\n")
	script.WriteString("count=0\n")
	script.WriteString("if [ -f \"$state_path\" ]; then count=$(cat \"$state_path\"); fi\n")
	script.WriteString("count=$((count + 1))\n")
	script.WriteString("printf '%s' \"$count\" > \"$state_path\"\n")
	script.WriteString("case \"$count\" in\n")
	for index, step := range steps {
		writeCommandCase(&script, index+1, step)
	}
	writeCommandCase(&script, -1, steps[len(steps)-1])
	script.WriteString("esac\n")

	if err := os.WriteFile(scriptPath, []byte(script.String()), 0o755); err != nil {
		t.Fatalf("write fake ssh script: %v", err)
	}

	return func(name string, args ...string) *exec.Cmd {
		cmdArgs := append([]string{scriptPath, name}, args...)
		return exec.Command("/bin/sh", cmdArgs...)
	}, logPath
}

func stdinCommand(t *testing.T, steps []fakeCommandStep) (commandFunc, string) {
	t.Helper()

	dir := t.TempDir()
	statePath := filepath.Join(dir, "state.txt")
	stdinPath := filepath.Join(dir, "stdin.log")
	scriptPath := filepath.Join(dir, "fake-ssh.sh")

	var script strings.Builder
	script.WriteString("#!/bin/sh\n")
	script.WriteString(fmt.Sprintf("state_path=%q\n", statePath))
	script.WriteString(fmt.Sprintf("stdin_path=%q\n", stdinPath))
	script.WriteString("cat > \"$stdin_path\"\n")
	script.WriteString("count=0\n")
	script.WriteString("if [ -f \"$state_path\" ]; then count=$(cat \"$state_path\"); fi\n")
	script.WriteString("count=$((count + 1))\n")
	script.WriteString("printf '%s' \"$count\" > \"$state_path\"\n")
	script.WriteString("case \"$count\" in\n")
	for index, step := range steps {
		writeCommandCase(&script, index+1, step)
	}
	writeCommandCase(&script, -1, steps[len(steps)-1])
	script.WriteString("esac\n")

	if err := os.WriteFile(scriptPath, []byte(script.String()), 0o755); err != nil {
		t.Fatalf("write fake ssh script: %v", err)
	}

	return func(name string, args ...string) *exec.Cmd {
		cmdArgs := append([]string{scriptPath, name}, args...)
		return exec.Command("/bin/sh", cmdArgs...)
	}, stdinPath
}

func writeCommandCase(script *strings.Builder, index int, step fakeCommandStep) {
	label := "*"
	if index > 0 {
		label = fmt.Sprintf("%d", index)
	}

	script.WriteString(label + ")\n")
	if step.stdout != "" {
		script.WriteString("cat <<'__CRS_STDOUT__'\n")
		script.WriteString(step.stdout)
		script.WriteString("\n__CRS_STDOUT__\n")
	}
	if step.stderr != "" {
		script.WriteString("cat 1>&2 <<'__CRS_STDERR__'\n")
		script.WriteString(step.stderr)
		script.WriteString("\n__CRS_STDERR__\n")
	}
	fmt.Fprintf(script, "exit %d\n", step.exitCode)
	script.WriteString(";;\n")
}
