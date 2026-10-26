package prompt

import (
	"strings"
	"unicode"
)

// Hindi / Hinglish distinctive marker words (common conversational keywords in Romanized Hinglish)
var hindiMarkers = []string{
	"kya", "kyu", "kyun", "hai", "hain", "hona", "nahi", "nhi", "na",
	"bhai", "yaar", "yrr", "yr", "kar", "karo", "kare", "karna", "raha", "rahi", "rahe",
	"hoon", "hun", "hu", "toh", "to", "mera", "meri", "mere", "tera", "teri", "tere",
	"tum", "tu", "aap", "kaha", "kahan", "kaise", "kaisa", "kaisi", "acha", "achha",
	"theek", "thik", "clg", "sun", "suno", "batana", "batao", "bata", "pehle",
	"samne", "baithi", "baitha", "chup", "dimaag", "dimag", "dost", "kaun", "kon",
	"yeh", "woh", "wo", "isse", "usse", "leke", "aao", "jao", "dekh", "dekho",
	"dekhna", "bhejo", "bhej", "diya", "wali", "wala", "vale", "vali", "haina",
	"sahi", "chal", "chalo", "nikal", "apna", "apni", "apne", "kuch", "sab", "sabko",
	"aayi", "aaya", "aaye", "gayi", "gaya", "gaye", "karta", "karti", "karte",
	"baat", "shuru", "khatam", "paas", "dur", "mat", "kr", "krna", "bol", "bolo",
	"khol", "de", "le", "lo", "do", "kise", "isko", "usko", "hum", "hume", "mujhe", "tujhe",
}

// English distinctive marker words
var englishMarkers = []string{
	"the", "is", "are", "am", "was", "were", "be", "been", "being",
	"has", "have", "had", "do", "did", "does", "done", "will", "would", "shall", "should",
	"you", "your", "yours", "what", "why", "how", "who", "where", "when",
	"she", "her", "hers", "he", "him", "his", "they", "them", "their", "it", "its",
	"i", "me", "my", "myself", "we", "us", "our", "ours",
	"loneliness", "lonely", "lonliness", "followed", "everywhere", "please", "tell", "dont", "don't", "cant", "can't",
	"cannot", "nice", "cute", "stupid", "bitch", "speak", "understand", "fine",
	"good", "morning", "night", "doing", "stay", "away", "from", "with", "about",
	"think", "love", "much", "nothing", "everything", "anyone", "everyone", "someone",
	"really", "very", "want", "need", "like", "feel",
	"always", "never", "sometimes", "happy", "sad", "friend", "sister", "brother",
	"girl", "boy", "today", "tomorrow", "yesterday", "here", "there", "which",
	"send", "sent", "give", "take", "make", "know", "see", "look", "come", "go",
}

// DetectLanguage determines whether a message is English ("en") or Romanized Hinglish ("hinglish").
func DetectLanguage(text string) string {
	cleaned := strings.TrimSpace(strings.ToLower(text))
	if cleaned == "" {
		return "hinglish"
	}

	words := strings.FieldsFunc(cleaned, func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})

	if len(words) == 0 {
		return "hinglish"
	}

	hindiScore := 0
	englishScore := 0

	for _, w := range words {
		for _, hm := range hindiMarkers {
			if w == hm {
				hindiScore += 2
				break
			}
		}
		for _, em := range englishMarkers {
			if w == em {
				englishScore += 2
				break
			}
		}
	}

	// If strong Hindi markers exist, prioritize Hinglish
	if hindiScore > 0 && hindiScore >= englishScore {
		return "hinglish"
	}

	// If strong English markers exist without Hindi markers, it's English
	if englishScore > 0 && hindiScore == 0 {
		return "en"
	}

	// Fallback check: if contains common English words and zero Hindi words
	if englishScore > hindiScore {
		return "en"
	}

	return "hinglish"
}

// IsStandaloneEmoji checks if the text contains strictly 1 to 4 emojis/symbols with no letters or words.
func IsStandaloneEmoji(text string) bool {
	trimmed := strings.TrimSpace(text)
	if trimmed == "" {
		return false
	}

	runeCount := 0
	for _, r := range trimmed {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return false
		}
		if !unicode.IsSpace(r) {
			runeCount++
		}
	}

	return runeCount >= 1 && runeCount <= 4
}

