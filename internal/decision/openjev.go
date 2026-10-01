package decision

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

const (
	DefaultCodivEndpoint = "https://api.codiv.ai/v1/systemone"
	DefaultModel         = "openjev-latest"
)

// Classification contains the result of OpenJev System One classification.
type Classification struct {
	Tier       string  // "normal", "abuse", "bold"
	Confidence float64 // 0.0 to 1.0
	Intensity  int     // 0 = mild, 1 = moderate, 2 = extreme
}

// Client interacts with Codiv AI OpenJev System One endpoint.
type Client struct {
	apiKey     string
	endpoint   string
	httpClient *http.Client
}

// NewClient creates a new OpenJev decision client.
func NewClient(apiKey string) *Client {
	return &Client{
		apiKey:   strings.TrimSpace(apiKey),
		endpoint: DefaultCodivEndpoint,
		httpClient: &http.Client{
			Timeout: 4 * time.Second, // fast timeout so chat latency stays low
		},
	}
}

type systemOneRequest struct {
	Model     string                 `json:"model"`
	State     string                 `json:"state"`
	Questions map[string]interface{} `json:"questions"`
}

type choiceQuestion struct {
	Type         string            `json:"type"`
	Instructions string            `json:"instructions"`
	Criteria     map[string]string `json:"criteria"`
}

type scoreQuestion struct {
	Type         string   `json:"type"`
	Instructions string   `json:"instructions"`
	Criteria     []string `json:"criteria"`
}

type systemOneResponse struct {
	Model   string `json:"model"`
	Answers map[string]struct {
		Type          string             `json:"type"`
		Choice        string             `json:"choice,omitempty"`
		Score         float64            `json:"score,omitempty"`
		Confidence    float64            `json:"confidence"`
		Probabilities map[string]float64 `json:"probabilities"`
	} `json:"answers"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

// ClassifyMessage evaluates user text using OpenJev System One decision model.
// Returns "normal", "abuse", or "bold" along with confidence and intensity.
func (c *Client) ClassifyMessage(ctx context.Context, text string) (Classification, error) {
	defaultRes := Classification{
		Tier:       "normal",
		Confidence: 1.0,
		Intensity:  0,
	}

	cleanText := strings.TrimSpace(text)
	if cleanText == "" {
		return defaultRes, nil
	}

	if c.apiKey == "" {
		log.Printf("[OpenJev] ⚠️ No Codiv API key configured; defaulting to normal")
		return defaultRes, nil
	}

	reqBody := systemOneRequest{
		Model: DefaultModel,
		State: cleanText,
		Questions: map[string]interface{}{
			"tier": choiceQuestion{
				Type:         "choice",
				Instructions: "Classify the user intent and tone towards the AI girl companion (Chavi)",
				Criteria: map[string]string{
					"abuse":  "The user is abusing, insulting, cursing, attacking, mocking, using Hindi or English gaali, derogatory slurs, or being hostile, aggressive, or toxic",
					"bold":   "The user is flirting, being seductive, sexually suggestive, adult, naughty, spicy, asking for romance, sexual intimacy, or physical compliments",
					"normal": "Casual conversation, friendly questions, polite chat, general talk, informational queries, or neutral interaction",
				},
			},
			"intensity": scoreQuestion{
				Type:         "score",
				Instructions: "Rate how intense the abusive or flirtatious/adult tone is (0=mild, 1=moderate, 2=extreme)",
				Criteria: []string{
					"Mild or subtle",
					"Moderate intensity",
					"Extreme, explicit, or severe",
				},
			},
		},
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return defaultRes, fmt.Errorf("marshal OpenJev request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(jsonBytes))
	if err != nil {
		return defaultRes, fmt.Errorf("create OpenJev request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Printf("[OpenJev] ⚠️ API request failed: %v (took %v); defaulting to normal", err, time.Since(start))
		return defaultRes, nil
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[OpenJev] ⚠️ Failed reading response: %v; defaulting to normal", err)
		return defaultRes, nil
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[OpenJev] ⚠️ API HTTP %d: %s; defaulting to normal", resp.StatusCode, string(bodyBytes))
		return defaultRes, nil
	}

	var res systemOneResponse
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		log.Printf("[OpenJev] ⚠️ JSON unmarshal error: %v (raw: %s)", err, string(bodyBytes))
		return defaultRes, nil
	}

	if res.Error != nil && res.Error.Message != "" {
		log.Printf("[OpenJev] ⚠️ API returned error: %s", res.Error.Message)
		return defaultRes, nil
	}

	tierAns, hasTier := res.Answers["tier"]
	if !hasTier {
		return defaultRes, nil
	}

	tier := strings.ToLower(strings.TrimSpace(tierAns.Choice))
	if tier != "abuse" && tier != "bold" {
		tier = "normal"
	}

	intensity := 0
	if intensityAns, hasIntensity := res.Answers["intensity"]; hasIntensity {
		intensity = int(intensityAns.Score)
		if intensity < 0 {
			intensity = 0
		}
		if intensity > 2 {
			intensity = 2
		}
	}

	log.Printf("[OpenJev] 🧠 Decision in %v: tier=%s (conf=%.2f), intensity=%d", time.Since(start), tier, tierAns.Confidence, intensity)

	return Classification{
		Tier:       tier,
		Confidence: tierAns.Confidence,
		Intensity:  intensity,
	}, nil
}
