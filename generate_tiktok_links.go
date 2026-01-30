package main

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

var (
	version = "dev" // This will be overridden at build time via ldflags
)

// VideoEntry represents a video with its collection information
type VideoEntry struct {
	Link       string
	Date       string
	Collection string
}

// Data represents the structure of user_data_tiktok.json
type Data struct {
	Activity struct {
		FavoriteVideos struct {
			FavoriteVideoList []struct {
				Link string `json:"Link"`
			} `json:"FavoriteVideoList"`
		} `json:"Favorite Videos"`
		LikedVideos struct {
			ItemFavoriteList []struct {
				Date string `json:"date"`
				Link string `json:"link"`
			} `json:"ItemFavoriteList"`
		} `json:"Like List"`
	} `json:"Likes and Favorites"`
}

// Config holds the application configuration
type Config struct {
	OrganizeByCollection bool
	IncludeLiked        bool
	JSONFile           string
	OutputName         string
}

// getOrDownloadYtdlp checks if yt-dlp.exe is present in the current directory.
// If not, it downloads the latest version from GitHub. Accepts an *http.Client
// so we can mock the download in tests.
func getOrDownloadYtdlp(client *http.Client, exeName string) error {
	// Check if the file already exists
	if _, err := os.Stat(exeName); err == nil {
		fmt.Printf("[*] Found %s in the current directory. Skipping download.\n", exeName)
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("[!!!] error checking for existing %s: %v", exeName, err)
	}

	fmt.Printf("[*] %s not found. Downloading the latest release from GitHub...\n", exeName)

	// 1. Retrieve the latest release info from GitHub
	releaseURL := "https://api.github.com/repos/yt-dlp/yt-dlp/releases/latest"
	resp, err := client.Get(releaseURL)
	if err != nil {
		return fmt.Errorf("[!!!] failed to fetch the latest release info: %v", err)
	}
	defer resp.Body.Close()

	var release struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("[!!!] failed to parse GitHub API release JSON: %v", err)
	}

	// 2. Find the asset with name "yt-dlp.exe"
	var downloadURL string
	for _, asset := range release.Assets {
		if strings.EqualFold(asset.Name, exeName) {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("[!!!] could not find %s in the latest release assets", exeName)
	}

	fmt.Printf("[*] Downloading %s...\n", downloadURL)

	// 3. Download the file
	out, err := os.Create(exeName)
	if err != nil {
		return fmt.Errorf("[!!!] error creating %s: %v", exeName, err)
	}
	defer out.Close()

	downloadResp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("[!!!] failed to download %s: %v", exeName, err)
	}
	defer downloadResp.Body.Close()

	// 4. Copy the response body to the file
	if _, err := io.Copy(out, downloadResp.Body); err != nil {
		return fmt.Errorf("[!!!] failed to write %s to disk: %v", exeName, err)
	}

	fmt.Println("[*] Successfully downloaded yt-dlp")
	return nil
}

// parseFavoriteVideosFromFile reads the given JSON file and returns the list of video entries.
func parseFavoriteVideosFromFile(jsonFile string, includeLiked bool) ([]VideoEntry, error) {
	file, err := os.Open(filepath.Clean(jsonFile))
	if err != nil {
		return nil, fmt.Errorf("error opening JSON file: %v", err)
	}
	defer file.Close()

	var data Data
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		return nil, fmt.Errorf("error parsing JSON: %v", err)
	}

	videoEntries := make([]VideoEntry, 0)

	// Always add favorited videos
	for _, item := range data.Activity.FavoriteVideos.FavoriteVideoList {
		videoEntries = append(videoEntries, VideoEntry{
			Link:       item.Link,
			Date:       "", // Favorite videos don't have dates in the current structure
			Collection: "favorites",
		})
	}

	// Add liked videos if the user requested them
	if includeLiked {
		for _, item := range data.Activity.LikedVideos.ItemFavoriteList {
			videoEntries = append(videoEntries, VideoEntry{
				Link:       item.Link,
				Date:       item.Date,
				Collection: "liked",
			})
		}
	}

	return videoEntries, nil
}

// sanitizeCollectionName sanitizes collection names for use as directory names
func sanitizeCollectionName(name string) string {
	// Replace invalid characters with underscores
	invalid := []string{"<", ">", ":", "\"", "/", "\\", "|", "?", "*"}
	for _, char := range invalid {
		name = strings.ReplaceAll(name, char, "_")
	}
	// Trim spaces and dots
	name = strings.Trim(name, " .")
	if name == "" {
		name = "unknown"
	}
	return name
}

// createCollectionDirectories creates directories for each collection
func createCollectionDirectories(videoEntries []VideoEntry, organizeByCollection bool) error {
	if !organizeByCollection {
		return nil
	}

	collections := make(map[string]bool)
	for _, entry := range videoEntries {
		collections[sanitizeCollectionName(entry.Collection)] = true
	}

	for collection := range collections {
		if err := os.MkdirAll(collection, 0755); err != nil {
			return fmt.Errorf("[!!!] Error creating directory %s: %v", collection, err)
		}
	}
	return nil
}