// ReactionCriteria defines the approved Telegram reaction emojis with human context, passed directly to OpenJev.
var ReactionCriteria = map[string]string{
	"heart":        "Heart ❤️ (sweet, warm, loving, cute, affectionate)",
	"laugh":        "Laugh 😂 (funny joke, banter, laughing)",
	"rofl":         "ROFL 🤣 (hilarious meme, laughing out loud)",
	"pleading":     "Pleading 🥺 (soft, cute puppy eyes, begging, emotional)",
	"fire":         "Fire 🔥 (hype, hot picture, cool compliment, savage)",
	"thumbs_up":    "Thumbs Up 👍 (agree, ok, acknowledgment, deal)",
	"see_no_evil":  "See No Evil 🙈 (shy blush, cute embarrassment, hiding face)",
	"sparkles":     "Sparkles ✨ (aesthetic, pretty, cute magical vibe)",
	"broken_heart": "Broken Heart 💔 (sad, heartbreak, empathetic condolences)",
	"crying":       "Crying 😭 (overdramatic, can't handle it, relatable pain)",
	"eyes":         "Side Eyes 👀 (noticing, curious, tea/gossip, suspicious)",
	"thinking":     "Thinking 🤔 (wondering, skeptical, curious)",
	"yawn":         "Yawn 🥱 (sleepy, late night, bored, tired)",
	"hundred":      "100 💯 (facts, full agreement, completely true)",
	"salute":       "Salute 🫡 (respect, agreed boss, on it)",
	"smirk":        "Smirk 😏 (flirty teasing, naughty witty vibe)",
	"heart_eyes":   "Heart Eyes 😍 (loving an outfit, cute gesture, gorgeous)",
	"shocked":      "Shocked 😱 (dramatic shock, unbelievable surprise)",
	"skull":        "Skull 💀 (dead laughing, savage roast, hilarious)",
	"clown":        "Clown 🤡 (foolish mistake, clown moment, funny fail)",
	"pray":         "Pray 🙏 (grateful, thank you, please, sarcastic bhagwan bachaye)",
	"party":        "Party 🎉 (celebrating, birthday, congratulations)",
	"tea":          "Tea/Coffee ☕ (chai lovers, chilling at Tapri, gossip)",
	"sassy":        "Nail Polish 💅 (sassy, confident girl, diva attitude)",
	"hug":          "Hug 🤗 (warm comforting hug, affectionate care)",
	"star_struck":  "Star-struck 🤩 (stunned, amazed, super impressed)",
	"angry":        "Angry 😡 (pissed off, annoyed, grumpy)",
	"sleepy":       "Sleeping 😴 (good night, sleeping, tired)",
	"innocent":     "Innocent 😇 (angelic, sweet innocence, harmless)",
}

// ReactionEmojiMap maps OpenJev decision keys directly to their UTF-8 Telegram reaction emoji character.
var ReactionEmojiMap = map[string]string{
	"heart":        "❤️",
	"laugh":        "😂",
	"rofl":         "🤣",
	"pleading":     "🥺",
	"fire":         "🔥",
	"thumbs_up":    "👍",
	"see_no_evil":  "🙈",
	"sparkles":     "✨",
	"broken_heart": "💔",
	"crying":       "😭",
	"eyes":         "👀",
	"thinking":     "🤔",
	"yawn":         "🥱",
	"hundred":      "💯",
	"salute":       "🫡",
	"smirk":        "😏",
	"heart_eyes":   "😍",
	"shocked":      "😱",
	"skull":        "💀",
	"clown":        "🤡",
	"pray":         "🙏",
	"party":        "🎉",
	"tea":          "☕",
	"sassy":        "💅",
	"hug":          "🤗",
	"star_struck":  "🤩",
	"angry":        "😡",
	"sleepy":       "😴",
	"innocent":     "😇",
}

// DefaultReactionEmoji returns a safe fallback emoji when OpenJev is temporarily unreachable.
func DefaultReactionEmoji() string {
	return "❤️"
}

// PreDecisionReactionCriteria returns the criteria map including "none" for OpenJev PreDecision.
func PreDecisionReactionCriteria() map[string]string {
	m := make(map[string]string, len(ReactionCriteria)+1)
	m["none"] = "No reaction"
	for k, v := range ReactionCriteria {
		m[k] = v
	}
	return m
}

// SplitIntoDMBubbles splits a multi-clause text message into two natural rapid bubbles for DMs.
// Returns (bubble1, bubble2). If the message is short or has no good split point, bubble2 is empty.
func SplitIntoDMBubbles(text string) (string, string) {
	trimmed := strings.TrimSpace(text)
	// Don't split if short (less than 35 characters)
	if len(trimmed) < 35 {
		return trimmed, ""
	}

	// 1. Check for newline break first
	if idx := strings.Index(trimmed, "\n"); idx != -1 {
		b1 := strings.TrimSpace(trimmed[:idx])
		b2 := strings.TrimSpace(trimmed[idx+1:])
		if len(b1) >= 8 && len(b2) >= 8 {
			return b1, b2
		}
	}

	// 2. Check for sentence endings: "? ", "! ", "... ", ". "
	sentenceEnders := []string{"? ", "! ", "... ", ". "}
	bestIdx := -1
	bestSeLen := 0

	for _, se := range sentenceEnders {
		idx := strings.Index(trimmed, se)
		if idx >= 5 && len(trimmed)-(idx+len(se)) >= 8 {
			if bestIdx == -1 || idx < bestIdx {
				bestIdx = idx
				bestSeLen = len(se)
			}
		}
	}

	if bestIdx != -1 {
		b1 := strings.TrimSpace(trimmed[:bestIdx+bestSeLen-1]) // includes the punctuation mark (e.g. "?")
		b2 := strings.TrimSpace(trimmed[bestIdx+bestSeLen:])   // starts right after the space
		if len(b1) >= 5 && len(b2) >= 8 {
			return b1, b2
		}
	}

	// 3. Check for common emoji / laughing break markers like " 😂 ", " 🤣 ", " ✨ ", " haha ", " lol "
	emojiBreaks := []string{" 😂 ", " 🤣 ", " ✨ ", " haha ", " lol "}
	for _, eb := range emojiBreaks {
		idx := strings.Index(trimmed, eb)
		if idx >= 10 && len(trimmed)-(idx+len(eb)) >= 10 {
			b1 := strings.TrimSpace(trimmed[:idx+len(eb)-1])
			b2 := strings.TrimSpace(trimmed[idx+len(eb):])
			if len(b1) >= 8 && len(b2) >= 8 {
				return b1, b2
			}
		}
	}

	return trimmed, ""
}



