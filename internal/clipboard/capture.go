package clipboard

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/aurokin/clip-remote-sync/internal/protocol"
)

func CaptureLocal(command commandFunc) (protocol.CaptureEnvelope, error) {
	switch runtime.GOOS {
	case "darwin":
		return captureDarwin(command)
	case "linux":
		return captureLinux(command)
	case "windows":
		return captureWindows(command)
	default:
		return protocol.CaptureEnvelope{}, fmt.Errorf("unsupported local OS: %s", runtime.GOOS)
	}
}

func SetLocalText(command commandFunc, text string) error {
	return SetLocal(command, text)
}

func captureDarwin(command commandFunc) (protocol.CaptureEnvelope, error) {
	tmpDir, err := os.MkdirTemp("", "crs-capture-*")
	if err != nil {
		return protocol.CaptureEnvelope{}, fmt.Errorf("create temp dir: %w", err)
	}
	defer os.RemoveAll(tmpDir)

	pngPath := filepath.Join(tmpDir, "clipboard.png")
	tiffPath := filepath.Join(tmpDir, "clipboard.tiff")

	if image, ok := readMacOSClipboardImage(command, pngPath, tiffPath); ok {
		return imageEnvelope(image), nil
	}

	text, err := run(command, "pbpaste")
	if err != nil {
		return protocol.CaptureEnvelope{}, err
	}
	text, ok := normalizeCapturedText(text)
	if !ok {
		return protocol.CaptureEnvelope{}, errors.New("clipboard is empty or unsupported")
	}

	return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: text}, nil
}

func captureLinux(command commandFunc) (protocol.CaptureEnvelope, error) {
	if image, ok := readLinuxClipboardImage(command); ok {
		return imageEnvelope(image), nil
	}
	if text, ok := readLinuxClipboardText(command); ok {
		return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: text}, nil
	}
	return protocol.CaptureEnvelope{}, errors.New("clipboard is empty or unsupported")
}

func captureWindows(command commandFunc) (protocol.CaptureEnvelope, error) {
	imageOutput, err := runPowerShell(command, buildWindowsImageCaptureScript())
	if err != nil {
		return protocol.CaptureEnvelope{}, fmt.Errorf("capture windows image clipboard: %w", err)
	}
	if strings.TrimSpace(imageOutput) != "__CRS_NO_IMAGE__" {
		return protocol.CaptureEnvelope{
			Kind:           protocol.KindImage,
			ImagePNGBase64: strings.TrimSpace(imageOutput),
		}, nil
	}

	textOutput, err := runPowerShell(command, "$text = Get-Clipboard -Raw; if ([string]::IsNullOrWhiteSpace($text)) { exit 4 }; [Console]::Out.Write($text)")
	if err != nil {
		return protocol.CaptureEnvelope{}, errors.New("clipboard is empty or unsupported")
	}
	textOutput, ok := normalizeCapturedText(textOutput)
	if !ok {
		return protocol.CaptureEnvelope{}, errors.New("clipboard is empty or unsupported")
	}

	return protocol.CaptureEnvelope{Kind: protocol.KindText, Text: textOutput}, nil
}

func readMacOSClipboardImage(command commandFunc, pngPath, tiffPath string) ([]byte, bool) {
	if err := writeClipboardPNGMacOS(command, pngPath); err == nil {
		image, readErr := os.ReadFile(pngPath)
		return image, readErr == nil
	}

	if err := writeClipboardTIFFMacOS(command, tiffPath); err != nil {
		return nil, false
	}
	if _, err := run(command, "sips", "-s", "format", "png", tiffPath, "--out", pngPath); err != nil {
		return nil, false
	}

	image, err := os.ReadFile(pngPath)
	return image, err == nil
}

func readLinuxClipboardImage(command commandFunc) ([]byte, bool) {
	if image, ok := readWaylandClipboardImage(command); ok {
		return image, true
	}
	if image, ok := readXclipClipboardImage(command); ok {
		return image, true
	}
	return nil, false
}

func readWaylandClipboardImage(command commandFunc) ([]byte, bool) {
	if !hasCommand("wl-paste") {
		return nil, false
	}
	typesOutput, err := run(command, "wl-paste", "--list-types")
	if err != nil || !strings.Contains(typesOutput, "image/png") {
		return nil, false
	}
	imageOutput, err := runBytes(command, "wl-paste", "--type", "image/png")
	if err != nil || len(imageOutput) == 0 {
		return nil, false
	}
	return imageOutput, true
}

