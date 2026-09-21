package capture

import (
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"
)

type pwInfo struct {
	Props map[string]any `json:"props"`
}

type pwMetadataEntry struct {
	Key   string          `json:"key"`
	Value json.RawMessage `json:"value"`
}

func (e *pwMetadataEntry) name() string {
	if len(e.Value) == 0 {
		return ""
	}
	var v struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(e.Value, &v); err != nil {
		return ""
	}
	return v.Name
}

type pwObject struct {
	Type     string            `json:"type"`
	Props    map[string]any    `json:"props"`
	Info     *pwInfo           `json:"info"`
	Metadata []pwMetadataEntry `json:"metadata"`
}

func getStr(m map[string]any, key string) string {
	if m == nil {
		return ""
	}
	s, _ := m[key].(string)
	return s
}

func (o *pwObject) mergedProps() map[string]any {
	p := map[string]any{}
	for k, v := range o.Props {
		p[k] = v
	}
	if o.Info != nil {
		for k, v := range o.Info.Props {
			p[k] = v
		}
	}
	return p
}

func pwDump() ([]pwObject, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "pw-dump", "-N").Output()
	if err != nil {
		return nil, err
	}
	var objs []pwObject
	if err := json.Unmarshal(out, &objs); err != nil {
		return nil, err
	}
	return objs, nil
}

func Props() (sink, source string) {
	objs, err := pwDump()
	if err != nil {
		return "", ""
	}
	sinkNames := map[string]bool{}
	sourceNames := map[string]bool{}
	for i := range objs {
		o := &objs[i]
		if o.Type != "PipeWire:Interface:Node" {
			continue
		}
		p := o.mergedProps()
		name := getStr(p, "node.name")
		if name == "" {
			continue
		}
		switch getStr(p, "media.class") {
		case "Audio/Sink":
			sinkNames[name] = true
		case "Audio/Source":
			sourceNames[name] = true
		}
	}
	for i := range objs {
		o := &objs[i]
		if o.Type != "PipeWire:Interface:Metadata" {
			continue
		}
		if getStr(o.mergedProps(), "metadata.name") != "default" {
			continue
		}
		for _, s := range o.Metadata {
			switch s.Key {
			case "default.audio.sink":
				if n := s.name(); n != "" && sinkNames[n] {
					sink = n
				}
			case "default.audio.source":
				if n := s.name(); n != "" && sourceNames[n] {
					source = n
				}
			}
		}
	}
	if source == "" && len(sourceNames) > 0 {
		names := make([]string, 0, len(sourceNames))
		for n := range sourceNames {
			names = append(names, n)
		}
		sort.Strings(names)
		source = names[0]
	}
	return sink, source
}

func PulseSourceName(label string) string {
	sink, source := Props()
	if label == "monitor" {
		if sink == "" {
			return ""
		}
		return sink + ".monitor"
	}
	return source
}

type pwNode struct {
	Class string
	Name  string
}

func pwNodes() []pwNode {
	objs, err := pwDump()
	if err != nil {
		return nil
	}
	var out []pwNode
	for i := range objs {
		o := &objs[i]
		if o.Type != "PipeWire:Interface:Node" {
			continue
		}
		p := o.mergedProps()
		name := getStr(p, "node.name")
		if name == "" {
			continue
		}
		if c := getStr(p, "media.class"); c == "Audio/Sink" || c == "Audio/Source" {
			out = append(out, pwNode{Class: c, Name: name})
		}
	}
	return out
}

func ListDevices() string {
	var b strings.Builder
	b.WriteString("=== PipeWire (pw-dump) ===\n")
	sink, source := Props()
	fmt.Fprintf(&b, "default sink:   %s\n", sink)
	fmt.Fprintf(&b, "default source: %s\n", source)
	for _, n := range pwNodes() {
		role := "sink  "
		if n.Class == "Audio/Source" {
			role = "source"
		}
		fmt.Fprintf(&b, "  [%s] %s\n", role, n.Name)
	}
	return b.String()
}

type OnAudioFunc func(label string, block []float32)

type source interface {
	Start(onAudio OnAudioFunc) error
	Stop()
	Alive() bool
	Name() string
}

type FfmpegPulseSource struct {
	Label      string
	Source     string
	Samplerate int
	BlockMs    int

	mu   sync.Mutex
	cmd  *exec.Cmd
	dead chan struct{}
}

func NewFfmpegPulseSource(label string, pulseSource string,
	samplerate, blockMs int) *FfmpegPulseSource {
	return &FfmpegPulseSource{
		Label:      label,
		Source:     pulseSource,
		Samplerate: samplerate,
		BlockMs:    blockMs,
	}
}

