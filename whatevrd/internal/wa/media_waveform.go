package wa

import (
	"bytes"
	"context"
	"encoding/binary"
	"math"
	"os"
	"os/exec"
	"time"

	appstore "whatevrd/internal/store"
)

const (
	// waveformSampleRate and waveformChannels are the shape we decode to: mono
	// at 8 kHz is far more resolution than 64 buckets need, and keeps the
	// decode cheap.
	waveformSampleRate = "8000"
	waveformChannels   = "1"

	// waveformMaxSourceBytes caps what we are willing to decode. Voice notes
	// are seconds long; anything bigger is not a voice note and is not worth
	// spawning a decoder for.
	waveformMaxSourceBytes = 20 << 20

	waveformDecodeTimeout = 10 * time.Second
)

// maybeDeriveVoiceWaveform fills in the amplitude envelope for a voice note
// whose sender did not ship one. WhatsApp's own clients always send a waveform,
// but bridges and third-party clients often do not, and a flat bar is a poor
// substitute for the shape of someone's speech.
//
// This is the daemon's only use of ffmpeg and it is entirely optional: with no
// ffmpeg on PATH the bubble simply renders without a waveform.
func (c *Client) maybeDeriveVoiceWaveform(ctx context.Context, message appstore.Message) {
	if message.MediaKind != appstore.MediaKindVoice || len(message.MediaWaveform) > 0 {
		return
	}
	if message.MediaLocalPath == "" {
		return
	}
	info, err := os.Stat(message.MediaLocalPath)
	if err != nil || info.Size() > waveformMaxSourceBytes {
		return
	}
	ffmpeg, err := exec.LookPath("ffmpeg")
	if err != nil {
		return
	}

	waveform, err := decodeWaveform(ctx, ffmpeg, message.MediaLocalPath)
	if err != nil || len(waveform) == 0 {
		if err != nil {
			c.log.Debugf("Could not derive waveform for %s: %v", message.ID, err)
		}
		return
	}

	updated, err := c.store.SetMessageMediaWaveform(ctx, message.ID, waveform)
	if err != nil {
		c.log.Warnf("Failed to store derived waveform for %s: %v", message.ID, err)
		return
	}
	c.daemon.PublishMessageUpdated(toDaemonMessage(updated))
}

// decodeWaveform runs the file through ffmpeg to signed 16-bit mono PCM and
// reduces it to waveformBuckets root-mean-square values scaled 0-100, which is
// the same shape and range WhatsApp puts on the wire.
func decodeWaveform(ctx context.Context, ffmpeg, path string) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, waveformDecodeTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, ffmpeg,
		"-v", "error",
		"-i", path,
		"-f", "s16le",
		"-ac", waveformChannels,
		"-ar", waveformSampleRate,
		"-",
	)
	var stdout bytes.Buffer
	cmd.Stdout = &stdout
	if err := cmd.Run(); err != nil {
		return nil, err
	}
	return waveformFromPCM(stdout.Bytes()), nil
}

// waveformFromPCM buckets little-endian s16 samples into waveformBuckets RMS
// values, then scales so the loudest bucket is 100. Scaling to the peak is what
// makes a quiet recording still look like speech rather than a flat line.
func waveformFromPCM(pcm []byte) []byte {
	samples := len(pcm) / 2
	if samples < waveformBuckets {
		return nil
	}

	sums := make([]float64, waveformBuckets)
	counts := make([]int, waveformBuckets)
	for i := 0; i < samples; i++ {
		bucket := i * waveformBuckets / samples
		value := float64(int16(binary.LittleEndian.Uint16(pcm[i*2:])))
		sums[bucket] += value * value
		counts[bucket]++
	}

	peak := 0.0
	rms := make([]float64, waveformBuckets)
	for i := range rms {
		if counts[i] == 0 {
			continue
		}
		rms[i] = math.Sqrt(sums[i] / float64(counts[i]))
		if rms[i] > peak {
			peak = rms[i]
		}
	}
	if peak <= 0 {
		return nil
	}

	waveform := make([]byte, waveformBuckets)
	for i, v := range rms {
		scaled := math.Round(v / peak * 100)
		if scaled > 100 {
			scaled = 100
		}
		waveform[i] = byte(scaled)
	}
	return waveform
}
