package app

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strings"

	"github.com/aurokin/clip-remote-sync/internal/clipboard"
	"github.com/aurokin/clip-remote-sync/internal/protocol"
)

func runInternal(args []string, stdout io.Writer, stderr io.Writer) int {
	switch args[0] {
	case "_capture":
		return captureToWriter(stdout, stderr)
	case "_capture-file":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "Usage: crs _capture-file <output-path>")
			return 2
		}
		return captureToFile(args[1], stderr)
	case "_capture-task-runner":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "Usage: crs _capture-task-runner <bridge-dir>")
			return 2
		}
		return runCaptureTask(args[1], stderr)
	case "_set-clipboard-text":
		textBytes, err := readClipboardTextInput(os.Stdin)
		if err != nil {
			fmt.Fprintf(stderr, "Failed to read clipboard text from stdin: %v\n", err)
			return 1
		}
		if err := clipboard.SetLocalText(exec.Command, string(textBytes)); err != nil {
			fmt.Fprintf(stderr, "Failed to set local clipboard text: %v\n", err)
			return 1
		}
		return 0
	case "_set-clipboard-text-file":
		if len(args) != 3 {
			fmt.Fprintln(stderr, "Usage: crs _set-clipboard-text-file <input-path> <ack-path>")
			return 2
		}
		return setClipboardTextFromFile(args[1], args[2], stderr)
	case "_set-clipboard-text-task-runner":
		if len(args) != 2 {
			fmt.Fprintln(stderr, "Usage: crs _set-clipboard-text-task-runner <bridge-dir>")
			return 2
		}
		return runSetClipboardTask(args[1], stderr)
	default:
		fmt.Fprintf(stderr, "Unknown internal command %q\n", args[0])
		return 2
	}
}

func readClipboardTextInput(r io.Reader) ([]byte, error) {
	return io.ReadAll(r)
}

func captureToWriter(stdout io.Writer, stderr io.Writer) int {
	envelope, err := clipboard.CaptureLocal(exec.Command)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to capture local clipboard: %v\n", err)
		return 1
	}
	if err := json.NewEncoder(stdout).Encode(envelope); err != nil {
		fmt.Fprintf(stderr, "Failed to encode capture payload: %v\n", err)
		return 1
	}
	return 0
}

func captureToFile(outputPath string, stderr io.Writer) int {
	envelope, err := clipboard.CaptureLocal(exec.Command)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to capture local clipboard: %v\n", err)
		return 1
	}
	return writeJSONAtomic(outputPath, envelope, stderr)
}

func setClipboardTextFromFile(inputPath, ackPath string, stderr io.Writer) int {
	textBytes, err := os.ReadFile(inputPath)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to read clipboard text file: %v\n", err)
		return 1
	}
	if err := clipboard.SetLocalText(exec.Command, string(textBytes)); err != nil {
		fmt.Fprintf(stderr, "Failed to set local clipboard text: %v\n", err)
		return 1
	}
	result := protocol.SetClipboardTaskResult{RequestID: inferTaskRequestID("set-text-", ackPath, inputPath), OK: true}
	return writeJSONAtomic(ackPath, result, stderr)
}

func runCaptureTask(bridgeDir string, stderr io.Writer) int {
	requestPath, request, ok, exitCode := claimTaskRequest(bridgeDir, "capture-", stderr)
	if !ok {
		return exitCode
	}
	defer cleanupWorkingRequest(requestPath)

	if err := validateCaptureTaskRequest(bridgeDir, request); err != nil {
		return writeCaptureTaskFailure(bridgeDir, request.RequestID, err, stderr)
	}

	envelope, err := clipboard.CaptureLocal(exec.Command)
	result := protocol.CaptureTaskResult{RequestID: request.RequestID}
	if err != nil {
		result.OK = false
		result.Error = err.Error()
	} else {
		result.OK = true
		result.Capture = &envelope
	}

	return writeJSONAtomic(expectedCaptureResultPath(bridgeDir, request.RequestID), result, stderr)
}

func runSetClipboardTask(bridgeDir string, stderr io.Writer) int {
	requestPath, request, ok, exitCode := claimTaskRequest(bridgeDir, "set-text-", stderr)
	if !ok {
		return exitCode
	}
	defer cleanupWorkingRequest(requestPath)

	if err := validateSetTextTaskRequest(bridgeDir, request); err != nil {
		return writeSetClipboardTaskFailure(bridgeDir, request.RequestID, err, stderr)
	}
	defer cleanupTaskInput(request.InputPath)

	result := protocol.SetClipboardTaskResult{RequestID: request.RequestID}
	textBytes, err := os.ReadFile(request.InputPath)
	if err != nil {
		result.OK = false
		result.Error = fmt.Sprintf("read set-text input: %v", err)
		return writeJSONAtomic(expectedSetTextResultPath(bridgeDir, request.RequestID), result, stderr)
	}
	if err := clipboard.SetLocalText(exec.Command, string(textBytes)); err != nil {
		result.OK = false
		result.Error = err.Error()
		return writeJSONAtomic(expectedSetTextResultPath(bridgeDir, request.RequestID), result, stderr)
	}
	result.OK = true
	return writeJSONAtomic(expectedSetTextResultPath(bridgeDir, request.RequestID), result, stderr)
}

