package globals

import "sync"

const (
	ColorReset  = "\033[0m"
	ColorRed    = "\033[31m"
	ColorGreen  = "\033[32m"
	ColorYellow = "\033[33m"
	ColorBlue   = "\033[34m"
	ModelName   = "meta-llama/Llama-3.2-3B-Instruct"
)

var (
	Server     bool
	Port       string
	Dir        string
	File       string
	ApiKey     string
	TargetLang string
	Verbose    bool
	VideoExts  = []string{".mkv", ".mp4", ".avi", ".mov", ".wmv", ".flv", ".webm", ".m4v", ".mpg", ".mpeg"}
)

var (
	IsScanning    bool
	ScanningMutex sync.Mutex
	ScanInterval  int
)
