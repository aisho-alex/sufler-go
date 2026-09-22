package asr

import (
	"fmt"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	whisper "github.com/ggerganov/whisper.cpp/bindings/go/pkg/whisper"
)

type Result struct {
	Source  string
	TsStart float64
	TsEnd   float64
	Text    string
	Latency float64
}

type ASR struct {
	Model       string
	Language    string
	BeamSize    int
	Device      string
	ComputeType string

	OnResult func(Result)
	Log      func(string)

	model    whisper.Model
	ctx      whisper.Context
	q        chan job
	stop     chan struct{}
	done     chan struct{}
	once     sync.Once
	mu       sync.Mutex
	started  bool
	stopping atomic.Bool
}

type job struct {
	seg       []float32
	tsStart   float64
	tsEnd     float64
	source    string
	submitted float64
}

func now() float64 { return float64(time.Now().UnixNano()) / 1e9 }

func New(model, language string, beamSize int, onResult func(Result),
	log func(string)) *ASR {
	if beamSize <= 0 {
		beamSize = 1
	}
	return &ASR{
		Model:    model,
		Language: language,
		BeamSize: beamSize,
		OnResult: onResult,
		Log:      log,
		q:        make(chan job, 512),
		stop:     make(chan struct{}),
		done:     make(chan struct{}),
	}
}

func (a *ASR) logf(format string, args ...any) {
	if a.Log != nil {
		a.Log("[asr] " + fmt.Sprintf(format, args...))
	}
}

func (a *ASR) ModelPath(modelsDir string) string {
	return filepath.Join(modelsDir, "ggml-"+a.Model+".bin")
}

func (a *ASR) Start(modelsDir string) error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.started {
		return nil
	}
	path := a.ModelPath(modelsDir)
	if _, err := os.Stat(path); err != nil {
		return fmt.Errorf("модель не найдена: %s", path)
	}
	t0 := time.Now()
	model, err := whisper.New(path)
	if err != nil {
		return fmt.Errorf("загрузка модели: %w", err)
	}
	a.model = model
	ctx, err := model.NewContext()
	if err != nil {
		model.Close()
		return fmt.Errorf("контекст: %w", err)
	}
	if a.Language != "" && a.Language != "auto" {
		if err := ctx.SetLanguage(a.Language); err != nil {
			model.Close()
			return fmt.Errorf("язык: %w", err)
		}
	}
	ctx.SetBeamSize(a.BeamSize)
	ctx.SetMaxContext(0)
	ctx.SetTemperature(0)
	ctx.SetTemperatureFallback(0)
	ctx.SetThreads(uint(runtime.NumCPU()))
	a.ctx = ctx
	a.started = true
	a.logf("модель '%s' загружена за %.1f с (%s)",
		a.Model, time.Since(t0).Seconds(), a.Device)

	go a.loop()
	return nil
}

func (a *ASR) Submit(seg []float32, tsStart, tsEnd float64, source string) {
	select {
	case a.q <- job{seg: seg, tsStart: tsStart, tsEnd: tsEnd,
		source: source, submitted: now()}:
	default:
		a.logf("очередь переполнена, сегмент пропущен")
	}
}

func (a *ASR) loop() {
	defer close(a.done)
	for {
		select {
		case <-a.stop:
			return
		case j := <-a.q:
			a.process(j)
		}
	}
}

func (a *ASR) process(j job) {
	defer func() {
		if r := recover(); r != nil {
			a.logf("ошибка распознавания: %v", r)
		}
	}()
	t0 := time.Now()
	var text string
	abort := func() bool { return a.stopping.Load() }
	err := a.ctx.Process(j.seg, abort, nil, nil)
	if err != nil {
		if a.stopping.Load() {
			return
		}
		a.logf("ошибка распознавания: %v", err)
		return
	}
	for {
		s, err := a.ctx.NextSegment()
		if err == io.EOF {
			break
		}
		if err != nil {
			a.logf("ошибка сегмента: %v", err)
			break
		}
		text += s.Text
	}
	text = strings.TrimSpace(text)
	inferS := time.Since(t0).Seconds()
	if text == "" {
		return
	}
	a.logf("%s: %.2f c, %d симв.", j.source, inferS, len([]rune(text)))
	if a.OnResult != nil {
		a.OnResult(Result{
			Source:  j.source,
			TsStart: j.tsStart,
			TsEnd:   j.tsEnd,
			Text:    text,
			Latency: now() - j.submitted,
		})
	}
}

func (a *ASR) Stop() {
	a.once.Do(func() {
		a.stopping.Store(true)
		close(a.stop)
	})
	select {
	case <-a.done:
	case <-time.After(30 * time.Second):
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.model != nil {
		a.model.Close()
		a.model = nil
	}
}
