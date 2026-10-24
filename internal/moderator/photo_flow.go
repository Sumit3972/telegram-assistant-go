package moderator

import (
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"math/rand"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"telegram-ai-assistant/internal/ai"
	"telegram-ai-assistant/internal/domain"
	"telegram-ai-assistant/internal/media"
	"telegram-ai-assistant/internal/prompt"
)

// processPhotoRequest executes the full dynamic photo request pipeline:
// 1. Keeps Telegram 'upload_photo' action alive continuously in the background.
// 2. Optimizes the user's raw desire into a bold, photorealistic Qwen-Image-2.1 prompt via LLM.
// 3. Generates the image via ModelScope (with fallback to Antigravity Imagen 3).
// 4. Inspects the rendered photo with Vision AI to craft an authentic, spontaneous in-character caption.
// 5. Delivers the photo with the vision caption attached directly.
func (h *ConversationHandler) processPhotoRequest(
	ctx context.Context,
	msg *domain.TelegramMessage,
	userPrompt string,
	recent []domain.ChatMessageLog,
	affectionScore int,
) {
	log.Printf("[PhotoFlow] 📸 Starting dynamic photo pipeline for user prompt: %q", userPrompt)

	photoKey := strconv.FormatInt(msg.Chat.ID, 10)
	h.inFlightPhotos.Store(photoKey, true)
	defer h.inFlightPhotos.Delete(photoKey)

	// Step 0: Continuous Telegram 'upload_photo' status indicator (refreshes every 4s)
	actionCtx, cancelAction := context.WithCancel(ctx)
	defer cancelAction()

	go func() {
		h.sendChatAction(actionCtx, msg.Chat.ID, msg.IsUserbot, "upload_photo")
		ticker := time.NewTicker(4 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-actionCtx.Done():
				return
			case <-ticker.C:
				h.sendChatAction(actionCtx, msg.Chat.ID, msg.IsUserbot, "upload_photo")
			}
		}
	}()

	chatIDStr := fmt.Sprintf("%d", msg.Chat.ID)
	userIDStr := fmt.Sprintf("%d", msg.From.ID)
	displayUser := prompt.FormatUserDisplayName(msg.From.FirstName, msg.From.LastName, msg.From.Username)
	if displayUser == "" {
		displayUser = fmt.Sprintf("User%d", msg.From.ID)
	}

	// Real Girl Reciprocity: If user already got a photo from Chavi and hasn't sent their own photo yet, refuse playfully!
	if h.hasUserPhotoPending(userIDStr) {
		cancelAction()
		log.Printf("[PhotoFlow] 🚫 User %s already received a photo. Demanding user's photo before sending again.", userIDStr)
		cues := []string{
			"arre maine to bhej di thi na pic haha! pehle tu apni photo bhej na mujhe, tab main doosri bhejungi 😏",
			"aise thodi na hota hai yrr, har baar main hi photo bhejti rahoon? pehle apni pic dikha mujhe phir main bhejti hoon haha ✨",
			"tu bas meri photos mangta rehta hai haha, pehle apni ek selfie bhej na dekhun to kaisa dikhta hai tu 😉",
			"pehle tu apni photo bhej na mujhe, phir main pakka doosri bhejungi haha 🙈 deal?",
		}
		cue := cues[rand.Intn(len(cues))]
		h.sendMessage(ctx, msg, cue)
		return
	}

	// Inspect existing outfit from session memory (if updated within last 2 hours)
	var existingOutfit string
	if h.relRepo != nil {
		if rel, err := h.relRepo.GetRelationship(ctx, chatIDStr, userIDStr); err == nil && rel != nil {
			if rel.CurrentOutfit != "" && (time.Now().Unix()-rel.OutfitUpdatedAt < 7200) {
				existingOutfit = rel.CurrentOutfit
			}
		}
	}

	// Detect if user requested batch photos (e.g. "5 pics ek sath", max 5)
	batchCount := 1
	lowerReq := strings.ToLower(userPrompt)
	if strings.Contains(lowerReq, "ek sath") || strings.Contains(lowerReq, "batch") || strings.Contains(lowerReq, "all pics") || strings.Contains(lowerReq, "saari pics") || strings.Contains(lowerReq, "aur bhejo") {
		if strings.Contains(lowerReq, "5") {
			batchCount = 5
		} else if strings.Contains(lowerReq, "4") {
			batchCount = 4
		} else if strings.Contains(lowerReq, "3") {
			batchCount = 3
		} else if strings.Contains(lowerReq, "2") {
			batchCount = 2
		}
	}

	// Step 1: Optimize prompt via LLM
	optimizedPrompt := h.optimizePhotoPrompt(actionCtx, userPrompt, recent, existingOutfit)

	// Step 2: Render image(s) via ModelScope Qwen-Image-2.1
	img, err := h.imageService.GenerateImage(actionCtx, optimizedPrompt)
	if err != nil || img == nil {
		cancelAction()
		log.Printf("[PhotoFlow] ❌ Failed to generate image: %v", err)
		h.sendMessage(ctx, msg, "arre camera glitch ho gaya yrr haha, thodi der me bhejti hoon ✨")
		return
	}

	// Save outfit into session memory if newly established
	if h.relRepo != nil && existingOutfit == "" {
		outfitAnchor := "casual everyday clothes"
		if strings.Contains(lowerReq, "nude") || strings.Contains(lowerReq, "naked") {
			outfitAnchor = "bare natural skin"
		} else if strings.Contains(lowerReq, "lingerie") || strings.Contains(lowerReq, "bra") || strings.Contains(lowerReq, "panty") {
			outfitAnchor = "delicate silk lace lingerie"
		} else if strings.Contains(lowerReq, "dress") {
			outfitAnchor = "cute fitted dress"
		}
		_ = h.relRepo.UpdateOutfit(ctx, chatIDStr, userIDStr, outfitAnchor)
	}

	// Step 3: Determine relationship tier
	relationshipTier := "casual_friend"
	if affectionScore >= 85 {
		relationshipTier = "deeply_bonded"
	} else if affectionScore >= 60 {
		relationshipTier = "close_friend"
	} else if affectionScore < 35 {
		relationshipTier = "aloof"
	}

	// Step 4: Generate in-character caption (with Jev review for adult/intimate requests)
	caption := h.generatePhotoCaption(actionCtx, img, userPrompt, recent, relationshipTier, displayUser)

	// Step 5: Stop upload ticker & dispatch photo
	cancelAction()
	h.sendPhoto(ctx, msg, img.Data, img.URL, caption)
	h.markPhotoSentToUser(userIDStr)
	log.Printf("[PhotoFlow] ✅ Successfully delivered photo with caption: %q", caption)

	// If batch was requested and > 1, deliver remaining photos smoothly (up to batchCount)
	if batchCount > 1 {
		go func() {
			for b := 2; b <= batchCount; b++ {
				time.Sleep(3 * time.Second)
				extraImg, extraErr := h.imageService.GenerateImage(context.Background(), optimizedPrompt)
				if extraErr == nil && extraImg != nil {
					h.sendPhoto(context.Background(), msg, extraImg.Data, extraImg.URL, "")
				}
			}
		}()
	}
}

