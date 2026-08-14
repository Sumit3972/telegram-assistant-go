package media

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type MusicService struct {
	baseURL    string
	secretKey  string
	httpClient *http.Client
}

func NewMusicService(baseURL, secretKey string) *MusicService {
	return &MusicService{
		baseURL:    strings.TrimRight(baseURL, "/"),
		secretKey:  secretKey,
		httpClient: &http.Client{Timeout: 20 * time.Second},
	}
}

type MusicResponse struct {
	Success       bool   `json:"success"`
	Status        string `json:"status,omitempty"`
	Title         string `json:"title,omitempty"`
	QueuePosition int    `json:"queue_position,omitempty"`
	Message       string `json:"message,omitempty"`
}

func (s *MusicService) ExecuteMusicCommand(ctx context.Context, chatID, toolName string, songName string) (string, error) {
	if s.baseURL == "" {
		return "❌ Error: Music bot is not configured on the server. Please set MUSIC_BOT_URL in .env", nil
	}

	endpoint := ""
	body := map[string]any{
		"chat_id": chatID,
	}

	switch toolName {
	case "play_music":
		endpoint = "/api/play"
		body["song_name"] = songName
	case "skip_music":
		endpoint = "/api/skip"
	case "pause_music":
		endpoint = "/api/pause"
	case "resume_music":
		endpoint = "/api/resume"
	case "stop_music":
		endpoint = "/api/stop"
	default:
		return fmt.Sprintf("❌ Error: Unknown music tool %s", toolName), nil
	}

	bodyBytes, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.baseURL+endpoint, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-API-Key", s.secretKey)

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return fmt.Sprintf("❌ Connection error to music bot: %v", err), nil
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		return fmt.Sprintf("❌ Music bot error %d: %s", resp.StatusCode, string(errBytes)), nil
	}

	var resData MusicResponse
	if err := json.NewDecoder(resp.Body).Decode(&resData); err != nil {
		return "❌ Failed to parse music bot response", nil
	}

	if resData.Success {
		if toolName == "play_music" {
			if resData.Status == "queued" {
				return fmt.Sprintf("🎵 Added to queue (position #%d): \"%s\"", resData.QueuePosition, resData.Title), nil
			}
			if resData.Status == "sent_as_file" {
				return fmt.Sprintf("🎵 **\"%s\"**\n\nVoice Chat is not active, so I have sent the audio file directly in the chat for you!", resData.Title), nil
			}
			return fmt.Sprintf("🎶 Now playing: \"%s\"", resData.Title), nil
		}
		if resData.Message != "" {
			return fmt.Sprintf("✅ %s", resData.Message), nil
		}
		return "✅ Action completed", nil
	}

	if resData.Message != "" {
		return fmt.Sprintf("❌ Failed: %s", resData.Message), nil
	}
	return "❌ Failed: Unknown error from music bot", nil
}
