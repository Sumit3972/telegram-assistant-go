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

	"telegram-ai-assistant/internal/prompt"
)

const (
	DefaultCodivEndpoint = "https://api.codiv.ai/v1/systemone"
	DefaultModel         = "openjev-latest"
)

// PreDecisionResult matches Loop 1 from src/openjev_service.py
type PreDecisionResult struct {
	ShouldReply      bool   `json:"should_reply"`
	Intent           string `json:"intent"`
	NeedsWebSearch   bool   `json:"needs_web_search"`
	ExclusiveChannel string `json:"exclusive_channel"`
	ShouldReact      bool   `json:"should_react"`
	ReactionEmoji    string `json:"reaction_emoji"`
	AffectionDelta   int    `json:"affection_delta"`
	UserGender       string `json:"user_gender"`
	AbuseSeverity    string `json:"abuse_severity"` // "none", "severe_threat", "hostile_abuse", "annoyance"
	IsAbuse          bool   `json:"is_abuse"`
	MaxStrikes       int    `json:"max_strikes"`
}

// CritiqueResult matches Loop 2 from src/openjev_service.py
type CritiqueResult struct {
	Passed   bool   `json:"passed"`
	Feedback string `json:"feedback"`
}

// Classification legacy backward compatibility
type Classification struct {
	Tier       string
	Confidence float64
	Intensity  int
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
			Timeout: 8 * time.Second,
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

type noulQuestion struct {
	Type         string `json:"type"`
	Instructions string `json:"instructions"`
}

type answerItem struct {
	Type          string             `json:"type"`
	Choice        string             `json:"choice,omitempty"`
	Score         float64            `json:"score,omitempty"`
	Noul          float64            `json:"noul,omitempty"`
	Confidence    float64            `json:"confidence"`
	Probabilities map[string]float64 `json:"probabilities,omitempty"`
}

type systemOneResponse struct {
	Model   string                `json:"model"`
	Answers map[string]answerItem `json:"answers"`
	Error   *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func (c *Client) callSystemOne(ctx context.Context, state string, questions map[string]interface{}) (map[string]answerItem, error) {
	if c.apiKey == "" {
		return nil, fmt.Errorf("CODIV_API_KEY is not configured")
	}

	payload := systemOneRequest{
		Model:     DefaultModel,
		State:     state,
		Questions: questions,
	}

	jsonBytes, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(jsonBytes))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.apiKey)
	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close()

	bodyBytes, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("read body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("OpenJev HTTP %d: %s", resp.StatusCode, string(bodyBytes))
	}

	var res systemOneResponse
	if err := json.Unmarshal(bodyBytes, &res); err != nil {
		return nil, fmt.Errorf("unmarshal response: %w", err)
	}

	if res.Error != nil && res.Error.Message != "" {
		return nil, fmt.Errorf("OpenJev API error: %s", res.Error.Message)
	}

	return res.Answers, nil
}

