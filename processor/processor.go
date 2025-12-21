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
	videoDir := filepath.Dir(inputFile)

	// 1. Check in Subs folder (same directory as video)
	subsDir := filepath.Join(videoDir, "Subs")

	fmt.Printf(globals.ColorBlue+"→ Checking Subs directory: %s\n"+globals.ColorReset, subsDir)

	if stat, err := os.Stat(subsDir); err == nil && stat.IsDir() {
		// Read directory and filter .srt files (case-insensitive)
		entries, err := os.ReadDir(subsDir)
		if err == nil {
			var subsDirFiles []string
			for _, entry := range entries {
				if !entry.IsDir() {
					lowerName := strings.ToLower(entry.Name())
					if strings.HasSuffix(lowerName, ".srt") {
						subsDirFiles = append(subsDirFiles, filepath.Join(subsDir, entry.Name()))
					}
				}
			}

			fmt.Printf(globals.ColorBlue+"→ Found %d .srt files in Subs\n"+globals.ColorReset, len(subsDirFiles))
			for _, f := range subsDirFiles {
				fmt.Printf(globals.ColorBlue+"  - %s\n"+globals.ColorReset, filepath.Base(f))
			}

			if len(subsDirFiles) > 0 {
				fmt.Println(globals.ColorBlue + "→ Using subtitle from Subs folder: " + filepath.Base(subsDirFiles[0]) + globals.ColorReset)
				return subsDirFiles[0], nil
			}
		}
	} else {
		fmt.Printf(globals.ColorYellow+"→ Subs directory not found or not accessible: %v\n"+globals.ColorReset, err)
	}

	// 2. Check for existing SRT files with same basename
	existingSRTs, err := filepath.Glob(baseName + "*.srt")
	if err == nil && len(existingSRTs) > 0 {
		fmt.Println(globals.ColorBlue + "→ Using existing subtitle file: " + filepath.Base(existingSRTs[0]) + globals.ColorReset)
		return existingSRTs[0], nil
	}

	// 3. Extract subtitles from video as last resort
	fmt.Println(globals.ColorBlue + "→ Extracting subtitles from video..." + globals.ColorReset)

	tempSub := filepath.Join(videoDir, fmt.Sprintf("temp_subtitle_%d.srt", time.Now().Unix()))

	if err := extractSubtitles(inputFile, tempSub); err != nil {
		return "", fmt.Errorf("error extracting subtitles: %w", err)
	}

	return tempSub, nil
}

func extractSubtitles(inputFile, outputFile string) error {
	probeCmd := exec.Command("ffprobe", "-v", "error", "-select_streams", "s",
		"-show_entries", "stream=index,codec_name:stream_tags=language,title",
		"-of", "csv=p=0", inputFile)

	probeOutput, err := probeCmd.Output()
	if err != nil {
		return fmt.Errorf("ffprobe failed: %w", err)
	}

	if globals.Verbose {
		fmt.Printf(globals.ColorBlue+"Available subtitle streams:\n%s"+globals.ColorReset, string(probeOutput))
	}

	streamIndex, streamInfo := findBestSubtitleStream(string(probeOutput), inputFile)

	if streamIndex == -1 {
		return fmt.Errorf("no suitable subtitle stream found")
	}

	fmt.Printf(globals.ColorBlue+"  Using: %s\n"+globals.ColorReset, streamInfo)

	cmd := exec.Command("ffmpeg", "-i", inputFile, "-map", fmt.Sprintf("0:%d", streamIndex), outputFile, "-y")
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if globals.Verbose {
			fmt.Println(stderr.String())
		}
		return fmt.Errorf("ffmpeg failed: %w", err)
	}

	fileInfo, err := os.Stat(outputFile)
	if err != nil {
		return fmt.Errorf("subtitle file not created: %w", err)
	}

	if fileInfo.Size() < 100 {
		return fmt.Errorf("extracted subtitle is too small (possibly empty or incomplete): %d bytes", fileInfo.Size())
	}

	if globals.Verbose {
		fmt.Printf(globals.ColorGreen+"  Extracted subtitle: %d bytes\n"+globals.ColorReset, fileInfo.Size())
	}

	return nil
}

