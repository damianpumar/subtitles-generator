package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/joho/godotenv"
)

const (
	colorReset  = "\033[0m"
	colorRed    = "\033[31m"
	colorGreen  = "\033[32m"
	colorYellow = "\033[33m"
	colorBlue   = "\033[34m"
	modelName   = "meta-llama/Llama-3.2-3B-Instruct"
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

var (
	watchDir   string
	singleFile string
	apiKey     string
	targetLang string
	verbose    bool
	videoExts  = []string{".mkv", ".mp4", ".avi", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".mpg", ".mpeg"}
)

func init() {
	if err := godotenv.Load(); err != nil {
		log.Println(colorYellow + "Warning: .env file not found, using system environment variables" + colorReset)
	}

	flag.StringVar(&watchDir, "watch", "", "Folder to watch for new video files")
	flag.StringVar(&singleFile, "file", "", "Process a single video file")
	flag.StringVar(&targetLang, "target", getEnvOrDefault("TARGET_LANG", "Spanish"), "Target language for translation")
	flag.BoolVar(&verbose, "verbose", false, "Show detailed information")
	flag.Parse()

	apiKey = os.Getenv("HF_TOKEN")
	if apiKey == "" {
		log.Fatal(colorRed + "Error: HF_TOKEN environment variable not set" + colorReset)
	}
}

func getEnvOrDefault(key, defaultValue string) string {
	if value := os.Getenv(key); value != "" {
		return value
	}
	return defaultValue
}

func main() {
	if err := checkDependencies(); err != nil {
		log.Fatal(colorRed + err.Error() + colorReset)
	}

	if singleFile != "" {
		if err := processFile(singleFile); err != nil {
			log.Fatal(colorRed + "Error processing file: " + err.Error() + colorReset)
		}
		fmt.Println(colorGreen + "✓ Processing completed" + colorReset)
	} else if watchDir != "" {
		if err := watchDirectory(watchDir); err != nil {
			log.Fatal(colorRed + "Error: " + err.Error() + colorReset)
		}
	} else {
		fmt.Println(colorYellow + "Usage:" + colorReset)
		fmt.Println("  Process a file:      " + os.Args[0] + " -file video.mkv")
		fmt.Println("  Watch a folder:      " + os.Args[0] + " -watch /path/to/folder")
		fmt.Println("\nOptions:")
		flag.PrintDefaults()
		fmt.Println("\nLanguage examples:")
		fmt.Println("  -target Spanish")
		fmt.Println("  -target English")
		fmt.Println("  -target French")
		os.Exit(1)
	}
}

func checkDependencies() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg is not installed")
	}
	return nil
}

func addRecursiveWatch(watcher *fsnotify.Watcher, root string) error {
	return filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.IsDir() {
			if verbose {
				fmt.Printf(colorBlue+"  Watching: %s\n"+colorReset, path)
			}
			return watcher.Add(path)
		}
		return nil
	})
}

func watchDirectory(dir string) error {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return err
	}
	defer watcher.Close()

	fmt.Println(colorBlue + "Scanning for existing videos..." + colorReset)
	err = filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if !info.IsDir() && isVideoFile(path) && !hasTargetSubtitle(path) {
			fmt.Printf(colorBlue+"Processing existing file: %s\n"+colorReset, path)
			if err := processFile(path); err != nil {
				log.Printf(colorRed+"Error processing %s: %v"+colorReset, path, err)
			}
		}
		return nil
	})
	if err != nil {
		log.Printf(colorYellow+"Warning scanning directory: %v"+colorReset, err)
	}

	if err := addRecursiveWatch(watcher, dir); err != nil {
		return err
	}

	done := make(chan bool)

	go func() {
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				if event.Op&fsnotify.Create == fsnotify.Create {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						if err := addRecursiveWatch(watcher, event.Name); err != nil {
							log.Printf(colorYellow+"Warning adding watch for %s: %v"+colorReset, event.Name, err)
						}
					} else if isVideoFile(event.Name) {
						time.Sleep(2 * time.Second)
						fmt.Printf(colorBlue+"New file detected: %s\n"+colorReset, event.Name)
						if err := processFile(event.Name); err != nil {
							log.Printf(colorRed+"Error: %v"+colorReset, err)
						}
					}
				}
			case err, ok := <-watcher.Errors:
				if !ok {
					return
				}
				log.Printf(colorRed+"Watcher error: %v"+colorReset, err)
			}
		}
	}()

	fmt.Printf(colorGreen+"Watching folder (recursive): %s\n"+colorReset, dir)
	fmt.Printf(colorBlue+"Target language: %s\n"+colorReset, targetLang)
	fmt.Printf(colorBlue+"Using model: %s\n"+colorReset, modelName)
	fmt.Println(colorYellow + "Press Ctrl+C to stop..." + colorReset)

	<-done
	return nil
}

