package remote

import (
	"bytes"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"time"
	"unicode/utf16"

	"github.com/aurokin/clip-remote-sync/internal/protocol"
)

var (
	taskResultTimeout         = 8 * time.Second
	taskPollInterval          = 250 * time.Millisecond
	newRequestIDFunc          = newRequestID
	missingRemoteFileSentinel = "__CRS_NOT_FOUND__"
)

type commandFunc func(name string, arg ...string) *exec.Cmd

type SourceOptions struct {
	SSHTarget       string
	RemoteBin       string
	LaunchMode      string
	TaskBridgeDir   string
	CaptureTaskName string
	SetTextTaskName string
}

type CapturedData struct {
	Kind     protocol.Kind
	Text     string
	ImagePNG []byte
}

type taskBridgePaths struct {
	rootDir    string
	requestDir string
	resultDir  string
}

func Capture(command commandFunc, source SourceOptions) (CapturedData, error) {
	if source.LaunchMode == "task" {
		return captureViaTask(command, source)
	}
	return captureDirect(command, source)
}

func SetClipboardText(command commandFunc, source SourceOptions, text string) error {
	if source.LaunchMode == "task" {
		return setClipboardTextViaTask(command, source, text)
	}
	return setClipboardTextDirect(command, source, text)
}

func captureDirect(command commandFunc, source SourceOptions) (CapturedData, error) {
	if isLikelyWindowsCommand(source.RemoteBin) {
		output, err := runRemotePowerShell(command, source.SSHTarget, buildDirectCaptureScript(source.RemoteBin))
		if err != nil {
			return CapturedData{}, err
		}
		return parseCapture(output)
	}

	cmd := command("ssh", source.SSHTarget, buildPOSIXRemoteCommand(source.RemoteBin, "_capture"))

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return CapturedData{}, formatRunError(err, stderr.String())
	}

	return parseCapture(stdout.Bytes())
}

