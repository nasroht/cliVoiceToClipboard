package audio

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/gen2brain/malgo"
)

var (
	// ErrAudioTooShort is returned when recorded audio is below the minimum threshold.
	ErrAudioTooShort = errors.New("recorded audio is too short")
	// ErrNotRecording is returned when stopping a recording that wasn't started.
	ErrNotRecording = errors.New("recorder is not actively recording")
)

const (
	// DefaultSampleRate is 16 kHz, standard for Whisper inference.
	DefaultSampleRate = 16000
	// DefaultChannels is 1 (mono).
	DefaultChannels = 1
	// DefaultBitsPerSample is 16-bit signed integer.
	DefaultBitsPerSample = 16
	// MinAudioDuration is minimum length of audio to consider for transcription (150ms).
	MinAudioDuration = 150 * time.Millisecond
)

// Recorder manages microphone audio capture and in-memory WAV packaging.
type Recorder struct {
	sampleRate    uint32
	channels      uint16
	bitsPerSample uint16

	ctx    *malgo.AllocatedContext
	device *malgo.Device

	mu        sync.Mutex
	recording bool
	pcmBuffer bytes.Buffer
}

// NewRecorder initializes a new audio recorder backed by malgo (miniaudio).
func NewRecorder(sampleRate uint32, channels uint16) (*Recorder, error) {
	if sampleRate == 0 {
		sampleRate = DefaultSampleRate
	}
	if channels == 0 {
		channels = DefaultChannels
	}

	ctx, err := malgo.InitContext(nil, malgo.ContextConfig{}, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to initialize malgo context: %w", err)
	}

	r := &Recorder{
		sampleRate:    sampleRate,
		channels:      channels,
		bitsPerSample: DefaultBitsPerSample,
		ctx:           ctx,
	}

	deviceConfig := malgo.DefaultDeviceConfig(malgo.Capture)
	deviceConfig.Capture.Format = malgo.FormatS16
	deviceConfig.Capture.Channels = uint32(channels)
	deviceConfig.SampleRate = sampleRate
	deviceConfig.Alsa.NoMMap = 1

	callbacks := malgo.DeviceCallbacks{
		Data: r.onAudioData,
	}

	device, err := malgo.InitDevice(ctx.Context, deviceConfig, callbacks)
	if err != nil {
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("failed to initialize audio capture device: %w", err)
	}

	r.device = device
	if err := r.device.Start(); err != nil {
		r.device.Uninit()
		_ = ctx.Uninit()
		ctx.Free()
		return nil, fmt.Errorf("failed to start audio capture stream: %w", err)
	}

	return r, nil
}

// onAudioData is invoked by miniaudio whenever microphone PCM frames are ready.
func (r *Recorder) onAudioData(pOutput, pInput []byte, frameCount uint32) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.recording || len(pInput) == 0 {
		return
	}

	r.pcmBuffer.Write(pInput)
}

// Start begins buffering incoming microphone audio in memory.
func (r *Recorder) Start() {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.pcmBuffer.Reset()
	r.recording = true
}

// Stop stops buffering and returns the in-memory standard WAV buffer.
func (r *Recorder) Stop() ([]byte, error) {
	r.mu.Lock()
	defer r.mu.Unlock()

	if !r.recording {
		return nil, ErrNotRecording
	}
	r.recording = false

	pcmBytes := r.pcmBuffer.Bytes()
	if len(pcmBytes) == 0 {
		return nil, ErrAudioTooShort
	}

	bytesPerSample := int(r.bitsPerSample / 8)
	totalSamples := len(pcmBytes) / (int(r.channels) * bytesPerSample)
	duration := time.Duration(totalSamples) * time.Second / time.Duration(r.sampleRate)

	if duration < MinAudioDuration {
		return nil, ErrAudioTooShort
	}

	wavData, err := EncodeWAV(pcmBytes, r.sampleRate, r.channels, r.bitsPerSample)
	if err != nil {
		return nil, fmt.Errorf("failed to encode WAV: %w", err)
	}

	return wavData, nil
}

// IsRecording returns true if recording is currently active.
func (r *Recorder) IsRecording() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.recording
}

// Close releases the malgo device and context resources.
func (r *Recorder) Close() error {
	r.mu.Lock()
	r.recording = false
	r.mu.Unlock()

	if r.device != nil {
		_ = r.device.Stop()
		r.device.Uninit()
	}

	if r.ctx != nil {
		_ = r.ctx.Uninit()
		r.ctx.Free()
	}

	return nil
}

// EncodeWAV packages raw PCM bytes into a standard RIFF/WAVE header and data block.
func EncodeWAV(pcm []byte, sampleRate uint32, channels uint16, bitsPerSample uint16) ([]byte, error) {
	if channels == 0 || bitsPerSample == 0 || sampleRate == 0 {
		return nil, errors.New("invalid WAV audio parameters")
	}

	bytesPerSample := bitsPerSample / 8
	blockAlign := channels * bytesPerSample
	byteRate := sampleRate * uint32(blockAlign)
	pcmLen := uint32(len(pcm))

	// RIFF chunk descriptor: 12 bytes
	// "fmt " sub-chunk: 24 bytes
	// "data" sub-chunk header: 8 bytes
	// Total header size = 44 bytes
	totalFileSize := 36 + pcmLen

	buf := bytes.NewBuffer(make([]byte, 0, 44+pcmLen))

	// ChunkID "RIFF"
	buf.WriteString("RIFF")
	// ChunkSize (FileSize - 8)
	if err := binary.Write(buf, binary.LittleEndian, totalFileSize); err != nil {
		return nil, err
	}
	// Format "WAVE"
	buf.WriteString("WAVE")

	// Subchunk1ID "fmt "
	buf.WriteString("fmt ")
	// Subchunk1Size (16 for PCM)
	if err := binary.Write(buf, binary.LittleEndian, uint32(16)); err != nil {
		return nil, err
	}
	// AudioFormat (1 for PCM)
	if err := binary.Write(buf, binary.LittleEndian, uint16(1)); err != nil {
		return nil, err
	}
	// NumChannels
	if err := binary.Write(buf, binary.LittleEndian, channels); err != nil {
		return nil, err
	}
	// SampleRate
	if err := binary.Write(buf, binary.LittleEndian, sampleRate); err != nil {
		return nil, err
	}
	// ByteRate
	if err := binary.Write(buf, binary.LittleEndian, byteRate); err != nil {
		return nil, err
	}
	// BlockAlign
	if err := binary.Write(buf, binary.LittleEndian, blockAlign); err != nil {
		return nil, err
	}
	// BitsPerSample
	if err := binary.Write(buf, binary.LittleEndian, bitsPerSample); err != nil {
		return nil, err
	}

	// Subchunk2ID "data"
	buf.WriteString("data")
	// Subchunk2Size
	if err := binary.Write(buf, binary.LittleEndian, pcmLen); err != nil {
		return nil, err
	}

	// PCM audio bytes
	buf.Write(pcm)

	return buf.Bytes(), nil
}
