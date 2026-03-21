package app

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aurokin/clip-remote-sync/internal/protocol"
)

func TestClaimTaskRequestClaimsMatchingRequestFile(t *testing.T) {
	t.Parallel()

	bridgeDir := t.TempDir()
	requestDir := filepath.Join(bridgeDir, "requests")
	if err := os.MkdirAll(requestDir, 0o755); err != nil {
		t.Fatalf("mkdir request dir: %v", err)
	}

	capturePath := filepath.Join(requestDir, "capture-abc123.json")
	captureJSON := `{"request_id":"abc123","result_path":"C:\\ProgramData\\clip-remote-sync\\results\\capture-abc123.json"}`
	if err := os.WriteFile(capturePath, []byte(captureJSON), 0o644); err != nil {
		t.Fatalf("write capture request: %v", err)
	}
	ignoredPath := filepath.Join(requestDir, "set-text-def456.json")
	if err := os.WriteFile(ignoredPath, []byte(`{"request_id":"def456","result_path":"ignored"}`), 0o644); err != nil {
		t.Fatalf("write ignored request: %v", err)
	}

	var stderr bytes.Buffer
	workingPath, request, ok, exitCode := claimTaskRequest(bridgeDir, "capture-", &stderr)
	if !ok {
		t.Fatal("expected capture request to be claimed")
	}
	if exitCode != 0 {
		t.Fatalf("expected zero exit code, got %d", exitCode)
	}
	if request.RequestID != "abc123" {
		t.Fatalf("unexpected request id: %q", request.RequestID)
	}
	if !strings.HasSuffix(workingPath, ".working") {
		t.Fatalf("expected working path, got %q", workingPath)
	}
	if stderr.Len() != 0 {
		t.Fatalf("unexpected stderr: %s", stderr.String())
	}
	if _, err := os.Stat(capturePath); !os.IsNotExist(err) {
		t.Fatalf("expected original request to be moved, stat err=%v", err)
	}
	if _, err := os.Stat(workingPath); err != nil {
		t.Fatalf("expected working request file to exist: %v", err)
	}
}

func TestClaimTaskRequestReportsInvalidJSONAndCleansWorkingFile(t *testing.T) {
	t.Parallel()

	bridgeDir := t.TempDir()
	requestDir := filepath.Join(bridgeDir, "requests")
	if err := os.MkdirAll(requestDir, 0o755); err != nil {
		t.Fatalf("mkdir request dir: %v", err)
	}

	requestPath := filepath.Join(requestDir, "capture-bad.json")
	if err := os.WriteFile(requestPath, []byte(`{"request_id":`), 0o644); err != nil {
		t.Fatalf("write invalid request: %v", err)
	}

	var stderr bytes.Buffer
	workingPath, _, ok, exitCode := claimTaskRequest(bridgeDir, "capture-", &stderr)
	if ok {
		t.Fatal("expected invalid request to fail")
	}
	if exitCode != 1 {
		t.Fatalf("expected exit code 1, got %d", exitCode)
	}
	if !strings.Contains(stderr.String(), "Failed to parse request file") {
		t.Fatalf("expected parse error on stderr, got %q", stderr.String())
	}
	if !strings.HasSuffix(workingPath, ".working") {
		t.Fatalf("expected working path for failed claim, got %q", workingPath)
	}
	if _, err := os.Stat(workingPath); !os.IsNotExist(err) {
		t.Fatalf("expected broken working file to be cleaned up, stat err=%v", err)
	}
}

