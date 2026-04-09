package mobileapi

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"io"
	"net/http"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unicode"
)

const (
	werkaAISearchDefaultModel  = "gemini-flash-lite-latest"
	werkaAISearchMaxUploadSize = 8 << 20
)

var werkaAISearchJSONPattern = regexp.MustCompile(`(?s)\{.*\}`)

type werkaAISearchSuggestion struct {
	DisplayQuery      string   `json:"display_query"`
	BackgroundQueries []string `json:"background_queries"`
	VisibleText       string   `json:"visible_text"`
}

type werkaAISearchError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *werkaAISearchError) Error() string {
	return strings.TrimSpace(e.Message)
}

type werkaAISearchService struct {
	client   *http.Client
	apiKey   string
	model    string
	endpoint string
}

func newWerkaAISearchService(apiKey, model string, timeout time.Duration) *werkaAISearchService {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	model = strings.TrimSpace(model)
	if model == "" {
		model = werkaAISearchDefaultModel
	}
	return &werkaAISearchService{
		client: &http.Client{Timeout: timeout},
		apiKey: strings.TrimSpace(apiKey),
		model:  model,
		endpoint: "https://generativelanguage.googleapis.com/v1beta/models/" +
			model + ":generateContent",
	}
}

func (s *Server) SetWerkaAISearchConfig(apiKey, model string, timeout time.Duration) {
	s.aiSearch = newWerkaAISearchService(apiKey, model, timeout)
}

func (s *Server) handleWerkaAISearchSuggestion(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{
			"error": "method not allowed",
			"code":  "method_not_allowed",
		})
		return
	}

	principal, ok := s.authorize(w, r)
	if !ok {
		return
	}
	if err := requireRole(principal, RoleWerka); err != nil {
		writeJSON(w, http.StatusForbidden, map[string]string{"error": "forbidden"})
		return
	}
	if s.aiSearch == nil || !s.aiSearch.configured() {
		writeJSON(w, http.StatusServiceUnavailable, map[string]string{
			"error": "werka ai search is not configured",
			"code":  "not_configured",
		})
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, werkaAISearchMaxUploadSize)
	if err := r.ParseMultipartForm(werkaAISearchMaxUploadSize); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "invalid image upload",
			"code":  "invalid_image",
		})
		return
	}

	file, header, err := r.FormFile("image")
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "image is required",
			"code":  "invalid_image",
		})
		return
	}
	defer file.Close()

	imageBytes, err := io.ReadAll(file)
	if err != nil || len(imageBytes) == 0 {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "image is required",
			"code":  "invalid_image",
		})
		return
	}

	suggestion, err := s.aiSearch.inferSuggestion(
		r.Context(),
		imageBytes,
		detectImageMIMEType(header.Filename, header.Header.Get("Content-Type"), imageBytes),
	)
	if err != nil {
		if aiErr, ok := err.(*werkaAISearchError); ok {
			status := http.StatusBadGateway
			switch aiErr.Code {
			case "not_configured":
				status = http.StatusServiceUnavailable
			case "invalid_image":
				status = http.StatusBadRequest
			case "no_result":
				status = http.StatusOK
				writeJSON(w, status, werkaAISearchSuggestion{})
				return
			}
			writeJSON(w, status, map[string]string{
				"error": aiErr.Message,
				"code":  aiErr.Code,
			})
			return
		}
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "werka ai search failed",
			"code":  "upstream_failed",
		})
		return
	}

	writeJSON(w, http.StatusOK, suggestion)
}

func (s *werkaAISearchService) configured() bool {
	return strings.TrimSpace(s.apiKey) != ""
}