func claimTaskRequest(bridgeDir, prefix string, stderr io.Writer) (string, protocol.TaskRequest, bool, int) {
	requestDir := bridgeRequestDir(bridgeDir)
	entries, err := os.ReadDir(requestDir)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to read request directory: %v\n", err)
		return "", protocol.TaskRequest{}, false, 1
	}

	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		name := entry.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".json") {
			continue
		}
		requestPath := filepath.Join(requestDir, name)
		workingPath := requestPath + ".working"
		if err := os.Rename(requestPath, workingPath); err != nil {
			continue
		}
		requestBytes, err := os.ReadFile(workingPath)
		if err != nil {
			cleanupWorkingRequest(workingPath)
			fmt.Fprintf(stderr, "Failed to read request file: %v\n", err)
			return workingPath, protocol.TaskRequest{}, false, 1
		}
		var request protocol.TaskRequest
		if err := json.Unmarshal(requestBytes, &request); err != nil {
			cleanupWorkingRequest(workingPath)
			fmt.Fprintf(stderr, "Failed to parse request file: %v\n", err)
			return workingPath, protocol.TaskRequest{}, false, 1
		}
		return workingPath, request, true, 0
	}

	return "", protocol.TaskRequest{}, false, 0
}

func cleanupWorkingRequest(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

func cleanupTaskInput(path string) {
	if path != "" {
		_ = os.Remove(path)
	}
}

func writeJSONAtomic(path string, value any, stderr io.Writer) int {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		fmt.Fprintf(stderr, "Failed to create output directory: %v\n", err)
		return 1
	}
	data, err := json.Marshal(value)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to encode JSON payload: %v\n", err)
		return 1
	}
	tempPath := path + ".tmp"
	if err := os.WriteFile(tempPath, data, 0o644); err != nil {
		fmt.Fprintf(stderr, "Failed to write temp output file: %v\n", err)
		return 1
	}
	if err := os.Rename(tempPath, path); err != nil {
		if removeErr := os.Remove(path); removeErr != nil && !errors.Is(removeErr, os.ErrNotExist) {
			fmt.Fprintf(stderr, "Failed to replace existing output file: %v\n", removeErr)
			return 1
		}
		if retryErr := os.Rename(tempPath, path); retryErr != nil {
			fmt.Fprintf(stderr, "Failed to move output file into place: %v\n", retryErr)
			return 1
		}
	}
	return 0
}

func validateCaptureTaskRequest(bridgeDir string, request protocol.TaskRequest) error {
	if request.RequestID == "" {
		return errors.New("capture request is missing request_id")
	}
	if request.InputPath != "" {
		return errors.New("capture request must not include input_path")
	}
	expectedResultPath := expectedCaptureResultPath(bridgeDir, request.RequestID)
	if !windowsPathsEqual(request.ResultPath, expectedResultPath) {
		return fmt.Errorf("capture request has invalid result_path %q", request.ResultPath)
	}
	return nil
}

func validateSetTextTaskRequest(bridgeDir string, request protocol.TaskRequest) error {
	if request.RequestID == "" {
		return errors.New("set-text request is missing request_id")
	}
	expectedInputPath := expectedSetTextInputPath(bridgeDir, request.RequestID)
	if !windowsPathsEqual(request.InputPath, expectedInputPath) {
		return fmt.Errorf("set-text request has invalid input_path %q", request.InputPath)
	}
	expectedResultPath := expectedSetTextResultPath(bridgeDir, request.RequestID)
	if !windowsPathsEqual(request.ResultPath, expectedResultPath) {
		return fmt.Errorf("set-text request has invalid result_path %q", request.ResultPath)
	}
	return nil
}

func writeCaptureTaskFailure(bridgeDir, requestID string, err error, stderr io.Writer) int {
	if requestID == "" {
		fmt.Fprintf(stderr, "Failed to validate capture task request: %v\n", err)
		return 1
	}
	result := protocol.CaptureTaskResult{RequestID: requestID, OK: false, Error: err.Error()}
	return writeJSONAtomic(expectedCaptureResultPath(bridgeDir, requestID), result, stderr)
}

func writeSetClipboardTaskFailure(bridgeDir, requestID string, err error, stderr io.Writer) int {
	if requestID == "" {
		fmt.Fprintf(stderr, "Failed to validate set-text task request: %v\n", err)
		return 1
	}
	result := protocol.SetClipboardTaskResult{RequestID: requestID, OK: false, Error: err.Error()}
	return writeJSONAtomic(expectedSetTextResultPath(bridgeDir, requestID), result, stderr)
}