func processFile(inputFile string) error {
	fmt.Printf(colorGreen+"=== Processing: %s ===\n"+colorReset, inputFile)

	if _, err := os.Stat(inputFile); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", inputFile)
	}

	if hasTargetSubtitle(inputFile) {
		fmt.Printf(colorYellow + "⚠ Subtitle with target language already exists (skipping)\n" + colorReset)
		return nil
	}

	existingTranslatedSRT, err := getSourceSubtitles(inputFile)
	if err != nil {
		return fmt.Errorf("error getting source subtitles: %w", err)
	}

	subtitleContent, err := os.ReadFile(existingTranslatedSRT)
	if err != nil {
		return fmt.Errorf("error reading subtitles: %w", err)
	}

	originalCount := countSubtitles(string(subtitleContent))
	fmt.Printf(colorBlue+"→ Original subtitles: %d\n"+colorReset, originalCount)

	fmt.Printf(colorBlue+"→ Translating to %s...\n"+colorReset, targetLang)
	translatedContent, err := translateSubtitlesInChunks(string(subtitleContent))
	if err != nil {
		return fmt.Errorf("error translating: %w", err)
	}

	translatedCount := countSubtitles(translatedContent)
	fmt.Printf(colorBlue+"→ Translated subtitles: %d\n"+colorReset, translatedCount)

	tolerance := int(float64(originalCount) * 0.05)
	if tolerance < 2 {
		tolerance = 2
	}

	if translatedCount < originalCount-tolerance {
		return fmt.Errorf("too few subtitles: got %d but expected around %d", translatedCount, originalCount)
	}

	if translatedCount > originalCount+tolerance {
		return fmt.Errorf("too many subtitles: got %d but expected around %d", translatedCount, originalCount)
	}

	langCode := getLangCode(targetLang)[0]
	outputFile := strings.TrimSuffix(inputFile, filepath.Ext(inputFile)) + "." + langCode + ".srt"

	if err := os.WriteFile(outputFile, []byte(translatedContent), 0644); err != nil {
		return fmt.Errorf("error saving file: %w", err)
	}

	if strings.HasPrefix(filepath.Base(existingTranslatedSRT), "temp_subtitle_") {
		os.Remove(existingTranslatedSRT)
	}

	fmt.Printf(colorGreen+"✓ Completed: %s (validated %d subtitles)\n"+colorReset, outputFile, translatedCount)
	return nil
}

func countSubtitles(content string) int {
	lines := strings.Split(content, "\n")
	count := 0
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if isNumeric(trimmed) {
			count++
		}
	}
	return count
}

func getSourceSubtitles(inputFile string) (string, error) {
	baseName := strings.TrimSuffix(inputFile, filepath.Ext(inputFile))

	existingSRTs, err := filepath.Glob(baseName + "*.srt")
	if err == nil && len(existingSRTs) > 0 {
		fmt.Println(colorBlue + "→ Using existing subtitle file: " + filepath.Base(existingSRTs[0]) + colorReset)
		return existingSRTs[0], nil
	}

	fmt.Println(colorBlue + "→ Extracting subtitles from video..." + colorReset)

	videoDir := filepath.Dir(inputFile)
	tempSub := filepath.Join(videoDir, fmt.Sprintf("temp_subtitle_%d.srt", time.Now().Unix()))

	if err := extractSubtitles(inputFile, tempSub); err != nil {
		return "", fmt.Errorf("error extracting subtitles: %w", err)
	}

	return tempSub, nil
}

