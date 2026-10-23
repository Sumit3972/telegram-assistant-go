package prompt

import (
	"regexp"
	"strings"
)

// NormalizeEmotionTags normalizes complex, multi-word LLM brackets
// into clean, natively recognized Fish Audio S2.1 emotion tags ([sigh], [laughing], [annoyed], etc.)
// strictly placed at the start of the audio script, and strips all emojis and acoustic artifacts.
// Exact port of VoiceService.normalize_emotion_tags from src/voice_service.py.
func NormalizeEmotionTags(text string) string {
	if strings.TrimSpace(text) == "" {
		return ""
	}

	cleaned := text

	// 1. Strip emojis and pictographs (TTS model stumbles or pronounces unicode descriptions)
	emojiRegex := regexp.MustCompile(`[\x{1F600}-\x{1F64F}\x{1F300}-\x{1F5FF}\x{1F680}-\x{1F6FF}\x{1F700}-\x{1F77F}\x{1F780}-\x{1F7FF}\x{1F800}-\x{1F8FF}\x{1F900}-\x{1F9FF}\x{1FA00}-\x{1FA6F}\x{1FA70}-\x{1FAFF}\x{2600}-\x{26FF}\x{2700}-\x{27BF}\x{2300}-\x{23FF}\x{2B50}]`)
	cleaned = emojiRegex.ReplaceAllString(cleaned, "")

	// 2. Extract and resolve all bracket tags to find the best primary emotion
	tagRegex := regexp.MustCompile(`\[(.*?)\]`)
	matches := tagRegex.FindAllStringSubmatch(cleaned, -1)

	chosenTag := ""
	for _, m := range matches {
		if len(m) < 2 {
			continue
		}
		raw := strings.ToLower(strings.TrimSpace(m[1]))
		switch {
		case strings.Contains(raw, "sigh"):
			chosenTag = "[sigh]"
		case strings.Contains(raw, "laugh") || strings.Contains(raw, "giggle") || strings.Contains(raw, "chuckle") || strings.Contains(raw, "haha") || strings.Contains(raw, "smile"):
			chosenTag = "[laughing]"
		case strings.Contains(raw, "whisper") || strings.Contains(raw, "hush") || strings.Contains(raw, "secret") || strings.Contains(raw, "soft") || strings.Contains(raw, "sensual") || strings.Contains(raw, "intimate"):
			chosenTag = "[whispering]"
		case strings.Contains(raw, "pant") || strings.Contains(raw, "moan") || strings.Contains(raw, "gasp") || strings.Contains(raw, "breath"):
			chosenTag = "[gasp]"
		case strings.Contains(raw, "annoy") || strings.Contains(raw, "irritat") || strings.Contains(raw, "frustrat"):
			chosenTag = "[annoyed]"
		case strings.Contains(raw, "angr") || strings.Contains(raw, "furious") || strings.Contains(raw, "scoff") || strings.Contains(raw, "rage") || strings.Contains(raw, "teeth") || strings.Contains(raw, "yell"):
			chosenTag = "[angry]"
		case strings.Contains(raw, "tired") || strings.Contains(raw, "sleep") || strings.Contains(raw, "exhaust") || strings.Contains(raw, "yawn"):
			chosenTag = "[tired]"
		case strings.Contains(raw, "happy") || strings.Contains(raw, "cheerful") || strings.Contains(raw, "excit") || strings.Contains(raw, "horny") || strings.Contains(raw, "flirt"):
			chosenTag = "[happy]"
		case strings.Contains(raw, "calm") || strings.Contains(raw, "relax"):
			chosenTag = "[calm]"
		case strings.Contains(raw, "sad") || strings.Contains(raw, "cry"):
			chosenTag = "[sad]"
		default:
			// Dynamically use the tag requested by the LLM
			cleanTag := strings.Trim(raw, "[] \t")
			if cleanTag != "" {
				chosenTag = "[" + cleanTag + "]"
			}
		}
		if chosenTag != "" {
			break
		}
	}

	// 3. Strip all bracketed tags from the body to avoid mid-sentence acoustic glitches
	body := tagRegex.ReplaceAllString(cleaned, "")

	// 4. Clean punctuation & whitespace
	body = regexp.MustCompile(`\.{4,}`).ReplaceAllString(body, "...")
	body = regexp.MustCompile(`!{2,}`).ReplaceAllString(body, "!")
	body = regexp.MustCompile(`\?{2,}`).ReplaceAllString(body, "?")
	body = strings.Join(strings.Fields(body), " ")

	if chosenTag != "" {
		return strings.TrimSpace(chosenTag + " " + body)
	}
	return strings.TrimSpace(body)
}
