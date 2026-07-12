package config

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"delayengine/internal/secret"
)

type Config struct {
	InputURL          string
	OutputURL         string
	HTTPAddr          string
	MaxBufferDuration time.Duration
	ReadTimeout       time.Duration
	WriteTimeout      time.Duration
	FixedDelay        time.Duration
	DelayEnabled      bool
}

func Load() (Config, error) {
	root := RuntimeRoot()
	settings := loadSettings(root)
	readTimeout, err := durationFromEnv("DELAYENGINE_READ_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := durationFromEnv("DELAYENGINE_WRITE_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	fixedDelay, err := durationFromEnv("DELAYENGINE_FIXED_DELAY", 0)
	if err != nil {
		return Config{}, err
	}

	localSourcePath := localSourcePathFromSettings(root, settings.LocalSourcePath)
	if settings.OutputMode == "" || settings.OutputMode == "direct" {
		settings.OutputMode = "copy"
	}
	if settings.OutputMode != "encoded" && settings.OutputMode != "copy" {
		settings.OutputMode = "copy"
	}
	inputURL := getenv("DELAYENGINE_INPUT_URL", settings.InputURL)
	if inputURL == "" || strings.HasSuffix(inputURL, "/live/teste") || isLocalRTMPInput(inputURL) {
		inputURL = "rtmp://127.0.0.1:1935/" + localSourcePath
	}

	outputURL := getenv("DELAYENGINE_OUTPUT_URL", "")
	if outputURL == "" && (settings.OutputMode == "encoded" || settings.OutputMode == "copy") {
		outputURL = settings.EncodedLocalOutputURL
		if outputURL == "" {
			outputURL = "rtmp://127.0.0.1:1935/live/delayengine-out"
		}
	}
	if outputURL == "" && settings.OutputMode == "direct" {
		outputURL = twitchOutputURL(root, settings.TwitchServer)
	}
	if outputURL == "" {
		outputURL = settings.OutputURL
	}
	if outputURL == "" {
		outputURL = "rtmp://127.0.0.1:1935/live/delayed"
	}

	return Config{
		InputURL:          inputURL,
		OutputURL:         outputURL,
		HTTPAddr:          getenv("DELAYENGINE_HTTP_ADDR", ":8080"),
		MaxBufferDuration: time.Hour,
		ReadTimeout:       readTimeout,
		WriteTimeout:      writeTimeout,
		FixedDelay:        fixedDelay,
		DelayEnabled:      boolFromEnv("DELAYENGINE_DELAY_ENABLED", false),
	}, nil
}

type settingsFile struct {
	InputURL              string `json:"inputUrl"`
	LocalSourcePath       string `json:"localSourcePath"`
	TwitchServer          string `json:"twitchServer"`
	OutputURL             string `json:"outputUrl"`
	OutputMode            string `json:"outputMode"`
	EncodedLocalOutputURL string `json:"encodedLocalOutputUrl"`
}

func loadSettings(root string) settingsFile {
	data, err := os.ReadFile(filepath.Join(root, "settings.json"))
	if err != nil {
		return settingsFile{}
	}
	data = bytes.TrimPrefix(data, []byte{0xEF, 0xBB, 0xBF})
	var settings settingsFile
	_ = json.Unmarshal(data, &settings)
	return settings
}

func twitchOutputURL(root string, server string) string {
	key, err := secret.Read(root)
	if err != nil {
		return ""
	}
	if key == "" {
		return ""
	}
	if server == "" {
		server = "rtmp://live.twitch.tv:1935/app"
	}
	return strings.TrimRight(server, "/") + "/" + key
}

func localSourcePathFromSettings(root string, value string) string {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if value != "" && value != "live/teste" && !isGeneratedLocalSourcePath(value) {
		_ = os.WriteFile(filepath.Join(root, ".local-stream-name"), []byte(value), 0600)
		return value
	}
	return defaultLocalSourcePath(root)
}

func defaultLocalSourcePath(root string) string {
	path := filepath.Join(root, ".local-stream-name")
	if data, err := os.ReadFile(path); err == nil {
		value := strings.Trim(strings.TrimSpace(string(data)), "/")
		if value != "" && value != "live/teste" && !isGeneratedLocalSourcePath(value) {
			return value
		}
	}

	name := "live/delayengine"
	_ = os.WriteFile(path, []byte(name), 0600)
	return name
}

func isGeneratedLocalSourcePath(value string) bool {
	value = strings.Trim(strings.TrimSpace(value), "/")
	if !strings.HasPrefix(value, "live/delayengine-") {
		return false
	}
	suffix := strings.TrimPrefix(value, "live/delayengine-")
	return len(suffix) >= 3 && len(suffix) <= 8
}

func isLocalRTMPInput(rawURL string) bool {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return false
	}
	host := parsed.Hostname()
	return parsed.Scheme == "rtmp" && (host == "127.0.0.1" || host == "localhost")
}

func RuntimeRoot() string {
	if envRoot := os.Getenv("DELAYENGINE_ROOT"); envRoot != "" {
		return envRoot
	}
	if cwd, err := os.Getwd(); err == nil {
		if hasRuntimeDirs(cwd) {
			return cwd
		}
		if hasRuntimeDirs(filepath.Dir(cwd)) {
			return filepath.Dir(cwd)
		}
	}
	if exe, err := os.Executable(); err == nil {
		exeDir := filepath.Dir(exe)
		if hasRuntimeDirs(exeDir) {
			return exeDir
		}
		if hasRuntimeDirs(filepath.Dir(exeDir)) {
			return filepath.Dir(exeDir)
		}
		return exeDir
	}
	return "."
}

func hasRuntimeDirs(path string) bool {
	if path == "" || path == "." {
		return false
	}
	if _, err := os.Stat(filepath.Join(path, "videos")); err == nil {
		return true
	}
	if _, err := os.Stat(filepath.Join(path, "web")); err == nil {
		return true
	}
	return false
}

func getenv(key string, fallback string) string {
	value := os.Getenv(key)
	if value == "" {
		return fallback
	}
	return value
}

func boolFromEnv(key string, fallback bool) bool {
	value := os.Getenv(key)
	switch value {
	case "1", "true", "TRUE", "yes", "YES", "on", "ON":
		return true
	case "0", "false", "FALSE", "no", "NO", "off", "OFF":
		return false
	default:
		return fallback
	}
}

func durationFromEnv(key string, fallback time.Duration) (time.Duration, error) {
	value := os.Getenv(key)
	if value == "" {
		return fallback, nil
	}

	duration, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid %s: %w", key, err)
	}

	return duration, nil
}

func RedactURLForLog(rawURL string) string {
	parsed, err := url.Parse(rawURL)
	if err != nil {
		return "<invalid-url>"
	}
	if parsed.User != nil {
		parsed.User = url.User("redacted")
	}
	parts := strings.Split(parsed.Path, "/")
	if len(parts) > 0 && parts[len(parts)-1] != "" {
		parts[len(parts)-1] = "redacted"
		parsed.Path = strings.Join(parts, "/")
	}
	parsed.RawQuery = ""
	return parsed.String()
}
