package whisper

import (
	"reflect"
	"testing"
)

func TestBuildArgs_CUDA(t *testing.T) {
	cfg := SupervisorConfig{
		ModelPath: "models/ggml-base.bin",
		Backend:   BackendCUDA,
		Host:      "127.0.0.1",
		Port:      8080,
	}

	args := BuildArgs(cfg)
	expected := []string{
		"-m", "models/ggml-base.bin",
		"--host", "127.0.0.1",
		"--port", "8080",
		"-ngl", "99",
	}

	if !reflect.DeepEqual(args, expected) {
		t.Errorf("BuildArgs(CUDA) = %v, expected %v", args, expected)
	}
}

func TestBuildArgs_CPU(t *testing.T) {
	cfg := SupervisorConfig{
		ModelPath: "models/ggml-medium.bin",
		Backend:   BackendCPU,
		Host:      "127.0.0.1",
		Port:      9090,
		Threads:   8,
	}

	args := BuildArgs(cfg)
	expected := []string{
		"-m", "models/ggml-medium.bin",
		"--host", "127.0.0.1",
		"--port", "9090",
		"-ngl", "0",
		"-t", "8",
	}

	if !reflect.DeepEqual(args, expected) {
		t.Errorf("BuildArgs(CPU) = %v, expected %v", args, expected)
	}
}

func TestBuildArgs_CPU_DefaultThreads(t *testing.T) {
	cfg := SupervisorConfig{
		ModelPath: "models/ggml-base.bin",
		Backend:   BackendCPU,
		Host:      "127.0.0.1",
		Port:      8080,
		Threads:   0, // Should default to 8
	}

	args := BuildArgs(cfg)
	expected := []string{
		"-m", "models/ggml-base.bin",
		"--host", "127.0.0.1",
		"--port", "8080",
		"-ngl", "0",
		"-t", "8",
	}

	if !reflect.DeepEqual(args, expected) {
		t.Errorf("BuildArgs(CPU Default Threads) = %v, expected %v", args, expected)
	}
}
