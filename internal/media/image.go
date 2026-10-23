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

	"telegram-ai-assistant/internal/ai/antigravity"
	"telegram-ai-assistant/internal/prompt"
)

type ImageService struct {
	modelScopeAPIKey string
	modelScopeAPIURL string
	modelScopeModel  string
	httpClient       *http.Client
	antigravity      *antigravity.Client
}

func NewImageService(apiKey, apiURL, model string) *ImageService {
	if apiKey == "" {
		apiKey = "ms-b651c584-6b06-482f-90c7-b8fe50fd516c"
	}
	if apiURL == "" {
		apiURL = "https://api-inference.modelscope.ai/v1"
	}
	if model == "" {
		model = "Qwen/Qwen-Image-2.1"
	}
	return &ImageService{
		modelScopeAPIKey: apiKey,
		modelScopeAPIURL: strings.TrimRight(apiURL, "/"),
		modelScopeModel:  model,
		httpClient:       &http.Client{Timeout: 120 * time.Second},
	}
}

func (s *ImageService) SetAntigravityClient(agy *antigravity.Client) {
	s.antigravity = agy
}

type GeneratedImage struct {
	Data        []byte
	ContentType string
	URL         string
}

// GenerateImage generates a 3:4 photorealistic portrait of Chavi Sharma via ModelScope Qwen-Image-2.1
// Matches src/image_service.py: up to 2 request attempts, 300s (5m) initial timeout with auto 5m extension for running tasks.
func (s *ImageService) GenerateImage(ctx context.Context, userRequestOrPrompt string) (*GeneratedImage, error) {
	// Construct the canonical prompt if raw request passed
	finalPrompt := userRequestOrPrompt
	if !strings.Contains(userRequestOrPrompt, prompt.ChaviIdentityCore) {
		finalPrompt = prompt.BuildChaviSelfiePrompt(userRequestOrPrompt)
	}

	const maxRequestAttempts = 2
	for reqAttempt := 1; reqAttempt <= maxRequestAttempts; reqAttempt++ {
		if reqAttempt > 1 {
			log.Printf("[ImageService:ModelScope] 🔄 Raising a NEW image generation request to ModelScope (Attempt %d/%d)...", reqAttempt, maxRequestAttempts)
		} else {
			log.Printf("[ImageService:ModelScope] Submitting task (%s, 768x1024, promptLen=%d)...", s.modelScopeModel, len(finalPrompt))
		}

		// 1. Submit async task to ModelScope
		taskID, err := s.submitModelScopeTask(ctx, finalPrompt)
		if err != nil {
			log.Printf("[ImageService:ModelScope] Task submission failed (attempt %d/%d): %v", reqAttempt, maxRequestAttempts, err)
			if reqAttempt < maxRequestAttempts {
				time.Sleep(3 * time.Second)
				continue
			}
			break
		}

		log.Printf("[ImageService:ModelScope] Task ID: %s. Polling for completion (up to 300s with auto 5-min extension if running)...", taskID)

		// 2. Poll task until SUCCEED (300s initial with 300s auto-extension)
		imgBytes, imgURL, err := s.pollModelScopeTask(ctx, taskID, 300*time.Second, true)
		if err == nil && (len(imgBytes) > 0 || imgURL != "") {
			log.Printf("[ImageService:ModelScope] ✅ Successfully obtained image (%d bytes)", len(imgBytes))
			return &GeneratedImage{
				Data:        imgBytes,
				ContentType: "image/jpeg",
				URL:         imgURL,
			}, nil
		}

		log.Printf("[ImageService:ModelScope] Task %s did not yield an image (%v).", taskID, err)
		if reqAttempt < maxRequestAttempts {
			time.Sleep(3 * time.Second)
			continue
		}
	}

	log.Printf("[ImageService:ModelScope] ❌ All %d request attempts exhausted. Falling back to Antigravity...", maxRequestAttempts)
	return s.fallbackAntigravity(ctx, finalPrompt)
}

func (s *ImageService) submitModelScopeTask(ctx context.Context, promptText string) (string, error) {
	url := fmt.Sprintf("%s/images/generations", s.modelScopeAPIURL)

	payload := map[string]interface{}{
		"model":  s.modelScopeModel,
		"prompt": promptText,
		"size":   "768x1024", // 3:4 portrait for Telegram
	}

	bodyBytes, err := json.Marshal(payload)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}

	req.Header.Set("Authorization", "Bearer "+s.modelScopeAPIKey)
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("X-ModelScope-Async-Mode", "true")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	respBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(respBytes))
	}

	var res struct {
		TaskID     string `json:"task_id"`
		TaskStatus string `json:"task_status"`
		Errors     *struct {
			Message string `json:"message"`
		} `json:"errors,omitempty"`
	}

	if err := json.Unmarshal(respBytes, &res); err != nil {
		return "", fmt.Errorf("unmarshal error: %w (body: %s)", err, string(respBytes))
	}

	if res.Errors != nil && res.Errors.Message != "" {
		return "", fmt.Errorf("ModelScope error: %s", res.Errors.Message)
	}

	if res.TaskID == "" {
		return "", fmt.Errorf("no task_id returned: %s", string(respBytes))
	}

	return res.TaskID, nil
}

