package asr

import (
	"encoding/binary"
	"math"
	"os"
	"testing"
	"time"

	"sufler-go/internal/vad"
)

func loadF32(t *testing.T, path string) []float32 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("тестовый файл недоступен: %v", err)
	}
	out := make([]float32, len(raw)/4)
	for i := range out {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return out
}

func TestTranscribeBenchmark(t *testing.T) {
	audio := loadF32(t, "/tmp/opencode/vad_combo.f32")
	if audio == nil {
		return
	}
	done := make(chan Result, 16)
	a := New("large-v3-turbo", "ru", 1,
		func(r Result) { done <- r },
		func(msg string) { t.Log(msg) })

	start := time.Now()
	if err := a.Start(vad.ModelsDir()); err != nil {
		t.Fatal(err)
	}
	t.Logf("init (в т.ч. CUDA warmup при первом прогоне): %s",
		time.Since(start).Round(time.Millisecond))

	a.Submit(audio, 0, float64(len(audio))/16000, "test")
	select {
	case r := <-done:
		audioDur := float64(len(audio)) / 16000
		t.Logf("RTF: %.3f (инференс %.2f c на %.2f c аудио)",
			r.Latency/audioDur, r.Latency, audioDur)
		t.Logf("текст: %s", r.Text)
		if r.Text == "" {
			t.Fatal("пустой транскрипт")
		}
	case <-time.After(120 * time.Second):
		t.Fatal("таймаут инференса")
	}

	a.Submit(audio, 0, float64(len(audio))/16000, "test2")
	select {
	case r := <-done:
		t.Logf("[прогрев] RTF: %.3f, текст: %s", r.Latency/(float64(len(audio))/16000), r.Text)
	case <-time.After(120 * time.Second):
		t.Fatal("таймаут инференса 2")
	}
	a.Stop()
}
