package media

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

type SearchService struct {
	apiKey     string
	httpClient *http.Client
}

func NewSearchService(apiKey string) *SearchService {
	if apiKey == "" || apiKey == "tvly-dev-dummy" {
		apiKey = "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"
	}
	return &SearchService{
		apiKey:     apiKey,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

type TavilySearchRequest struct {
	APIKey        string `json:"api_key"`
	Query         string `json:"query"`
	SearchDepth   string `json:"search_depth"`
	IncludeAnswer bool   `json:"include_answer"`
	MaxResults    int    `json:"max_results"`
}

type TavilySearchResultItem struct {
	Title   string `json:"title"`
	URL     string `json:"url"`
	Content string `json:"content"`
}

type TavilySearchResponse struct {
	Answer  string                   `json:"answer"`
	Results []TavilySearchResultItem `json:"results"`
}

func (s *SearchService) Search(ctx context.Context, query string) (string, error) {
	log.Printf("[SearchService] Executing Tavily web search: %q", query)

	reqBody := TavilySearchRequest{
		APIKey:        s.apiKey,
		Query:         query,
		SearchDepth:   "basic",
		IncludeAnswer: true,
		MaxResults:    5,
	}

	bodyBytes, _ := json.Marshal(reqBody)
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.tavily.com/search", bytes.NewReader(bodyBytes))
	if err != nil {
		return "", err
	}
	httpReq.Header.Set("Content-Type", "application/json")

	resp, err := s.httpClient.Do(httpReq)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		errBytes, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("Tavily API error %d: %s", resp.StatusCode, string(errBytes))
	}

	var data TavilySearchResponse
	if err := json.NewDecoder(resp.Body).Decode(&data); err != nil {
		return "", err
	}

	var sb strings.Builder
	if data.Answer != "" {
		sb.WriteString(fmt.Sprintf("Search summary: %s\n\nSources:\n", data.Answer))
		for _, r := range data.Results {
			sb.WriteString(fmt.Sprintf("- %s: %s (%s)\n", r.Title, r.Content, r.URL))
		}
		return sb.String(), nil
	}

	if len(data.Results) > 0 {
		for _, r := range data.Results {
			sb.WriteString(fmt.Sprintf("- %s: %s (%s)\n", r.Title, r.Content, r.URL))
		}
		return sb.String(), nil
	}

	return "No results found.", nil
}
