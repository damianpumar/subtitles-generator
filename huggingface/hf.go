package huggingface

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"subtitles-generator/globals"
	"subtitles-generator/utils"
	"time"
)

type HuggingFaceRequest struct {
	Model     string    `json:"model"`
	Messages  []Message `json:"messages"`
	Stream    bool      `json:"stream"`
	MaxTokens int       `json:"max_tokens"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type HuggingFaceResponse struct {
	Choices []struct {
		Message Message `json:"message"`
	} `json:"choices"`
	Error *struct {
		Message string `json:"message"`
		Type    string `json:"type"`
	} `json:"error,omitempty"`
}

func TranslateSubtitles(content, targetLang string) (string, error) {
	systemPrompt := fmt.Sprintf(`You are a subtitle translator. Your task is to translate subtitle dialogue to %s maintaining the same SRT FORMAT.
CRITICAL RULES - FOLLOW EXACTLY:
1. Keep the EXACT same structure for each subtitle
2. NEVER remove or modify timestamps
3. NEVER remove or modify subtitle numbers
4. Translate ONLY the dialogue text, nothing else
5. Return ONLY valid SRT format, no explanations
7. Complete ALL subtitles in the block
`, targetLang)

	reqBody := HuggingFaceRequest{
		Model: globals.ModelName,
		Messages: []Message{
			{Role: "system", Content: systemPrompt},
			{Role: "user", Content: content},
		},
		Stream:    false,
		MaxTokens: 4096,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		return "", err
	}

	url := "https://router.huggingface.co/v1/chat/completions"
	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+globals.ApiKey)

	client := &http.Client{Timeout: 180 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}

	if resp.StatusCode != 200 {
		if globals.Verbose {
			fmt.Printf("Response body: %s\n", string(body))
		}
		return "", fmt.Errorf("API error (%d): %s", resp.StatusCode, string(body))
	}

	var hfResp HuggingFaceResponse
	if err := json.Unmarshal(body, &hfResp); err != nil {
		return "", fmt.Errorf("error parsing response: %w - body: %s", err, string(body))
	}

	if hfResp.Error != nil {
		return "", fmt.Errorf("API error: %s (%s)", hfResp.Error.Message, hfResp.Error.Type)
	}

	if len(hfResp.Choices) == 0 || hfResp.Choices[0].Message.Content == "" {
		return "", fmt.Errorf("no response received from model")
	}

	translated := hfResp.Choices[0].Message.Content

	if !utils.ValidateTimestamps(content, translated) {
		return "", fmt.Errorf("translation missing timestamps")
	}

	return translated, nil
}