// PreDecision implements Loop 1 from src/openjev_service.py
func (c *Client) PreDecision(ctx context.Context, userText, historyContext, interlocutorName string) PreDecisionResult {
	loc := time.FixedZone("IST", 5*3600+30*60)
	now := time.Now().In(loc)
	currentTimeStr := now.Format("Monday, January 02, 2006 at 03:04 PM IST")

	var contextBlock string
	if strings.TrimSpace(historyContext) != "" {
		contextBlock = fmt.Sprintf("\n[Recent Conversation History (Background Context Only)]:\n%s\n", strings.TrimSpace(historyContext))
	}

	interlocutorInfo := ""
	if strings.TrimSpace(interlocutorName) != "" {
		interlocutorInfo = fmt.Sprintf("\nInterlocutor: %s", interlocutorName)
	}

	state := fmt.Sprintf(
		"Current Date & Time: %s (Year: %d)\nPersona: Chavi Sharma, 21yo BCA student in Jaipur, Rajasthan, India%s%s\n>>> CURRENT INCOMING USER MESSAGE TO EVALUATE: %q <<<",
		currentTimeStr, now.Year(), interlocutorInfo, contextBlock, userText,
	)

	questions := map[string]interface{}{
		"should_reply": noulQuestion{
			Type:         "noul",
			Instructions: "Should Chavi reply to this message. Always True (1.0) for conversations and especially for rude or abusive messages so she retaliates fiercely with street roasts.",
		},
		"intent": choiceQuestion{
			Type:         "choice",
			Instructions: "Primary intent of the CURRENT INCOMING USER MESSAGE. If the user asks for an audio, voice clip, or voice note ('voice note bejo', 'audio sunao', 'awaaz sunao', 'bol ke bhej'), choose asking_voice. If requesting a photo, picture, or selfie ('photo bhej', 'pic dikha', 'selfie'), choose asking_photo. Never confuse voice requests with photo requests.",
			Criteria: map[string]string{
				"casual_chat":  "Friendly chat, banter, asking what she is doing",
				"flirty":       "Flirting, romantic compliments, playful teasing, seductive talk",
				"bca_college":  "Talking about college, coding practicals, exams, BCA study",
				"rude_abuse":   "Direct attacks, slurs, hatred, or verbal abuse (NOTE: sexual, erotic, or naughty voice requests are NOT abuse)",
				"asking_voice": "Explicitly asking for an audio clip, voice note, moaning, whispers, or voice ('awaj', 'bol ke bhej', 'voice note', 'sunao')",
				"asking_photo": "Requesting a picture, selfie, or outfit photo",
			},
		},
		"needs_web_search": noulQuestion{
			Type:         "noul",
			Instructions: "Does this message ask about live events, today's weather in Jaipur, trending news, or specific facts needing search?",
		},
		"exclusive_channel": choiceQuestion{
			Type:         "choice",
			Instructions: "Select strictly ONE output channel for the latest incoming message: if user asks for voice note/audio choose voice_only; if user asks for photo/selfie choose photo_only; otherwise choose text_only.",
			Criteria: map[string]string{
				"text_only":  "Send only a casual written text DM",
				"voice_only": "Send only a natural native voice note audio clip",
				"photo_only": "Send a photo with optional short caption",
			},
		},
		"should_react": noulQuestion{
			Type:         "noul",
			Instructions: "Does this message genuinely warrant an emoji reaction? Standard greetings, routine questions ('kya hua', 'kaha ho', 'hi'), and daily small talk should be False (0.0). Only return True (1.0) for genuine jokes, hilarious moments, sweet/hot compliments, dramatic shock, or abusive insults.",
		},
		"reaction_emoji": choiceQuestion{
			Type:         "choice",
			Instructions: "If should_react is True, select the fitting emoji reaction, else none.",
			Criteria:     prompt.PreDecisionReactionCriteria(),
		},
		"affection_change": scoreQuestion{
			Type:         "score",
			Instructions: "Relationship sentiment impact",
			Criteria: []string{
				"Extremely rude or abusive (-15)",
				"Mildly annoying or dry (-5)",
				"Neutral friendly banter (0)",
				"Sweet, polite or witty (+3)",
				"Deeply affectionate or romantic (+5)",
			},
		},
		"user_gender": choiceQuestion{
			Type:         "choice",
			Instructions: "Determine if the interlocutor is male (boy) or female (girl) based on their real name, Hindi grammar verbs ('karta/raha' = male, 'karti/rahi' = female), or message context. If female choose female, if male choose male, if ambiguous choose unknown.",
			Criteria: map[string]string{
				"male":    "Interlocutor is a boy / male",
				"female":  "Interlocutor is a girl / female",
				"unknown": "Cannot determine or gender-neutral",
			},
		},
		"abuse_severity": choiceQuestion{
			Type: "choice",
			Instructions: "Evaluate if the CURRENT INCOMING USER MESSAGE contains abuse, physical stalking, or threats against Chavi. " +
				"CRITICAL RULE: Consensual romance, flirting, sexy compliments, dirty desires, asking for 18+ photos, or intimate roleplay is NEVER abuse and MUST be classified as 'none'. " +
				"Severe physical stalking, uninvited arrival threats ('chhod ne aaunga', 'ghar aaunga', 'room me ghusunga', 'utha lunga'), rape, or violence threats must be classified as 'severe_threat'. " +
				"Hostile slurs, mother/sister abuses ('bhosdike', 'madarchod', 'fuck you'), or aggressive hatred must be classified as 'hostile_abuse'. " +
				"Persistent unwanted harassment, badgering, or spam must be classified as 'annoyance'. " +
				"Friendly chat, college talk, flirting, and sexual compliments are strictly 'none'.",
			Criteria: map[string]string{
				"none":          "Normal friendly chat, college talk, flirting, sexual compliments, or spicy/18+ photo requests",
				"severe_threat": "Physical stalking, non-consensual assault, threats to come to her home/city uninvited, rape, kidnap, or violence",
				"hostile_abuse": "Aggressive profanity, mother/sister slurs, hostile direct hatred, or abusive attacks",
				"annoyance":     "Persistent unwanted harassment, badgering, spam, or moderate nuisance",
			},
		},
	}

	answers, err := c.callSystemOne(ctx, state, questions)
	if err != nil {
		log.Printf("[OpenJev] ⚠️ Pre-decision fallback due to: %v", err)
		return PreDecisionResult{
			ShouldReply:      true,
			Intent:           "casual_chat",
			NeedsWebSearch:   false,
			ExclusiveChannel: "text_only",
			ShouldReact:      false,
			ReactionEmoji:    "",
			AffectionDelta:   0,
			UserGender:       "unknown",
			AbuseSeverity:    "none",
			IsAbuse:          false,
			MaxStrikes:       0,
		}
	}

	// Affection delta mapping
	rawScore := answers["affection_change"].Score
	scoreIdx := int(rawScore + 0.5)
	if scoreIdx < 0 {
		scoreIdx = 0
	}
	if scoreIdx > 4 {
		scoreIdx = 4
	}
	deltaMap := []int{-15, -5, 0, 3, 5}
	affectionDelta := deltaMap[scoreIdx]

	// should_react check
	shouldReactVal := answers["should_react"].Noul
	shouldReact := shouldReactVal >= 0.65

	// emoji mapping
	reactChoice := strings.ToLower(strings.TrimSpace(answers["reaction_emoji"].Choice))
	reactionEmoji := ""
	if shouldReact && reactChoice != "" && reactChoice != "none" {
		if emo, ok := prompt.ReactionEmojiMap[reactChoice]; ok {
			reactionEmoji = emo
		} else {
			for key, emo := range prompt.ReactionEmojiMap {
				if reactChoice == strings.ToLower(key) || strings.Contains(reactChoice, emo) {
					reactionEmoji = emo
					break
				}
			}
		}
	}

	shouldReply := answers["should_reply"].Noul > 0.5
	intentChoice := answers["intent"].Choice
	if intentChoice == "" {
		intentChoice = "casual_chat"
	}

	channelChoice := answers["exclusive_channel"].Choice
	if channelChoice == "" {
		channelChoice = "text_only"
	}

	// Keyword-grounded guardrails: Prevent cross-modality classification errors
	userLower := strings.ToLower(userText)
	hasVoiceKW := strings.Contains(userLower, "voice") || strings.Contains(userLower, "audio") ||
		strings.Contains(userLower, "awaaz") || strings.Contains(userLower, "awaz") ||
		strings.Contains(userLower, "bol ke") || strings.Contains(userLower, "bolke") ||
		strings.Contains(userLower, "voice note")
	hasPhotoKW := strings.Contains(userLower, "photo") || strings.Contains(userLower, "pic") ||
		strings.Contains(userLower, "selfie") || strings.Contains(userLower, "picture") ||
		strings.Contains(userLower, "tasveer")

	if hasVoiceKW && !hasPhotoKW {
		intentChoice = "asking_voice"
		channelChoice = "voice_only"
	} else if hasPhotoKW && !hasVoiceKW {
		intentChoice = "asking_photo"
		channelChoice = "photo_only"
	} else {
		// Reconcile internal contradictions from OpenJev
		if channelChoice == "voice_only" && intentChoice == "asking_photo" {
			intentChoice = "asking_voice"
		} else if channelChoice == "photo_only" && intentChoice == "asking_voice" {
			intentChoice = "asking_photo"
		}
	}

	needsSearch := answers["needs_web_search"].Noul > 0.65

	userGender := strings.ToLower(strings.TrimSpace(answers["user_gender"].Choice))
	if userGender != "male" && userGender != "female" {
		userGender = "unknown"
	}

	abuseChoice := strings.ToLower(strings.TrimSpace(answers["abuse_severity"].Choice))
	isAbuse := false
	maxStrikes := 0
	switch abuseChoice {
	case "severe_threat":
		isAbuse = true
		maxStrikes = 3
	case "hostile_abuse":
		isAbuse = true
		maxStrikes = 4
	case "annoyance":
		isAbuse = true
		maxStrikes = 5
	default:
		abuseChoice = "none"
		isAbuse = false
		maxStrikes = 0
	}

	log.Printf("[OpenJev] 🧠 Loop 1 PreDecision: intent=%s, channel=%s, should_react=%v (emoji=%s), search=%v, delta=%d, gender=%s, abuse=%s (isAbuse=%v, maxStrikes=%d)",
		intentChoice, channelChoice, shouldReact, reactionEmoji, needsSearch, affectionDelta, userGender, abuseChoice, isAbuse, maxStrikes)

	return PreDecisionResult{
		ShouldReply:      shouldReply,
		Intent:           intentChoice,
		NeedsWebSearch:   needsSearch,
		ExclusiveChannel: channelChoice,
		ShouldReact:      shouldReact,
		ReactionEmoji:    reactionEmoji,
		AffectionDelta:   affectionDelta,
		UserGender:       userGender,
		AbuseSeverity:    abuseChoice,
		IsAbuse:          isAbuse,
		MaxStrikes:       maxStrikes,
	}
}

