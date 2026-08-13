package wa

import (
	"encoding/binary"
	"math"
	"testing"
)

func pcmFromSamples(samples []int16) []byte {
	out := make([]byte, len(samples)*2)
	for i, s := range samples {
		binary.LittleEndian.PutUint16(out[i*2:], uint16(s))
	}
	return out
}

// TestWaveformFromPCMScalesToPeak checks the shape the bubble draws: 64 buckets
// of 0-100 with the loudest bucket at 100, so a quiet recording still reads as
// speech rather than a flat line.
func TestWaveformFromPCMScalesToPeak(t *testing.T) {
	// Two seconds at 8 kHz, quiet for the first half and loud for the second.
	samples := make([]int16, 16000)
	for i := range samples {
		amplitude := 1000.0
		if i >= len(samples)/2 {
			amplitude = 8000.0
		}
		samples[i] = int16(amplitude * math.Sin(float64(i)))
	}

	waveform := waveformFromPCM(pcmFromSamples(samples))
	if len(waveform) != waveformBuckets {
		t.Fatalf("length = %d, want %d", len(waveform), waveformBuckets)
	}

	peak := byte(0)
	for _, v := range waveform {
		if v > 100 {
			t.Fatalf("bucket value %d exceeds 100", v)
		}
		if v > peak {
			peak = v
		}
	}
	if peak != 100 {
		t.Errorf("peak bucket = %d, want 100", peak)
	}
	if waveform[0] >= waveform[waveformBuckets-1] {
		t.Errorf("quiet half (%d) should be below the loud half (%d)", waveform[0], waveform[waveformBuckets-1])
	}
}

// TestWaveformFromPCMRejectsUnusableInput keeps a bogus waveform out of the
// store: too few samples to bucket, or pure silence with no peak to scale to.
func TestWaveformFromPCMRejectsUnusableInput(t *testing.T) {
	if got := waveformFromPCM(nil); got != nil {
		t.Errorf("nil pcm = %v, want nil", got)
	}
	if got := waveformFromPCM(pcmFromSamples(make([]int16, 10))); got != nil {
		t.Errorf("short pcm = %v, want nil", got)
	}
	if got := waveformFromPCM(pcmFromSamples(make([]int16, 8000))); got != nil {
		t.Errorf("silent pcm = %v, want nil", got)
	}
}
