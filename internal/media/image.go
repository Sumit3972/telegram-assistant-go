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
	"time"

	"telegram-ai-assistant/internal/assets"
)

type ImageService struct {
	apiURL       string
	apiKey       string
	primaryModel string
	httpClient   *http.Client
}

func NewImageService(apiURL, apiKey string, primaryModel ...string) *ImageService {
	if apiURL == "" {
		apiURL = "https://api.anyapi.ai/v1/images/generations"
	}
	model := "openai/gpt-5-image-mini"
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

func (s *ImageService) GenerateImage(ctx context.Context, prompt string) (*GeneratedImage, error) {
	primary := s.primaryModel
	if primary == "" {
		primary = "openai/gpt-5-image-mini"
	}

	models := []string{primary}
	fallbacks := []string{
		"openai/gpt-5-image",
		"google/gemini-3-pro-image",
		"google/gemini-3.1-flash-image-preview",
		
	}
	for _, m := range fallbacks {
		if m != primary {
			models = append(models, m)
		}
	}

	refImages := assets.GetJanviAIReferenceImages()
	fullPrompt := prompt + ", 8K resolution, photorealistic, natural skin texture, masterpiece, authentic photography, no watermark, no text"

	var lastErr error
	for _, model := range models {
		log.Printf("[ImageService] Generating image via %s...", model)

		reqBody := map[string]any{
			"model":           model,
			"prompt":          fullPrompt,
			"n":               1,
			"size":            "1024x1024",
			"response_format": "b64_json",
		}
		if len(refImages) > 0 {
			reqBody["image_urls"] = refImages
			reqBody["image"] = refImages[0]
		}

		bodyBytes, _ := json.Marshal(reqBody)
		httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			lastErr = err
			continue
		}

		httpReq.Header.Set("Content-Type", "application/json")
		httpReq.Header.Set("Authorization", "Bearer "+s.apiKey)

		resp, err := s.httpClient.Do(httpReq)
		if err != nil {
			log.Printf("[ImageService] Model %s failed: %v", model, err)
			lastErr = err
			continue
		}

		respBytes, err := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 400 {
			errStr := string(respBytes)
			log.Printf("[ImageService] Model %s HTTP %d: %s", model, resp.StatusCode, errStr)
			lastErr = fmt.Errorf("HTTP %d: %s", resp.StatusCode, errStr)
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
	}

	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("all image models failed")
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
