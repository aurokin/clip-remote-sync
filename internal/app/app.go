package app

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/aurokin/clip-remote-sync/internal/clipboard"
	"github.com/aurokin/clip-remote-sync/internal/protocol"
	"github.com/aurokin/clip-remote-sync/internal/remote"
)

type dependencies struct {
	defaultConfigPath      func() (string, error)
	loadConfig             func(string) (Config, error)
	remoteCapture          func(source remote.SourceOptions) (remote.CapturedData, error)
	localCapturePreferText func() (protocol.CaptureEnvelope, error)
	remoteSetClipboardText func(source remote.SourceOptions, text string) error
	setLocalClipboard      func(text string) error
	setLocalImageClipboard func([]byte) error
	saveImage              func([]byte) (string, error)
}

func defaultDependencies() dependencies {
	return dependencies{
		defaultConfigPath: defaultConfigPath,
		loadConfig:        loadConfig,
		remoteCapture: func(source remote.SourceOptions) (remote.CapturedData, error) {
			return remote.Capture(exec.Command, source)
		},
		localCapturePreferText: func() (protocol.CaptureEnvelope, error) {
			return clipboard.CaptureLocalPreferText(exec.Command)
		},
		remoteSetClipboardText: func(source remote.SourceOptions, text string) error {
			return remote.SetClipboardText(exec.Command, source, text)
		},
		setLocalClipboard: func(text string) error {
			return clipboard.SetLocal(exec.Command, text)
		},
		setLocalImageClipboard: func(imagePNG []byte) error {
			return clipboard.SetLocalImage(exec.Command, imagePNG)
		},
		saveImage: saveImage,
	}
}

type application struct {
	deps dependencies
}

type publicRequest struct {
	configPath string
	reverse    bool
	sourceName string
}

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	return application{deps: defaultDependencies()}.run(args, stdout, stderr)
}

func (a application) run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 && strings.HasPrefix(args[0], "_") {
		return runInternal(args, stdout, stderr)
	}

	req, exitCode, stop := a.parsePublicArgs(args, stdout, stderr)
	if stop {
		return exitCode
	}

	cfg, _, remoteSource, exitCode, ok := a.loadPublicContext(req, stderr)
	if !ok {
		return exitCode
	}

	if req.reverse {
		return a.runReverse(cfg, req.sourceName, remoteSource, stdout, stderr)
	}

	captured, err := a.deps.remoteCapture(remoteSource)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to capture clipboard from %s: %v\n", req.sourceName, err)
		return 1
	}

	return a.applyCapturedData(cfg, req.sourceName, remoteSource, captured, stdout, stderr)
}

func (a application) parsePublicArgs(args []string, stdout io.Writer, stderr io.Writer) (publicRequest, int, bool) {
	fs := flag.NewFlagSet("crs", flag.ContinueOnError)
	fs.SetOutput(stderr)

	configPathFlag := fs.String("config", "", "Path to config file")
	help := fs.Bool("help", false, "Show help")
	reverse := fs.Bool("reverse", false, "Sync clipboard content from the local machine to the source")
	fs.BoolVar(help, "h", false, "Show help")
	fs.BoolVar(reverse, "r", false, "Sync clipboard content from the local machine to the source")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			printUsage(stdout)
			return publicRequest{}, 0, true
		}
		return publicRequest{}, 2, true
	}

	if *help {
		printUsage(stdout)
		return publicRequest{}, 0, true
	}

	if fs.NArg() != 1 {
		printUsage(stderr)
		return publicRequest{}, 2, true
	}

	configPath := *configPathFlag
	if configPath == "" {
		var err error
		configPath, err = a.deps.defaultConfigPath()
		if err != nil {
			fmt.Fprintf(stderr, "Failed to resolve config path: %v\n", err)
			return publicRequest{}, 1, true
		}
	}

	return publicRequest{configPath: configPath, reverse: *reverse, sourceName: fs.Arg(0)}, 0, false
}