func (s *werkaAISearchService) inferSuggestion(
	ctx context.Context,
	imageBytes []byte,
	mimeType string,
) (werkaAISearchSuggestion, error) {
	if !s.configured() {
		return werkaAISearchSuggestion{}, &werkaAISearchError{
			Code:    "not_configured",
			Message: "werka ai search is not configured",
		}
	}
	if len(imageBytes) == 0 {
		return werkaAISearchSuggestion{}, &werkaAISearchError{
			Code:    "invalid_image",
			Message: "image is required",
		}
	}
	if strings.TrimSpace(mimeType) == "" {
		mimeType = "image/jpeg"
	}

	payload := map[string]any{
		"contents": []map[string]any{
			{
				"parts": []map[string]any{
					{"text": werkaAISearchPrompt},
					{
						"inline_data": map[string]any{
							"mime_type": mimeType,
							"data":      base64.StdEncoding.EncodeToString(imageBytes),
						},
					},
				},
			},
		},
		"generationConfig": map[string]any{
			"temperature":      0,
			"responseMimeType": "application/json",
		},
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return werkaAISearchSuggestion{}, &werkaAISearchError{
			Code:    "upstream_failed",
			Message: "failed to encode ai request",
		}
	}

	req, err := http.NewRequestWithContext(
		ctx,
		http.MethodPost,
		s.endpoint+"?key="+s.apiKey,
		strings.NewReader(string(body)),
	)
	if err != nil {
		return werkaAISearchSuggestion{}, &werkaAISearchError{
			Code:    "upstream_failed",
			Message: "failed to build ai request",
		}
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return werkaAISearchSuggestion{}, &werkaAISearchError{
			Code:    "upstream_failed",
			Message: err.Error(),
		}
	}
	defer resp.Body.Close()

	var geminiResp struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
		} `json:"candidates"`
		Error *struct {
			Message string `json:"message"`
		} `json:"error"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&geminiResp); err != nil {
		return werkaAISearchSuggestion{}, &werkaAISearchError{
			Code:    "upstream_failed",
			Message: "invalid ai response",
		}
	}

	if resp.StatusCode != http.StatusOK {
		message := ""
		if geminiResp.Error != nil {
			message = strings.TrimSpace(geminiResp.Error.Message)
		}
		if message == "" {
			message = "AI search request failed"
		}
		return werkaAISearchSuggestion{}, &werkaAISearchError{
			Code:    "upstream_failed",
			Message: message,
		}
	}

	if len(geminiResp.Candidates) == 0 ||
		len(geminiResp.Candidates[0].Content.Parts) == 0 {
		return werkaAISearchSuggestion{}, &werkaAISearchError{
			Code:    "no_result",
			Message: "no ai search suggestion",
		}
	}

	rawText := strings.TrimSpace(geminiResp.Candidates[0].Content.Parts[0].Text)
	parsed := decodeWerkaAISearchPayload(rawText)
	if parsed == nil {
		return werkaAISearchSuggestion{}, &werkaAISearchError{
			Code:    "no_result",
			Message: "no ai search suggestion",
		}
	}

	searchQuery := normalizeServerFriendlyQuery(parsed.SearchQuery)
	altQuery := normalizeServerFriendlyQuery(parsed.AltQuery)
	visibleBrand := sanitizeSearchQuery(parsed.VisibleBrand)
	displayQuery := searchQuery
	if displayQuery == "" {
		displayQuery = pickFallbackDisplayQuery(visibleBrand, altQuery)
	}

	backgroundQueries := rankQueries(append(
		[]string{displayQuery, altQuery, visibleBrand},
		expandPhrasePrefixes([]string{displayQuery, altQuery, visibleBrand})...,
	))
	resolvedDisplayQuery := normalizeServerFriendlyQuery(displayQuery)
	if resolvedDisplayQuery == "" && len(backgroundQueries) > 0 {
		resolvedDisplayQuery = normalizeServerFriendlyQuery(backgroundQueries[0])
	}
	visibleText := sanitizeSearchQuery(parsed.VisibleText)
	if visibleText == "" {
		visibleText = visibleBrand
	}

	return werkaAISearchSuggestion{
		DisplayQuery:      resolvedDisplayQuery,
		BackgroundQueries: backgroundQueries,
		VisibleText:       visibleText,
	}, nil
}

type werkaAISearchPayload struct {
	SearchQuery  string `json:"search_query"`
	AltQuery     string `json:"alt_query"`
	VisibleBrand string `json:"visible_brand"`
	VisibleText  string `json:"visible_text"`
}

func decodeWerkaAISearchPayload(rawText string) *werkaAISearchPayload {
	if strings.TrimSpace(rawText) == "" {
		return nil
	}
	var payload werkaAISearchPayload
	if err := json.Unmarshal([]byte(rawText), &payload); err == nil {
		return &payload
	}
	match := werkaAISearchJSONPattern.FindString(rawText)
	if strings.TrimSpace(match) == "" {
		return nil
	}
	if err := json.Unmarshal([]byte(match), &payload); err != nil {
		return nil
	}
	return &payload
}

func sanitizeSearchQuery(raw string) string {
	value := strings.TrimSpace(raw)
	if value == "" {
		return ""
	}
	if idx := strings.IndexAny(value, "\r\n"); idx >= 0 {
		value = strings.TrimSpace(value[:idx])
	}
	value = strings.Join(strings.Fields(value), " ")
	value = trimEdgeQuotes(value)
	runes := []rune(value)
	if len(runes) > 64 {
		value = strings.TrimRightFunc(string(runes[:64]), unicode.IsSpace)
	}
	return value
}

func trimEdgeQuotes(value string) string {
	edgeChars := map[rune]struct{}{
		'"': {}, '\'': {}, '`': {}, '“': {}, '”': {}, '‘': {}, '’': {},
	}
	for {
		runes := []rune(value)
		if len(runes) == 0 {
			return ""
		}
		if _, ok := edgeChars[runes[0]]; ok {
			value = strings.TrimLeftFunc(string(runes[1:]), unicode.IsSpace)
			continue
		}
		break
	}
	for {
		runes := []rune(value)
		if len(runes) == 0 {
			return ""
		}
		if _, ok := edgeChars[runes[len(runes)-1]]; ok {
			value = strings.TrimRightFunc(string(runes[:len(runes)-1]), unicode.IsSpace)
			continue
		}
		break
	}
	return value
}

