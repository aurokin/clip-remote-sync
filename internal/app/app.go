package app

import (
	"crypto/rand"
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
	fs.BoolVar(help, "h", false, "Show help")

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

	return publicRequest{configPath: configPath, sourceName: fs.Arg(0)}, 0, false
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

func destinationName(cfg Config) string {
	if cfg.Destination.Name != "" {
		return cfg.Destination.Name
	}
	return "destination"
}

func printUsage(w io.Writer) {
	fmt.Fprintln(w, "Usage: crs [--config PATH] <source>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Sync clipboard content from a configured source to the local machine.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "If the source clipboard contains an image, it is saved locally into /tmp/clip/<generated>.png,")
	fmt.Fprintln(w, "the local clipboard is updated to the image, and the source clipboard is updated to that local path.")
}

func saveImage(imagePNG []byte) (string, error) {
	if err := os.MkdirAll("/tmp/clip", 0o755); err != nil {
		return "", fmt.Errorf("create /tmp/clip: %w", err)
	}

	fileName := fmt.Sprintf("clipboard-%s-%s.png", timestampNow(), randomSuffix())
	path := filepath.Join("/tmp/clip", fileName)
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
