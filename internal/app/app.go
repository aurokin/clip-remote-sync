package app

import (
	"bufio"
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
	"strconv"
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
	in   io.Reader
}

type publicRequest struct {
	configPath  string
	interactive bool
	reverse     bool
	sourceName  string
}

type interactiveOption struct {
	command     string
	description string
	reverse     bool
	sourceName  string
}

type interactiveHost struct {
	name    string
	options []interactiveOption
}

func Run(args []string, stdout io.Writer, stderr io.Writer) int {
	return application{deps: defaultDependencies(), in: os.Stdin}.run(args, stdout, stderr)
}

func (a application) run(args []string, stdout io.Writer, stderr io.Writer) int {
	if len(args) > 0 && strings.HasPrefix(args[0], "_") {
		return runInternal(args, stdout, stderr)
	}

	req, exitCode, stop := a.parsePublicArgs(args, stdout, stderr)
	if stop {
		return exitCode
	}

	cfg, exitCode, ok := a.loadConfig(req.configPath, stderr)
	if !ok {
		return exitCode
	}

	if req.interactive {
		selectedReq, selectedExitCode, selected := a.selectInteractiveRequest(cfg, stdout, stderr)
		if !selected {
			return selectedExitCode
		}
		req.sourceName = selectedReq.sourceName
		req.reverse = selectedReq.reverse
	}

	_, remoteSource, exitCode, ok := a.resolvePublicSource(cfg, req, stderr)
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

	if fs.NArg() == 0 {
		if len(args) == 0 {
			configPath := *configPathFlag
			if configPath == "" {
				var err error
				configPath, err = a.deps.defaultConfigPath()
				if err != nil {
					fmt.Fprintf(stderr, "Failed to resolve config path: %v\n", err)
					return publicRequest{}, 1, true
				}
			}
			return publicRequest{configPath: configPath, interactive: true}, 0, false
		}
		printUsage(stderr)
		return publicRequest{}, 2, true
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

func (a application) loadConfig(configPath string, stderr io.Writer) (Config, int, bool) {
	cfg, err := a.deps.loadConfig(configPath)
	if err != nil {
		fmt.Fprintf(stderr, "Failed to load config: %v\n", err)
		fmt.Fprintf(stderr, "Create %s from config.example.json and add your source hosts there.\n", configPath)
		return Config{}, 1, false
	}

	return cfg, 0, true
}

func (a application) resolvePublicSource(cfg Config, req publicRequest, stderr io.Writer) (SourceConfig, remote.SourceOptions, int, bool) {
	sourceCfg, ok := cfg.Sources[req.sourceName]
	if !ok {
		fmt.Fprintf(stderr, "Unknown source %q. Configured sources: %s\n", req.sourceName, strings.Join(configuredSources(cfg), ", "))
		return SourceConfig{}, remote.SourceOptions{}, 1, false
	}
	if err := validateSourceUsage(sourceCfg, req.reverse); err != nil {
		fmt.Fprintf(stderr, "Invalid source %q config: %v\n", req.sourceName, err)
		return SourceConfig{}, remote.SourceOptions{}, 1, false
	}

	return sourceCfg, buildRemoteSource(sourceCfg), 0, true
}

func (a application) selectInteractiveRequest(cfg Config, stdout io.Writer, stderr io.Writer) (publicRequest, int, bool) {
	hosts := interactiveHosts(cfg)
	if len(hosts) == 0 {
		fmt.Fprintln(stderr, "No runnable sources found in config.")
		return publicRequest{}, 1, false
	}

	reader := bufio.NewReader(a.input())
	hostLabels := make([]string, 0, len(hosts))
	for _, host := range hosts {
		hostLabels = append(hostLabels, host.name)
	}
	hostIndex, exitCode, ok := promptInteractiveSelection(reader, stdout, stderr, "Select source host:", hostLabels)
	if !ok {
		return publicRequest{}, exitCode, false
	}

	host := hosts[hostIndex]
	actionLabels := make([]string, 0, len(host.options))
	for _, option := range host.options {
		actionLabels = append(actionLabels, fmt.Sprintf("%s (%s)", option.description, option.command))
	}
	actionIndex, exitCode, ok := promptInteractiveSelection(
		reader,
		stdout,
		stderr,
		fmt.Sprintf("Select action for %s:", host.name),
		actionLabels,
	)
	if !ok {
		return publicRequest{}, exitCode, false
	}

	option := host.options[actionIndex]
	return publicRequest{reverse: option.reverse, sourceName: option.sourceName}, 0, true
}

func interactiveHosts(cfg Config) []interactiveHost {
	var hosts []interactiveHost
	for _, name := range configuredSources(cfg) {
		options := interactiveOptions(cfg, name)
		if len(options) > 0 {
			hosts = append(hosts, interactiveHost{name: name, options: options})
		}
	}
	return hosts
}

func promptInteractiveSelection(reader *bufio.Reader, stdout io.Writer, stderr io.Writer, title string, labels []string) (int, int, bool) {
	fmt.Fprintln(stdout, title)
	for idx, label := range labels {
		fmt.Fprintf(stdout, "  %d. %s\n", idx+1, label)
	}

	for {
		fmt.Fprint(stdout, "> ")

		line, err := reader.ReadString('\n')
		if err != nil && !errors.Is(err, io.EOF) {
			fmt.Fprintf(stderr, "Failed to read selection: %v\n", err)
			return 0, 1, false
		}

		choice := strings.TrimSpace(line)
		if choice == "" {
			if errors.Is(err, io.EOF) {
				fmt.Fprintln(stderr, "No selection provided.")
				return 0, 1, false
			}
			continue
		}
		if choice == "q" || choice == "quit" {
			fmt.Fprintln(stdout, "Cancelled.")
			return 0, 0, false
		}

		index, convErr := strconv.Atoi(choice)
		if convErr == nil && index >= 1 && index <= len(labels) {
			return index - 1, 0, true
		}

		fmt.Fprintf(stderr, "Invalid selection %q. Choose 1-%d or q to quit.\n", choice, len(labels))
		if errors.Is(err, io.EOF) {
			return 0, 1, false
		}
	}
}

func interactiveOptions(cfg Config, name string) []interactiveOption {
	sourceCfg := cfg.Sources[name]
	var options []interactiveOption
	if validateSourceUsage(sourceCfg, false) == nil {
		options = append(options, interactiveOption{
			command:     "crs " + name,
			description: "Pull from " + name,
			sourceName:  name,
		})
	}
	if validateSourceUsage(sourceCfg, true) == nil {
		options = append(options, interactiveOption{
			command:     "crs -r " + name,
			description: "Push to " + name,
			reverse:     true,
			sourceName:  name,
		})
	}
	return options
}

func (a application) input() io.Reader {
	if a.in != nil {
		return a.in
	}
	return os.Stdin
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
	fmt.Fprintln(w, "Usage: crs")
	fmt.Fprintln(w, "       crs [--config PATH] [-r] <source>")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Sync clipboard content between the local machine and a configured source.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "With no arguments, crs shows an interactive menu of configured source actions.")
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