func normalizeServerFriendlyQuery(raw string) string {
	value := sanitizeSearchQuery(raw)
	if value == "" {
		return ""
	}
	lower := strings.ToLower(value)
	tokens := splitSearchTokens(lower, true)
	if len(tokens) > 0 && len(tokens) <= 2 {
		compact := strings.Join(tokens, " ")
		switch compact {
		case "nivea":
			return "nivea"
		case "musaffo":
			return "musaffo"
		}
	}
	if strings.Contains(lower, "hot lunch") || strings.Contains(lower, "xot lanch") {
		if strings.Contains(lower, "xot lanch") {
			return "xot lanch"
		}
		return "hot lunch"
	}
	if strings.Contains(lower, "musaffo") || strings.Contains(lower, "мусаффо") {
		return "musaffo"
	}
	if strings.Contains(lower, "simba") && strings.Contains(lower, "chips") {
		return "simba chips"
	}
	if strings.Contains(lower, "mini") &&
		(strings.Contains(lower, "rulet") || strings.Contains(lower, "рулет")) {
		return "mini rulet"
	}
	if strings.Contains(lower, "nivea") || strings.Contains(lower, "нивеа") {
		return "nivea"
	}
	return value
}

func pickFallbackDisplayQuery(visibleBrand, altQuery string) string {
	fallbacks := uniqueQueries([]string{visibleBrand, altQuery})
	if len(fallbacks) == 0 {
		return ""
	}
	return fallbacks[0]
}

func uniqueQueries(values []string) []string {
	unique := make([]string, 0, len(values))
	seen := map[string]struct{}{}
	for _, raw := range values {
		value := sanitizeSearchQuery(raw)
		if value == "" {
			continue
		}
		key := strings.ToLower(value)
		if _, ok := seen[key]; ok {
			continue
		}
		seen[key] = struct{}{}
		unique = append(unique, value)
	}
	return unique
}

