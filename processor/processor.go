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
	"subtitles-generator/utils"
	"time"
)

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

	originalCount := countSubtitles(string(subtitleContent))
	fmt.Printf(globals.ColorBlue+"→ Original subtitles: %d\n"+globals.ColorReset, originalCount)

	fmt.Printf(globals.ColorBlue+"→ Translating to %s...\n"+globals.ColorReset, targetLang)
	translatedContent, err := translateSubtitlesInChunks(string(subtitleContent), targetLang)
	if err != nil {
		return fmt.Errorf("error translating: %w", err)
	}

	translatedCount := countSubtitles(translatedContent)
	fmt.Printf(globals.ColorBlue+"→ Translated subtitles: %d\n"+globals.ColorReset, translatedCount)

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

	fmt.Printf(globals.ColorGreen+"✓ Completed: %s (validated %d subtitles)\n"+globals.ColorReset, outputFile, translatedCount)
	return nil
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
	cmd := exec.Command("ffmpeg", "-i", inputFile, "-map", "0:s:0", outputFile, "-y")
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

func isNumeric(s string) bool {
	for _, c := range s {
		if c < '0' || c > '9' {
			return false
		}
	}
	return len(s) > 0
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

func translateSubtitlesInChunks(content, targetLang string) (string, error) {
	blocks := splitIntoBlocks(content, 20)
	var translatedBlocks []string

	fmt.Printf(globals.ColorBlue+"→ Split into %d blocks for translation\n"+globals.ColorReset, len(blocks))

	for i, block := range blocks {
		if globals.Verbose {
			fmt.Printf(globals.ColorBlue+"  Translating block %d/%d...\n"+globals.ColorReset, i+1, len(blocks))
		} else {
			fmt.Printf(globals.ColorBlue+"  Block %d/%d..."+globals.ColorReset, i+1, len(blocks))
		}

		var translated string
		var err error

		for attempt := 1; attempt <= 3; attempt++ {
			translated, err = huggingface.TranslateSubtitles(block, targetLang)
			if err != nil {
				if attempt == 3 {
					return "", fmt.Errorf("error in block %d after 3 attempts: %w", i+1, err)
				}
				fmt.Printf(globals.ColorYellow+" retry %d..."+globals.ColorReset, attempt)
				time.Sleep(2 * time.Second)
				continue
			}

			if strings.TrimSpace(translated) == "" {
				if attempt == 3 {
					return "", fmt.Errorf("block %d returned empty translation after 3 attempts", i+1)
				}
				fmt.Printf(globals.ColorYellow+" empty, retry %d..."+globals.ColorReset, attempt)
				time.Sleep(2 * time.Second)
				continue
			}

			if !utils.ValidateTimestamps(block, translated) {
				if attempt == 3 {
					return "", fmt.Errorf("block %d missing timestamps after 3 attempts", i+1)
				}
				fmt.Printf(globals.ColorYellow+" missing timestamps, retry %d..."+globals.ColorReset, attempt)
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