// optimizePhotoPrompt uses LLM to craft a bold, layered Qwen-Image-2.1 prompt combining
// Chavi's core identity, conversational context, and safe aesthetic translation for bold requests.
func (h *ConversationHandler) optimizePhotoPrompt(
	ctx context.Context,
	userPrompt string,
	recent []domain.ChatMessageLog,
	existingOutfit string,
) string {
	lowerReq := strings.ToLower(userPrompt)
	isAdultPrompt := strings.Contains(lowerReq, "nude") || strings.Contains(lowerReq, "naked") ||
		strings.Contains(lowerReq, "boob") || strings.Contains(lowerReq, "breast") ||
		strings.Contains(lowerReq, "panty") || strings.Contains(lowerReq, "bra") ||
		strings.Contains(lowerReq, "lingerie") || strings.Contains(lowerReq, "bina kapdo") ||
		strings.Contains(lowerReq, "sexy") || strings.Contains(lowerReq, "hot") ||
		strings.Contains(lowerReq, "chut") || strings.Contains(lowerReq, "pussy") ||
		strings.Contains(lowerReq, "bikini") || strings.Contains(lowerReq, "thong") ||
		strings.Contains(lowerReq, "topless") || strings.Contains(lowerReq, "gandi")

	// If adult/nude prompt, use prompt builder directly to protect Gemini from triggering policy filters
	if isAdultPrompt {
		log.Printf("[PhotoFlow] 🔒 Adult photo request detected: %q. Bypassing Gemini to prevent safety trigger.", userPrompt)
		return prompt.BuildChaviSelfiePrompt(userPrompt)
	}

	log.Printf("[PhotoFlow] 🧠 Optimizing image prompt via LLM for user request: %q", userPrompt)

	sysPrompt := prompt.GetEnhancerSystemPrompt(h.botName)
	messages := []domain.ChatMessage{
		{Role: "system", Content: sysPrompt},
	}

	// Append recent conversation context (last 6 messages)
	startIdx := 0
	if len(recent) > 6 {
		startIdx = len(recent) - 6
	}
	for _, m := range recent[startIdx:] {
		isAssistant := m.UserID == "assistant" || strings.EqualFold(m.Username, h.botUsername) || strings.EqualFold(m.Username, h.botName)
		if isAssistant {
			messages = append(messages, domain.ChatMessage{Role: "assistant", Content: m.MessageText})
		} else {
			messages = append(messages, domain.ChatMessage{Role: "user", Content: m.MessageText})
		}
	}

	outfitContext := ""
	if existingOutfit != "" {
		outfitContext = fmt.Sprintf(" Session outfit anchor: %s (maintain wardrobe consistency unless user asked to change/undress).", existingOutfit)
	}

	// Current turn
	messages = append(messages, domain.ChatMessage{
		Role:    "user",
		Content: fmt.Sprintf("User photo request: %q.%s Strictly apply distance framing to avoid extreme anatomical close-up morphs. Generate a CUTE everyday girl photo (Level 2-4). FACE LOCK = exact same face always.", userPrompt, outfitContext),
	})

	optCtx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()

	opts := ai.ChatCompletionOptions{}
	res, err := h.aiClient.ChatCompletions(optCtx, messages, opts)
	if err == nil && res != nil {
		raw := strings.TrimSpace(res.Message.GetStringContent())
		clean := cleanLLMPrompt(raw)
		if len(clean) > 50 {
			log.Printf("[PhotoFlow] ✅ LLM optimized prompt (%d chars): %s", len(clean), clean)
			return clean
		}
	}

	log.Printf("[PhotoFlow] ⚠️ LLM prompt optimization failed or returned short text (%v). Falling back to template prompt.", err)
	return prompt.BuildChaviSelfiePrompt(userPrompt)
}