func expandPhrasePrefixes(values []string) []string {
	phrases := make([]string, 0, len(values)*2)
	for _, raw := range values {
		value := sanitizeSearchQuery(raw)
		if value == "" {
			continue
		}
		tokens := splitSearchTokens(value, false)
		if len(tokens) < 2 {
			continue
		}
		phrases = append(phrases, strings.Join(tokens[:2], " "))
		if len(tokens) >= 3 {
			phrases = append(phrases, strings.Join(tokens[:3], " "))
		}
	}
	return uniqueQueries(phrases)
}

func rankQueries(values []string) []string {
	unique := uniqueQueries(values)
	sort.SliceStable(unique, func(i, j int) bool {
		left, right := unique[i], unique[j]
		leftTokens := queryTokenCount(left)
		rightTokens := queryTokenCount(right)
		if leftTokens != rightTokens {
			return rightTokens < leftTokens
		}
		return len(right) < len(left)
	})
	return unique
}

func queryTokenCount(value string) int {
	count := 0
	for _, token := range splitSearchTokens(value, false) {
		if len([]rune(strings.TrimSpace(token))) >= 2 {
			count++
		}
	}
	return count
}

func splitSearchTokens(value string, filterNoise bool) []string {
	var tokens []string
	var current []rune
	flush := func() {
		if len(current) == 0 {
			return
		}
		token := strings.TrimSpace(string(current))
		current = current[:0]
		if token == "" {
			return
		}
		if filterNoise {
			if _, ok := werkaAIBrandNoiseTokens[token]; ok {
				return
			}
		}
		tokens = append(tokens, token)
	}
	for _, r := range value {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			current = append(current, r)
			continue
		}
		flush()
	}
	flush()
	return tokens
}

func detectImageMIMEType(filename, contentType string, imageBytes []byte) string {
	contentType = strings.TrimSpace(strings.ToLower(contentType))
	switch contentType {
	case "image/png", "image/webp", "image/jpeg", "image/jpg":
		if contentType == "image/jpg" {
			return "image/jpeg"
		}
		return contentType
	}

	switch strings.ToLower(filepath.Ext(filename)) {
	case ".png":
		return "image/png"
	case ".webp":
		return "image/webp"
	case ".jpg", ".jpeg":
		return "image/jpeg"
	}

	if len(imageBytes) > 0 {
		detected := strings.ToLower(http.DetectContentType(imageBytes))
		switch detected {
		case "image/png":
			return "image/png"
		case "image/webp":
			return "image/webp"
		case "image/jpeg":
			return "image/jpeg"
		}
	}

	return "image/jpeg"
}

const werkaAISearchPrompt = "Look at the package image and identify only the main brand or product family name that a warehouse worker should type for search. " +
	"If there is a clear standalone brand, return only that brand. " +
	"Do not include care words, category words, flavor, size, or descriptors such as cream, care, soap, sovun, milk, dairy, spicy. " +
	"Prefer the shortest searchable family query and server-friendly retail spelling or transliteration. " +
	"Examples: HOTLUNCH spicy chicken -> hot lunch, Musaffo -> musaffo, Simba Chips -> simba chips, Yashkino Mini-Rulet -> mini rulet, Nivea Creme Care -> nivea. " +
	"Return strict JSON with keys search_query, alt_query, visible_brand, visible_text, confidence."

var werkaAIBrandNoiseTokens = map[string]struct{}{
	"cream":        {},
	"care":         {},
	"soap":         {},
	"sovun":        {},
	"milk":         {},
	"dairy":        {},
	"soft":         {},
	"creme":        {},
	"molochnaya":   {},
	"mahsulotlari": {},
	"products":     {},
	"product":      {},
	"spicy":        {},
}
