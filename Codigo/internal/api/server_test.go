package api

import (
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
)

func TestIsStillImagePath(t *testing.T) {
	for _, path := range []string{"photo.jpg", "PHOTO.JPEG", "card.png", "notice.webp", "frame.bmp"} {
		if !isStillImagePath(path) {
			t.Fatalf("expected %q to be detected as a still image", path)
		}
	}
	for _, path := range []string{"clip.mp4", "transition.webm", "animation.gif"} {
		if isStillImagePath(path) {
			t.Fatalf("expected %q to keep the video conversion path", path)
		}
	}
}

func TestRequiredOutputModeForDelayArmMode(t *testing.T) {
	tests := map[string]string{
		"short-loading": "encoded",
		"loading":       "encoded",
		"buffer":        "copy",
	}
	for armMode, want := range tests {
		if got := requiredOutputModeForDelayArmMode(armMode); got != want {
			t.Fatalf("arm mode %q selected %q, want %q", armMode, got, want)
		}
	}
}

func TestMediaKindFromConvertedName(t *testing.T) {
	tests := map[string]string{
		"loading_imagem_1920x1080_60fps_6000k_2s_20260712_001.flv": "image",
		"loading_video_1920x1080_60fps_6000k_2s_20260712_001.flv":  "video",
		"loading_1920x1080_60fps_6000k_2s_20260712_001.flv":        "loading",
	}
	for name, want := range tests {
		if got := mediaKindFromName(name); got != want {
			t.Fatalf("mediaKindFromName(%q) = %q, want %q", name, got, want)
		}
	}
}

func TestNextReadyVideoPathIncludesMediaKind(t *testing.T) {
	dir := t.TempDir()
	request := conversionRequest{
		profile:         conversionProfile{Width: 1920, Height: 1080, FPS: 60, VideoBitrate: "6000k"},
		durationSeconds: 2,
		mediaKind:       "image",
	}
	path, err := nextReadyVideoPath(dir, request)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(strings.ToLower(filepath.Base(path)), "loading_imagem_") {
		t.Fatalf("image output name = %q, want loading_imagem_ prefix", path)
	}
}

func TestImageConversionMessagesIdentifyImage(t *testing.T) {
	if got := conversionRunningMessage("image"); !strings.Contains(got, "imagem") {
		t.Fatalf("running message = %q, want image reference", got)
	}
	if got := conversionDoneMessage("image", true); !strings.HasPrefix(got, "Imagem") {
		t.Fatalf("done message = %q, want image reference", got)
	}
}

func TestEncodedRelayUsesBoundedCatchup(t *testing.T) {
	args := encodedRelayArgs(appSettings{OutputMode: "encoded", EncodedFPS: 60}, "rtmp://example.invalid/live", "", true)
	for i := 0; i+1 < len(args); i++ {
		if args[i] == "-readrate_catchup" {
			if args[i+1] != "1.05" {
				t.Fatalf("readrate catchup = %q, want 1.05", args[i+1])
			}
			return
		}
	}
	t.Fatal("encoded relay args missing -readrate_catchup")
}

func TestCopyRelayPreservesEncodedPackets(t *testing.T) {
	args := encodedRelayArgs(appSettings{OutputMode: "copy"}, "rtmp://example.invalid/live", "", true)
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, "-c copy") {
		t.Fatalf("copy relay must use codec copy: %s", joined)
	}
	for _, encoder := range []string{"h264_amf", "h264_nvenc", "h264_qsv", "libx264"} {
		if strings.Contains(joined, encoder) {
			t.Fatalf("copy relay unexpectedly uses %s: %s", encoder, joined)
		}
	}
}

func TestEncodedQualityPresetsAvoidLowestEfficiencyModes(t *testing.T) {
	nvenc := strings.Join(encodedVideoEncoderArgs("h264_nvenc", 120, "6000k", "latency", "strict"), " ")
	if !strings.Contains(nvenc, "-preset p4") || strings.Contains(nvenc, "-preset p1") {
		t.Fatalf("NVENC quality protection missing: %s", nvenc)
	}
	x264 := strings.Join(encodedVideoEncoderArgs("libx264", 120, "6000k", "latency", "strict"), " ")
	if !strings.Contains(x264, "-preset veryfast") || strings.Contains(x264, "-preset ultrafast") {
		t.Fatalf("x264 quality protection missing: %s", x264)
	}
}

func TestSettingsNormalizeHotkeyDefaults(t *testing.T) {
	settings := normalizeSettings(appSettings{})
	if settings.HotkeyArm != "Ctrl+Alt+D" || settings.HotkeyLive != "Ctrl+Alt+A" {
		t.Fatalf("unexpected hotkey defaults: %q / %q", settings.HotkeyArm, settings.HotkeyLive)
	}
}

func TestFirstUseDefaultsToBufferCutAndCopy(t *testing.T) {
	settings := normalizeSettings(appSettings{})
	if settings.Mode != "twitch" {
		t.Fatalf("first-use mode = %q, want twitch", settings.Mode)
	}
	if settings.DelayArmMode != "buffer" || settings.OutputMode != "copy" {
		t.Fatalf("first-use transition = %q/%q, want buffer/copy", settings.DelayArmMode, settings.OutputMode)
	}
}

func TestScaleQualityChoices(t *testing.T) {
	tests := map[string]string{
		"fast":     "fast_bilinear",
		"balanced": "bicubic",
		"sharp":    "lanczos",
	}
	for choice, expected := range tests {
		if got := scaleFilterFlag(choice); got != expected {
			t.Fatalf("scaleFilterFlag(%q) = %q, want %q", choice, got, expected)
		}
	}
}

func TestSettingsSaveResponseKeepsSettingsAtTopLevel(t *testing.T) {
	data, err := json.Marshal(settingsSaveResponse{
		appSettings:       appSettings{OK: true, OutputMode: "encoded", DelayArmMode: "short-loading"},
		OutputModeChanged: true,
		OutputRestarted:   true,
	})
	if err != nil {
		t.Fatal(err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded["outputMode"] != "encoded" || decoded["delayArmMode"] != "short-loading" || decoded["outputRestarted"] != true {
		t.Fatalf("unexpected response JSON: %s", data)
	}
}