func (a application) loadPublicContext(req publicRequest, stderr io.Writer) (Config, SourceConfig, remote.SourceOptions, int, bool) {
	cfg, err := a.deps.loadConfig(req.configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to load config: %v\n", err)
		fmt.Fprintf(stderr, "Create %s from config.example.json and add your source hosts there.\n", req.configPath)
		return Config{}, SourceConfig{}, remote.SourceOptions{}, 1, false
	}

	sourceCfg, ok := cfg.Sources[req.sourceName]
	if !ok {
		fmt.Fprintf(stderr, "Unknown source %q. Configured sources: %s\n", req.sourceName, strings.Join(configuredSources(cfg), ", "))
		return Config{}, SourceConfig{}, remote.SourceOptions{}, 1, false
	}
	if err := validateSourceUsage(sourceCfg, req.reverse); err != nil {
		fmt.Fprintf(stderr, "Invalid source %q config: %v\n", req.sourceName, err)
		return Config{}, SourceConfig{}, remote.SourceOptions{}, 1, false
	}

	return cfg, sourceCfg, buildRemoteSource(sourceCfg), 0, true
}

func buildRemoteSource(sourceCfg SourceConfig) remote.SourceOptions {
	remoteBin := sourceCfg.RemoteBin
	if remoteBin == "" {
		remoteBin = "crs"
	}

	launchMode := sourceCfg.LaunchMode
	if launchMode == "" {
		launchMode = "direct"
	}

	return remote.SourceOptions{
		SSHTarget:       sourceCfg.SSHTarget,
		RemoteBin:       remoteBin,
		LaunchMode:      launchMode,
		TaskBridgeDir:   sourceCfg.TaskBridgeDir,
		CaptureTaskName: sourceCfg.CaptureTaskName,
		SetTextTaskName: sourceCfg.SetTextTaskName,
	}
}

func (a application) applyCapturedData(cfg Config, sourceName string, remoteSource remote.SourceOptions, captured remote.CapturedData, stdout io.Writer, stderr io.Writer) int {
	switch captured.Kind {
	case protocol.KindText:
		return a.applyCapturedText(cfg, sourceName, captured.Text, stdout, stderr)
	case protocol.KindImage:
		return a.applyCapturedImage(sourceName, remoteSource, captured.ImagePNG, stdout, stderr)
	default:
		fmt.Fprintf(stderr, "Unsupported capture kind %q from %s\n", captured.Kind, sourceName)
		return 1
	}
}

func (a application) applyCapturedText(cfg Config, sourceName, text string, stdout io.Writer, stderr io.Writer) int {
	if err := a.deps.setLocalClipboard(text); err != nil {
		fmt.Fprintf(stderr, "Failed to set local clipboard: %v\n", err)
		return 1
	}
	fmt.Fprintf(stdout, "Clipboard synced from %s to %s\n", sourceName, destinationName(cfg))
	return 0
}

func (a application) applyCapturedImage(sourceName string, remoteSource remote.SourceOptions, imagePNG []byte, stdout io.Writer, stderr io.Writer) int {
	savedPath, err := a.deps.saveImage(imagePNG)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to save image locally: %v\n", err)
		return 1
	}

	if err := a.deps.setLocalImageClipboard(imagePNG); err != nil {
		_ = os.Remove(savedPath)
		fmt.Fprintf(stderr, "Failed to set local image clipboard: %v\n", err)
		return 1
	}
	if err := a.deps.remoteSetClipboardText(remoteSource, savedPath); err != nil {
		fmt.Fprintf(stderr, "Local image clipboard updated and saved to %s, but failed to update %s clipboard: %v. Remote clipboard state is unknown.\n", savedPath, sourceName, err)
		return 1
	}
	fmt.Fprintf(stdout, "Image captured from %s and saved to %s\n", sourceName, savedPath)
	return 0
}

