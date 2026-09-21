package vad

import (
	"time"
)

type Segmenter struct {
	model *Model

	threshold    float64
	minSpeechMs  int
	minSilenceMs int
	maxSegmentMs int
	padMs        int
	sr           int

	onSegment func(seg []float32, tsStart, tsEnd float64)
	Log       func(string)

	rest      []float32
	buf       []float32
	tail      []float32
	tailMax   int
	inSpeech  bool
	speechMs  float64
	silenceMs float64
}

func NewSegmenter(model *Model, threshold float64, minSpeechMs, minSilenceMs,
	maxSegmentMs, padMs, samplerate int,
	onSegment func(seg []float32, tsStart, tsEnd float64)) *Segmenter {
	return &Segmenter{
		model:        model,
		threshold:    threshold,
		minSpeechMs:  minSpeechMs,
		minSilenceMs: minSilenceMs,
		maxSegmentMs: maxSegmentMs,
		padMs:        padMs,
		sr:           samplerate,
		onSegment:    onSegment,
		tailMax:      int(0.4 * float64(samplerate)),
	}
}

func (s *Segmenter) Feed(block []float32) {
	s.rest = append(s.rest, block...)
	nFull := (len(s.rest) / HOP) * HOP
	if nFull == 0 {
		return
	}
	audio := make([]float32, nFull)
	copy(audio, s.rest)
	rest := len(s.rest) - nFull
	copy(s.rest, s.rest[nFull:])
	s.rest = s.rest[:rest]

	probs, err := s.model.Probs(audio)
	if err != nil {
		if s.Log != nil {
			s.Log("[vad] ошибка: " + err.Error())
		}
		return
	}
	for i, p := range probs {
		s.processHop(audio[i*HOP:(i+1)*HOP], p > float32(s.threshold))
	}
}

func (s *Segmenter) Flush() {
	if s.inSpeech {
		s.emit(false)
	}
}

func (s *Segmenter) processHop(chunk []float32, speech bool) {
	hopMs := float64(HOP) * 1000 / float64(s.sr)
	if s.inSpeech {
		s.buf = append(s.buf, chunk...)
		s.speechMs += hopMs
		if speech {
			s.silenceMs = 0
		} else {
			s.silenceMs += hopMs
		}
		if s.silenceMs >= float64(s.minSilenceMs) ||
			s.speechMs >= float64(s.maxSegmentMs) {
			s.emit(s.silenceMs >= float64(s.minSilenceMs))
		}
	} else {
		s.tail = append(s.tail, chunk...)
		if len(s.tail) > s.tailMax {
			copy(s.tail, s.tail[len(s.tail)-s.tailMax:])
			s.tail = s.tail[:s.tailMax]
		}
		if speech {
			s.inSpeech = true
			s.buf = make([]float32, len(s.tail))
			copy(s.buf, s.tail)
			s.tail = s.tail[:0]
			s.speechMs = hopMs
			s.silenceMs = 0
		}
	}
}

func (s *Segmenter) emit(trimSilence bool) {
	seg := s.buf
	s.buf = nil
	s.inSpeech = false
	s.speechMs = 0
	s.silenceMs = 0
	if trimSilence {
		cut := s.minSilenceMs * s.sr / 1000
		if len(seg) > cut {
			seg = seg[:len(seg)-cut]
		}
	}
	padded := make([]float32, s.padMs*s.sr/1000, len(seg)+2*(s.padMs*s.sr/1000))
	padded = append(padded, seg...)
	padded = append(padded, make([]float32, s.padMs*s.sr/1000)...)
	seg = padded
	if float64(len(seg)) < float64(s.minSpeechMs)*float64(s.sr)/1000 {
		return
	}
	tsEnd := float64(time.Now().UnixNano()) / 1e9
	tsStart := tsEnd - float64(len(seg))/float64(s.sr)
	if s.onSegment != nil {
		s.onSegment(seg, tsStart, tsEnd)
	}
}