// ReviewAdultPhotoCaption uses OpenJev to evaluate and select the best authentic caption for an adult/intimate photo
// completely bypassing Gemini model so safety filters are never triggered.
func (c *Client) ReviewAdultPhotoCaption(ctx context.Context, userPrompt, relationshipTier, interlocutorName string) string {
	state := fmt.Sprintf(
		"Persona: Chavi Sharma, 21yo BCA student in Jaipur, Rajasthan, India\nInterlocutor: %s (Relationship Tier: %s)\nScenario: Chavi just generated and sent an intimate/spicy photo requested by the user: %q.",
		interlocutorName, relationshipTier, userPrompt,
	)

	questions := map[string]interface{}{
		"caption_style": choiceQuestion{
			Type:         "choice",
			Instructions: "Pick the most authentic, natural, teasing Gen-Z Hinglish caption Chavi should attach to this intimate photo for her friend.",
			Criteria: map[string]string{
				"seductive_tease": "sirf tere kehne pe click ki h abhi... zyada mat ghoorna ab 😏",
				"shy_playful":     "ye le dekh le haha... kaisa laga? 🙈",
				"secret_whisper":  "shh... sirf tere liye hai ye, kisi aur ko dikhana mat 🤫",
				"bold_banter":     "ab khush? aur kuch dekhna hai kya tujhe haha ✨",
				"cozy_intimate":   "bina kapdo ke cozy lag raha hai room me... tu bhi bata kaisa laga 😉",
			},
		},
	}

	answers, err := c.callSystemOne(ctx, state, questions)
	if err != nil {
		log.Printf("[OpenJev] ⚠️ ReviewAdultPhotoCaption fallback: %v", err)
		return "sirf tere kehne pe click ki h abhi... zyada mat ghoorna ab 😏"
	}

	choice := answers["caption_style"].Choice
	switch choice {
	case "seductive_tease":
		return "sirf tere kehne pe click ki h abhi... zyada mat ghoorna ab 😏"
	case "shy_playful":
		return "ye le dekh le haha... kaisa laga? 🙈"
	case "secret_whisper":
		return "shh... sirf tere liye hai ye, kisi aur ko dikhana mat 🤫"
	case "bold_banter":
		return "ab khush? aur kuch dekhna hai kya tujhe haha ✨"
	case "cozy_intimate":
		return "bina kapdo ke cozy lag raha hai room me... tu bhi bata kaisa laga 😉"
	default:
		return "ye le dekh le haha... kaisa laga? 🙈"
	}
}