// generatePhotoCaption inspects the generated photo with Gemini Vision for regular photos,
// or uses OpenJev to evaluate and craft captions for adult/nude photos without ever triggering Gemini.
func (h *ConversationHandler) generatePhotoCaption(
	ctx context.Context,
	img *media.GeneratedImage,
	userPrompt string,
	recent []domain.ChatMessageLog,
	relationshipTier string,
	displayUser string,
) string {
	lowerPrompt := strings.ToLower(userPrompt)
	isAdultPhoto := strings.Contains(lowerPrompt, "nude") || strings.Contains(lowerPrompt, "naked") ||
		strings.Contains(lowerPrompt, "boob") || strings.Contains(lowerPrompt, "breast") ||
		strings.Contains(lowerPrompt, "panty") || strings.Contains(lowerPrompt, "bra") ||
		strings.Contains(lowerPrompt, "lingerie") || strings.Contains(lowerPrompt, "bina kapdo") ||
		strings.Contains(lowerPrompt, "sexy") || strings.Contains(lowerPrompt, "hot") ||
		strings.Contains(lowerPrompt, "chut") || strings.Contains(lowerPrompt, "pussy") ||
		strings.Contains(lowerPrompt, "bikini") || strings.Contains(lowerPrompt, "thong") ||
		strings.Contains(lowerPrompt, "topless") || strings.Contains(lowerPrompt, "gandi")

	// If adult/nude photo: DO NOT send to Gemini Vision or Gemini text to avoid Google safety triggers! Use OpenJev instead!
	if isAdultPhoto {
		log.Printf("[PhotoFlow] 🔒 Adult/intimate photo detected. Bypassing Gemini to prevent safety trigger. Reviewing caption via OpenJev...")
		if h.decisionClient != nil {
			caption := h.decisionClient.ReviewAdultPhotoCaption(ctx, userPrompt, relationshipTier, displayUser)
			if caption != "" {
				log.Printf("[PhotoFlow] ✅ OpenJev chosen adult caption: %q", caption)
				return caption
			}
		}
		return getRandomIntimateCaption()
	}

	log.Printf("[PhotoFlow] 👁️ Generating vision-grounded caption with Vision AI...")

	var imageBase64 string
	if len(img.Data) > 0 {
		contentType := img.ContentType
		if contentType == "" {
			contentType = "image/jpeg"
		}
		imageBase64 = fmt.Sprintf("data:%s;base64,%s", contentType, base64.StdEncoding.EncodeToString(img.Data))
	} else if img.URL != "" {
		if data, err := downloadPhotoBytes(ctx, img.URL); err == nil && len(data) > 0 {
			img.Data = data
			imageBase64 = fmt.Sprintf("data:image/jpeg;base64,%s", base64.StdEncoding.EncodeToString(data))
		}
	}

	if imageBase64 == "" {
		log.Printf("[PhotoFlow] ⚠️ No image base64 available for vision inspection. Using persona fallback caption.")
		return getRandomCuteCaption()
	}

	sysPrompt := prompt.GetVisionCaptionSystemPrompt(h.botName, relationshipTier)
	messages := []domain.ChatMessage{
		{Role: "system", Content: sysPrompt},
	}

	// Add recent dialogue for context
	startIdx := 0
	if len(recent) > 4 {
		startIdx = len(recent) - 4
	}
	for _, m := range recent[startIdx:] {
		isAssistant := m.UserID == "assistant" || strings.EqualFold(m.Username, h.botUsername) || strings.EqualFold(m.Username, h.botName)
		if isAssistant {
			messages = append(messages, domain.ChatMessage{Role: "assistant", Content: m.MessageText})
		} else {
			messages = append(messages, domain.ChatMessage{Role: "user", Content: m.MessageText})
		}
	}

	userParts := []domain.ChatMessageContentPart{
		{
			Type: "text",
			Text: fmt.Sprintf("User photo request: %q. Here is the selfie photo I just clicked. Give me a natural, brief 1-2 sentence Hinglish caption reacting to the photo.", userPrompt),
		},
		{
			Type: "image_url",
			ImageURL: &domain.ImageURLDetail{
				URL: imageBase64,
			},
		},
	}

	messages = append(messages, domain.ChatMessage{
		Role:    "user",
		Content: userParts,
	})

	visCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	opts := ai.ChatCompletionOptions{}
	res, err := h.aiClient.ChatCompletions(visCtx, messages, opts)
	if err == nil && res != nil {
		raw := strings.TrimSpace(res.Message.GetStringContent())
		caption := cleanCaption(raw)
		if caption != "" && !isGenericOrBannedCaption(caption) {
			log.Printf("[PhotoFlow] ✅ In-character caption generated: %q", caption)
			return caption
		}
	}

	log.Printf("[PhotoFlow] ⚠️ Vision caption generation failed or returned generic text (%v). Using persona fallback.", err)
	return getRandomCuteCaption()
}

