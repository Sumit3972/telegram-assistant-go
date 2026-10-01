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

// QualityEvaluation contains the result of OpenJev evaluating an AI candidate response.
type QualityEvaluation struct {
	Approved    bool    // true if approved
	Defect      string  // "none", "too_long", "robotic_assistant", "irrelevant", "unnatural_hinglish", "voice_broken"
	Feedback    string  // actionable advice for prompt rewriting
	Confidence  float64 // confidence of decision
}

// EvaluateResponse uses OpenJev System One to rigorously evaluate if the AI's generated response (text and/or voice)
// sounds like a real modern Indian girl texting on a phone: short, punchy, authentic Hinglish, zero robotic vibes.
func (c *Client) EvaluateResponse(ctx context.Context, userText, replyText, voiceText string) (QualityEvaluation, error) {
	defaultApprove := QualityEvaluation{
		Approved:   true,
		Defect:     "none",
		Feedback:   "",
		Confidence: 1.0,
	}

	cleanUser := strings.TrimSpace(userText)
	cleanReply := strings.TrimSpace(replyText)
	cleanVoice := strings.TrimSpace(voiceText)

	if cleanReply == "" && cleanVoice == "" {
		return defaultApprove, nil
	}

	if c.apiKey == "" {
		return defaultApprove, nil
	}

	state := fmt.Sprintf("User Message: %q\nProposed AI Girl Reply: %q", cleanUser, cleanReply)
	if cleanVoice != "" {
		state += fmt.Sprintf("\nVoice Note Script: %q", cleanVoice)
	}

	reqBody := systemOneRequest{
		Model: DefaultModel,
		State: state,
		Questions: map[string]interface{}{
			"approved": choiceQuestion{
				Type:         "choice",
				Instructions: "Judge whether this response sounds like a real modern 25yo Indian girl texting on WhatsApp: short, punchy, casual, natural Hinglish (using words like yrr, haina, acha), relevant, and completely non-robotic.",
				Criteria: map[string]string{
					"yes": "High quality: sounds like a real human girl texting on her phone, short, crisp, relevant, authentic vibe.",
					"no":  "Unacceptable: sounds robotic, assistant-like, too formal, essay-like, repetitive, or irrelevant.",
				},
			},
			"defect": choiceQuestion{
				Type:         "choice",
				Instructions: "If not approved, what is the main defect?",
				Criteria: map[string]string{
					"none":               "No defect, message is good.",
					"too_long":           "Too long or explanatory. Real girls send short 1-2 sentence bursts.",
					"robotic_assistant":  "Sounds like an AI bot, polite customer service, or assistant instead of a real companion girl.",
					"unnatural_hinglish": "Stiff, textbook Hindi or awkward phrasing; lacks casual words like 'yrr', 'acha', 'haina'.",
					"irrelevant":         "Doesn't actually answer or acknowledge what the user said.",
					"voice_broken":       "Voice note script is unnatural, repetitive, or contains weird bracket tags.",
				},
			},
		},
	}

	jsonBytes, err := json.Marshal(reqBody)
	if err != nil {
		return defaultApprove, fmt.Errorf("marshal OpenJev quality request: %w", err)
	}

	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(jsonBytes))
	if err != nil {
		return defaultApprove, fmt.Errorf("create OpenJev quality request: %w", err)
	}

	httpReq.Header.Set("Authorization", "Bearer "+c.apiKey)
	httpReq.Header.Set("Content-Type", "application/json")

	start := time.Now()
	resp, err := c.httpClient.Do(httpReq)
	if err != nil {
		log.Printf("[OpenJev] ⚠️ Quality check failed: %v (took %v); defaulting to approved", err, time.Since(start))
		return defaultApprove, nil
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("[OpenJev] ⚠️ Failed reading quality response: %v; defaulting to approved", err)
		return defaultApprove, nil
	}

	if resp.StatusCode != http.StatusOK {
		log.Printf("[OpenJev] ⚠️ Quality check HTTP %d: %s; defaulting to approved", resp.StatusCode, string(bodyBytes))
		return defaultApprove, nil
	}

	var res systemOneResponse
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		log.Printf("[OpenJev] ⚠️ JSON unmarshal error in quality check: %v; defaulting to approved", err)
		return defaultApprove, nil
	}

	if res.Error != nil && res.Error.Message != "" {
		log.Printf("[OpenJev] ⚠️ Quality API returned error: %s; defaulting to approved", res.Error.Message)
		return defaultApprove, nil
	}

	approvedAns, hasApproved := res.Answers["approved"]
	if !hasApproved {
		return defaultApprove, nil
	}

	isApproved := strings.EqualFold(strings.TrimSpace(approvedAns.Choice), "yes")
	defect := "none"
	if defAns, hasDef := res.Answers["defect"]; hasDef {
		defect = strings.ToLower(strings.TrimSpace(defAns.Choice))
	}

	feedback := ""
	switch defect {
	case "too_long":
		feedback = "Make it much shorter (4 to 12 words max). Real girls send quick punchy texts, never paragraphs."
	case "robotic_assistant":
		feedback = "Sound like a real girl, not an AI or helper. Use casual attitude, nakhre, or witty tease."
	case "unnatural_hinglish":
		feedback = "Use authentic casual Hinglish particles like 'yrr', 'haina', 'acha', or 'chal na' naturally."
	case "irrelevant":
		feedback = "Directly reply to what the user said instead of evading."
	case "voice_broken":
		feedback = "Write a natural 1-sentence voice note script that sounds authentic when spoken out loud."
	}

	log.Printf("[OpenJev] 🎯 Quality review in %v: approved=%v (conf=%.2f, defect=%s)",
		time.Since(start), isApproved, approvedAns.Confidence, defect)

	return QualityEvaluation{
		Approved:   isApproved,
		Defect:     defect,
		Feedback:   feedback,
		Confidence: approvedAns.Confidence,
	}, nil
}

