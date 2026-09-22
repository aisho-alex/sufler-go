//go:build windows

package capture

import (
	"fmt"
	"os/exec"
	"strings"
)

func wasapiDeviceNames() []string {
	return nil
}

type wasapiSource struct {
	label      string
	samplerate int
	blockMs    int
}

func newWasapiSource(label string, samplerate, blockMs int) *wasapiSource {
	return &wasapiSource{label: label, samplerate: samplerate, blockMs: blockMs}
}

func (s *wasapiSource) Start(onAudio OnAudioFunc) error {
	return fmt.Errorf("wasapi-захват ещё не реализован (W1)")
}

func (s *wasapiSource) Stop() {}

func (s *wasapiSource) Alive() bool { return false }

func (s *wasapiSource) Name() string {
	if s.label == "monitor" {
		return "wasapi:loopback"
	}
	return "wasapi:mic"
}

func ffmpegAvailable() bool {
	_, err := exec.LookPath("ffmpeg")
	return err == nil
}

func ListDevices() string {
	var b strings.Builder
	b.WriteString("=== WASAPI (sufler) ===\n")
	names := wasapiDeviceNames()
	if len(names) == 0 {
		b.WriteString("  (энумерация появится в W1)\n")
	}
	b.WriteString(strings.Join(names, "\n"))
	if len(names) > 0 {
		b.WriteString("\n")
	}
	if ffmpegAvailable() {
		b.WriteString("=== DirectShow (ffmpeg -list_devices) ===\n")
		cmd := exec.Command("ffmpeg", "-hide_banner", "-loglevel", "info",
			"-list_devices", "true", "-f", "dshow", "-i", "dummy")
		out, _ := cmd.CombinedOutput()
		for _, line := range strings.Split(string(out), "\n") {
			if strings.Contains(line, "DirectShow audio") ||
				strings.Contains(line, "(audio") {
				b.WriteString(strings.TrimSpace(line) + "\n")
			}
		}
	}
	return b.String()
}

func (s *AudioStream) resolve() source {
	return newWasapiSource(s.Label, s.Samplerate, s.BlockMs)
}

func (s *AudioStream) defaultSourceName() string { return s.src.Name() }

func (s *AudioStream) checkDeviceChange() bool { return false }