// writeFavoriteVideosToFile writes the video entries to output files, organized by collection if enabled.
func writeFavoriteVideosToFile(videoEntries []VideoEntry, outputName string, organizeByCollection bool) error {
	if organizeByCollection {
		// Create collection directories first
		if err := createCollectionDirectories(videoEntries, true); err != nil {
			return err
		}

		// Group entries by collection
		collectionGroups := make(map[string][]VideoEntry)
		for _, entry := range videoEntries {
			collection := sanitizeCollectionName(entry.Collection)
			collectionGroups[collection] = append(collectionGroups[collection], entry)
		}

		// Write separate files for each collection
		for collection, entries := range collectionGroups {
			collectionOutputName := filepath.Join(collection, outputName)
			if err := writeVideoEntriesToFile(entries, collectionOutputName); err != nil {
				return err
			}
			fmt.Printf("[*] Extracted %d video URLs to '%s'\n", len(entries), collectionOutputName)
		}
	} else {
		// Write all entries to a single file (flat structure)
		return writeVideoEntriesToFile(videoEntries, outputName)
	}
	return nil
}

// writeVideoEntriesToFile writes video entries to a single file
func writeVideoEntriesToFile(videoEntries []VideoEntry, outputName string) error {
	outFile, err := os.Create(outputName)
	if err != nil {
		return fmt.Errorf("[!!!] Error creating %s: %v", outputName, err)
	}
	defer outFile.Close()

	for _, entry := range videoEntries {
		_, writeErr := outFile.WriteString(entry.Link + "\n")
		if writeErr != nil {
			return fmt.Errorf("[!!!] Error writing to %s: %v", outputName, writeErr)
		}
	}
	return nil
}

// isRunningInPowershell does a simple check to see if we're (likely) in PowerShell.
func isRunningInPowershell() bool {
	// A common environment variable set by PowerShell is PSModulePath,
	// often containing 'PowerShell' in its path. This is a heuristic.
	return strings.Contains(os.Getenv("PSModulePath"), "PowerShell")
}

// CommandRunner interface for testing command execution
type CommandRunner interface {
	Run(name string, args ...string) error
}

// RealCommandRunner implements CommandRunner using exec.Command
type RealCommandRunner struct{}

func (r *RealCommandRunner) Run(name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	return cmd.Run()
}

// runYtdlp runs the yt-dlp command for the user
func runYtdlp(psPrefix, outputName string, organizeByCollection bool) {
	runYtdlpWithRunner(&RealCommandRunner{}, psPrefix, outputName, organizeByCollection)
}

// runYtdlpWithRunner allows dependency injection for testing
func runYtdlpWithRunner(runner CommandRunner, psPrefix, outputName string, organizeByCollection bool) {
	fmt.Println("[*] Running yt-dlp now...")
	cmdStr := fmt.Sprintf("%syt-dlp.exe", psPrefix)

	// Configure output format based on organization preference
	var outputFormat string
	if organizeByCollection {
		// If organizing by collection, yt-dlp will find the files in subdirectories
		// Use a more descriptive filename format
		outputFormat = "%(upload_date)s_%(uploader_id)s.%(ext)s"
	} else {
		// Flat structure with original format
		outputFormat = "%(upload_date)s_%(uploader_id)s.%(ext)s"
	}

	if err := runner.Run(cmdStr, "-a", outputName, "--output", outputFormat); err != nil {
		fmt.Printf("[!!!] Error running yt-dlp: %v\n", err)
	} else {
		fmt.Println("[*] yt-dlp completed successfully.")
	}
}

func getExeName() string {
	exePath, err := os.Executable()
	if err != nil {
		// If we can’t get the path, default to a known name
		return "tiktok-favvideo-downloader.exe"
	}
	// Otherwise, return the filename (base) part of the path
	return filepath.Base(exePath)
}

// parseFlags parses command line flags and returns configuration
func parseFlags() *Config {
	config := &Config{
		OrganizeByCollection: true, // Default to organizing by collection
		OutputName:          "fav_videos.txt",
	}

	flatStructure := flag.Bool("flat-structure", false, "Disable collection organization (use flat directory structure)")
	help := flag.Bool("help", false, "Show help message")
	h := flag.Bool("h", false, "Show help message")

	flag.Parse()

	if *help || *h {
		printUsage()
		os.Exit(0)
	}

	config.OrganizeByCollection = !*flatStructure

	// Handle positional argument for JSON file
	args := flag.Args()
	if len(args) > 0 {
		config.JSONFile = args[0]
	} else {
		config.JSONFile = "user_data_tiktok.json"
	}

	return config
}

