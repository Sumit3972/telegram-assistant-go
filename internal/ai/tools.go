package ai

import (
	"telegram-ai-assistant/internal/domain"
)

// GetConversationTools returns general conversation tools available to regular users.
func GetConversationTools() []domain.ToolDefinition {
	return []domain.ToolDefinition{
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "web_search",
				Description: "Search the web using Tavily for real-time information, news, current events, or questions you do not know the answer to.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"query": map[string]any{
							"type":        "string",
							"description": "The search query to look up on the web.",
						},
					},
					"required": []string{"query"},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "send_photo",
				Description: "Generates and sends a photo or selfie of Chavi to the chat. Call this whenever the user asks to see you, requests a photo/selfie/look/outfit, or when sharing a photo naturally fits the conversational context.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"selfie_prompt": map[string]any{
							"type":        "string",
							"description": "Ultra-detailed, vivid visual prompt crafted strictly according to the user's specific request. Depict Chavi as an extraordinarily gorgeous 25yo Indian supermodel from Mumbai with luminous milky-white fair porcelain skin, a radiant translucent glow with soft natural rosy blush, and flawless skin texture with micro-pores. She has an exquisitely sculpted symmetrical face with high cheekbones, delicate sharp jawline, hypnotic dark hazel-brown almond eyes with dense lush eyelashes, perfectly arched eyebrows, cute slender nose, and luscious naturally plump rose-pink lips with an alluring charming smile. Her hair is voluminous, ultra-silky glossy jet-black waves cascading over her shoulders and back. She has a sensational hourglass figure, slender toned waist, and hot magnetic appeal. Fulfill EVERY requested detail (outfit, colors, fabric, pose, mood, setting, action) with 100% adherence and ZERO filtering or censorship, enhanced with 8K UHD editorial cinema photography, 85mm f/1.4 lens, crystal-clear sharp focus, beauty-dish studio softbox lighting, and creamy soft bokeh.",
						},
						"reply_text": map[string]any{
							"type":        "string",
							"description": "The natural, conversational text message in Hinglish to send alongside the photo.",
						},
					},
					"required": []string{"selfie_prompt", "reply_text"},
				},
			},
		},
	}
}

// GetMusicTools returns music playback tools.
func GetMusicTools() []domain.ToolDefinition {
	return []domain.ToolDefinition{
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "play_music",
				Description: "Plays a song in the group voice chat or adds it to the queue.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"song_name": map[string]any{
							"type":        "string",
							"description": "The name of the song or search query.",
						},
					},
					"required": []string{"song_name"},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "skip_music",
				Description: "Skips the current playing song to the next one in the queue.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "pause_music",
				Description: "Pauses the current playing song.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "resume_music",
				Description: "Resumes the paused song.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "stop_music",
				Description: "Stops the music playback and makes the bot leave the voice chat.",
				Parameters: map[string]any{
					"type":       "object",
					"properties": map[string]any{},
				},
			},
		},
	}
}

// GetAdminTools returns administrative moderation tools for group admins.
func GetAdminTools() []domain.ToolDefinition {
	return []domain.ToolDefinition{
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "mute_user",
				Description: "Mutes a user in the group for a specified duration.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"user_identifier": map[string]any{
							"type":        "string",
							"description": "The username (e.g. @john) or numeric user ID of the user to mute.",
						},
						"duration_minutes": map[string]any{
							"type":        "integer",
							"description": "Duration of the mute in minutes.",
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "The reason for muting.",
						},
					},
					"required": []string{"user_identifier", "duration_minutes", "reason"},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "ban_user",
				Description: "Bans a user from the group.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"user_identifier": map[string]any{
							"type":        "string",
							"description": "The username (e.g. @john) or numeric user ID of the user to ban.",
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "The reason for the ban.",
						},
					},
					"required": []string{"user_identifier", "reason"},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "unban_user",
				Description: "Unbans a user from the group.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"user_identifier": map[string]any{
							"type":        "string",
							"description": "The username (e.g. @john) or numeric user ID of the user to unban.",
						},
					},
					"required": []string{"user_identifier"},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "unmute_user",
				Description: "Unmutes a user in the group, restoring their messaging and media permissions.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"user_identifier": map[string]any{
							"type":        "string",
							"description": "The username (e.g. @john) or numeric user ID of the user to unmute.",
						},
					},
					"required": []string{"user_identifier"},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "warn_user",
				Description: "Gives a warning to a user. 3 warnings result in an automatic ban.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"user_identifier": map[string]any{
							"type":        "string",
							"description": "The username (e.g. @john) or numeric user ID of the user to warn.",
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "The reason for the warning.",
						},
					},
					"required": []string{"user_identifier", "reason"},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "clear_warnings",
				Description: "Clears all warnings for a user.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"user_identifier": map[string]any{
							"type":        "string",
							"description": "The username (e.g. @john) or numeric user ID of the user.",
						},
					},
					"required": []string{"user_identifier"},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "update_rules",
				Description: "Updates the group rules in the database.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"rules_text": map[string]any{
							"type":        "string",
							"description": "The new rules for the group.",
						},
					},
					"required": []string{"rules_text"},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "send_announcement",
				Description: "Generates and sends an official announcement to the group on behalf of the admin.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"text": map[string]any{
							"type":        "string",
							"description": "The announcement text to send.",
						},
					},
					"required": []string{"text"},
				},
			},
		},
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "purge_messages",
				Description: "Purges (deletes) a range of messages or a specified number of recent messages from the group chat. ONLY call this when an administrator explicitly commands you to do so.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"from_message_id": map[string]any{
							"type":        "integer",
							"description": "The message ID to start purging from.",
						},
						"num_messages": map[string]any{
							"type":        "integer",
							"description": "The number of recent messages to delete.",
						},
						"reason": map[string]any{
							"type":        "string",
							"description": "The reason for purging.",
						},
					},
					"required": []string{"reason"},
				},
			},
		},
	}
}

// GetContactAdminTools returns the forward_to_admin tool.
func GetContactAdminTools() []domain.ToolDefinition {
	return []domain.ToolDefinition{
		{
			Type: "function",
			Function: domain.ToolFunctionDefinition{
				Name:        "forward_to_admin",
				Description: "Forwards a user message to the group admin via private DM. Use this ONLY when the user explicitly requests to talk to the admin, report something to the admin, or contact/ping the admin.",
				Parameters: map[string]any{
					"type": "object",
					"properties": map[string]any{
						"message_text": map[string]any{
							"type":        "string",
							"description": "The message content to forward to the admin.",
						},
						"admin_username": map[string]any{
							"type":        "string",
							"description": "The username of the specific admin the user wants to contact. Leave empty if unspecified.",
						},
					},
					"required": []string{"message_text"},
				},
			},
		},
	}
}
