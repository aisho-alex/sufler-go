package capture

import (
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestListDevices(t *testing.T) {
	out := ListDevices()
	t.Log("\n" + out)
	if !strings.Contains(out, "default sink") {
		t.Fatal("pw-dump не вернул свойства устройств")
	}
}

func TestLiveMonitorCapture(t *testing.T) {
	if testing.Short() {
		t.Skip("живой захват пропущен в short-режиме")
	}
	var blocks, samples atomic.Int64
	stream := NewAudioStream("monitor", "auto", 16000, 250,
		func(label string, block []float32) {
			blocks.Add(1)
			samples.Add(int64(len(block)))
		},
		func(msg string) { t.Log(msg) })
	stream.Start()
	time.Sleep(2 * time.Second)
	stream.Stop()

	if blocks.Load() == 0 {
		t.Fatal("за 2 с не получено ни одного блока")
	}
	rate := float64(samples.Load()) / 2.0
	if rate < 12000 {
		t.Fatalf("темп сэмплов слишком низкий: %.0f Гц вместо ~16000", rate)
	}
	t.Logf("принято %d блоков, %d сэмплов (~%.0f Гц)",
		blocks.Load(), samples.Load(), rate)
}