// printUsage prints basic usage info for this program.
func printUsage() {
	exeName := getExeName()

	fmt.Println("\nUsage:")
	fmt.Printf("  %s [flags] [optional path to user_data_tiktok.json]\n", exeName)
	fmt.Println("\nFlags:")
	fmt.Println("  --flat-structure     Disable collection organization (use flat directory structure)")
	fmt.Println("  --help, -h          Show this help message")
	fmt.Println("\nExamples:")
	fmt.Println("  1) Double-click (no arguments) if 'user_data_tiktok.json' is in the same folder.")
	fmt.Printf("  2) Or drag & drop a JSON file onto '%s' to specify a different JSON file.\n", exeName)
	fmt.Printf("  3) Or run from command line: %s path\\to\\my_tiktok_data.json\n", exeName)
	fmt.Printf("  4) Use flat structure: %s --flat-structure\n", exeName)
	fmt.Println("\nCollection Organization (Default):")
	fmt.Println("  Videos are organized into subdirectories by collection type:")
	fmt.Println("    favorites/    - Your favorited videos")
	fmt.Println("    liked/        - Your liked videos")
	fmt.Println("\nHow do I even use this thing?")
	fmt.Println("  1. Go to https://www.tiktok.com/setting")
	fmt.Println("  2. Under Privacy, Data, click on \"Download your data\"")
	fmt.Println("  3. Select \"JSON\" & \"All Available Data\", then hit Request Data")
	fmt.Println("  4. Wait for data to be generated, can take 5-15min, hit refresh every once in a while")
	fmt.Println("  5. Download and extract the JSON file into same directory as this executable")
	fmt.Printf("  6. Run %s\n\n", exeName)
}

func main() {
	fmt.Printf("[*] TikTok Favorite Videos Extractor (Version %s)\n", version)

	// Parse command line flags
	config := parseFlags()

	// Check if JSON file exists before proceeding
	if _, err := os.Stat(config.JSONFile); os.IsNotExist(err) {
		fmt.Printf("[!!!] Error: JSON file '%s' does not exist.\n", config.JSONFile)
		printUsage()
		os.Exit(1)
	}

	// Attempt to get or download yt-dlp.exe
	if err := getOrDownloadYtdlp(http.DefaultClient, "yt-dlp.exe"); err != nil {
		fmt.Printf("[!] Warning: %v\n", err)
		// Not exiting here so you can still generate fav_videos.txt if needed
	}

	fmt.Print("[*] Would you like to include 'Liked' videos as well? (y/n, default is 'n'): ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	input := strings.TrimSpace(strings.ToLower(scanner.Text()))
	// Update includeLiked to true if the input is "y"
	if input == "y" || input == "yes" {
		config.IncludeLiked = true
	}

	// Extract video entries
	videoEntries, err := parseFavoriteVideosFromFile(config.JSONFile, config.IncludeLiked)
	if err != nil {
		fmt.Printf("[!!!] Error parsing JSON. Are you sure '%s' is valid JSON?\n", config.JSONFile)
		fmt.Printf("Details: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[*] Successfully loaded %d video entries from '%s'\n", len(videoEntries), config.JSONFile)

	// Write video entries to files
	if err := writeFavoriteVideosToFile(videoEntries, config.OutputName, config.OrganizeByCollection); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}

	if !config.OrganizeByCollection {
		fmt.Printf("[*] Extracted %d video URLs to '%s'.\n", len(videoEntries), config.OutputName)
	}

	// Construct the recommended yt-dlp command
	psPrefix := ""
	if isRunningInPowershell() {
		psPrefix = ".\\"
	}

	if config.OrganizeByCollection {
		fmt.Println("[*] Collection organization enabled. Videos will be downloaded to collection subdirectories.")
		fmt.Println("[*] yt-dlp will process each collection's URL file separately.")
	} else {
		ytDlpCmd := fmt.Sprintf("%syt-dlp.exe -a \"%s\" --output \"%%(upload_date)s_%%(uploader_id)s.%%(ext)s\"", psPrefix, config.OutputName)
		fmt.Println("[*] Done! You can now run yt-dlp like this:")
		fmt.Printf("  %s\n", ytDlpCmd)
	}

	// Offer to run the command automatically
	fmt.Print("\n*** Would you like me to run yt-dlp for you instead? (y/n): ")
	answer := bufio.NewReader(os.Stdin)
	response, _ := answer.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))
	if response == "y" || response == "yes" {
		if config.OrganizeByCollection {
			// Run yt-dlp for each collection
			collections := make(map[string]bool)
			for _, entry := range videoEntries {
				collections[sanitizeCollectionName(entry.Collection)] = true
			}
			for collection := range collections {
				collectionOutputName := filepath.Join(collection, config.OutputName)
				fmt.Printf("[*] Processing collection: %s\n", collection)
				runYtdlp(psPrefix, collectionOutputName, config.OrganizeByCollection)
			}
		} else {
			runYtdlp(psPrefix, config.OutputName, config.OrganizeByCollection)
		}
	}
}