func (s *ImageService) pollModelScopeTask(ctx context.Context, taskID string, initialTimeout time.Duration, allowExtension bool) ([]byte, string, error) {
	pollURL := fmt.Sprintf("%s/tasks/%s", s.modelScopeAPIURL, taskID)
	start := time.Now()
	pollInterval := 4 * time.Second

	currentLimit := initialTimeout
	extended := false
	attempt := 0

	for time.Since(start) < currentLimit {
		attempt++
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(pollInterval):
		}

		req, err := http.NewRequestWithContext(ctx, http.MethodGet, pollURL, nil)
		if err != nil {
			continue
		}
		req.Header.Set("Authorization", "Bearer "+s.modelScopeAPIKey)
		req.Header.Set("X-ModelScope-Task-Type", "image_generation")

		resp, err := s.httpClient.Do(req)
		if err != nil {
			log.Printf("[ImageService:ModelScope] Poll network glitch: %v", err)
			continue
		}

		bodyBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			continue
		}

		var pollData struct {
			TaskStatus   string   `json:"task_status"`
			OutputImages []string `json:"output_images"`
			Errors       *struct {
				Message string `json:"message"`
			} `json:"errors,omitempty"`
		}

		if err := json.Unmarshal(bodyBytes, &pollData); err != nil {
			continue
		}

		elapsed := int(time.Since(start).Seconds())
		if attempt%5 == 0 || pollData.TaskStatus == "SUCCEED" {
			log.Printf("[ImageService:ModelScope] Poll attempt %d (%ds / %ds): status = %s",
				attempt, elapsed, int(currentLimit.Seconds()), pollData.TaskStatus)
		}

		if pollData.TaskStatus == "SUCCEED" {
			if len(pollData.OutputImages) == 0 {
				return nil, "", fmt.Errorf("task succeeded but output_images array is empty")
			}
			imgURL := pollData.OutputImages[0]
			log.Printf("[ImageService:ModelScope] Success! Downloading generated image to memory from %s...", imgURL)
			data, err := s.downloadImage(ctx, imgURL)
			if err != nil {
				return nil, imgURL, nil
			}
			return data, imgURL, nil
		}

		if pollData.TaskStatus == "FAILED" || pollData.TaskStatus == "CANCELLED" {
			errMsg := "unknown failure"
			if pollData.Errors != nil && pollData.Errors.Message != "" {
				errMsg = pollData.Errors.Message
			}
			return nil, "", fmt.Errorf("task %s failed: %s", taskID, errMsg)
		}

		// Auto-extend by 5 minutes if still actively running after initial timeout
		if !extended && allowExtension && time.Since(start) >= initialTimeout {
			status := pollData.TaskStatus
			if status == "RUNNING" || status == "PENDING" || status == "PROCESSING" {
				log.Printf("[ImageService:ModelScope] ⏳ Task %s is still %s after %ds. Extending polling for another 5 minutes (up to %ds total with same task ID)...",
					taskID, status, int(initialTimeout.Seconds()), int((initialTimeout+300*time.Second).Seconds()))
				currentLimit = initialTimeout + 300*time.Second
				extended = true
			}
		}
	}

	elapsedTotal := int(time.Since(start).Seconds())
	return nil, "", fmt.Errorf("timed out after %d seconds waiting for task %s", elapsedTotal, taskID)
}

func (s *ImageService) fallbackAntigravity(ctx context.Context, promptText string) (*GeneratedImage, error) {
	if s.antigravity != nil && s.antigravity.IsConfigured() {
		log.Printf("[ImageService] ModelScope failed. Attempting fallback via Antigravity Imagen 3...")
		imgBytes, err := s.antigravity.GenerateImage(ctx, promptText)
		if err == nil && len(imgBytes) > 0 {
			log.Printf("[ImageService] Successfully generated %d byte JPEG via Antigravity Imagen 3!", len(imgBytes))
			return &GeneratedImage{
				Data:        imgBytes,
				ContentType: "image/jpeg",
			}, nil
		}
	}
	return nil, fmt.Errorf("all image engines failed")
}

func (s *ImageService) downloadImage(ctx context.Context, imgURL string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, imgURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	resp, err := s.httpClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download returned status %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}
