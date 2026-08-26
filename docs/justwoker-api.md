# Justwoker API Provider — Usage Guide

The Justwoker provider is an **OpenAI-compatible** endpoint used by this bot to run
Claude models. Any tool or code that can talk to the OpenAI Chat Completions API can
use it by just changing the base URL and API key.

## Connection Details

| Field | Value |
|-------|-------|
| Base URL | `https://api.justwoker.icu/v1` |
| Chat endpoint | `https://api.justwoker.icu/v1/chat/completions` |
| Auth header | `Authorization: Bearer <API_KEY>` |
| API key | `sk-d2WlIK9RFjNniWReJ3SulMkSa1bA4Clfecn9wbc0ICB4LqeV` |
| Protocol | OpenAI Chat Completions (v1) |

## Required / Recommended Headers

This bot sends the following headers on every Justwoker request
(see [client.go](../internal/ai/client.go)). Some proxy providers gate access on
`Origin` / `Referer` / `X-Title`, so replicate these if you get `401`/`403` with the
key alone:

| Header | Value |
|--------|-------|
| `Content-Type` | `application/json` |
| `Authorization` | `Bearer sk-d2WlIK9RFjNniWReJ3SulMkSa1bA4Clfecn9wbc0ICB4LqeV` |
| `Origin` | `https://trae.ai` |
| `Referer` | `https://trae.ai/` |
| `HTTP-Referer` | `https://trae.ai` |
| `X-Title` | `Trae` |
| `User-Agent` | `Trae/1.0.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36` |
| `Accept` | `application/json, text/plain, */*` |
| `Accept-Language` | `en-US,en;q=0.9` |

> Note: `Origin`, `Referer`, `HTTP-Referer`, and `X-Title` identify the calling app.
> The alternate `Originator` / `codex_cli_rs` User-Agent in the code applies ONLY to the
> `alwaysdata` / `agentrouter` providers — **not** to Justwoker.

## Available Models

- `claude-opus-4-8`
- `claude-opus-4-8-thinking`
- `claude-opus-5`
- `claude-opus-5-thinking`

> These models also support **vision** (image input) on this provider.

## Quick Start

### cURL

```bash
curl https://api.justwoker.icu/v1/chat/completions \
  -H "Authorization: Bearer sk-d2WlIK9RFjNniWReJ3SulMkSa1bA4Clfecn9wbc0ICB4LqeV" \
  -H "Content-Type: application/json" \
  -H "Origin: https://trae.ai" \
  -H "Referer: https://trae.ai/" \
  -H "HTTP-Referer: https://trae.ai" \
  -H "X-Title: Trae" \
  -d '{
    "model": "claude-opus-4-8",
    "messages": [
      { "role": "system", "content": "You are a helpful assistant." },
      { "role": "user", "content": "Say hello in one short sentence." }
    ],
    "stream": false
  }'
```

### Python (openai SDK)

```python
from openai import OpenAI

client = OpenAI(
    base_url="https://api.justwoker.icu/v1",
    api_key="sk-d2WlIK9RFjNniWReJ3SulMkSa1bA4Clfecn9wbc0ICB4LqeV",
    default_headers={
        "Origin": "https://trae.ai",
        "Referer": "https://trae.ai/",
        "HTTP-Referer": "https://trae.ai",
        "X-Title": "Trae",
    },
)

resp = client.chat.completions.create(
    model="claude-opus-4-8",
    messages=[
        {"role": "system", "content": "You are a helpful assistant."},
        {"role": "user", "content": "Say hello in one short sentence."},
    ],
)
print(resp.choices[0].message.content)
```

### Node.js (openai SDK)

```js
import OpenAI from "openai";

const client = new OpenAI({
  baseURL: "https://api.justwoker.icu/v1",
  apiKey: "sk-d2WlIK9RFjNniWReJ3SulMkSa1bA4Clfecn9wbc0ICB4LqeV",
  defaultHeaders: {
    "Origin": "https://trae.ai",
    "Referer": "https://trae.ai/",
    "HTTP-Referer": "https://trae.ai",
    "X-Title": "Trae",
  },
});

const resp = await client.chat.completions.create({
  model: "claude-opus-4-8",
  messages: [
    { role: "system", content: "You are a helpful assistant." },
    { role: "user", content: "Say hello in one short sentence." },
  ],
});
console.log(resp.choices[0].message.content);
```

## Using It In This Bot (Go)

The provider is registered in [main.go](../cmd/bot/main.go) inside the `providers` slice.
To add or reuse it:

```go
providers := []ai.ProviderConfig{
    {
        BaseURL: "https://api.justwoker.icu/v1",
        APIKey:  "sk-d2WlIK9RFjNniWReJ3SulMkSa1bA4Clfecn9wbc0ICB4LqeV",
        Models: []string{
            "claude-opus-4-8",
            "claude-opus-4-8-thinking",
            "claude-opus-5",
            "claude-opus-5-thinking",
        },
    },
    // ...other providers act as fallbacks in order
}
```

Notes for this codebase:
- Providers are tried **top to bottom**; Justwoker is listed first so it is the primary.
- For **image/vision** requests, only `justwoker` and `gorouter` providers are used
  (see the vision filter in [client.go](../internal/ai/client.go)).
- On failure a model is put on a 1-minute cooldown and the next candidate is tried.

## JSON / Structured Output

The bot forces JSON responses with:

```json
{ "response_format": { "type": "json_object" } }
```

Pass the same `response_format` field if you need guaranteed JSON output.

## Optional Parameters

| Parameter | Purpose |
|-----------|---------|
| `temperature` | Randomness (0.0–1.0+) |
| `max_tokens` | Cap the response length |
| `tools` / `tool_choice` | Function/tool calling (OpenAI format) |
| `stream` | Set `true` for token streaming |
| `response_format` | Force `json_object` output |

## Troubleshooting

| Symptom | Likely cause / fix |
|---------|--------------------|
| `401 Unauthorized` | Wrong/expired key, or missing `Bearer ` prefix |
| `404` on request | Base URL missing `/v1`, or wrong `/chat/completions` path |
| `429 Too Many Requests` | Rate limited — retry with backoff or use a fallback provider |
| Empty `choices` | Model name typo, or provider returned an error object |

## Security Note

The API key above is a **live secret**. Anyone with it can spend against your account.
Do not paste it into public repos, issues, or screenshots. Prefer loading it from an
environment variable rather than hardcoding it, and rotate it if it is ever exposed.
