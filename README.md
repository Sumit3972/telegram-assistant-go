# Telegram AI Assistant (Go Edition)

High-performance, clean-architecture AI companion & moderation bot for Telegram written in Go (1.22+).

---

## 🌟 Features

- **Clean Architecture**: Decoupled domain models, repository interfaces, and services.
- **High Concurrency**: Goroutine Worker Pool for asynchronous Telegram update processing without blocking webhooks.
- **Database Pooling**: Native PostgreSQL connection pooling with `pgx/v5` and automatic boot migrations.
- **Upgraded Persona & Prompt Engine**: Calibrated Hinglish Gen-Z persona, strict 0-1 emoji discipline, memory context injection.
- **Multimodal AI**: Chat completions with AnyAPI key rotation, 8K portrait prompt enhancement, Fish Audio S2.1 Pro TTS, Tavily Web Search, and Hinata Music Bot integration.
- **Group Moderation**: CAPTCHA verification, Karma points (+/-), admin away alerts & private DM forwarding, slash commands (`/rules`, `/warn`, `/mute`, `/ban`, `/ship`, `/summarize`), and profanity filtering.

---

## 🚀 Quick Start

### 1. Configure `.env`
Ensure your `.env` contains:
```env
AI_API_KEY=sk-...
AI_BASE_URL=https://api.anyapi.ai/v1
AI_MODEL=deepseek/deepseek-v4-flash-0731
GEMINI_IMAGE_API_URL=https://api.anyapi.ai/v1/images/generations
DATABASE_URL=postgresql://...
TELEGRAM_BOT_TOKEN=...
MY_PERSONAL_NAME=Janvi
MY_PERSONAL_USERNAME=Janvi3976
MY_PERSONAL_USER_ID=...
```

### 2. Run in Development Mode
```powershell
go run cmd/bot/main.go
```

### 3. Build & Run Standalone Executable
```powershell
go build -o dist/bot.exe cmd/bot/main.go
./dist/bot.exe
```

### 4. Run Unit Tests
```powershell
go test -v ./...
```