type SubtitleStreamInfo struct {
	Index    int
	Language string
	Title    string
	Codec    string
	Priority int
}

func findBestSubtitleStream(probeOutput string, inputFile string) (int, string) {
	lines := strings.Split(strings.TrimSpace(probeOutput), "\n")
	if len(lines) == 0 {
		return -1, ""
	}

	var streams []SubtitleStreamInfo

	for _, line := range lines {
		parts := strings.Split(line, ",")
		if len(parts) < 2 {
			continue
		}

		var stream SubtitleStreamInfo
		fmt.Sscanf(parts[0], "%d", &stream.Index)

		if stream.Index == -1 {
			continue
		}

		if len(parts) > 1 {
			stream.Codec = parts[1]
		}
		if len(parts) > 2 {
			stream.Language = strings.ToLower(parts[2])
		}
		if len(parts) > 3 {
			stream.Title = strings.ToLower(parts[3])
		}

		metadata := stream.Language + " " + stream.Title

		stream.Priority = 0

		if strings.Contains(stream.Language, "eng") ||
			strings.Contains(stream.Language, "en") ||
			strings.Contains(metadata, "english") {
			stream.Priority += 1000
		}

		if !strings.Contains(metadata, "forced") &&
			!strings.Contains(metadata, "commentary") &&
			!strings.Contains(metadata, "comment") {
			stream.Priority += 500
		}

		if strings.Contains(metadata, "full") ||
			strings.Contains(metadata, "complete") {
			stream.Priority += 200
		}

		if !strings.Contains(metadata, "sdh") &&
			!strings.Contains(metadata, "hearing impaired") {
			stream.Priority += 100
		}

		if strings.Contains(metadata, "forced") {
			stream.Priority -= 500
		}

		if strings.Contains(metadata, "commentary") || strings.Contains(metadata, "comment") {
			stream.Priority -= 1000
		}

		streams = append(streams, stream)
	}

	if len(streams) == 0 {
		return -1, ""
	}

	var topStreams []SubtitleStreamInfo
	maxPriority := streams[0].Priority

	for _, stream := range streams {
		if stream.Priority > maxPriority {
			maxPriority = stream.Priority
		}
	}

	for _, stream := range streams {
		if stream.Priority == maxPriority {
			topStreams = append(topStreams, stream)
		}
	}

	if len(topStreams) == 1 {
		info := fmt.Sprintf("Stream %d [%s] %s (priority: %d)",
			topStreams[0].Index, topStreams[0].Language, topStreams[0].Title, topStreams[0].Priority)
		return topStreams[0].Index, info
	}

	if globals.Verbose {
		fmt.Printf(globals.ColorYellow + "  Multiple streams with same priority, checking sizes...\n" + globals.ColorReset)
	}

	bestStream := topStreams[0]
	maxSize := int64(0)

	for _, stream := range topStreams {
		size := getSubtitleStreamSize(inputFile, stream.Index)

		if globals.Verbose {
			fmt.Printf(globals.ColorBlue+"    Stream %d: %d bytes\n"+globals.ColorReset, stream.Index, size)
		}

		if size > maxSize {
			maxSize = size
			bestStream = stream
		}
	}

	info := fmt.Sprintf("Stream %d [%s] %s (priority: %d, size: %d bytes)",
		bestStream.Index, bestStream.Language, bestStream.Title, bestStream.Priority, maxSize)

	return bestStream.Index, info
}

func getSubtitleStreamSize(inputFile string, streamIndex int) int64 {
	tempFile := filepath.Join(os.TempDir(), fmt.Sprintf("temp_sub_check_%d_%d.srt", time.Now().Unix(), streamIndex))
	defer os.Remove(tempFile)

	cmd := exec.Command("ffmpeg", "-i", inputFile, "-map", fmt.Sprintf("0:%d", streamIndex),
		"-t", "60",
		tempFile, "-y")

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		return 0
	}

	fileInfo, err := os.Stat(tempFile)
	if err != nil {
		return 0
	}

	return fileInfo.Size()
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
