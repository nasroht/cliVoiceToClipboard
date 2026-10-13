package sidecar

import (
	"path/filepath"
	"reflect"
	"testing"
)

func TestGetAppDirs(t *testing.T) {
	dirs, err := GetAppDirs()
	if err != nil {
		t.Fatalf("GetAppDirs failed: %v", err)
	}

	if filepath.Base(dirs.Root) != "v2c" {
		t.Errorf("expected Root base to be 'v2c', got %s", dirs.Root)
	}
	if filepath.Base(dirs.Bin) != "bin" {
		t.Errorf("expected Bin base to be 'bin', got %s", dirs.Bin)
	}
	if filepath.Base(dirs.Models) != "models" {
		t.Errorf("expected Models base to be 'models', got %s", dirs.Models)
	}
}

func TestBuildArguments_CUDA(t *testing.T) {
	cfg := ProcessConfig{
		BinaryPath: "whisper-server.exe",
		ModelPath:  "models/ggml-base.bin",
		Backend:    "cuda",
		Host:       "127.0.0.1",
		Port:       8080,
	}

	args := BuildArguments(cfg)
	expected := []string{
		"-m", "models/ggml-base.bin",
		"--host", "127.0.0.1",
		"--port", "8080",
		"-ngl", "99",
	}

	if !reflect.DeepEqual(args, expected) {
		t.Errorf("BuildArguments(CUDA) = %v, expected %v", args, expected)
	}
}

func TestBuildArguments_CPU(t *testing.T) {
	cfg := ProcessConfig{
		BinaryPath: "whisper-server.exe",
		ModelPath:  "models/ggml-base.bin",
		Backend:    "cpu",
		Host:       "127.0.0.1",
		Port:       8080,
		Threads:    6,
	}

	args := BuildArguments(cfg)
	expected := []string{
		"-m", "models/ggml-base.bin",
		"--host", "127.0.0.1",
		"--port", "8080",
		"-ngl", "0",
		"-t", "6",
	}

	if !reflect.DeepEqual(args, expected) {
		t.Errorf("BuildArguments(CPU) = %v, expected %v", args, expected)
	}
}

func TestCheckPortAvailable(t *testing.T) {
	// Pick a free high port to test
	err := CheckPortAvailable("127.0.0.1", 39871)
	if err != nil {
		t.Errorf("expected port 39871 to be available, got %v", err)
	}
}
