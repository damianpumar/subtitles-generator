package huggingface

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"subtitles-generator/globals"
	"time"
)

type HuggingFaceRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Stream      bool      `json:"stream"`
	MaxTokens   int       `json:"max_tokens"`
	Temperature float64   `json:"temperature"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type HuggingFaceResponse struct {
	Choices []struct {
		Message      Message `json:"message"`
		FinishReason string  `json:"finish_reason"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func TranslateTexts(texts []string, targetLang string) ([]string, error) {
	if len(texts) == 0 {
		return []string{}, nil
	}

	var inputBuilder strings.Builder
	for i, text := range texts {
		inputBuilder.WriteString(fmt.Sprintf("[%d]\n%s\n\n", i+1, text))
	}

	systemPrompt := fmt.Sprintf(`You are a professional subtitle translator. Translate each numbered text block to %s.

CRITICAL RULES:
1. You will receive %d numbered text blocks like: [1] text [2] text [3] text
2. Translate ONLY the text content of each block
3. Return EXACTLY %d translations in the same format: [1] translated_text [2] translated_text [3] translated_text
4. Keep the SAME number [N] for each translation
5. Maintain the same number of lines within each text block
6. Do NOT skip any blocks
7. Do NOT add explanations or notes
8. Translate ALL %d blocks

Example input:
[1]
Hello world

[2]
How are you?

Example output:
[1]
Hola mundo

[2]
¿Cómo estás?`, targetLang, len(texts), len(texts), len(texts))

	reqBody := HuggingFaceRequest{
		Model: globals.ModelName,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: inputBuilder.String()},
		},
		Stream:      false,
		MaxTokens:   8000,
		Temperature: 0.3,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return nil, err
	}

	url := "https://router.huggingface.co/v1/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+globals.ApiKey)

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		if globals.Verbose {
			fmt.Printf("Response body: %s\n", string(body))
		}
		return nil, fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var hfResp HuggingFaceResponse
	if err := json.Unmarshal(body, &hfResp); err != nil {
		return nil, fmt.Errorf("error parsing response: %w", err)
	}

	if hfResp.Error != nil {
		return nil, fmt.Errorf("API error: %s (%s)", hfResp.Error.Message, hfResp.Error.Type)
	}

	if len(hfResp.Choices) == 0 || hfResp.Choices[0].Message.Content == "" {
		return nil, fmt.Errorf("no response received from model")
	}

	translated := strings.TrimSpace(hfResp.Choices[0].Message.Content)

	translatedTexts := parseNumberedResponse(translated, len(texts))

	if len(translatedTexts) != len(texts) {
		return nil, fmt.Errorf("expected %d translations but got %d", len(texts), len(translatedTexts))
	}

	return translatedTexts, nil
}

func parseNumberedResponse(response string, expectedCount int) []string {
	results := make([]string, expectedCount)
	lines := strings.Split(response, "\n")

	currentIndex := -1
	var currentText []string

	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "[") {
			if currentIndex >= 0 && currentIndex < expectedCount {
				results[currentIndex] = strings.TrimSpace(strings.Join(currentText, "\n"))
			}

			var idx int
			if _, err := fmt.Sscanf(line, "[%d]", &idx); err == nil {
				currentIndex = idx - 1
				currentText = []string{}
			}
			continue
		}

		if currentIndex >= 0 && line != "" {
			currentText = append(currentText, line)
		}
	}

	if currentIndex >= 0 && currentIndex < expectedCount {
		results[currentIndex] = strings.TrimSpace(strings.Join(currentText, "\n"))
	}

	return results
}