func inferTaskRequestID(prefix string, paths ...string) string {
	for _, value := range paths {
		base := path.Base(strings.ReplaceAll(value, "\\", "/"))
		if !strings.HasPrefix(base, prefix) || !strings.HasSuffix(base, ".json") {
			continue
		}
		requestID := strings.TrimSuffix(strings.TrimPrefix(base, prefix), ".json")
		if requestID == "" || requestID == "ack" || requestID == "input" {
			continue
		}
		return requestID
	}
	for _, value := range paths {
		base := path.Base(strings.ReplaceAll(value, "\\", "/"))
		base = strings.TrimSuffix(base, path.Ext(base))
		if base != "" {
			return "legacy-" + base
		}
	}
	return "legacy"
}

func bridgeRootDir(bridgeDir string) string {
	if bridgeDir == "" {
		bridgeDir = `C:\ProgramData\clip-remote-sync`
	}
	return cleanWindowsPath(bridgeDir)
}

func bridgeRequestDir(bridgeDir string) string {
	return joinWindowsPath(bridgeRootDir(bridgeDir), "requests")
}

func bridgeResultDir(bridgeDir string) string {
	return joinWindowsPath(bridgeRootDir(bridgeDir), "results")
}

func expectedCaptureResultPath(bridgeDir, requestID string) string {
	return joinWindowsPath(bridgeResultDir(bridgeDir), fmt.Sprintf("capture-%s.json", requestID))
}

func expectedSetTextInputPath(bridgeDir, requestID string) string {
	return joinWindowsPath(bridgeRequestDir(bridgeDir), fmt.Sprintf("set-text-%s.txt", requestID))
}

func expectedSetTextResultPath(bridgeDir, requestID string) string {
	return joinWindowsPath(bridgeResultDir(bridgeDir), fmt.Sprintf("set-text-%s.json", requestID))
}

func joinWindowsPath(base string, parts ...string) string {
	if isUnixLikePath(base) {
		segments := append([]string{filepath.Clean(base)}, parts...)
		return filepath.Clean(filepath.Join(segments...))
	}

	current := cleanWindowsPath(base)
	for _, part := range parts {
		current = joinWindowsPathPart(current, part)
	}
	return current
}

func cleanWindowsPath(value string) string {
	if value == "" {
		return ""
	}
	if isUnixLikePath(value) {
		return filepath.Clean(value)
	}

	normalized := strings.ReplaceAll(value, "/", `\`)
	switch {
	case strings.HasPrefix(normalized, `\\`):
		cleaned := cleanWindowsSegments(strings.TrimPrefix(normalized, `\\`), true)
		if cleaned == "" {
			return `\\`
		}
		return `\\` + cleaned
	case hasWindowsDriveAbsolutePrefix(normalized):
		volume := normalized[:2]
		rest := strings.TrimPrefix(normalized[2:], `\`)
		cleaned := cleanWindowsSegments(rest, true)
		if cleaned == "" {
			return volume + `\`
		}
		return volume + `\` + cleaned
	case hasWindowsDrivePrefix(normalized):
		volume := normalized[:2]
		rest := normalized[2:]
		cleaned := cleanWindowsSegments(rest, false)
		if cleaned == "" {
			return volume
		}
		return volume + cleaned
	case strings.HasPrefix(normalized, `\`):
		cleaned := cleanWindowsSegments(strings.TrimPrefix(normalized, `\`), true)
		if cleaned == "" {
			return `\`
		}
		return `\` + cleaned
	default:
		return cleanWindowsSegments(normalized, false)
	}
}

func windowsPathsEqual(left, right string) bool {
	return strings.EqualFold(cleanWindowsPath(left), cleanWindowsPath(right))
}

func joinWindowsPathPart(base, part string) string {
	part = strings.ReplaceAll(part, "/", `\`)
	if part == "" {
		return cleanWindowsPath(base)
	}
	if base == "" || strings.HasPrefix(part, `\\`) || strings.HasPrefix(part, `\`) || hasWindowsDrivePrefix(part) {
		return cleanWindowsPath(part)
	}
	return cleanWindowsPath(base + `\` + part)
}

func cleanWindowsSegments(value string, absolute bool) string {
	slashed := strings.ReplaceAll(value, `\`, "/")
	cleaned := path.Clean(slashed)
	if absolute {
		cleaned = strings.TrimPrefix(cleaned, "/")
	}
	if cleaned == "." {
		return ""
	}
	return strings.ReplaceAll(cleaned, "/", `\`)
}

func hasWindowsDrivePrefix(value string) bool {
	return len(value) >= 2 && ((value[0] >= 'a' && value[0] <= 'z') || (value[0] >= 'A' && value[0] <= 'Z')) && value[1] == ':'
}

func hasWindowsDriveAbsolutePrefix(value string) bool {
	return hasWindowsDrivePrefix(value) && len(value) >= 3 && (value[2] == '\\' || value[2] == '/')
}

func isUnixLikePath(value string) bool {
	return strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//")
}
