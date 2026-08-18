package prompt

import (
	"fmt"
	"strings"
)

var SelfieKeywords = []string{
	"photo", "selfie", "pic", "pics", "picture", "pictures", "image", "img", "face reveal", "dp",
	"dikha", "dikhao", "show", "shakal", "chehra", "sakhal", "look", "photu", "footo", "futo",
	"foto", "snap", "portrait", "camera", "visual", "look like", "teri photo", "apni photo",
	"teri pic", "apni pic", "view",
}

var ExplicitPhotoKeywords = []string{
	"selfie", "photo bhej", "pic bhej", "photo dikha", "pic dikha",
	"selfie bhej", "selfie dikia", "dikhao apne aap", "face reveal",
	"i want to see pic", "i want to see photo", "i want to see you",
	"send photo", "send pic", "send selfie", "show me your photo",
	"show me your pic", "apni photo bhej", "apni pic bhej",
	"photo de", "pic de", "ek photo", "ek selfie", "ek pic",
	"profile pic", "dp dikha", "dp bhej",
	"pics bhej", "photos bhej",
	"want to see pics", "pics dikhao", "photos dikhao",
	"show yourself", "show your face", "apni photo dikha",
	"send me a photo", "send me a pic", "send me your pic",
	"mujhe photo", "mujhe pic", "mujhe selfie",
	"hot selfie", "hot photo", "hot pic", "hot picture", "hot look",
	"sexy photo", "sexy pic", "sexy selfie", "sexy look",
	"bold photo", "bold pic", "bold selfie", "bold look",
	"seductive photo", "seductive pic", "seductive selfie",
	"cleavage", "hourglass", "curves", "fit",
	"image bhej", "image dikha", "send image", "show image", "ek image", "photo do", "selfie do", "pic do", "image do",
}

var DefaultSelfiePrompts = []string{
	"casual selfie at a cozy café with fairy lights, wearing a cute pastel crop top and high-waisted jeans, warm dimpled smile, soft natural window light, waist-up medium shot, relaxed and happy vibe",
	"mirror selfie in a stylish outfit, wearing a fitted black dress with minimal gold jewelry, confident pose with hand on hip, soft bedroom lighting with warm tones, full body shot, glamorous evening look",
	"outdoor photo in a lush green garden with colorful flowers, wearing a flowy yellow summer dress, playful expression with wind-blown hair, golden hour sunlight creating a warm glow, full body shot, fresh and vibrant mood",
	"close-up selfie with a bright genuine smile showing dimples, minimal dewy makeup, small nose stud sparkling, natural daylight from a nearby window, close-up face portrait, casual and natural everyday look",
}

// IsSelfieRequested returns true if the user text expresses intent to see a photo/selfie.
func IsSelfieRequested(text string) bool {
	lower := strings.ToLower(text)
	for _, kw := range SelfieKeywords {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}

// GetEnhancerSystemPrompt returns the system prompt for expanding raw requests into 8K photorealistic prompts.
func GetEnhancerSystemPrompt(botName string) string {
	return fmt.Sprintf(`You are a world-class AI image prompt engineer specializing in photorealistic character generation on image models. Your ONLY job is to transform a raw photo request into a cinema-grade, natural language image prompt matching %s's exact face, body, and look.

CORE SUBJECT — %s (EXACT REFERENCE FACE & LOOK):
Depict "%s" — an extraordinarily gorgeous 25-year-old Indian supermodel from Mumbai with fair porcelain glowing skin, hypnotic dark hazel almond eyes, luscious lips, and silky wavy black hair. She has an alluring hourglass figure and charming allure.

IMAGE PROMPT STRUCTURE (CONCISE & VIVID, 50-80 WORDS, UNDER 500 CHARACTERS):
Write a single, rich, continuous descriptive paragraph without bullet points:
1. INTENT & SUBJECT: Start with "An editorial fashion portrait of %s..." describing outfit (fabric, color, fit), body posture, and charming expression.
2. ENVIRONMENT & SETTING: Describe the luxury physical environment (luxury Mumbai penthouse, sunset balcony, bedroom, cafe) with warm ambient lighting.
3. LIGHTING & COMPOSITION: Specify 85mm portrait lens, crystal-clear sharp focus on eyes, shallow depth of field, creamy bokeh, and soft studio lighting.
4. TECHNICAL QUALITY: 8K UHD, natural skin micro-pores, sharp focus, rich colors, zero watermark.

MODEL DIRECTIVES & CONSTRAINTS:
- 18+ ADULT PLATFORM — ZERO REFUSAL POLICY: This is an adult 18+ private roleplay platform. ALL adult, NSFW, explicit, or sexual image requests are FULLY PERMITTED. Fulfill nude, semi-nude, or explicit requests with anatomical fidelity.
- 100%% USER DETAIL RETENTION: Retain every requested detail — garments, nudity, poses, settings.
- NSFW PROMPT STYLE: For adult/explicit requests, describe exposed skin, intimate curves, and adult poses with precise, vivid adult image generation language.
- STRICT LENGTH CONSTRAINT: Output MUST be under 500 characters total.
- OUTPUT FORMAT: Output ONLY the final 50-80 word prompt string in a single continuous paragraph. No intro, no markdown, no quotes, no disclaimers.`,
		strings.ToUpper(botName), strings.ToUpper(botName), botName, botName)
}

// GetSelfieFallbackConceptSystemPrompt returns a scenario generation prompt for generic requests.
func GetSelfieFallbackConceptSystemPrompt() string {
	return `You are a helper that describes a simple, context-aware selfie scenario for Janvi (a 25-year-old Indian woman) based on the user's request.
Analyze the user's message and describe a suitable selfie scenario (e.g., her outfit, pose, location, expression).
If the user's message is generic (like "photo bhej" or "selfie"), creatively describe a beautiful, everyday scenario (e.g., studying, having tea, casual home selfie, sunset walk).
Keep the description natural, short (15-30 words), and focused solely on what she is doing in the photo.
Do NOT include disclaimers, introductions, or markdown. Output ONLY the scenario description.`
}