func cleanLLMPrompt(raw string) string {
	clean := strings.TrimSpace(raw)
	// Remove <think> blocks
	if start := strings.Index(clean, "<think>"); start != -1 {
		if end := strings.Index(clean, "</think>"); end != -1 && end > start {
			clean = strings.TrimSpace(clean[end+len("</think>"):])
		}
	}
	// Strip markdown fences
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimPrefix(clean, "markdown")
	clean = strings.TrimPrefix(clean, "prompt")
	clean = strings.TrimSuffix(clean, "```")
	clean = strings.Trim(clean, "\"'\n\r\t ")
	return clean
}

func cleanCaption(raw string) string {
	clean := strings.TrimSpace(raw)
	// Remove think blocks
	if start := strings.Index(clean, "<think>"); start != -1 {
		if end := strings.Index(clean, "</think>"); end != -1 && end > start {
			clean = strings.TrimSpace(clean[end+len("</think>"):])
		}
	}
	// Strip quotes and formatting
	clean = strings.TrimPrefix(clean, "```")
	clean = strings.TrimSuffix(clean, "```")
	clean = strings.Trim(clean, "\"'\n\r\t ")

	// Strip emotion tags like [happy] or actions like *smiles*
	tagRegex := regexp.MustCompile(`\[[a-zA-Z0-9_\-\s]+\]`)
	clean = tagRegex.ReplaceAllString(clean, "")
	actionRegex := regexp.MustCompile(`\*[a-zA-Z0-9_\-\s]+\*`)
	clean = actionRegex.ReplaceAllString(clean, "")

	return strings.TrimSpace(clean)
}

