package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"subtitles-generator/globals"
	"subtitles-generator/processor"
	"time"

	"github.com/damianpumar/mate"
	"github.com/damianpumar/mate/database"
	"github.com/joho/godotenv"
)

func init() {
	if err := godotenv.Load(); err != nil {
		log.Println(globals.ColorYellow + "Warning: .env file not found, using system environment variables" + globals.ColorReset)
	}

	flag.StringVar(&globals.Port, "port", "9595", "Port to listen on")
	flag.StringVar(&globals.Dir, "dir", "", "Folder to watch for new video files")
	flag.StringVar(&globals.File, "file", "", "Process a single video file")
	flag.StringVar(&globals.TargetLang, "target", "Spanish", "Target language for translation")
	flag.BoolVar(&globals.Verbose, "verbose", false, "Show detailed information")
	flag.IntVar(&globals.ScanInterval, "scan-interval", 10, "Minutes between directory scans")
	flag.BoolVar(&globals.Server, "server", true, "Run in server mode")
	flag.Parse()

	globals.ApiKey = os.Getenv("HF_TOKEN")
	if globals.ApiKey == "" {
		log.Fatal(globals.ColorRed + "Error: HF_TOKEN environment variable not set" + globals.ColorReset)
	}

	if globals.Server && globals.Dir == "" {
		log.Fatal(globals.ColorRed + "Error: -dir must be specified in server mode" + globals.ColorReset)
	}
}

func main() {
	if err := checkDependencies(); err != nil {
		log.Fatal(globals.ColorRed + err.Error() + globals.ColorReset)
	}

	if globals.Dir == "" {
		fmt.Println(globals.ColorYellow + "Warning: No directory specified to scan" + globals.ColorReset)
		os.Exit(1)
	}

	if globals.File == "" && globals.Dir == "" {
		fmt.Println(globals.ColorYellow + "Usage:" + globals.ColorReset)
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

	fmt.Println(globals.ColorBlue + "🧉 Starting Subtitle Translator..." + globals.ColorReset)

	db := database.Connect()

	if globals.Server {

		server := mate.New()

		server.Get("/", func(c *mate.Context) {
			c.JSON(200, map[string]any{
				"message": "Subtitle Translator API is running",
			})
		})

		server.Get("/state", func(c *mate.Context) {
			c.JSON(200, map[string]any{
				"scanning": globals.IsScanning,
			})
		})

		server.Get("/stats", func(c *mate.Context) {
			processed := db.Select("processed")

			c.JSON(200, map[string]any{
				"total_processed_files":   len(processed),
				"total_unprocessed_files": len(getAllVideoFiles(globals.Dir)) - len(processed),
				"processed_files":         processed,
			})
		})

		server.Get("/metadata", func(c *mate.Context) {
			videoFiles := getAllVideoFiles(globals.Dir)

			c.JSON(200, videoFiles)
		})

		server.Post("/process/{file}/{targetLang}", func(c *mate.Context) {
			file := c.GetPathValue("file")
			targetLang := c.GetPathValue("targetLang")

			if targetLang == "" {
				targetLang = globals.TargetLang
			}

			filePath := ""
			for _, vf := range getAllVideoFiles(globals.Dir) {
				if filepath.Base(vf) == file {
					filePath = vf
					break
				}
			}

			if filePath == "" {
				c.JSON(404, map[string]any{
					"error": "File not found",
				})

				return
			}

			if err := processor.ProcessFile(filePath, targetLang); err != nil {
				c.JSON(500, map[string]any{
					"error": err.Error(),
				})
				return
			}

			c.JSON(200, map[string]any{
				"message": "File processed successfully",
			})
		})

		server.Post("/process", func(c *mate.Context) {
			go startPeriodicScanning(db, globals.Dir)

			c.JSON(200, map[string]any{
				"message": "Started periodic scanning",
			})
		})

		fmt.Println(globals.ColorBlue + "🧉 Subtitle Translator API running on port " + globals.Port + globals.ColorReset)

		go startPeriodicScanning(db, globals.Dir)

		server.Start(globals.Port)
	} else {
		if globals.File != "" {
			processFile(db, globals.File, globals.TargetLang)

			return
		}

		if globals.Dir != "" {
			go startPeriodicScanning(db, globals.Dir)
		}
	}
}

func checkDependencies() error {
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg is not installed")
	}
	return nil
}

func startPeriodicScanning(db *database.DB, dir string) {
	if globals.Dir == "" {
		return
	}

	fmt.Printf(globals.ColorGreen+"Starting periodic scan of: %s\n"+globals.ColorReset, dir)
	fmt.Printf(globals.ColorBlue+"Scan interval: %d minutes\n"+globals.ColorReset, globals.ScanInterval)
	fmt.Printf(globals.ColorBlue+"Target language: %s\n"+globals.ColorReset, globals.TargetLang)
	fmt.Println(globals.ColorYellow + "Press Ctrl+C to stop..." + globals.ColorReset)
	fmt.Println()

	processDirectorySafe(db, dir)

	ticker := time.NewTicker(time.Duration(globals.ScanInterval) * time.Minute)
	defer ticker.Stop()

	for range ticker.C {
		processDirectorySafe(db, dir)
	}
}

