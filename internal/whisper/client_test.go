package whisper

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestClient_Transcribe_JSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/inference" {
			http.NotFound(w, r)
			return
		}
		if r.Method != http.MethodPost {
			http.Error(w, "bad method", http.StatusMethodNotAllowed)
			return
		}
		_ = r.ParseMultipartForm(10 << 20)
		lang := r.FormValue("language")
		if lang != "en" {
			t.Errorf("expected language en, got %q", lang)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"text":" Hello world from whisper"}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		Language:   "en",
		MaxRetries: 2,
		RetryDelay: 10 * time.Millisecond,
	})

	text, err := client.Transcribe(context.Background(), []byte("dummy-wav-data"))
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}

	if text != " Hello world from whisper" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestClient_Transcribe_PlainTextFallback(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("Raw transcribed string"))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		MaxRetries: 1,
	})

	text, err := client.Transcribe(context.Background(), []byte("dummy-wav-data"))
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}

	if text != "Raw transcribed string" {
		t.Fatalf("unexpected text: %q", text)
	}
}

func TestClient_Transcribe_Retry(t *testing.T) {
	var attempts int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		count := atomic.AddInt32(&attempts, 1)
		if count < 2 {
			http.Error(w, "server busy", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"text":"Retried successfully"}`))
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL:    server.URL,
		MaxRetries: 3,
		RetryDelay: 10 * time.Millisecond,
	})

	text, err := client.Transcribe(context.Background(), []byte("dummy-wav-data"))
	if err != nil {
		t.Fatalf("Transcribe failed: %v", err)
	}

	if text != "Retried successfully" {
		t.Fatalf("unexpected text: %q", text)
	}

	if atomic.LoadInt32(&attempts) != 2 {
		t.Fatalf("expected 2 attempts, got %d", attempts)
	}
}

func TestClient_WaitReady(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := NewClient(ClientConfig{
		BaseURL: server.URL,
	})

	err := client.WaitReady(context.Background(), 2*time.Second)
	if err != nil {
		t.Fatalf("WaitReady failed: %v", err)
	}
}
