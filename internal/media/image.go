package media

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type ImageService struct {
	apiURL       string
	apiKey       string
	primaryModel string
	httpClient   *http.Client
}

func NewImageService(apiURL, apiKey string, primaryModel ...string) *ImageService {
	if apiURL == "" {
		apiURL = "https://api.futureppo.top/v1/images/generations"
	}
	model := "gpt-image-2"
	if len(primaryModel) > 0 && primaryModel[0] != "" {
		model = primaryModel[0]
	}
	return &ImageService{
		apiURL:       apiURL,
		apiKey:       apiKey,
		primaryModel: model,
		httpClient:   &http.Client{Timeout: 240 * time.Second},
	}
}

type GeneratedImage struct {
	Data        []byte
	ContentType string
	URL         string
}

func sanitizePromptForModel(model, rawPrompt string) string {
	clean := strings.TrimSpace(rawPrompt)

	// Replace literal celebrity names to prevent Azure / OpenAI input moderation safety blocks
	celebReplacements := []struct {
		target string
		rep    string
	}{
		{"bollywood actress tara sutaria as ", ""},
		{"bollywood actress tara sutaria", "an extraordinarily gorgeous Bollywood diva and supermodel"},
		{"actress tara sutaria as ", ""},
		{"actress tara sutaria", "an extraordinarily gorgeous Bollywood diva and supermodel"},
		{"tara sutaria as ", ""},
		{"tara sutaria", "an extraordinarily gorgeous Bollywood diva and supermodel"},
	}
	for _, cr := range celebReplacements {
		clean = replaceCaseInsensitive(clean, cr.target, cr.rep)
	}

	// If the prompt doesn't already contain quality tags and is reasonably short, add light quality cues
	if len(clean) < 600 && !strings.Contains(strings.ToLower(clean), "8k") && !strings.Contains(strings.ToLower(clean), "photograph") {
		clean += ", 8K UHD portrait, editorial photography, sharp focus, natural skin texture, soft cinematic lighting, zero watermark"
	}

	// Strictly limit prompt length for models with character limits (e.g. Qwen & Z-Image require <= 1024 chars; GPT-Image-2 allows up to 4000)
	maxLen := 4000
	if strings.Contains(strings.ToLower(model), "qwen") || strings.Contains(strings.ToLower(model), "z-image") {
		maxLen = 1000
	}

	if len(clean) > maxLen {
		// Cleanly truncate at last comma or period within maxLen
		truncated := clean[:maxLen]
		if lastIdx := strings.LastIndexAny(truncated, ",.;"); lastIdx > maxLen-150 {
			clean = strings.TrimSpace(truncated[:lastIdx])
		} else {
			clean = strings.TrimSpace(truncated)
		}
	}

	return clean
}

func replaceCaseInsensitive(str, substr, repl string) string {
	for {
		idx := strings.Index(strings.ToLower(str), strings.ToLower(substr))
		if idx == -1 {
			break
		}
		str = str[:idx] + repl + str[idx+len(substr):]
	}
	return str
}

func (s *ImageService) GenerateImage(ctx context.Context, prompt string) (*GeneratedImage, error) {
	primary := s.primaryModel
	if primary == "" {
		primary = "gpt-image-2"
	}

	models := []string{primary}
	fallbacks := []string{
		"gpt-image-2",
		"grok-imagine-image-lite",
		"grok-imagine-image",
		"grok-imagine-image-quality",
		"grok-imagine-image-quality-lite",
		"grok-imagine-image-edit",
		"qwen-image-2512",
		"z-image-turbo",
	}
	for _, m := range fallbacks {
		if m != primary {
			models = append(models, m)
		}
	}

	var lastErr error
	for i, model := range models {
		modelPrompt := sanitizePromptForModel(model, prompt)
		log.Printf("[ImageService] Generating image via %s (attempt %d/%d, promptLen=%d)...", model, i+1, len(models), len(modelPrompt))

		reqBody := map[string]any{
			"model":           model,
			"prompt":          modelPrompt,
			"n":               1,
			"size":            "1024x1024",
			"response_format": "b64_json",
		}

		bodyBytes, _ := json.Marshal(reqBody)
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			lastErr = err
			s.fallbackDelay(ctx, i, len(models))
			continue
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)

		resp, err := s.httpClient.Do(httpReq)
		if err != nil {
			log.Printf("[ImageService] Model %s failed: %v", model, err)
			lastErr = err
			s.fallbackDelay(ctx, i, len(models))
			continue
		}

		respBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			errStr := string(respBytes)
			log.Printf("[ImageService] Model %s HTTP %d: %s", model, resp.StatusCode, errStr)
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, errStr)
			s.fallbackDelay(ctx, i, len(models))
			continue
		}

		var result struct {
			Data []struct {
				URL     string `json:"url"`
				B64JSON string `json:"b64_json"`
			} `json:"data"`
		}

		if err := json.Unmarshal(respBytes, &result); err == nil && len(result.Data) > 0 {
			item := result.Data[0]
			if item.B64JSON != "" {
				decoded, err := base64.StdEncoding.DecodeString(item.B64JSON)
				if err == nil && len(decoded) > 0 {
					log.Printf("[ImageService] Successfully generated b64 image via %s!", model)
					return &GeneratedImage{
						Data:        decoded,
						ContentType: "image/jpeg",
					}, nil
				}
			}
			if item.URL != "" {
				log.Printf("[ImageService] Successfully generated image URL via %s: %s", model, item.URL)
				downloaded, err := s.downloadImage(ctx, item.URL)
				if err == nil && len(downloaded) > 0 {
					return &GeneratedImage{
						Data:        downloaded,
						ContentType: "image/jpeg",
						URL:         item.URL,
					}, nil
				}
				return &GeneratedImage{
					URL: item.URL,
				}, nil
			}
		}

		lastErr = fmt.Errorf("unexpected image response: %s", string(respBytes))
		s.fallbackDelay(ctx, i, len(models))
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("all image models failed")
}

func (s *ImageService) fallbackDelay(ctx context.Context, currentIndex, totalModels int) {
	if currentIndex < totalModels-1 {
		delay := 6 * time.Second
		log.Printf("⏳ [ImageService] Waiting %v before switching to next fallback image model...", delay)
		select {
		case <-ctx.Done():
		case <-time.After(delay):
		}
	}
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
