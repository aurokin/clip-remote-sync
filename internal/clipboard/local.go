package clipboard

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
)

type commandFunc func(name string, arg ...string) *exec.Cmd

func SetLocal(command commandFunc, text string) error {
	switch runtime.GOOS {
	case "darwin":
		return pipeTo(command("pbcopy"), text)
	case "linux":
		return setLocalLinuxText(command, text)
	case "windows":
		return runPowerShellWithInput(command, buildWindowsSetTextScript(), text)
	default:
		return fmt.Errorf("unsupported local OS: %s", runtime.GOOS)
	}
}

func SetLocalImage(command commandFunc, imagePNG []byte) error {
	switch runtime.GOOS {
	case "linux":
		return setLocalLinuxImage(command, imagePNG)
	default:
		return fmt.Errorf("unsupported local image clipboard OS: %s", runtime.GOOS)
	}
}

func setLocalLinuxText(command commandFunc, text string) error {
	if _, err := exec.LookPath("wl-copy"); err == nil {
		return pipeTo(command("wl-copy"), text)
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return runDetachedXclip(command, text, "clipboard")
	}
	if _, err := exec.LookPath("xsel"); err == nil {
		return pipeTo(command("xsel", "--clipboard", "--input"), text)
	}
	return fmt.Errorf("no supported clipboard writer found (need wl-copy, xclip, or xsel)")
}

func setLocalLinuxImage(command commandFunc, imagePNG []byte) error {
	if _, err := exec.LookPath("wl-copy"); err == nil {
		cmd := command("wl-copy", "--type", "image/png")
		cmd.Stdin = bytes.NewReader(imagePNG)
		output, err := cmd.CombinedOutput()
		if err != nil {
			return fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
		}
		return nil
	}
	if _, err := exec.LookPath("xclip"); err == nil {
		return runDetachedXclipBytes(command, imagePNG, "clipboard", "image/png")
	}
	return fmt.Errorf("no supported image clipboard writer found (need wl-copy or xclip)")
}

func runDetachedXclip(command commandFunc, text string, selection string) error {
	return runDetachedXclipBytes(command, []byte(text), selection, "")
}

func runDetachedXclipBytes(command commandFunc, data []byte, selection, target string) error {
	tmpFile, err := os.CreateTemp("", "crs-xclip-*")
	if err != nil {
		return fmt.Errorf("create xclip temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	if _, err := tmpFile.Write(data); err != nil {
		_ = tmpFile.Close()
		_ = os.Remove(tmpPath)
		return fmt.Errorf("write xclip temp file: %w", err)
	}
	if err := tmpFile.Close(); err != nil {
		_ = os.Remove(tmpPath)
		return fmt.Errorf("close xclip temp file: %w", err)
	}

	args := []string{"xclip", "-selection", selection}
	if target != "" {
		args = append(args, "-t", target)
	}
	args = append(args, "-i", "<", "$1", ">/dev/null", "2>&1", "&")
	cmd := command("sh", "-c", strings.Join(args, " "), "sh", tmpPath)
	output, err := cmd.CombinedOutput()
	_ = os.Remove(tmpPath)
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

func pipeTo(cmd *exec.Cmd, text string) error {
	cmd.Stdin = bytes.NewBufferString(text)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

func buildWindowsSetTextScript() string {
	return strings.Join([]string{
		"Add-Type -AssemblyName System.Windows.Forms",
		"$text = [Console]::In.ReadToEnd()",
		"[System.Windows.Forms.Clipboard]::Clear()",
		"[System.Windows.Forms.Clipboard]::SetText($text)",
	}, "; ")
}

func runPowerShellWithInput(command commandFunc, script, input string) error {
	cmd := command("powershell", "-NoProfile", "-NonInteractive", "-Command", script)
	cmd.Stdin = bytes.NewBufferString(input)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}