func isGenericOrBannedCaption(caption string) bool {
	lower := strings.ToLower(caption)
	banned := []string{
		"ye lo meri photo",
		"ye le meri photo",
		"ye rahi meri photo",
		"here is my photo",
		"here is the picture",
		"as an ai",
		"i am an ai",
		"i am a bot",
		"generated image",
		"safety policy",
		"content policy",
		"policy violation",
		"i cannot generate",
		"i can't generate",
		"i am unable to",
		"i cannot provide",
		"i cannot fulfill",
		"sexually explicit",
		"against our policies",
		"violates",
		"i apologize",
		"policies.google.com",
		"sensitive words",
		"the model could not",
		"the prompt could not",
		"troubleshooting",
		"ai.google.dev",
		"use-policy",
		"google",
		"http://",
		"https://",
	}
	for _, b := range banned {
		if strings.Contains(lower, b) {
			return true
		}
	}
	return false
}

func getRandomCuteCaption() string {
	captions := []string{
		"kya lag rahi hu na? ab bata kaisa hai 😉",
		"room me baithi thi bore ho rahi thi, ye le dekh le ✨",
		"kaisa laga outfit? tere kehne pe click kiya abhi haha 🙈",
		"ye abhi click ki... bata kaisa lag raha hai? ✨",
		"le dekh le, ab zyada tareef mat karna haha 😉",
		"bata kaisa hai, bas tere liye li abhi ✨",
	}
	return captions[rand.Intn(len(captions))]
}

func getRandomIntimateCaption() string {
	captions := []string{
		"ye le dekh le haha, abhi room me baithi thi... bata kaisi lag rahi hoon? ✨",
		"sirf tere kehne pe bhej di abhi bed se... zyada mat ghoorna ab 😏",
		"le dekh li teri demand? kaisa laga phir? 🙈✨",
		"bas tere liye click ki thi... ab bata kaisi lag rahi hu? 😉",
		"le bhej di photo, khush ab? haha 🙈",
	}
	return captions[rand.Intn(len(captions))]
}

func downloadPhotoBytes(ctx context.Context, url string) ([]byte, error) {
	client := &http.Client{Timeout: 30 * time.Second}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}
