package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/nasroht/cliVoiceToClipboard/internal/audio"
	"github.com/nasroht/cliVoiceToClipboard/internal/clipboard"
	"github.com/nasroht/cliVoiceToClipboard/internal/hotkey"
	"github.com/nasroht/cliVoiceToClipboard/internal/whisper"
)

const version = "1.0.0"

func main() {
	backendFlag := flag.String("backend", "cuda", "Inference backend: 'cuda' (RTX 5080) or 'cpu' (9800X3D AVX-512)")
	modelFlag := flag.String("model", "models/ggml-base.bin", "Path to GGML model file")
	portFlag := flag.Int("port", 8080, "Port for the local whisper-server")
	hotkeyFlag := flag.String("hotkey", "0x7B", "Virtual key code to trigger recording (e.g., 0x7B or F12)")
	beepFlag := flag.Bool("beep", true, "Play audio completion chime when text is copied to clipboard")
	languageFlag := flag.String("language", "auto", "Transcription language code or 'auto'")
	serverBinFlag := flag.String("server-bin", "whisper-server.exe", "Path to whisper-server executable")
	noServerFlag := flag.Bool("no-server", false, "Connect to existing whisper server without spawning a subprocess")
	threadsFlag := flag.Int("threads", 8, "Number of threads for CPU inference (default: 8 for 9800X3D)")

	flag.Parse()

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	fmt.Println("==================================================================")
	fmt.Printf("   cliVoiceToClipboard v%s - Windows 11 Voice-to-Text Companion\n", version)
	fmt.Println("   Designed for WoW & Gaming: Strict Policy & Anti-Cheat TOS Compliant")
	fmt.Println("==================================================================")

	// Validate backend
	backend := whisper.BackendType(*backendFlag)
	if backend != whisper.BackendCUDA && backend != whisper.BackendCPU {
		log.Fatalf("Invalid backend %q. Choose 'cuda' or 'cpu'.", *backendFlag)
	}

	// Parse hotkey
	vkCode, err := hotkey.ParseHotkey(*hotkeyFlag)
	if err != nil {
		log.Fatalf("Invalid hotkey: %v", err)
	}
	keyName := hotkey.KeyName(vkCode)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", *portFlag)

	fmt.Printf(" [Config] Backend:     %s\n", backend)
	fmt.Printf(" [Config] Model:       %s\n", *modelFlag)
	fmt.Printf(" [Config] Endpoint:    %s\n", baseURL)
	fmt.Printf(" [Config] Push-To-Talk: %s\n", keyName)
	fmt.Printf(" [Config] Language:    %s\n", *languageFlag)
	fmt.Printf(" [Config] Chime:       %v\n", *beepFlag)
	fmt.Println("------------------------------------------------------------------")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Signal handler for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// Subprocess supervisor (if not connecting to an external server)
	var supervisor *whisper.Supervisor
	if !*noServerFlag {
		supervisor = whisper.NewSupervisor(whisper.SupervisorConfig{
			ServerBin: *serverBinFlag,
			ModelPath: *modelFlag,
			Backend:   backend,
			Host:      "127.0.0.1",
			Port:      *portFlag,
			Threads:   *threadsFlag,
		})

		log.Printf("Starting whisper-server subprocess...")
		if err := supervisor.Start(ctx); err != nil {
			log.Printf("Warning: Failed to launch whisper-server automatically: %v", err)
			log.Printf("Continuing with client check in case server is already running externally...")
		}
		defer func() {
			if supervisor != nil {
				_ = supervisor.Stop()
			}
		}()
	}

	// HTTP Client for inference
	client := whisper.NewClient(whisper.ClientConfig{
		BaseURL:    baseURL,
		Language:   *languageFlag,
		Timeout:    30 * time.Second,
		MaxRetries: 3,
		RetryDelay: 300 * time.Millisecond,
	})

	log.Printf("Verifying whisper server connectivity at %s...", baseURL)
	checkCtx, checkCancel := context.WithTimeout(ctx, 3*time.Second)
	if err := client.Ping(checkCtx); err != nil {
		checkCancel()
		log.Printf("Waiting for whisper-server to become ready (up to 30s)...")
		if err := client.WaitReady(ctx, 30*time.Second); err != nil {
			log.Printf("Warning: Whisper server not reachable yet (%v). Will keep retrying on first audio capture.", err)
		} else {
			log.Printf("Whisper server is ready!")
		}
	} else {
		checkCancel()
		log.Printf("Whisper server is online and responding.")
	}

	// Initialize Audio Recorder
	log.Printf("Initializing microphone capture stream (16 kHz, 16-bit mono)...")
	rec, err := audio.NewRecorder(audio.DefaultSampleRate, audio.DefaultChannels)
	if err != nil {
		log.Fatalf("Failed to initialize audio recorder: %v", err)
	}
	defer func() {
		log.Printf("Closing audio capture...")
		_ = rec.Close()
	}()

	log.Println("Ready! Hold push-to-talk key to record, release to transcribe to clipboard.")

	// Hotkey listener callbacks
	onKeyDown := func() {
		log.Printf("[PTT] Key DOWN (%s) -> Recording audio...", keyName)
		rec.Start()
	}

	onKeyUp := func() {
		wavData, err := rec.Stop()
		if err != nil {
			if err == audio.ErrAudioTooShort {
				log.Printf("[PTT] Key released too quickly (tap ignored).")
				return
			}
			log.Printf("[PTT] Audio capture error: %v", err)
			return
		}

		durationSec := float64(len(wavData)-44) / float64(audio.DefaultSampleRate*2)
		log.Printf("[PTT] Key UP (%s) -> Captured %.2fs audio. Sending for inference...", keyName, durationSec)

		go func(data []byte) {
			startTime := time.Now()
			inferCtx, inferCancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer inferCancel()

			rawText, err := client.Transcribe(inferCtx, data)
			if err != nil {
				log.Printf("[PTT] Transcription failed: %v", err)
				return
			}

			cleaned, err := clipboard.Write(rawText)
			if err != nil {
				if err == clipboard.ErrEmptyText {
					log.Printf("[PTT] Transcription resulted in silence/noise. Clipboard unchanged.")
					return
				}
				log.Printf("[PTT] Failed to copy text to clipboard: %v", err)
				return
			}

			elapsed := time.Since(startTime)
			log.Printf("[PTT] Transcribed in %v: %q", elapsed.Round(time.Millisecond), cleaned)
			log.Printf("[PTT] Successfully copied to clipboard! (Press CTRL+V to paste)")

			if *beepFlag {
				hotkey.PlayChime()
			}
		}(wavData)
	}

	listener := hotkey.NewListener(vkCode, hotkey.DefaultPollInterval)

	go func() {
		if err := listener.Listen(ctx, onKeyDown, onKeyUp); err != nil && err != context.Canceled {
			log.Printf("Hotkey listener error: %v", err)
		}
	}()

	// Wait for shutdown signal
	sig := <-sigChan
	log.Printf("Received signal %s, shutting down cleanly...", sig)
	cancel()

	time.Sleep(300 * time.Millisecond)
	log.Println("cliVoiceToClipboard daemon stopped.")
}
