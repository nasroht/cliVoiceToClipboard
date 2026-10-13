package audio

import (
	"bytes"
	"encoding/binary"
	"testing"
)

func TestEncodeWAV(t *testing.T) {
	sampleRate := uint32(16000)
	channels := uint16(1)
	bitsPerSample := uint16(16)

	// Create 1 second of dummy 16 kHz 16-bit mono audio (32,000 bytes)
	pcm := make([]byte, sampleRate*uint32(channels)*(uint32(bitsPerSample)/8))
	for i := range pcm {
		pcm[i] = byte(i % 256)
	}

	wav, err := EncodeWAV(pcm, sampleRate, channels, bitsPerSample)
	if err != nil {
		t.Fatalf("EncodeWAV failed: %v", err)
	}

	if len(wav) != 44+len(pcm) {
		t.Fatalf("expected WAV size %d, got %d", 44+len(pcm), len(wav))
	}

	if string(wav[0:4]) != "RIFF" {
		t.Errorf("expected RIFF, got %s", string(wav[0:4]))
	}

	if string(wav[8:12]) != "WAVE" {
		t.Errorf("expected WAVE, got %s", string(wav[8:12]))
	}

	if string(wav[12:16]) != "fmt " {
		t.Errorf("expected 'fmt ', got %s", string(wav[12:16]))
	}

	var audioFormat uint16
	_ = binary.Read(bytes.NewReader(wav[20:22]), binary.LittleEndian, &audioFormat)
	if audioFormat != 1 {
		t.Errorf("expected audio format 1 (PCM), got %d", audioFormat)
	}

	var numChannels uint16
	_ = binary.Read(bytes.NewReader(wav[22:24]), binary.LittleEndian, &numChannels)
	if numChannels != channels {
		t.Errorf("expected channels %d, got %d", channels, numChannels)
	}

	var readSampleRate uint32
	_ = binary.Read(bytes.NewReader(wav[24:28]), binary.LittleEndian, &readSampleRate)
	if readSampleRate != sampleRate {
		t.Errorf("expected sampleRate %d, got %d", sampleRate, readSampleRate)
	}

	if string(wav[36:40]) != "data" {
		t.Errorf("expected 'data', got %s", string(wav[36:40]))
	}

	var dataSize uint32
	_ = binary.Read(bytes.NewReader(wav[40:44]), binary.LittleEndian, &dataSize)
	if dataSize != uint32(len(pcm)) {
		t.Errorf("expected dataSize %d, got %d", len(pcm), dataSize)
	}

	if !bytes.Equal(wav[44:], pcm) {
		t.Errorf("PCM payload mismatch")
	}
}

func TestEncodeWAV_InvalidParams(t *testing.T) {
	_, err := EncodeWAV([]byte{1, 2, 3}, 0, 1, 16)
	if err == nil {
		t.Errorf("expected error with 0 sample rate")
	}

	_, err = EncodeWAV([]byte{1, 2, 3}, 16000, 0, 16)
	if err == nil {
		t.Errorf("expected error with 0 channels")
	}

	_, err = EncodeWAV([]byte{1, 2, 3}, 16000, 1, 0)
	if err == nil {
		t.Errorf("expected error with 0 bitsPerSample")
	}
}