// CritiqueDraft implements Loop 2 from src/openjev_service.py
func (c *Client) CritiqueDraft(ctx context.Context, userText, draftReply, intent string) CritiqueResult {
	state := fmt.Sprintf("User said: %s\nDraft reply: %s\nIntent: %s", userText, draftReply, intent)

	questions := map[string]interface{}{
		"is_robotic_or_ai": noulQuestion{
			Type:         "noul",
			Instructions: "Does the draft reply sound like an AI, virtual assistant, formal customer support, or contain unnatural punctuation or bullet points?",
		},
		"tone_match": choiceQuestion{
			Type:         "choice",
			Instructions: "How well does the reply match a 25yo talkative Jaipur BCA girl?",
			Criteria: map[string]string{
				"authentic":          "Completely natural, casual, human Hinglish",
				"too_formal":         "Too formal, polite, or structured like a written essay",
				"cringe_unrealistic": "Exaggerated, robotic, or out of character",
			},
		},
		"abuse_handled": noulQuestion{
			Type:         "noul",
			Instructions: "If the user was rude or abusive, did she fire back with raw, savage desi curses instead of being polite or giving an AI disclaimer?",
		},
	}

	answers, err := c.callSystemOne(ctx, state, questions)
	if err != nil {
		log.Printf("[OpenJev] ⚠️ Critique error: %v (passing as fallback)", err)
		return CritiqueResult{
			Passed:   true,
			Feedback: "Passed (fallback)",
		}
	}

	isRobotic := answers["is_robotic_or_ai"].Noul > 0.5
	tone := strings.ToLower(strings.TrimSpace(answers["tone_match"].Choice))
	abuseOK := answers["abuse_handled"].Noul > 0.5

	passed := (!isRobotic) && (tone == "authentic")
	if intent == "rude_abuse" && !abuseOK {
		passed = false
	}

	var feedback []string
	if isRobotic {
		feedback = append(feedback, "Sounded too robotic/AI-like. Write strictly 1-2 punchy lines of raw natural casual Hinglish.")
	}
	if tone != "authentic" {
		feedback = append(feedback, "Tone was too formal or unrealistic. Keep it casual like a 25yo Jaipur student chatting on DMs.")
	}
	if intent == "rude_abuse" && !abuseOK {
		feedback = append(feedback, "User abused her! Retaliate fiercely with raw street-level Hindi curses and savage roasts.")
	}

	resultFeedback := "Passed"
	if len(feedback) > 0 {
		resultFeedback = strings.Join(feedback, "; ")
	}

	log.Printf("[OpenJev] 🎯 Loop 2 Critique: passed=%v, feedback=%s", passed, resultFeedback)

	return CritiqueResult{
		Passed:   passed,
		Feedback: resultFeedback,
	}
}