func (s *FfmpegPulseSource) Start(onAudio OnAudioFunc) error {
	src := s.Source
	if src == "" {
		src = PulseSourceName(s.Label)
		if src == "" {
			return fmt.Errorf("pulse-источник для '%s' не найден", s.Label)
		}
		s.Source = src
	}
	frameSamples := s.Samplerate * s.BlockMs / 1000
	frameBytes := frameSamples * 4

	cmd := exec.Command("ffmpeg",
		"-v", "error", "-nostdin",
		"-f", "pulse", "-i", src,
		"-f", "f32le", "-acodec", "pcm_f32le",
		"-ac", "1", "-ar", fmt.Sprint(s.Samplerate),
		"-")
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	cmd.Stderr = io.Discard
	if err := cmd.Start(); err != nil {
		return err
	}

	s.mu.Lock()
	s.cmd = cmd
	s.dead = make(chan struct{})
	dead := s.dead
	s.mu.Unlock()

	go func() {
		cmd.Wait()
		close(dead)
	}()
	go func() {
		raw := make([]byte, frameBytes)
		for {
			if _, err := io.ReadFull(stdout, raw); err != nil {
				return
			}
			block := make([]float32, frameSamples)
			for i := range block {
				block[i] = math.Float32frombits(
					binary.LittleEndian.Uint32(raw[i*4:]))
			}
			onAudio(s.Label, block)
		}
	}()
	return nil
}

func (s *FfmpegPulseSource) Stop() {
	s.mu.Lock()
	cmd, dead := s.cmd, s.dead
	s.mu.Unlock()
	if cmd == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	if dead != nil {
		select {
		case <-dead:
		case <-time.After(2 * time.Second):
			_ = cmd.Process.Kill()
		}
	}
	s.mu.Lock()
	s.cmd = nil
	s.mu.Unlock()
}

func (s *FfmpegPulseSource) Alive() bool {
	s.mu.Lock()
	cmd, dead := s.cmd, s.dead
	s.mu.Unlock()
	if cmd == nil || dead == nil {
		return false
	}
	select {
	case <-dead:
		return false
	default:
		return true
	}
}

func (s *FfmpegPulseSource) Name() string { return s.Source }

type AudioStream struct {
	Label         string
	Backend       string
	MicDevice     *int
	MonitorDevice *int
	Samplerate    int
	BlockMs       int
	OnAudio       OnAudioFunc
	Log           func(string)

	src     source
	running bool
	stopCh  chan struct{}
	done    chan struct{}
}

func NewAudioStream(label, backend string, samplerate, blockMs int,
	onAudio OnAudioFunc, log func(string)) *AudioStream {
	return &AudioStream{
		Label:      label,
		Backend:    backend,
		Samplerate: samplerate,
		BlockMs:    blockMs,
		OnAudio:    onAudio,
		Log:        log,
	}
}

func (s *AudioStream) Start() {
	if s.running {
		return
	}
	s.running = true
	s.stopCh = make(chan struct{})
	s.done = make(chan struct{})
	go s.supervise()
}

func (s *AudioStream) Stop() {
	if !s.running {
		return
	}
	s.running = false
	close(s.stopCh)
	if s.done != nil {
		<-s.done
	}
	if s.src != nil {
		s.src.Stop()
		s.src = nil
	}
}

func (s *AudioStream) logf(format string, a ...any) {
	if s.Log != nil {
		s.Log("[" + s.Label + "] " + fmt.Sprintf(format, a...))
	}
}

func (s *AudioStream) sleep(d time.Duration) bool {
	select {
	case <-s.stopCh:
		return false
	case <-time.After(d):
		return true
	}
}

func (s *AudioStream) resolve() source {
	if s.Backend == "sounddevice" {
		s.logf("backend sounddevice не поддержан, используется ffmpeg")
	}
	return NewFfmpegPulseSource(s.Label, "", s.Samplerate, s.BlockMs)
}

func (s *AudioStream) supervise() {
	defer close(s.done)
	var checkTs time.Time
	for s.running {
		if s.src == nil || !s.src.Alive() {
			checkTs = time.Time{}
			if s.src != nil {
				s.src.Stop()
				s.src = nil
				s.logf("поток упал, переподключение...")
			}
			src := s.resolve()
			if err := src.Start(s.OnAudio); err != nil {
				s.logf("устройство не найдено, ретрай через 3 с (%v)", err)
				if !s.sleep(3 * time.Second) {
					return
				}
				continue
			}
			s.src = src
			name := src.Name()
			if name == "" {
				name = "ffmpeg"
			}
			s.logf("захват запущен (%s)", name)
		} else if s.Label == "monitor" {
			if time.Since(checkTs) >= 10*time.Second {
				checkTs = time.Now()
				ffs, ok := s.src.(*FfmpegPulseSource)
				if ok && ffs.Source != "" {
					want := PulseSourceName("monitor")
					if want != "" && want != ffs.Source {
						s.logf("устройство вывода сменилось, переключение...")
						s.src.Stop()
						s.src = nil
						continue
					}
				}
			}
		}
		if !s.sleep(500 * time.Millisecond) {
			return
		}
	}
}
