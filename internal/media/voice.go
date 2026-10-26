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

	"telegram-ai-assistant/internal/prompt"
)

type VoiceService struct {
	apiKey      string
	referenceID string
	httpClient  *http.Client
}

func NewVoiceService(apiKey string, refID ...string) *VoiceService {
	chosenRef := "ac315c50a9734bb7a608102fce24e601"
	if len(refID) > 0 && strings.TrimSpace(refID[0]) != "" {
		chosenRef = strings.TrimSpace(refID[0])
	}
	return &VoiceService{
		apiKey:      apiKey,
		referenceID: chosenRef,
		httpClient:  &http.Client{Timeout: 120 * time.Second},
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

	// 1. Normalize emotion tags to Fish Audio standard format ([sigh], [laughing], etc.)
	normalizedText := prompt.NormalizeEmotionTags(text)
	if len(normalizedText) > 2500 {
		normalizedText = normalizedText[:2500]
	}

	log.Printf("[VoiceService:FishAudio] Synthesizing script: %q (VoiceRef=%s)", normalizedText, s.referenceID)

	reqBody := TTSRequest{
		Text:        normalizedText,
		ReferenceID: s.referenceID,
		Format:      "mp3",
		Latency:     "balanced",
	}

	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal TTS request: %w", err)
	}

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

	log.Printf("[VoiceService:FishAudio] ✅ Generated %d bytes of audio successfully", len(audioData))
	return audioData, nil
}
