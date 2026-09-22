package vad

import (
	"encoding/binary"
	"fmt"
	"math"
	"os"
	"sync"
	"testing"
)

func loadF32(t *testing.T, path string) []float32 {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Skipf("тестовый файл недоступен: %v", err)
	}
	n := len(raw) / 4
	out := make([]float32, n)
	for i := 0; i < n; i++ {
		out[i] = math.Float32frombits(binary.LittleEndian.Uint32(raw[i*4:]))
	}
	return out
}

func TestSegmenterParity(t *testing.T) {
	audio := loadF32(t, "testdata/vad_combo.f32")
	if audio == nil {
		return
	}
	model, err := NewModel(ModelsDir())
	if err != nil {
		t.Fatal(err)
	}
	defer model.Destroy()

	var mu sync.Mutex
	var segLens []int
	seg := NewSegmenter(model, 0.5, 250, 600, 15000, 120, 16000,
		func(s []float32, tsStart, tsEnd float64) {
			mu.Lock()
			segLens = append(segLens, len(s))
			mu.Unlock()
		})
	seg.Log = func(msg string) { t.Log(msg) }
	block := 4000
	for i := 0; i < len(audio); i += block {
		end := i + block
		if end > len(audio) {
			end = len(audio)
		}
		seg.Feed(audio[i:end])
	}
	seg.Flush()

	t.Logf("go segments: %d %v", len(segLens), segLens)
	if len(segLens) != 1 {
		t.Fatalf("ожидался 1 сегмент, получено %d", len(segLens))
	}
	const etalon = 124032
	diff := segLens[0] - etalon
	if diff < 0 {
		diff = -diff
	}
	if diff > 5*HOP {
		t.Fatalf("расхождение с эталоном: %d сэмплов (%d хопов)",
			diff, diff/HOP)
	}
	fmt.Printf("parity ok: %d vs %d сэмплов (Δ=%d)\n", segLens[0], etalon, diff)
}