func extractSubtitles(inputFile, outputFile string) error {
	cmd := exec.Command("ffmpeg", "-i", inputFile, "-map", "0:s:0", outputFile, "-y")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if verbose {
			fmt.Println(stderr.String())
		}
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	return nil
}

func translateSubtitlesInChunks(content string) (string, error) {
	blocks := splitIntoBlocks(content, 20)
	var translatedBlocks []string

	fmt.Printf(colorBlue+"→ Split into %d blocks for translation\n"+colorReset, len(blocks))

	for i, block := range blocks {
		if verbose {
			fmt.Printf(colorBlue+"  Translating block %d/%d...\n"+colorReset, i+1, len(blocks))
		} else {
			fmt.Printf(colorBlue+"  Block %d/%d..."+colorReset, i+1, len(blocks))
		}

		var translated string
		var err error

		for attempt := 1; attempt <= 3; attempt++ {
			translated, err = translateSubtitles(block)
			if err != nil {
				if attempt == 3 {
					return "", fmt.Errorf("error in block %d after 3 attempts: %w", i+1, err)
				}
				fmt.Printf(colorYellow+" retry %d..."+colorReset, attempt)
				time.Sleep(2 * time.Second)
				continue
			}

			if strings.TrimSpace(translated) == "" {
				if attempt == 3 {
					return "", fmt.Errorf("block %d returned empty translation after 3 attempts", i+1)
				}
				fmt.Printf(colorYellow+" empty, retry %d..."+colorReset, attempt)
				time.Sleep(2 * time.Second)
				continue
			}

			if !validateTimestamps(block, translated) {
				if attempt == 3 {
					return "", fmt.Errorf("block %d missing timestamps after 3 attempts", i+1)
				}
				fmt.Printf(colorYellow+" missing timestamps, retry %d..."+colorReset, attempt)
				time.Sleep(2 * time.Second)
				continue
			}

			break
		}

		translatedBlocks = append(translatedBlocks, translated)

		if i < len(blocks)-1 {
			time.Sleep(500 * time.Millisecond)
		}
	}

	return strings.Join(translatedBlocks, "\n\n"), nil
}

func splitIntoBlocks(content string, subtitlesPerBlock int) []string {
	if subtitlesPerBlock <= 0 {
		subtitlesPerBlock = 20
	}

	lines := strings.Split(content, "\n")
	var blocks []string
	var currentBlock []string
	currentSubtitleCount := 0
	inSubtitle := false

	for i := 0; i < len(lines); i++ {
		line := lines[i]
		trimmed := strings.TrimSpace(line)
		currentBlock = append(currentBlock, line)

		if !inSubtitle && trimmed != "" && isNumeric(trimmed) {
			currentSubtitleCount++
			inSubtitle = true
		}

		if trimmed == "" {
			inSubtitle = false

			if currentSubtitleCount >= subtitlesPerBlock {
				blocks = append(blocks, strings.Join(currentBlock, "\n"))
				currentBlock = []string{}
				currentSubtitleCount = 0
			}
		}
	}

	if len(currentBlock) > 0 {
		blocks = append(blocks, strings.Join(currentBlock, "\n"))
	}

	return blocks
}

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
}

