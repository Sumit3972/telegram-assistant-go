package telegram

import (
	"html"
	"regexp"
	"strings"
)

var (
	codeBlockRegex = regexp.MustCompile("(?s)```(.*?)```")
	inlineCodeRegex = regexp.MustCompile("`([^`\n]+?)`")
	boldRegex       = regexp.MustCompile(`\*\*([^*\n]+?)\*\*`)
	italicRegex     = regexp.MustCompile(`__([^_\n]+?)__`)
)

// ProcessTextAndParseMode automatically converts Markdown formatting to valid Telegram HTML.
func ProcessTextAndParseMode(text, requestedMode string) (processedText, parseMode string) {
	processedText = text
	parseMode = requestedMode

	if parseMode == "" {
		hasMarkdown := strings.Contains(text, "**") || strings.Contains(text, "__") || strings.Contains(text, "`")
		if hasMarkdown {
			// Escape HTML special characters
			processedText = html.EscapeString(text)

			// Convert backticks code block
			processedText = codeBlockRegex.ReplaceAllString(processedText, "<pre>$1</pre>")

			// Convert inline code
			processedText = inlineCodeRegex.ReplaceAllString(processedText, "<code>$1</code>")

			// Convert bold (**text**)
			processedText = boldRegex.ReplaceAllString(processedText, "<b>$1</b>")

			// Convert italic (__text__)
			processedText = italicRegex.ReplaceAllString(processedText, "<i>$1</i>")

			parseMode = "HTML"
		}
	} else if parseMode == "Markdown" || parseMode == "MarkdownV2" {
		processedText = strings.ReplaceAll(text, "**", "*")
	}

	return processedText, parseMode
}
