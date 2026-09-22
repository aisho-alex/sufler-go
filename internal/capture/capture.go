package capture

import (
	"fmt"
	"time"
)

type OnAudioFunc func(label string, block []float32)

type source interface {
	Start(onAudio OnAudioFunc) error
	Stop()
	Alive() bool
	Name() string
}

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

func (s *AudioStream) supervise() {
	defer close(s.done)
	checkTs := time.Now()
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
				name = s.defaultSourceName()
			}
			s.logf("захват запущен (%s)", name)
		} else if s.Label == "monitor" {
			if time.Since(checkTs) >= 10*time.Second {
				checkTs = time.Now()
				if s.checkDeviceChange() {
					s.logf("устройство вывода сменилось, переключение...")
					s.src.Stop()
					s.src = nil
					continue
				}
			}
		}
		if !s.sleep(500 * time.Millisecond) {
			return
		}
	}
}
