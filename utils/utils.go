package utils

import (
	"fmt"
	"strings"
	"subtitles-generator/globals"
)

func ValidateTimestamps(original, translated string) bool {
	originalTimestamps := countTimestamps(original)
	translatedTimestamps := countTimestamps(translated)

	if translatedTimestamps < originalTimestamps {
		if globals.Verbose {
			fmt.Printf(globals.ColorYellow+"\n  Warning: Missing timestamps (%d/%d)\n"+globals.ColorReset,
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
