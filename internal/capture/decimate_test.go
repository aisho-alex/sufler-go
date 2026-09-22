package capture

import (
	"math"
	"testing"
)

func sine48k(freq float64, seconds float64) []byte {
	n := int(48000 * seconds)
	raw := make([]byte, n*2)
	for i := 0; i < n; i++ {
		v := int16(16000 * math.Sin(2*math.Pi*freq*float64(i)/48000))
		raw[i*2] = byte(uint16(v))
		raw[i*2+1] = byte(uint16(v) >> 8)
	}
	return raw
}

func TestDecimate48to16Length(t *testing.T) {
	raw := make([]byte, 24000)
	out := decimate48to16(raw)
	if len(out) != 4000 {
		t.Fatalf("len = %d, хочу 4000", len(out))
	}
	if len(decimate48to16(raw[:17])) != 2 {
		t.Fatal("хвост < 6 байт должен отбрасываться")
	}
}

func TestDecimate48to16Sine(t *testing.T) {
	const freq = 440.0
	raw := sine48k(freq, 1.0)
	out := decimate48to16(raw)
	if len(out) != 16000 {
		t.Fatalf("len = %d", len(out))
	}
	maxDiff := 0.0
	for i, v := range out {
		// центр окна усреднения — на 1/3 сэмпла 16 кГц позже
		want := 16000.0 / 32768.0 *
			math.Sin(2*math.Pi*freq*(float64(i)+1.0/3.0)/16000)
		d := math.Abs(float64(v) - want)
		if d > maxDiff {
			maxDiff = d
		}
	}
	if maxDiff > 0.005 {
		t.Fatalf("макс. расхождение с эталонным синусом: %.4f", maxDiff)
	}
}

func TestDecimate48to16Silence(t *testing.T) {
	out := decimate48to16(make([]byte, 6000))
	for i, v := range out {
		if v != 0 {
			t.Fatalf("out[%d] = %v, хочу 0", i, v)
		}
	}
}
