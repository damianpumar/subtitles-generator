package processor

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"subtitles-generator/globals"
	"subtitles-generator/huggingface"
	"time"
)

const chunkSize = 20

type SubtitleBlock struct {
	Number    string
	Timestamp string
	Text      string
}

func ProcessFile(inputFile, targetLang string) error {
	fmt.Printf(globals.ColorGreen+"=== Processing: %s ===\n"+globals.ColorReset, inputFile)

	if _, err := os.Stat(inputFile); os.IsNotExist(err) {
		return fmt.Errorf("file does not exist: %s", inputFile)
	}

	if hasTargetSubtitle(inputFile, targetLang) {
		fmt.Printf(globals.ColorYellow + "⚠ Subtitle with target language already exists (skipping)\n" + globals.ColorReset)

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

	originalBlocks := parseSRT(string(subtitleContent))
	fmt.Printf(globals.ColorBlue+"→ Original subtitles: %d\n"+globals.ColorReset, len(originalBlocks))

	fmt.Printf(globals.ColorBlue+"→ Translating to %s...\n"+globals.ColorReset, targetLang)

	translatedTexts, err := translateTextsInChunks(originalBlocks, targetLang)
	if err != nil {
		return fmt.Errorf("error translating: %w", err)
	}

	translatedContent := reconstructSRT(originalBlocks, translatedTexts)

	fmt.Printf(globals.ColorBlue+"→ Translated subtitles: %d\n"+globals.ColorReset, len(originalBlocks))

	langCode := getLangCode(targetLang)[0]
	outputFile := strings.TrimSuffix(inputFile, filepath.Ext(inputFile)) + "." + langCode + ".srt"

	if err := os.WriteFile(outputFile, []byte(translatedContent), 0644); err != nil {
		return fmt.Errorf("error saving file: %w", err)
	}

	if strings.HasPrefix(filepath.Base(existingTranslatedSRT), "temp_subtitle_") {
		os.Remove(existingTranslatedSRT)
	}

	fmt.Printf(globals.ColorGreen+"✓ Created: %s (validated %d subtitles)\n"+globals.ColorReset, outputFile, len(originalBlocks))
	return nil
}

func parseSRT(content string) []SubtitleBlock {
	var blocks []SubtitleBlock
	lines := strings.Split(content, "\n")

	timestampRegex := regexp.MustCompile(`^\d{2}:\d{2}:\d{2},\d{3} --> \d{2}:\d{2}:\d{2},\d{3}$`)
	numberRegex := regexp.MustCompile(`^\d+$`)

	var currentBlock SubtitleBlock
	var textLines []string
	state := "number"

	for _, line := range lines {
		line = strings.TrimSpace(line)

		if line == "" {
			if currentBlock.Number != "" && currentBlock.Timestamp != "" && len(textLines) > 0 {
				currentBlock.Text = strings.Join(textLines, "\n")
				blocks = append(blocks, currentBlock)
				currentBlock = SubtitleBlock{}
				textLines = []string{}
				state = "number"
			}
			continue
		}

		if state == "number" && numberRegex.MatchString(line) {
			currentBlock.Number = line
			state = "timestamp"
		} else if state == "timestamp" && timestampRegex.MatchString(line) {
			currentBlock.Timestamp = line
			state = "text"
		} else if state == "text" {
			textLines = append(textLines, line)
		}
	}

	if currentBlock.Number != "" && currentBlock.Timestamp != "" && len(textLines) > 0 {
		currentBlock.Text = strings.Join(textLines, "\n")
		blocks = append(blocks, currentBlock)
	}

	return blocks
}

func reconstructSRT(originalBlocks []SubtitleBlock, translatedTexts []string) string {
	var result strings.Builder

	for i, block := range originalBlocks {
		result.WriteString(block.Number)
		result.WriteString("\n")
		result.WriteString(block.Timestamp)
		result.WriteString("\n")

		if i < len(translatedTexts) && translatedTexts[i] != "" {
			result.WriteString(translatedTexts[i])
		} else {
			result.WriteString(block.Text)
		}

		result.WriteString("\n\n")
	}

	return strings.TrimSpace(result.String())
}

func translateTextsInChunks(blocks []SubtitleBlock, targetLang string) ([]string, error) {
	totalBlocks := len(blocks)
	translatedTexts := make([]string, totalBlocks)

	numChunks := (totalBlocks + chunkSize - 1) / chunkSize
	fmt.Printf(globals.ColorBlue+"→ Processing %d chunks of ~%d subtitles\n"+globals.ColorReset, numChunks, chunkSize)

	for chunkIdx := 0; chunkIdx < totalBlocks; chunkIdx += chunkSize {
		end := chunkIdx + chunkSize
		if end > totalBlocks {
			end = totalBlocks
		}

		chunk := blocks[chunkIdx:end]
		chunkNum := (chunkIdx / chunkSize) + 1

		if globals.Verbose {
			fmt.Printf(globals.ColorBlue+"  Chunk %d/%d (subtitles %d-%d)...\n"+globals.ColorReset,
				chunkNum, numChunks, chunkIdx+1, end)
		} else {
			fmt.Printf(globals.ColorBlue+"  Chunk %d/%d..."+globals.ColorReset, chunkNum, numChunks)
		}

		var textsToTranslate []string
		for _, block := range chunk {
			textsToTranslate = append(textsToTranslate, block.Text)
		}

		var translatedChunk []string
		var err error

		for attempt := 1; attempt <= 3; attempt++ {
			translatedChunk, err = huggingface.TranslateTexts(textsToTranslate, targetLang)
			if err != nil {
				if attempt == 3 {
					return nil, fmt.Errorf("chunk %d failed after 3 attempts: %w", chunkNum, err)
				}
				fmt.Printf(globals.ColorYellow+" retry %d..."+globals.ColorReset, attempt)
				time.Sleep(2 * time.Second)
				continue
			}

			if len(translatedChunk) != len(textsToTranslate) {
				if attempt == 3 {
					return nil, fmt.Errorf("chunk %d: expected %d translations, got %d",
						chunkNum, len(textsToTranslate), len(translatedChunk))
				}
				fmt.Printf(globals.ColorYellow+" count mismatch, retry %d..."+globals.ColorReset, attempt)
				time.Sleep(2 * time.Second)
				continue
			}

			break
		}

		for i, translated := range translatedChunk {
			translatedTexts[chunkIdx+i] = translated
		}

		if end < totalBlocks {
			time.Sleep(500 * time.Millisecond)
		}
	}

	return translatedTexts, nil
}

func getSourceSubtitles(inputFile string) (string, error) {
	baseName := strings.TrimSuffix(inputFile, filepath.Ext(inputFile))

	existingSRTs, err := filepath.Glob(baseName + "*.srt")
	if err == nil && len(existingSRTs) > 0 {
		fmt.Println(globals.ColorBlue + "→ Using existing subtitle file: " + filepath.Base(existingSRTs[0]) + globals.ColorReset)
		return existingSRTs[0], nil
	}

	fmt.Println(globals.ColorBlue + "→ Extracting subtitles from video..." + globals.ColorReset)

	videoDir := filepath.Dir(inputFile)
	tempSub := filepath.Join(videoDir, fmt.Sprintf("temp_subtitle_%d.srt", time.Now().Unix()))

	if err := extractSubtitles(inputFile, tempSub); err != nil {
		return "", fmt.Errorf("error extracting subtitles: %w", err)
	}

	return tempSub, nil
}

func extractSubtitles(inputFile, outputFile string) error {
	probeCmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "s",
		"-show_entries", "stream=index:stream_tags=language,title",
		"-of", "csv=p=0", inputFile)

	probeOutput, err := probeCmd.Output()
	if err != nil {
		return fmt.Errorf("ffprobe failed: %w", err)
	}

	streamIndex := findBestSubtitleStream(string(probeOutput))

	if streamIndex == -1 {
		return fmt.Errorf("no suitable subtitle stream found")
	}

	if globals.Verbose {
		fmt.Printf(globals.ColorBlue+"  Using subtitle stream index: %d\n"+globals.ColorReset, streamIndex)
	}

	cmd := exec.Command("ffmpeg", "-i", inputFile, "-map", fmt.Sprintf("0:%d", streamIndex), outputFile, "-y")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if globals.Verbose {
			fmt.Println(stderr.String())
		}
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	return nil
}

func findBestSubtitleStream(probeOutput string) int {
	lines := strings.Split(strings.TrimSpace(probeOutput), "\n")
	if len(lines) == 0 {
		return -1
	}

	var bestIndex = -1
	var bestPriority = -1

	for _, line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}

		index := -1
		fmt.Sscanf(parts[0], "%d", &index)
		if index == -1 {
			continue
		}

		metadata := strings.ToLower(strings.Join(parts[1:], ","))

		priority := 3

		if strings.Contains(metadata, "forced") {
			priority = 1
		} else if strings.Contains(metadata, "sdh") || strings.Contains(metadata, "hearing impaired") {
			priority = 2
		}

		if priority > bestPriority {
			bestPriority = priority
			bestIndex = index
		}
	}

	if bestIndex == -1 && len(lines) > 0 {
		fmt.Sscanf(lines[0], "%d", &bestIndex)
	}

	return bestIndex
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

func hasTargetSubtitle(videoFile, targetLang string) bool {
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