func translateSubtitles(content string) (string, error) {
	systemPrompt := fmt.Sprintf(`You are a subtitle translator. Your task is to translate subtitle dialogue to %s while preserving the EXACT SRT format.
CRITICAL RULES - FOLLOW EXACTLY:
1. Keep the EXACT same structure for each subtitle:
   - Number (e.g., 1)
   - Timestamp (e.g., 00:01:23,456 --> 00:01:25,789)
   - Text (translate this)
   - Blank line

2. NEVER remove or modify timestamps
3. NEVER skip any subtitle number
4. Translate ONLY the dialogue text, nothing else
5. Return ONLY valid SRT format, no explanations
6. Start with the first subtitle number from the input
7. Complete ALL subtitles in the block
8. Maintain the original timing and sequence

Example:
Input:
1
00:00:01,000 --> 00:00:03,000
Hello world

Output:
1
00:00:01,000 --> 00:00:03,000
Hola mundo`, targetLang)

	reqBody := HuggingFaceRequest{
		Model: modelName,
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
	req.Header.Set("Authorization", "Bearer "+apiKey)

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
		if verbose {
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

	if !validateTimestamps(content, translated) {
		return "", fmt.Errorf("translation missing timestamps")
	}

	return translated, nil
}

func validateTimestamps(original, translated string) bool {
	originalTimestamps := countTimestamps(original)
	translatedTimestamps := countTimestamps(translated)

	if translatedTimestamps < originalTimestamps {
		if verbose {
			fmt.Printf(colorYellow+"\n  Warning: Missing timestamps (%d/%d)\n"+colorReset,
				translatedTimestamps, originalTimestamps)
		}
		return false
	}
	return true
}

func countTimestamps(content string) int {
	lines := strings.Split(content, "\n")
	count := 0
	for _, line := range lines {
		if strings.Contains(line, " --> ") {
			count++
		}
	}
	return count
}

func getLangCode(language string) []string {
	langMap := map[string][]string{
		"spanish": {"spa", "es"}, "english": {"en", "eng"},
		"french": {"fr", "fra", "fre"}, "german": {"de", "deu", "ger"},
		"italian": {"it", "ita"}, "portuguese": {"pt", "por"},
		"chinese": {"zh", "chi", "zho"}, "japanese": {"ja", "jpn"},
		"korean": {"ko", "kor"}, "russian": {"ru", "rus"},
		"arabic": {"ar", "ara"}, "hindi": {"hi", "hin"},
		"dutch": {"nl", "nld", "dut"}, "polish": {"pl", "pol"},
		"turkish": {"tr", "tur"}, "swedish": {"sv", "swe"},
		"norwegian": {"no", "nor"}, "danish": {"da", "dan"},
		"finnish": {"fi", "fin"}, "czech": {"cs", "cze", "ces"},
		"romanian": {"ro", "rum", "ron"}, "hungarian": {"hu", "hun"},
		"thai": {"th", "tha"}, "vietnamese": {"vi", "vie"},
	}

	langLower := strings.ToLower(language)
	if codes, exists := langMap[langLower]; exists {
		return codes
	}

	if len(language) >= 2 {
		return []string{strings.ToLower(language[:2])}
	}

	return []string{"tr"}
}

func isVideoFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	for _, validExt := range videoExts {
		if ext == validExt {
			return true
		}
	}
	return false
}

func hasTargetSubtitle(videoFile string) bool {
	baseName := strings.TrimSuffix(videoFile, filepath.Ext(videoFile))
	langCodes := getLangCode(targetLang)

	for _, langCode := range langCodes {
		targetSRTUnderscore := baseName + "_" + langCode + ".srt"
		targetSRTDot := baseName + "." + langCode + ".srt"

		if _, err := os.Stat(targetSRTUnderscore); err == nil {
			return true
		}
		if _, err := os.Stat(targetSRTDot); err == nil {
			return true
		}
	}

	existingSRTs, err := filepath.Glob(baseName + "*.srt")
	if err != nil {
		return false
	}

	for _, srtFile := range existingSRTs {
		pattern := regexp.MustCompile(`[_.]([a-z]{2,3})\.srt$`)
		matches := pattern.FindStringSubmatch(filepath.Base(srtFile))
		if len(matches) > 1 {
			detectedCode := matches[1]
			for _, langCode := range langCodes {
				if detectedCode == langCode {
					return true
				}
			}
		}
	}

	return false
}
