package whisper

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

var (
	// ErrBinaryNotFound is returned when whisper-server.exe cannot be located.
	ErrBinaryNotFound = errors.New("whisper-server executable not found")
	// ErrModelNotFound is returned when the model file does not exist.
	ErrModelNotFound = errors.New("whisper model file not found")
)

// BackendType represents the inference execution backend.
type BackendType string

const (
	// BackendCUDA enables NVIDIA GPU offload (RTX 5080).
	BackendCUDA BackendType = "cuda"
	// BackendCPU enables CPU inference with AVX-512 optimization (Ryzen 7 9800X3D).
	BackendCPU BackendType = "cpu"
)

// SupervisorConfig defines configuration for the whisper-server subprocess.
type SupervisorConfig struct {
	ServerBin string      // Path to whisper-server.exe
	ModelPath string      // Path to GGML model file
	Backend   BackendType // "cuda" or "cpu"
	Host      string      // Listen host (default 127.0.0.1)
	Port      int         // Listen port (default 8080)
	Threads   int         // Thread count for CPU inference (default 8)
}

// Supervisor manages the lifecycle of the whisper-server.exe subprocess.
type Supervisor struct {
	cfg SupervisorConfig

	mu      sync.Mutex
	cmd     *exec.Cmd
	stopped bool
}

// NewSupervisor creates a new supervisor for the whisper-server subprocess.
func NewSupervisor(cfg SupervisorConfig) *Supervisor {
	if cfg.ServerBin == "" {
		cfg.ServerBin = "whisper-server.exe"
	}
	if cfg.ModelPath == "" {
		cfg.ModelPath = "models/ggml-base.bin"
	}
	if cfg.Backend == "" {
		cfg.Backend = BackendCUDA
	}
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port <= 0 {
		cfg.Port = 8080
	}
	if cfg.Threads <= 0 {
		// Default to 8 threads for AMD Ryzen 7 9800X3D (8 physical cores / 16 threads)
		cfg.Threads = 8
	}

	return &Supervisor{
		cfg: cfg,
	}
}

// FindServerBinary searches for the whisper-server executable in specified path, PATH, or standard locations.
func FindServerBinary(hint string) (string, error) {
	candidates := []string{
		hint,
		"whisper-server.exe",
		"./whisper-server.exe",
		"bin/whisper-server.exe",
		"../bin/whisper-server.exe",
		filepath.Join(os.Getenv("USERPROFILE"), "whisper-server.exe"),
	}

	for _, c := range candidates {
		if c == "" {
			continue
		}
		if fi, err := os.Stat(c); err == nil && !fi.IsDir() {
			abs, err := filepath.Abs(c)
			if err == nil {
				return abs, nil
			}
			return c, nil
		}
	}

	// Check system PATH
	path, err := exec.LookPath("whisper-server.exe")
	if err == nil {
		return path, nil
	}
	path, err = exec.LookPath("whisper-server")
	if err == nil {
		return path, nil
	}

	return "", fmt.Errorf("%w: searched hint %q and system PATH", ErrBinaryNotFound, hint)
}

// BuildArgs constructs the command-line flags for whisper-server based on the selected backend.
func BuildArgs(cfg SupervisorConfig) []string {
	args := []string{
		"-m", cfg.ModelPath,
		"--host", cfg.Host,
		"--port", strconv.Itoa(cfg.Port),
	}

	switch BackendType(strings.ToLower(string(cfg.Backend))) {
	case BackendCPU:
		// CPU fallback: AMD Ryzen 7 9800X3D AVX-512 thread optimization.
		// Avoid offloading to GPU and set dedicated threads.
		args = append(args, "-ngl", "0")
		threads := cfg.Threads
		if threads <= 0 {
			threads = 8
		}
		args = append(args, "-t", strconv.Itoa(threads))
	case BackendCUDA:
		fallthrough
	default:
		// CUDA acceleration: NVIDIA RTX 5080. Offload all 99 layers to GPU.
		args = append(args, "-ngl", "99")
	}

	return args
}

// Start launches the whisper-server.exe subprocess and pipes logs with a prefix.
func (s *Supervisor) Start(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	binPath, err := FindServerBinary(s.cfg.ServerBin)
	if err != nil {
		return err
	}
	s.cfg.ServerBin = binPath

	if _, err := os.Stat(s.cfg.ModelPath); os.IsNotExist(err) {
		return fmt.Errorf("%w: %s", ErrModelNotFound, s.cfg.ModelPath)
	}

	args := BuildArgs(s.cfg)
	log.Printf("[supervisor] Launching %s with backend=%s (args: %v)", binPath, s.cfg.Backend, args)

	cmd := exec.CommandContext(ctx, binPath, args...)

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("failed to pipe stdout: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return fmt.Errorf("failed to pipe stderr: %w", err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start whisper-server: %w", err)
	}

	s.cmd = cmd
	s.stopped = false

	go s.pipeLog(stdout, "stdout")
	go s.pipeLog(stderr, "stderr")

	// Wait in background to track exit
	go func() {
		err := cmd.Wait()
		s.mu.Lock()
		stopped := s.stopped
		s.mu.Unlock()

		if err != nil && !stopped {
			log.Printf("[supervisor] whisper-server exited unexpectedly: %v", err)
		}
	}()

	return nil
}

func (s *Supervisor) pipeLog(r io.Reader, stream string) {
	scanner := bufio.NewScanner(r)
	for scanner.Scan() {
		line := scanner.Text()
		if line != "" {
			log.Printf("[whisper-server:%s] %s", stream, line)
		}
	}
}

// Stop gracefully terminates the whisper-server subprocess.
func (s *Supervisor) Stop() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.cmd == nil || s.stopped {
		return nil
	}

	s.stopped = true
	log.Println("[supervisor] Terminating whisper-server process...")

	if s.cmd.Process != nil {
		// On Windows, Kill forcefully terminates the process tree
		_ = s.cmd.Process.Kill()
	}

	// Give it up to 2 seconds to release resources
	done := make(chan struct{})
	go func() {
		_ = s.cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
	}

	return nil
}