func readXclipClipboardImage(command commandFunc) ([]byte, bool) {
	if !hasCommand("xclip") {
		return nil, false
	}
	imageOutput, err := runBytes(command, "xclip", "-selection", "clipboard", "-t", "image/png", "-o")
	if err != nil || len(imageOutput) == 0 {
		return nil, false
	}
	return imageOutput, true
}

func readLinuxClipboardText(command commandFunc) (string, bool) {
	readers := []func(commandFunc) (string, bool){
		readWaylandClipboardText,
		readXclipClipboardText,
		readXselClipboardText,
	}
	for _, reader := range readers {
		if text, ok := reader(command); ok {
			return text, true
		}
	}
	return "", false
}

func readWaylandClipboardText(command commandFunc) (string, bool) {
	if !hasCommand("wl-paste") {
		return "", false
	}
	if text, err := run(command, "wl-paste", "--no-newline"); err == nil {
		if normalized, ok := normalizeCapturedText(text); ok {
			return normalized, true
		}
	}
	if text, err := run(command, "wl-paste"); err == nil {
		if normalized, ok := normalizeCapturedText(text); ok {
			return normalized, true
		}
	}
	return "", false
}

func readXclipClipboardText(command commandFunc) (string, bool) {
	if !hasCommand("xclip") {
		return "", false
	}
	text, err := run(command, "xclip", "-selection", "clipboard", "-o")
	if err != nil {
		return "", false
	}
	return normalizeCapturedText(text)
}

func readXselClipboardText(command commandFunc) (string, bool) {
	if !hasCommand("xsel") {
		return "", false
	}
	text, err := run(command, "xsel", "--clipboard", "--output")
	if err != nil {
		return "", false
	}
	return normalizeCapturedText(text)
}

func normalizeCapturedText(text string) (string, bool) {
	text = strings.TrimRight(text, "\r\n")
	if strings.TrimSpace(text) == "" {
		return "", false
	}
	return text, true
}

func buildWindowsImageCaptureScript() string {
	return strings.Join([]string{
		"$img = Get-Clipboard -Format Image -ErrorAction SilentlyContinue",
		"if ($null -eq $img) { [Console]::Out.Write('__CRS_NO_IMAGE__'); exit 0 }",
		"$path = Join-Path $env:TEMP ('crs-' + [guid]::NewGuid().ToString('N') + '.png')",
		"try {",
		"$img.Save($path, [System.Drawing.Imaging.ImageFormat]::Png)",
		"[Console]::Out.Write([Convert]::ToBase64String([System.IO.File]::ReadAllBytes($path)))",
		"} finally {",
		"Remove-Item -LiteralPath $path -Force -ErrorAction SilentlyContinue",
		"}",
	}, "\n")
}

func imageEnvelope(image []byte) protocol.CaptureEnvelope {
	return protocol.CaptureEnvelope{
		Kind:           protocol.KindImage,
		ImagePNGBase64: base64.StdEncoding.EncodeToString(image),
	}
}

func writeClipboardPNGMacOS(command commandFunc, outPath string) error {
	script := `
set outPath to system attribute "CLIP_IMAGE_PATH"
set outFile to POSIX file outPath
try
    set imgData to the clipboard as «class PNGf»
on error
    error number 1
end try
set fh to open for access outFile with write permission
try
    set eof fh to 0
    write imgData to fh
    close access fh
on error
    try
        close access fh
    end try
    error number 2
end try
`
	cmd := command("osascript")
	cmd.Env = append(os.Environ(), "CLIP_IMAGE_PATH="+outPath)
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

func writeClipboardTIFFMacOS(command commandFunc, outPath string) error {
	script := `
set outPath to system attribute "CLIP_IMAGE_PATH"
set outFile to POSIX file outPath
try
    set imgData to the clipboard as TIFF picture
on error
    error number 1
end try
set fh to open for access outFile with write permission
try
    set eof fh to 0
    write imgData to fh
    close access fh
on error
    try
        close access fh
    end try
    error number 2
end try
`
	cmd := command("osascript")
	cmd.Env = append(os.Environ(), "CLIP_IMAGE_PATH="+outPath)
	cmd.Stdin = strings.NewReader(script)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
	}
	return nil
}

func run(command commandFunc, name string, args ...string) (string, error) {
	output, err := command(name, args...).CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
	}
	return string(output), nil
}

func runBytes(command commandFunc, name string, args ...string) ([]byte, error) {
	output, err := command(name, args...).CombinedOutput()
	if err != nil {
		return nil, fmt.Errorf("%w: %s", err, bytes.TrimSpace(output))
	}
	return output, nil
}

func runPowerShell(command commandFunc, script string) (string, error) {
	return run(command, "powershell", "-NoProfile", "-NonInteractive", "-Command", script)
}

func hasCommand(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