func (a application) runReverse(cfg Config, sourceName string, remoteSource remote.SourceOptions, stdout io.Writer, stderr io.Writer) int {
	captured, err := a.deps.localCapturePreferText()
	if err != nil {
		fmt.Fprintf(stderr, "Failed to capture local clipboard: %v\n", err)
		return 1
	}

	switch captured.Kind {
	case protocol.KindText:
		if err := a.deps.remoteSetClipboardText(remoteSource, captured.Text); err != nil {
			fmt.Fprintf(stderr, "Failed to set %s clipboard: %v\n", sourceName, err)
			return 1
		}
		fmt.Fprintf(stdout, "Clipboard synced from %s to %s\n", destinationName(cfg), sourceName)
		return 0
	case protocol.KindImage:
		imagePNG, err := decodeCapturedImage(captured)
		if err != nil {
			fmt.Fprintf(stderr, "Failed to decode local image clipboard: %v\n", err)
			return 1
		}
		savedPath, err := a.deps.saveImage(imagePNG)
		if err != nil {
			fmt.Fprintf(stderr, "Failed to save local image: %v\n", err)
			return 1
		}
		if err := a.deps.remoteSetClipboardText(remoteSource, savedPath); err != nil {
			fmt.Fprintf(stderr, "Local image saved to %s, but failed to update %s clipboard: %v. Remote clipboard state is unknown.\n", savedPath, sourceName, err)
			return 1
		}
		fmt.Fprintf(stdout, "Local image saved to %s and synced to %s as text\n", savedPath, sourceName)
		return 0
	default:
		fmt.Fprintf(stderr, "Unsupported local capture kind %q\n", captured.Kind)
		return 1
	}
}

func destinationName(cfg Config) string {
	if cfg.Destination.Name != "" {
		return cfg.Destination.Name
	}
	return "destination"
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: crs [--config PATH] [-r] <source>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Sync clipboard content between the local machine and a configured source.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Default mode pulls from the source clipboard into the local clipboard.")
	fmt.Fprintln(w, "With -r, local text is pushed to the source clipboard; if the local clipboard only has an image,")
	fmt.Fprintln(w, "it is saved locally and that destination-local path is pushed to the source clipboard.")
}

func saveImage(imagePNG []byte) (string, error) {
	return saveImageToDir(imagePNG, imageSaveDir())
}

func saveImageToDir(imagePNG []byte, dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("create %s: %w", dir, err)
	}

	fileName := fmt.Sprintf("clipboard-%s-%s.png", timestampNow(), randomSuffix())
	path := filepath.Join(dir, fileName)
	if err := os.WriteFile(path, imagePNG, 0o644); err != nil {
		return "", fmt.Errorf("write %s: %w", path, err)
	}

	return path, nil
}

func timestampNow() string {
	return time.Now().Format("20060102-150405")
}

func randomSuffix() string {
	buf := make([]byte, 4)
	if _, err := rand.Read(buf); err != nil {
		return "00000000"
	}
	return hex.EncodeToString(buf)
}

func decodeCapturedImage(captured protocol.CaptureEnvelope) ([]byte, error) {
	return base64.StdEncoding.DecodeString(captured.ImagePNGBase64)
}

func imageSaveDir() string {
	return imageSaveDirFor(filepath.Separator, os.TempDir())
}

func imageSaveDirFor(separator uint8, tempDir string) string {
	if separator == '\\' {
		return strings.TrimRight(tempDir, `\/`) + `\clip`
	}
	return "/tmp/clip"
}

func validateSourceUsage(sourceCfg SourceConfig, reverse bool) error {
	if sourceCfg.LaunchMode != "task" {
		return nil
	}
	if reverse {
		if sourceCfg.SetTextTaskName == "" {
			return errors.New("task launch_mode requires set_text_task_name for reverse sync")
		}
		return nil
	}
	if sourceCfg.CaptureTaskName == "" {
		return errors.New("task launch_mode requires capture_task_name")
	}
	if sourceCfg.SetTextTaskName == "" {
		return errors.New("task launch_mode requires set_text_task_name")
	}
	return nil
}