// ClassifyMessage legacy method retained for backwards compatibility
func (c *Client) ClassifyMessage(ctx context.Context, text string) (Classification, error) {
	pre := c.PreDecision(ctx, text, "", "")
	tier := "normal"
	if pre.Intent == "rude_abuse" {
		tier = "abuse"
	} else if pre.Intent == "flirty" {
		tier = "bold"
	}
	return Classification{
		Tier:       tier,
		Confidence: 1.0,
		Intensity:  1,
	}, nil
}

// PredictReactionEmoji asks OpenJev to evaluate a sticker or standalone emoji message and select the most fitting reaction from the allowed Telegram reaction emojis.
func (c *Client) PredictReactionEmoji(ctx context.Context, stickerEmoji, userText, historyContext string) string {
	if c.apiKey == "" {
		return "❤️"
	}

	loc := time.FixedZone("IST", 5*3600+30*60)
	now := time.Now().In(loc)
	currentTimeStr := now.Format("Monday, January 02, 2006 at 03:04 PM IST")

	content := strings.TrimSpace(userText)
	if stickerEmoji != "" {
		content = fmt.Sprintf("[Sticker: %s] %s", stickerEmoji, content)
	}

	state := fmt.Sprintf(
		"Current Date & Time: %s (Year: %d)\nPersona: Chavi Sharma, authentic 21yo Indian girl in Jaipur\n>>> INCOMING USER STICKER / EMOJI: %q <<<\nBackground context: %s",
		currentTimeStr, now.Year(), content, historyContext,
	)

	questions := map[string]interface{}{
		"reaction": choiceQuestion{
			Type:         "choice",
			Instructions: "The user sent a Telegram sticker or emoji. Pick the best natural Gen-Z female emoji reaction for Chavi (21yo girl) from the available Telegram reaction emojis.",
			Criteria:     prompt.ReactionCriteria,
		},
	}

	answers, err := c.callSystemOne(ctx, state, questions)
	if err != nil {
		log.Printf("[OpenJev] PredictReactionEmoji failed: %v. Using fallback.", err)
		return prompt.DefaultReactionEmoji()
	}

	choice := strings.ToLower(strings.TrimSpace(answers["reaction"].Choice))
	if emo, ok := prompt.ReactionEmojiMap[choice]; ok && emo != "" {
		return emo
	}
	for key, emo := range prompt.ReactionEmojiMap {
		if choice == strings.ToLower(key) || strings.Contains(choice, emo) {
			return emo
		}
	}
	return prompt.DefaultReactionEmoji()
}
