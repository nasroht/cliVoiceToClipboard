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
	"github.com/nasroht/cliVoiceToClipboard/internal/sidecar"
	"github.com/nasroht/cliVoiceToClipboard/internal/whisper"
)

const version = "1.1.0"

func main() {
	backendFlag := flag.String("backend", "cuda", "Inference backend: 'cuda' (RTX 5080) or 'cpu' (9800X3D AVX-512)")
	portFlag := flag.Int("port", 8080, "Port for the local whisper server")
	modelFlag := flag.String("model", "models/ggml-base.bin", "Path or name of the model")
	hotkeyFlag := flag.String("hotkey", "0x7B", "Virtual key code to trigger recording (e.g., 0x7B or F12)")
	chimeFlag := flag.Bool("chime", true, "Play audio notification chime when text is copied to clipboard")
	beepFlag := flag.Bool("beep", true, "Alias for --chime")
	languageFlag := flag.String("language", "auto", "Language code or 'auto'")
	threadsFlag := flag.Int("threads", 6, "CPU threads to use if cpu backend is active (gaming-safe for 9800X3D)")
	serverBinFlag := flag.String("server-bin", "", "Explicit path to whisper-server executable (optional)")
	noServerFlag := flag.Bool("no-server", false, "Connect to existing whisper server without spawning a sidecar")

	flag.Parse()

	// Respect either --chime or --beep if explicitly set
	useChime := *chimeFlag && *beepFlag

	log.SetFlags(log.Ldate | log.Ltime | log.Lmicroseconds)
	fmt.Println("==================================================================")
	fmt.Printf("   cliVoiceToClipboard v%s - Windows 11 Silent PTT Daemon\n", version)
	fmt.Println("   Zero-Keystroke & Anti-Cheat Safe: WoW & Gaming Policy Compliant")
	fmt.Println("==================================================================")

	// Validate backend
	if *backendFlag != "cuda" && *backendFlag != "cpu" {
		log.Fatalf("Invalid backend %q. Must be 'cuda' or 'cpu'.", *backendFlag)
	}

	// Parse hotkey
	vkCode, err := hotkey.ParseHotkey(*hotkeyFlag)
	if err != nil {
		log.Fatalf("Invalid hotkey: %v", err)
	}
	keyName := hotkey.KeyName(vkCode)

	baseURL := fmt.Sprintf("http://127.0.0.1:%d", *portFlag)

	fmt.Printf(" [Config] Backend:      %s\n", *backendFlag)
	fmt.Printf(" [Config] Model:        %s\n", *modelFlag)
	fmt.Printf(" [Config] Local Port:   %d\n", *portFlag)
	fmt.Printf(" [Config] Push-To-Talk: %s (15ms polling)\n", keyName)
	fmt.Printf(" [Config] Language:     %s\n", *languageFlag)
	fmt.Printf(" [Config] Chime:        %v (MessageBeep native audio)\n", useChime)
	if *backendFlag == "cpu" {
		fmt.Printf(" [Config] CPU Threads:  %d (9800X3D optimized)\n", *threadsFlag)
	}
	fmt.Println("------------------------------------------------------------------")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Intercept termination signals
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, os.Interrupt, syscall.SIGTERM)

	// 1. Initialization and Sidecar Extraction
	dirs, err := sidecar.EnsureDirectories()
	if err != nil {
		log.Printf("Warning: Failed to create LocalAppData directory: %v", err)
	} else {
		// Sync local binaries from ./bin to %LOCALAPPDATA%/v2c/bin
		_ = sidecar.SyncLocalBinaries(dirs)
	}

	var processManager *sidecar.ProcessManager

	if !*noServerFlag {
		// Check if a server is already answering on target port
		if sidecar.IsWhisperResponding(baseURL) {
			log.Printf("[sidecar] Detected existing whisper-server running on %s. Reusing active instance.", baseURL)
		} else {
			// Check port availability
			if err := sidecar.CheckPortAvailable("127.0.0.1", *portFlag); err != nil {
				log.Fatalf("Cannot bind port %d: %v", *portFlag, err)
			}

			// Locate whisper-server.exe
			serverBin, err := sidecar.ResolveServerBinary(*serverBinFlag)
			if err != nil {
				log.Printf("Notice: whisper-server.exe not found (%v).", err)
				log.Printf("Please download whisper-server.exe into %s or ./bin/.", dirs.Bin)
				log.Fatalf("Fatal: missing whisper-server.exe sidecar.")
			}

			// Locate model file
			modelPath, err := sidecar.ResolveModelPath(*modelFlag)
			if err != nil {
				log.Fatalf("Fatal: %v", err)
			}

			// Launch silent sidecar protected by Windows Job Object
			processManager = sidecar.NewProcessManager(sidecar.ProcessConfig{
				BinaryPath: serverBin,
				ModelPath:  modelPath,
				Backend:    *backendFlag,
				Host:       "127.0.0.1",
				Port:       *portFlag,
				Threads:    *threadsFlag,
			})

			log.Printf("[sidecar] Starting silent child process via Windows Job Object...")
			if err := processManager.Start(ctx); err != nil {
				log.Fatalf("Failed to launch sidecar process: %v", err)
			}
			defer func() {
				if processManager != nil {
					_ = processManager.Stop()
				}
			}()

			// Readiness probe: wait for HTTP 200 OK
			if err := processManager.WaitReady(ctx, 35*time.Second); err != nil {
				log.Fatalf("Sidecar readiness probe failed: %v", err)
			}
		}
	}

	// 2. HTTP Client for whisper inference
	client := whisper.NewClient(whisper.ClientConfig{
		BaseURL:    baseURL,
		Language:   *languageFlag,
		Timeout:    30 * time.Second,
		MaxRetries: 3,
		RetryDelay: 250 * time.Millisecond,
	})

	// 3. Audio Capture initialization (16000 Hz, 16-bit mono)
	log.Printf("[audio] Starting WASAPI audio capture stream via MiniAudio (16 kHz, 16-bit mono)...")
	rec, err := audio.NewRecorder(audio.DefaultSampleRate, audio.DefaultChannels)
	if err != nil {
		log.Fatalf("Failed to initialize audio recorder: %v", err)
	}
	defer func() {
		log.Printf("[audio] Closing audio capture stream...")
		_ = rec.Close()
	}()

	log.Println("==================================================================")
	log.Printf("Ready! Hold [%s] to speak. Release to copy text to clipboard.", keyName)
	log.Println("==================================================================")

	// 4. Hotkey Detection and Inference Loop
	onKeyDown := func() {
		log.Printf("[PTT] Key DOWN (%s) -> Buffering speech in memory...", keyName)
		rec.Start()
	}

	onKeyUp := func() {
		wavData, err := rec.Stop()
		if err != nil {
			if err == audio.ErrAudioTooShort {
				log.Printf("[PTT] Key released too quickly (brief tap ignored).")
				return
			}
			log.Printf("[PTT] Audio capture error: %v", err)
			return
		}

		durationSec := float64(len(wavData)-44) / float64(audio.DefaultSampleRate*2)
		log.Printf("[PTT] Key UP (%s) -> Recorded %.2fs audio. Dispatching inference...", keyName, durationSec)

		// Dispatch inference in separate goroutine
		go func(data []byte) {
			startTime := time.Now()
			inferCtx, inferCancel := context.WithTimeout(context.Background(), 25*time.Second)
			defer inferCancel()

			rawText, err := client.Transcribe(inferCtx, data)
			if err != nil {
				log.Printf("[PTT] Inference failed: %v", err)
				return
			}

			cleaned, err := clipboard.Write(rawText)
			if err != nil {
				if err == clipboard.ErrEmptyText {
					log.Printf("[PTT] Audio contained only silence/noise. Clipboard preserved.")
					return
				}
				log.Printf("[PTT] Failed to write to clipboard: %v", err)
				return
			}

			elapsed := time.Since(startTime)
			log.Printf("[PTT] Transcribed in %v: %q", elapsed.Round(time.Millisecond), cleaned)
			log.Printf("[PTT] Copied to clipboard! Press CTRL+V to paste into chat.")

			if useChime {
				clipboard.PlayChime()
			}
		}(wavData)
	}

	listener := hotkey.NewListener(vkCode, hotkey.DefaultPollInterval)

	go func() {
		if err := listener.Listen(ctx, onKeyDown, onKeyUp); err != nil && err != context.Canceled {
			log.Printf("Hotkey listener error: %v", err)
		}
	}()

	// 5. Graceful Teardown
	sig := <-sigChan
	log.Printf("Received shutdown signal (%s). Terminating gracefully...", sig)
	cancel()

	// Short pause for goroutine teardown
	time.Sleep(200 * time.Millisecond)
	log.Println("v2c daemon stopped cleanly.")
}