func processDirectorySafe(db *database.DB, dir string) {
	globals.ScanningMutex.Lock()

	if globals.IsScanning {
		fmt.Println(globals.ColorYellow + "⚠ Previous scan still in progress, skipping this cycle" + globals.ColorReset)
		globals.ScanningMutex.Unlock()
		return
	}

	globals.IsScanning = true
	globals.ScanningMutex.Unlock()

	defer func() {
		globals.ScanningMutex.Lock()
		globals.IsScanning = false
		globals.ScanningMutex.Unlock()
	}()

	processDirectory(db, dir)
}

func processDirectory(db *database.DB, dir string) {
	startTime := time.Now()
	fmt.Printf(globals.ColorBlue+"\n=== Starting scan at %s ===\n"+globals.ColorReset, startTime.Format("15:04:05"))

	videoFiles := getAllVideoFiles(dir)

	if len(videoFiles) == 0 {
		fmt.Println(globals.ColorYellow + "No video files found" + globals.ColorReset)
		return
	}

	fmt.Printf(globals.ColorBlue+"Found %d video files\n"+globals.ColorReset, len(videoFiles))
	var videosToProcess []string

	for _, videoPath := range videoFiles {
		if !shouldProcessVideo(db, videoPath) {
			continue
		}

		videosToProcess = append(videosToProcess, videoPath)
	}

	if len(videosToProcess) == 0 {
		fmt.Println(globals.ColorGreen + "✓ All videos are up to date" + globals.ColorReset)
		return
	}

	fmt.Printf(globals.ColorBlue+"Processing %d videos...\n"+globals.ColorReset, len(videosToProcess))

	for _, videoPath := range videosToProcess {
		if err := processFile(db, videoPath, globals.TargetLang); err != nil {
			log.Printf(globals.ColorRed+"Error processing file %s: %v"+globals.ColorReset, videoPath, err)
			continue
		}
	}

	duration := time.Since(startTime)
	fmt.Printf(globals.ColorGreen+"\n=== Scan completed in %s ===\n"+globals.ColorReset, duration.Round(time.Second))
}

type ProcessedFile struct {
	Id       string `json:"id"`
	Language string `json:"language"`
	Status   string `json:"status"`
	Retries  int    `json:"retries"`
}

func getAllVideoFiles(dir string) []string {
	var videoFiles []string

	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			if globals.Verbose {
				log.Printf(globals.ColorYellow+"Error accessing path %s: %v"+globals.ColorReset, path, err)
			}
			return nil
		}

		if !info.IsDir() && isVideoFile(path) {
			videoFiles = append(videoFiles, path)
		}

		return nil
	})

	if err != nil {
		log.Printf(globals.ColorRed+"Error walking directory: %v"+globals.ColorReset, err)
	}

	return videoFiles
}

func isVideoFile(filename string) bool {
	ext := strings.ToLower(filepath.Ext(filename))
	for _, validExt := range globals.VideoExts {
		if ext == validExt {
			return true
		}
	}
	return false
}

func shouldProcessVideo(db *database.DB, id string) bool {
	process, found := database.SelectByIDTyped[ProcessedFile](db, "processed", id)

	if !found {
		return true
	}

	if process.Status == "completed" {
		return false
	}

	if process.Status == "error" && process.Retries >= 3 {
		if globals.Verbose {
			log.Printf(globals.ColorYellow+"Max retries reached for file %s, skipping"+globals.ColorReset, id)
		}

		return false
	}

	return true
}

func processFile(db *database.DB, file, targetLang string) error {
	if err := processor.ProcessFile(file, targetLang); err != nil {
		updateDatabase(db, file, targetLang, "error")

		fmt.Printf(globals.ColorRed+"✗ Error processing: %s - %v"+globals.ColorReset+"\n", filepath.Base(file), err)

		return err
	}

	updateDatabase(db, file, targetLang, "completed")

	fmt.Printf(globals.ColorGreen+"✓ Processed: %s"+globals.ColorReset+"\n", filepath.Base(file))

	return nil
}

func updateDatabase(db *database.DB, file, targetLang, status string) {
	database.UpsertWhereTyped(db, "processed", func(i ProcessedFile) bool {
		return i.Id == file
	}, ProcessedFile{
		Id:       file,
		Language: strings.ToLower(targetLang),
		Status:   status,
		Retries:  0,
	},
		func(existing ProcessedFile) ProcessedFile {
			existing.Status = status

			if status == "completed" {
				existing.Retries = 0
			} else {
				existing.Retries++
			}

			return existing
		})
}