func setClipboardTextDirect(command commandFunc, source SourceOptions, text string) error {
	if isLikelyWindowsCommand(source.RemoteBin) {
		_, err := runRemotePowerShell(command, source.SSHTarget, buildDirectSetClipboardTextScript(source.RemoteBin, text))
		return err
	}

	cmd := command("ssh", source.SSHTarget, buildPOSIXRemoteCommand(source.RemoteBin, "_set-clipboard-text"))
	cmd.Stdin = strings.NewReader(text)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

func captureViaTask(command commandFunc, source SourceOptions) (_ CapturedData, retErr error) {
	requestID, err := newRequestIDFunc()
	if err != nil {
		return CapturedData{}, fmt.Errorf("generate request id: %w", err)
	}

	bridge := bridgePaths(source)
	requestPath := bridge.captureRequestPath(requestID)
	resultPath := bridge.captureResultPath(requestID)
	defer func() {
		if retErr != nil {
			cleanupRemoteArtifacts(command, source.SSHTarget, requestPath, resultPath)
		}
	}()

	request := protocol.TaskRequest{
		RequestID:  requestID,
		ResultPath: resultPath,
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		retErr = fmt.Errorf("encode capture request: %w", err)
		return CapturedData{}, retErr
	}

	if err := ensureBridgeDirs(command, source.SSHTarget, bridge); err != nil {
		retErr = fmt.Errorf("prepare bridge directories: %w", err)
		return CapturedData{}, retErr
	}
	if _, err := runRemotePowerShell(command, source.SSHTarget, writeUTF8FileScript(requestPath, string(requestBytes))); err != nil {
		retErr = fmt.Errorf("write capture request: %w", err)
		return CapturedData{}, retErr
	}
	if _, err := runSSH(command, source.SSHTarget, "schtasks", "/Run", "/TN", source.CaptureTaskName); err != nil {
		retErr = fmt.Errorf("run capture task: %w", err)
		return CapturedData{}, retErr
	}

	result, err := waitForCaptureResult(command, source.SSHTarget, resultPath, requestID, taskResultTimeout)
	if err != nil {
		retErr = fmt.Errorf("wait for capture result: %w", err)
		return CapturedData{}, retErr
	}
	_ = removeRemoteFile(command, source.SSHTarget, resultPath)

	if !result.OK {
		retErr = errors.New(result.Error)
		return CapturedData{}, retErr
	}
	if result.Capture == nil {
		retErr = errors.New("capture result missing capture payload")
		return CapturedData{}, retErr
	}

	captureBytes, err := json.Marshal(result.Capture)
	if err != nil {
		retErr = fmt.Errorf("re-encode capture payload: %w", err)
		return CapturedData{}, retErr
	}

	captured, err := parseCapture(captureBytes)
	if err != nil {
		retErr = err
		return CapturedData{}, retErr
	}
	return captured, nil
}

func setClipboardTextViaTask(command commandFunc, source SourceOptions, text string) (retErr error) {
	requestID, err := newRequestIDFunc()
	if err != nil {
		return fmt.Errorf("generate request id: %w", err)
	}

	bridge := bridgePaths(source)
	inputPath := bridge.setTextInputPath(requestID)
	requestPath := bridge.setTextRequestPath(requestID)
	resultPath := bridge.setTextResultPath(requestID)
	defer func() {
		if retErr != nil {
			cleanupRemoteArtifacts(command, source.SSHTarget, inputPath, requestPath, resultPath)
		}
	}()

	request := protocol.TaskRequest{
		RequestID:  requestID,
		InputPath:  inputPath,
		ResultPath: resultPath,
	}
	requestBytes, err := json.Marshal(request)
	if err != nil {
		return fmt.Errorf("encode set-text request: %w", err)
	}

	if err := ensureBridgeDirs(command, source.SSHTarget, bridge); err != nil {
		return fmt.Errorf("prepare bridge directories: %w", err)
	}
	if _, err := runRemotePowerShell(command, source.SSHTarget, writeUTF8FileScript(inputPath, text)); err != nil {
		return fmt.Errorf("write set-text input: %w", err)
	}
	if _, err := runRemotePowerShell(command, source.SSHTarget, writeUTF8FileScript(requestPath, string(requestBytes))); err != nil {
		return fmt.Errorf("write set-text request: %w", err)
	}
	if _, err := runSSH(command, source.SSHTarget, "schtasks", "/Run", "/TN", source.SetTextTaskName); err != nil {
		return fmt.Errorf("run set-text task: %w", err)
	}

	result, err := waitForSetTextResult(command, source.SSHTarget, resultPath, requestID, taskResultTimeout)
	if err != nil {
		return fmt.Errorf("wait for set-text result: %w", err)
	}
	_ = removeRemoteFile(command, source.SSHTarget, resultPath)

	if !result.OK {
		return errors.New(result.Error)
	}
	return nil
}

func parseCapture(payload []byte) (CapturedData, error) {
	var envelope protocol.CaptureEnvelope
	if err := json.Unmarshal(payload, &envelope); err != nil {
		return CapturedData{}, fmt.Errorf("parse remote capture output: %w", err)
	}

	switch envelope.Kind {
	case protocol.KindText:
		return CapturedData{Kind: envelope.Kind, Text: envelope.Text}, nil
	case protocol.KindImage:
		imagePNG, err := base64.StdEncoding.DecodeString(envelope.ImagePNGBase64)
		if err != nil {
			return CapturedData{}, fmt.Errorf("decode image payload: %w", err)
		}
		return CapturedData{Kind: envelope.Kind, ImagePNG: imagePNG}, nil
	default:
		return CapturedData{}, fmt.Errorf("unsupported capture kind %q", envelope.Kind)
	}
}

func bridgePaths(source SourceOptions) taskBridgePaths {
	dir := source.TaskBridgeDir
	if dir == "" {
		dir = `C:\ProgramData\clip-remote-sync`
	}
	return taskBridgePaths{
		rootDir:    dir,
		requestDir: dir + `\requests`,
		resultDir:  dir + `\results`,
	}
}

func (p taskBridgePaths) captureRequestPath(requestID string) string {
	return p.requestDir + `\capture-` + requestID + `.json`
}

func (p taskBridgePaths) captureResultPath(requestID string) string {
	return p.resultDir + `\capture-` + requestID + `.json`
}

func (p taskBridgePaths) setTextRequestPath(requestID string) string {
	return p.requestDir + `\set-text-` + requestID + `.json`
}

func (p taskBridgePaths) setTextInputPath(requestID string) string {
	return p.requestDir + `\set-text-` + requestID + `.txt`
}

func (p taskBridgePaths) setTextResultPath(requestID string) string {
	return p.resultDir + `\set-text-` + requestID + `.json`
}

func ensureBridgeDirs(command commandFunc, target string, bridge taskBridgePaths) error {
	script := fmt.Sprintf(`New-Item -ItemType Directory -Force -Path '%s' | Out-Null; New-Item -ItemType Directory -Force -Path '%s' | Out-Null`, escapeSingleQuotes(bridge.requestDir), escapeSingleQuotes(bridge.resultDir))
	_, err := runRemotePowerShell(command, target, script)
	return err
}

func waitForCaptureResult(command commandFunc, target, resultPath, requestID string, timeout time.Duration) (protocol.CaptureTaskResult, error) {
	resultText, err := waitForRemoteJSONFile(command, target, resultPath, timeout)
	if err != nil {
		return protocol.CaptureTaskResult{}, err
	}

	var result protocol.CaptureTaskResult
	if err := json.Unmarshal([]byte(resultText), &result); err != nil {
		return protocol.CaptureTaskResult{}, fmt.Errorf("parse capture result: %w", err)
	}
	if result.RequestID != requestID {
		return protocol.CaptureTaskResult{}, fmt.Errorf("capture result request id mismatch: got %q want %q", result.RequestID, requestID)
	}
	return result, nil
}

func waitForSetTextResult(command commandFunc, target, resultPath, requestID string, timeout time.Duration) (protocol.SetClipboardTaskResult, error) {
	resultText, err := waitForRemoteJSONFile(command, target, resultPath, timeout)
	if err != nil {
		return protocol.SetClipboardTaskResult{}, err
	}

	var result protocol.SetClipboardTaskResult
	if err := json.Unmarshal([]byte(resultText), &result); err != nil {
		return protocol.SetClipboardTaskResult{}, fmt.Errorf("parse set-text result: %w", err)
	}
	if result.RequestID != requestID {
		return protocol.SetClipboardTaskResult{}, fmt.Errorf("set-text result request id mismatch: got %q want %q", result.RequestID, requestID)
	}
	return result, nil
}

func waitForRemoteJSONFile(command commandFunc, target, path string, timeout time.Duration) (string, error) {
	deadline := time.Now().Add(timeout)
	script := fmt.Sprintf(`if (Test-Path -LiteralPath '%s') { [Console]::Out.Write((Get-Content -LiteralPath '%s' -Raw)) } else { [Console]::Out.Write('%s') }`, escapeSingleQuotes(path), escapeSingleQuotes(path), missingRemoteFileSentinel)
	var lastErr error
	for time.Now().Before(deadline) {
		output, err := runRemotePowerShell(command, target, script)
		if err == nil {
			trimmed := strings.TrimSpace(string(output))
			if trimmed == missingRemoteFileSentinel || trimmed == "" {
				time.Sleep(taskPollInterval)
				continue
			}
			if json.Valid(output) {
				return string(output), nil
			}
			lastErr = errors.New("result file exists but JSON is incomplete")
			time.Sleep(taskPollInterval)
			continue
		}
		lastErr = err
		time.Sleep(taskPollInterval)
	}
	if lastErr != nil {
		return "", lastErr
	}
	return "", fmt.Errorf("timed out waiting for %s", path)
}

func removeRemoteFile(command commandFunc, target, path string) error {
	_, err := runRemotePowerShell(command, target, fmt.Sprintf(`if (Test-Path -LiteralPath '%s') { Remove-Item -LiteralPath '%s' -Force -ErrorAction Stop }`, escapeSingleQuotes(path), escapeSingleQuotes(path)))
	return err
}

func cleanupRemoteArtifacts(command commandFunc, target string, paths ...string) {
	for _, artifactPath := range paths {
		if artifactPath == "" {
			continue
		}
		_ = removeRemoteFile(command, target, artifactPath)
	}
}

func runSSH(command commandFunc, target string, args ...string) ([]byte, error) {
	cmdArgs := append([]string{target}, args...)
	output, err := command("ssh", cmdArgs...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
	}
	return output, nil
}

func runRemotePowerShell(command commandFunc, target, script string) ([]byte, error) {
	return runSSH(command, target, buildEncodedPowerShellCommand(script))
}

func writeUTF8FileScript(path, value string) string {
	return fmt.Sprintf(`$dir=[System.IO.Path]::GetDirectoryName('%s'); if (-not [string]::IsNullOrEmpty($dir)) { New-Item -ItemType Directory -Force -Path $dir | Out-Null }; [System.IO.File]::WriteAllText('%s', [System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String('%s')), (New-Object System.Text.UTF8Encoding($false)))`, escapeSingleQuotes(path), escapeSingleQuotes(path), base64.StdEncoding.EncodeToString([]byte(value)))
}

func escapeSingleQuotes(value string) string {
	return strings.ReplaceAll(value, `'`, `''`)
}

func buildPOSIXRemoteCommand(bin string, args ...string) string {
	parts := []string{quotePOSIXShell(bin)}
	for _, arg := range args {
		parts = append(parts, quotePOSIXShell(arg))
	}
	return strings.Join(parts, " ")
}

func quotePOSIXShell(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}

func buildDirectCaptureScript(remoteBin string) string {
	return fmt.Sprintf(`& '%s' _capture`, escapeSingleQuotes(remoteBin))
}

func buildEncodedPowerShellCommand(script string) string {
	script = "$ProgressPreference = 'SilentlyContinue'; " + script
	utf16Units := utf16.Encode([]rune(script))
	bytes := make([]byte, 0, len(utf16Units)*2)
	for _, unit := range utf16Units {
		bytes = append(bytes, byte(unit), byte(unit>>8))
	}
	return "powershell -NoProfile -NonInteractive -EncodedCommand " + base64.StdEncoding.EncodeToString(bytes)
}

func buildDirectSetClipboardTextScript(remoteBin, text string) string {
	return fmt.Sprintf(`$text=[System.Text.Encoding]::UTF8.GetString([System.Convert]::FromBase64String('%s')); $psi=New-Object System.Diagnostics.ProcessStartInfo; $psi.FileName='%s'; $psi.Arguments='_set-clipboard-text'; $psi.RedirectStandardInput=$true; $psi.RedirectStandardOutput=$true; $psi.RedirectStandardError=$true; $psi.UseShellExecute=$false; $psi.CreateNoWindow=$true; $proc=[System.Diagnostics.Process]::Start($psi); $proc.StandardInput.Write($text); $proc.StandardInput.Close(); $proc.WaitForExit(); $stdout=$proc.StandardOutput.ReadToEnd(); $stderr=$proc.StandardError.ReadToEnd(); if (-not [string]::IsNullOrEmpty($stdout)) { [Console]::Out.Write($stdout) }; if (-not [string]::IsNullOrEmpty($stderr)) { [Console]::Error.Write($stderr) }; if ($proc.ExitCode -ne 0) { exit $proc.ExitCode }`, base64.StdEncoding.EncodeToString([]byte(text)), escapeSingleQuotes(remoteBin))
}

func isLikelyWindowsCommand(remoteBin string) bool {
	lower := strings.ToLower(remoteBin)
	return strings.Contains(remoteBin, "\\") || strings.Contains(remoteBin, ":") || strings.HasSuffix(lower, ".exe")
}

func newRequestID() (string, error) {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err != nil {
		return "", err
	}
	return hex.EncodeToString(buf), nil
}

func formatRunError(err error, stderr string) error {
	msg := strings.TrimSpace(stderr)
	if msg == "" {
		msg = err.Error()
	}
	return errors.New(msg)
}
