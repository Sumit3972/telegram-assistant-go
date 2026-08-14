package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type VoiceService struct {
	apiKey     string
	httpClient *http.Client
}

func NewVoiceService(apiKey string) *VoiceService {
	return &VoiceService{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 90 * time.Second},
	}
}

type TTSRequest struct {
	Text        string `json:"text"`
	ReferenceID string `json:"reference_id"`
	Format      string `json:"format"`
	Latency     string `json:"latency"`
}

func (s *VoiceService) GenerateVoice(ctx context.Context, text string) ([]byte, error) {
	if s.apiKey == "" {
		return nil, fmt.Errorf("FISH_AUDIO_API_KEY is not configured")
	}

	cleanText := strings.TrimSpace(text)
	if len(cleanText) > 2000 {
		cleanText = cleanText[:2000]
	}

	reqBody := TTSRequest{
		Text:        cleanText,
		ReferenceID: "a5ee840053874f6f9c5a612291b7ceb0",
		Format:      "mp3",
		Latency:     "balanced",
	}

	bodyBytes, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.fish.audio/v1/tts", bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create TTS request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("model", "s2.1-pro-free")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("TTS request failed: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("Fish Audio API error %d: %s", resp.StatusCode, string(errBytes))
	}

	audioData, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read audio stream: %w", err)
	}

	log.Printf("[TTS:FishAudio] Generated %d bytes of audio successfully", len(audioData))
	return audioData, nil
}