func TestValidateCaptureTaskRequestRejectsUnexpectedResultPath(t *testing.T) {
	t.Parallel()

	err := validateCaptureTaskRequest(`C:\ProgramData\clip-remote-sync`, protocolTaskRequest("abc123", "", `C:\temp\capture-abc123.json`))
	if err == nil {
		t.Fatal("expected capture task validation to fail")
	}
	if !strings.Contains(err.Error(), "invalid result_path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateSetTextTaskRequestRejectsUnexpectedInputPath(t *testing.T) {
	t.Parallel()

	err := validateSetTextTaskRequest(`C:\ProgramData\clip-remote-sync`, protocolTaskRequest("abc123", `C:\temp\set-text-abc123.txt`, `C:\ProgramData\clip-remote-sync\results\set-text-abc123.json`))
	if err == nil {
		t.Fatal("expected set-text task validation to fail")
	}
	if !strings.Contains(err.Error(), "invalid input_path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestInferTaskRequestIDPrefersScopedPath(t *testing.T) {
	t.Parallel()

	requestID := inferTaskRequestID("set-text-", `C:\ProgramData\clip-remote-sync\results\set-text-abc123.json`, `C:\ProgramData\clip-remote-sync\requests\set-text-def456.txt`)
	if requestID != "abc123" {
		t.Fatalf("expected request id abc123, got %q", requestID)
	}
}

func TestInferTaskRequestIDFallsBackForLegacyPaths(t *testing.T) {
	t.Parallel()

	requestID := inferTaskRequestID("set-text-", `C:\ProgramData\clip-remote-sync\set-text-ack.json`, `C:\ProgramData\clip-remote-sync\set-text-input.txt`)
	if requestID != "legacy-set-text-ack" {
		t.Fatalf("expected legacy fallback request id, got %q", requestID)
	}
}

func TestWindowsPathsEqualPreservesUNCPaths(t *testing.T) {
	t.Parallel()

	left := `\\server\share\clip-remote-sync\results\capture-abc123.json`
	right := `//server/share/clip-remote-sync/results/./capture-abc123.json`
	if !windowsPathsEqual(left, right) {
		t.Fatalf("expected UNC paths to compare equal: %q vs %q", left, right)
	}
}

func TestValidateSetTextTaskRequestAcceptsUNCBridgePath(t *testing.T) {
	t.Parallel()

	bridgeDir := `\\server\share\clip-remote-sync`
	err := validateSetTextTaskRequest(bridgeDir, protocolTaskRequest(
		"abc123",
		`\\server\share\clip-remote-sync\requests\set-text-abc123.txt`,
		`\\server\share\clip-remote-sync\results\set-text-abc123.json`,
	))
	if err != nil {
		t.Fatalf("expected UNC bridge path to validate, got %v", err)
	}
}

func TestValidateSetTextTaskRequestRejectsDriveRelativePath(t *testing.T) {
	t.Parallel()

	err := validateSetTextTaskRequest(`C:\ProgramData\clip-remote-sync`, protocolTaskRequest(
		"abc123",
		`C:ProgramData\clip-remote-sync\requests\set-text-abc123.txt`,
		`C:\ProgramData\clip-remote-sync\results\set-text-abc123.json`,
	))
	if err == nil {
		t.Fatal("expected drive-relative input path to be rejected")
	}
	if !strings.Contains(err.Error(), "invalid input_path") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWriteJSONAtomicReplacesExistingFile(t *testing.T) {
	t.Parallel()

	outputPath := filepath.Join(t.TempDir(), "result.json")
	if err := os.WriteFile(outputPath, []byte(`{"old":true}`), 0o644); err != nil {
		t.Fatalf("write existing file: %v", err)
	}

	var stderr bytes.Buffer
	exitCode := writeJSONAtomic(outputPath, map[string]bool{"new": true}, &stderr)
	if exitCode != 0 {
		t.Fatalf("expected success, got exit code %d, stderr=%q", exitCode, stderr.String())
	}

	outputBytes, err := os.ReadFile(outputPath)
	if err != nil {
		t.Fatalf("read output file: %v", err)
	}
	if !strings.Contains(string(outputBytes), `"new":true`) {
		t.Fatalf("expected new payload, got %q", string(outputBytes))
	}
}

func protocolTaskRequest(requestID, inputPath, resultPath string) protocol.TaskRequest {
	return protocol.TaskRequest{RequestID: requestID, InputPath: inputPath, ResultPath: resultPath}
}
