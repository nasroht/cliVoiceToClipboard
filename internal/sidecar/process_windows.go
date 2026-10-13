//go:build windows

package sidecar

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

// ProcessConfig holds configuration for the silent whisper-server child process.
type ProcessConfig struct {
	BinaryPath string
	ModelPath  string
	Backend    string // "cuda" or "cpu"
	Host       string
	Port       int
	Threads    int
}

// ProcessManager supervises whisper-server.exe as an invisible sidecar child process,
// locked to a Windows Job Object with JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE.
type ProcessManager struct {
	cfg ProcessConfig

	mu        sync.Mutex
	jobHandle windows.Handle
	cmd       *exec.Cmd
	stopped   bool
}

// NewProcessManager initializes a new silent process manager.
func NewProcessManager(cfg ProcessConfig) *ProcessManager {
	if cfg.Host == "" {
		cfg.Host = "127.0.0.1"
	}
	if cfg.Port <= 0 {
		cfg.Port = 8080
	}
	if cfg.Threads <= 0 {
		cfg.Threads = 6 // AMD Ryzen 7 9800X3D gaming-safe thread count
	}
	if cfg.Backend == "" {
		cfg.Backend = "cuda"
	}

	return &ProcessManager{
		cfg: cfg,
	}
}

// BuildArguments prepares CLI flags for whisper-server.exe based on hardware targets.
func BuildArguments(cfg ProcessConfig) []string {
	args := []string{
		"-m", cfg.ModelPath,
		"--host", cfg.Host,
		"--port", strconv.Itoa(cfg.Port),
	}

	if strings.ToLower(cfg.Backend) == "cpu" {
		// CPU fallback: AMD Ryzen 7 9800X3D AVX-512 optimization (-ng disables GPU)
		args = append(args, "-ng", "-t", strconv.Itoa(cfg.Threads))
	} else {
		// CUDA acceleration: NVIDIA GeForce RTX 5080 (Blackwell)
		// whisper-server offloads all model weights into GPU VRAM by default
	}

	return args
}

// createJobObject configures a Windows Job Object with KILL_ON_JOB_CLOSE semantics.
func createJobObject() (windows.Handle, error) {
	hJob, err := windows.CreateJobObject(nil, nil)
	if err != nil {
		return 0, fmt.Errorf("failed to create Windows Job Object: %w", err)
	}

	info := windows.JOBOBJECT_EXTENDED_LIMIT_INFORMATION{
		BasicLimitInformation: windows.JOBOBJECT_BASIC_LIMIT_INFORMATION{
			LimitFlags: windows.JOB_OBJECT_LIMIT_KILL_ON_JOB_CLOSE,
		},
	}

	_, err = windows.SetInformationJobObject(
		hJob,
		windows.JobObjectExtendedLimitInformation,
		uintptr(unsafe.Pointer(&info)),
		uint32(unsafe.Sizeof(info)),
	)
	if err != nil {
		_ = windows.CloseHandle(hJob)
		return 0, fmt.Errorf("failed to set Job Object KILL_ON_JOB_CLOSE flag: %w", err)
	}

	return hJob, nil
}

// Start launches whisper-server.exe invisibly and binds it to the Windows Job Object.
func (pm *ProcessManager) Start(ctx context.Context) error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	// 1. Create Job Object with automatic kill on close
	job, err := createJobObject()
	if err != nil {
		return err
	}
	pm.jobHandle = job

	// 2. Prepare command with CREATE_NO_WINDOW
	args := BuildArguments(pm.cfg)
	log.Printf("[sidecar] Spawning silent child process %s (backend: %s, args: %v)", pm.cfg.BinaryPath, pm.cfg.Backend, args)

	cmd := exec.CommandContext(ctx, pm.cfg.BinaryPath, args...)
	cmd.SysProcAttr = &syscall.SysProcAttr{
		// 0x08000000 = CREATE_NO_WINDOW: invisible, no console window or flash
		CreationFlags: windows.CREATE_NO_WINDOW,
	}

	// 3. Start child process
	if err := cmd.Start(); err != nil {
		_ = windows.CloseHandle(pm.jobHandle)
		pm.jobHandle = 0
		return fmt.Errorf("failed to start whisper-server process: %w", err)
	}
	pm.cmd = cmd
	pm.stopped = false

	// 4. Attach process to Job Object
	hProcess, err := windows.OpenProcess(windows.PROCESS_SET_QUOTA|windows.PROCESS_TERMINATE, false, uint32(cmd.Process.Pid))
	if err != nil {
		_ = cmd.Process.Kill()
		_ = windows.CloseHandle(pm.jobHandle)
		return fmt.Errorf("failed to open child process handle for Job Object: %w", err)
	}
	defer windows.CloseHandle(hProcess)

	if err := windows.AssignProcessToJobObject(pm.jobHandle, hProcess); err != nil {
		_ = cmd.Process.Kill()
		_ = windows.CloseHandle(pm.jobHandle)
		return fmt.Errorf("failed to assign child process to Job Object: %w", err)
	}

	log.Printf("[sidecar] whisper-server (PID %d) locked to Windows Job Object [KILL_ON_JOB_CLOSE]", cmd.Process.Pid)

	// Background monitor
	go func() {
		_ = cmd.Wait()
		pm.mu.Lock()
		stopped := pm.stopped
		pm.mu.Unlock()
		if !stopped {
			log.Printf("[sidecar] whisper-server process exited.")
		}
	}()

	return nil
}

// WaitReady polls the HTTP endpoint until whisper-server returns 200 OK.
func (pm *ProcessManager) WaitReady(ctx context.Context, timeout time.Duration) error {
	probeURL := fmt.Sprintf("http://%s:%d/", pm.cfg.Host, pm.cfg.Port)
	log.Printf("[sidecar] Running readiness probe against %s...", probeURL)

	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 1 * time.Second}

	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			if time.Now().After(deadline) {
				return fmt.Errorf("timed out after %v waiting for whisper-server at %s", timeout, probeURL)
			}

			req, err := http.NewRequestWithContext(ctx, http.MethodGet, probeURL, nil)
			if err != nil {
				continue
			}

			resp, err := client.Do(req)
			if err == nil {
				_ = resp.Body.Close()
				if resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusNotFound {
					// Server is answering HTTP requests
					log.Printf("[sidecar] whisper-server is healthy and ready (HTTP %d)", resp.StatusCode)
					return nil
				}
			}
		}
	}
}

// Stop cleanly terminates the child process and closes the Job Object handle.
func (pm *ProcessManager) Stop() error {
	pm.mu.Lock()
	defer pm.mu.Unlock()

	if pm.stopped {
		return nil
	}
	pm.stopped = true

	log.Println("[sidecar] Stopping whisper-server process...")

	if pm.cmd != nil && pm.cmd.Process != nil {
		_ = pm.cmd.Process.Kill()
	}

	if pm.jobHandle != 0 {
		_ = windows.CloseHandle(pm.jobHandle)
		pm.jobHandle = 0
	}

	return nil
}
