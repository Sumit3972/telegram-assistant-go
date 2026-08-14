package main

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"
)

func main() {
	apiKey := "sk-keX97M0DcDPCTdR9Udg7fw"
	apiURL := "https://api.anyapi.ai/v1/images/generations"

	prompt := "A photorealistic 8K cinema portrait of Janvi, an exceptionally gorgeous 25yo Bollywood actress from Mumbai with the youthful ethereal beauty and charming dimples of Alia Bhatt, radiant glowing fair golden complexion with natural rosy flush, soft round-oval face, cute deep dimples on her cheeks, sweet infectious dimpled smile, large expressive warm dark almond eyes with delicate lashes, graceful arched eyebrows, cute slender nose, naturally plump rose-pink lips, and luxurious glossy jet-black wavy hair cascading in soft layers past her shoulders, wearing an elegant fitted black satin dress, 85mm portrait lens, 8K resolution, photorealistic."

	models := []string{
		"openai/gpt-5-image-mini",
		"openai/gpt-5-image",
		"google/gemini-3.1-flash-image-preview",
		"google/gemini-3-pro-image",
	}

	client := &http.Client{Timeout: 60 * time.Second}

	for _, model := range models {
		fmt.Printf("\n🧪 Testing: %s\n", model)

		// Test with default response format (URL & b64)
		reqBody := map[string]any{
			"model":  model,
			"prompt": prompt,
			"n":      1,
			"size":   "1024x1024",
		}
		bodyBytes, _ := json.Marshal(reqBody)

		req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, apiURL, bytes.NewReader(bodyBytes))
		if err != nil {
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", "Bearer "+apiKey)

		startTime := time.Now()
		resp, err := client.Do(req)
		elapsed := time.Since(startTime)

		if err != nil {
			fmt.Printf("❌ Failed (%s): %v\n", elapsed, err)
			continue
		}

		respBytes, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		fmt.Printf("Status: %d (took %s)\n", resp.StatusCode, elapsed)

		var rawMap map[string]any
		_ = json.Unmarshal(respBytes, &rawMap)

		if dataArr, ok := rawMap["data"].([]any); ok && len(dataArr) > 0 {
			item := dataArr[0].(map[string]any)
			if u, ok := item["url"].(string); ok && u != "" {
				fmt.Printf("✅ SUCCESS! Got URL: %s\n", u)
				// Save sample
				saveImageFromURL(u, fmt.Sprintf("sample_%s.jpg", sanitizeName(model)))
				break
			}
			if b64, ok := item["b64_json"].(string); ok && b64 != "" {
				fmt.Printf("✅ SUCCESS! Got b64_json (length %d)\n", len(b64))
				decoded, _ := base64.StdEncoding.DecodeString(b64)
				_ = os.WriteFile(fmt.Sprintf("sample_%s.jpg", sanitizeName(model)), decoded, 0644)
				break
			}
		} else {
			fmt.Printf("⚠️ Empty or error data: %s\n", string(respBytes))
		}
	}
}

func saveImageFromURL(imgURL, filename string) {
	resp, err := http.Get(imgURL)
	if err != nil {
		return
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	_ = os.WriteFile(filename, data, 0644)
	fmt.Printf("💾 Saved to %s (%d bytes)\n", filename, len(data))
}

func sanitizeName(m string) string {
	var s string
	for _, c := range m {
		if (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') {
			s += string(c)
		} else {
			s += "_"
		}
	}
	return s
}
