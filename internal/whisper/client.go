package whisper

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"strings"
	"time"
)

var (
	// ErrServerUnreachable is returned when whisper-server cannot be contacted.
	ErrServerUnreachable = errors.New("whisper server unreachable")
	// ErrInferenceFailed is returned when whisper-server returns an error response.
	ErrInferenceFailed = errors.New("inference request failed")
)

// ClientConfig holds settings for communicating with the whisper.cpp HTTP server.
type ClientConfig struct {
	BaseURL     string
	Language    string
	Timeout     time.Duration
	MaxRetries  int
	RetryDelay  time.Duration
}

// Client interacts with the local whisper.cpp server via HTTP.
type Client struct {
	cfg        ClientConfig
	httpClient *http.Client
}

// InferenceResponse represents the JSON output from whisper.cpp server /inference.
type InferenceResponse struct {
	Text string `json:"text"`
}

// NewClient returns a new Client with sensible defaults.
func NewClient(cfg ClientConfig) *Client {
	if cfg.BaseURL == "" {
		cfg.BaseURL = "http://127.0.0.1:8080"
	}
	// Strip any trailing slash
	cfg.BaseURL = strings.TrimRight(cfg.BaseURL, "/")

	if cfg.Language == "" {
		cfg.Language = "auto"
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 20 * time.Second
	}
	if cfg.MaxRetries <= 0 {
		cfg.MaxRetries = 3
	}
	if cfg.RetryDelay <= 0 {
		cfg.RetryDelay = 250 * time.Millisecond
	}

	return &Client{
		cfg: cfg,
		httpClient: &http.Client{
			Timeout: cfg.Timeout,
		},
	}
}

// Ping checks if the whisper server is online and accepting HTTP requests.
func (c *Client) Ping(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.cfg.BaseURL+"/", nil)
	if err != nil {
		return err
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrServerUnreachable, err)
	}
	_ = resp.Body.Close()
	return nil
}

// WaitReady polls until the server is ready or context is cancelled.
func (c *Client) WaitReady(ctx context.Context, timeout time.Duration) error {
	ctxTimeout, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	ticker := time.NewTicker(200 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctxTimeout.Done():
			return fmt.Errorf("timed out waiting for whisper server at %s: %w", c.cfg.BaseURL, ctxTimeout.Err())
		case <-ticker.C:
			if err := c.Ping(ctxTimeout); err == nil {
				return nil
			}
		}
	}
}

// Transcribe sends the in-memory WAV buffer to /inference with automatic retry logic.
func (c *Client) Transcribe(ctx context.Context, wavData []byte) (string, error) {
	if len(wavData) == 0 {
		return "", errors.New("cannot transcribe empty audio buffer")
	}

	var lastErr error
	delay := c.cfg.RetryDelay

	for attempt := 0; attempt < c.cfg.MaxRetries; attempt++ {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		default:
		}

		if attempt > 0 {
			time.Sleep(delay)
			delay *= 2
		}

		text, err := c.doInference(ctx, wavData)
		if err == nil {
			return text, nil
		}

		lastErr = err
	}

	return "", fmt.Errorf("transcription failed after %d attempts: %w", c.cfg.MaxRetries, lastErr)
}

func (c *Client) doInference(ctx context.Context, wavData []byte) (string, error) {
	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)

	// Audio file form field
	part, err := writer.CreateFormFile("file", "audio.wav")
	if err != nil {
		return "", fmt.Errorf("failed to create multipart file part: %w", err)
	}
	if _, err := io.Copy(part, bytes.NewReader(wavData)); err != nil {
		return "", fmt.Errorf("failed to copy wav payload: %w", err)
	}

	// Parameters
	if err := writer.WriteField("response_format", "json"); err != nil {
		return "", err
	}
	if c.cfg.Language != "" && c.cfg.Language != "auto" {
		if err := writer.WriteField("language", c.cfg.Language); err != nil {
			return "", err
		}
	}
	if err := writer.WriteField("temperature", "0.0"); err != nil {
		return "", err
	}

	if err := writer.Close(); err != nil {
		return "", fmt.Errorf("failed to close multipart writer: %w", err)
	}

	reqURL := c.cfg.BaseURL + "/inference"
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, reqURL, body)
	if err != nil {
		return "", fmt.Errorf("failed to construct HTTP request: %w", err)
	}

	req.Header.Set("Content-Type", writer.FormDataContentType())

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("%w: %v", ErrServerUnreachable, err)
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("%w: HTTP status %d: %s", ErrInferenceFailed, resp.StatusCode, string(respBytes))
	}

	// Try parsing JSON format
	var result InferenceResponse
	if err := json.Unmarshal(respBytes, &result); err == nil && result.Text != "" {
		return result.Text, nil
	}

	// Fallback to raw string if server returned plain text
	raw := strings.TrimSpace(string(respBytes))
	return raw, nil
}
