package main

import (
	"bufio"
	_ "embed"
	"encoding/json"
	"flag"
	"fmt"
	"html/template"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

var (
	version = "dev" // This will be overridden at build time via ldflags

	// Pre-compiled regex patterns for extracting video IDs from TikTok URLs
	videoIDPatterns = []*regexp.Regexp{
		regexp.MustCompile(`/video/(\d+)`),
		regexp.MustCompile(`/v/(\d+)`),
	}
)

// VideoEntry represents a video with its collection information and metadata
type VideoEntry struct {
	// From TikTok JSON export
	Link       string `json:"link"`
	Date       string `json:"favorited_date"` // When user favorited/liked
	Collection string `json:"collection"`     // "favorites" or "liked"

	// Derived from URL
	VideoID string `json:"video_id"`

	// From yt-dlp metadata (populated after download)
	Title         string `json:"title,omitempty"`
	Creator       string `json:"creator,omitempty"`
	CreatorID     string `json:"creator_id,omitempty"`
	UploadDate    string `json:"upload_date,omitempty"`
	Description   string `json:"description,omitempty"`
	Duration      int    `json:"duration,omitempty"`
	ViewCount     int64  `json:"view_count,omitempty"`
	LikeCount     int64  `json:"like_count,omitempty"`
	ThumbnailURL  string `json:"thumbnail_url,omitempty"`
	ThumbnailFile string `json:"thumbnail_file,omitempty"`

	// Download status
	Downloaded    bool   `json:"downloaded"`
	LocalFilename string `json:"local_filename,omitempty"`
	DownloadError string `json:"download_error,omitempty"`
}

// YtdlpInfo represents relevant fields from yt-dlp's .info.json files
type YtdlpInfo struct {
	ID          string `json:"id"`
	Title       string `json:"title"`
	Uploader    string `json:"uploader"`
	UploaderID  string `json:"uploader_id"`
	UploadDate  string `json:"upload_date"`
	Description string `json:"description"`
	Duration    int    `json:"duration"`
	ViewCount   int64  `json:"view_count"`
	LikeCount   int64  `json:"like_count"`
	Thumbnail   string `json:"thumbnail"`
	Filename    string `json:"filename"`
}

// CollectionIndex represents the complete index for a collection
type CollectionIndex struct {
	Name        string       `json:"name"`
	GeneratedAt string       `json:"generated_at"`
	TotalVideos int          `json:"total_videos"`
	Downloaded  int          `json:"downloaded"`
	Failed      int          `json:"failed"`
	Videos      []VideoEntry `json:"videos"`
}

// Data represents the structure of user_data_tiktok.json
type Data struct {
	Activity struct {
		FavoriteVideos struct {
			FavoriteVideoList []struct {
				Link string `json:"Link"`
				Date string `json:"Date"` // Favorited date from TikTok export
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
	IncludeLiked         bool
	SkipThumbnails       bool
	IndexOnly            bool
	JSONFile             string
	OutputName           string
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
	defer func() { _ = resp.Body.Close() }()

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
	defer func() { _ = out.Close() }()

	downloadResp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("[!!!] failed to download %s: %v", exeName, err)
	}
	defer func() { _ = downloadResp.Body.Close() }()

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
	defer func() { _ = file.Close() }()

	var data Data
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		return nil, fmt.Errorf("error parsing JSON: %v", err)
	}

	videoEntries := make([]VideoEntry, 0)

	// Always add favorited videos
	for _, item := range data.Activity.FavoriteVideos.FavoriteVideoList {
		videoEntries = append(videoEntries, VideoEntry{
			Link:       item.Link,
			Date:       item.Date,
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

// extractVideoID extracts the video ID from a TikTok URL.
// Supports various TikTok URL formats:
//   - https://www.tiktokv.com/share/video/7600559584901647646/
//   - https://www.tiktok.com/@user/video/7600559584901647646
//   - https://m.tiktok.com/v/7600559584901647646.html
func extractVideoID(url string) string {
	for _, re := range videoIDPatterns {
		if matches := re.FindStringSubmatch(url); len(matches) > 1 {
			return matches[1]
		}
	}
	return ""
}

// parseInfoJSON reads a yt-dlp .info.json file and extracts metadata
func parseInfoJSON(infoPath string) (*YtdlpInfo, error) {
	data, err := os.ReadFile(infoPath)
	if err != nil {
		return nil, err
	}
	var info YtdlpInfo
	if err := json.Unmarshal(data, &info); err != nil {
		return nil, err
	}
	return &info, nil
}

// getOutputFilename returns the appropriate URL list filename for a collection
func getOutputFilename(collection string) string {
	if collection == "liked" {
		return "liked_videos.txt"
	}
	return "fav_videos.txt"
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

		// Write separate files for each collection with collection-specific filenames
		for collection, entries := range collectionGroups {
			// Use collection-specific filename (fav_videos.txt for favorites, liked_videos.txt for liked)
			collectionFilename := getOutputFilename(collection)
			collectionOutputName := filepath.Join(collection, collectionFilename)
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
	defer func() { _ = outFile.Close() }()

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
func runYtdlp(psPrefix, outputName string, organizeByCollection, skipThumbnails bool) {
	runYtdlpWithRunner(&RealCommandRunner{}, psPrefix, outputName, organizeByCollection, skipThumbnails)
}

// runYtdlpWithRunner allows dependency injection for testing
func runYtdlpWithRunner(runner CommandRunner, psPrefix, outputName string, organizeByCollection, skipThumbnails bool) {
	fmt.Println("[*] Running yt-dlp now...")
	cmdStr := fmt.Sprintf("%syt-dlp.exe", psPrefix)

	// Configure output format based on organization preference
	// New format includes video ID and truncated title for better identification
	var outputFormat string
	if organizeByCollection {
		// Include directory from outputName so videos download to collection folder
		dir := filepath.Dir(outputName)
		outputFormat = filepath.Join(dir, "%(upload_date)s_%(id)s_%(title).50B.%(ext)s")
	} else {
		// Flat structure with new format
		outputFormat = "%(upload_date)s_%(id)s_%(title).50B.%(ext)s"
	}

	// Build yt-dlp arguments with metadata options
	args := []string{
		"-a", outputName,
		"--output", outputFormat,
		"--write-info-json", // Save metadata JSON for each video
	}

	// Add thumbnail download unless skipped
	if !skipThumbnails {
		args = append(args, "--write-thumbnail")
	}

	if err := runner.Run(cmdStr, args...); err != nil {
		fmt.Printf("[!!!] Error running yt-dlp: %v\n", err)
	} else {
		fmt.Println("[*] yt-dlp completed successfully.")
	}
}

// HTML template for the visual index browser
//
//go:embed templates/index.html
var htmlTemplate string

// getTemplateFuncs returns template helper functions
func getTemplateFuncs() template.FuncMap {
	return template.FuncMap{
		"formatDuration": func(seconds int) string {
			m := seconds / 60
			s := seconds % 60
			return fmt.Sprintf("%d:%02d", m, s)
		},
		"formatNumber": func(n int64) string {
			if n >= 1000000 {
				return fmt.Sprintf("%.1fM", float64(n)/1000000)
			}
			if n >= 1000 {
				return fmt.Sprintf("%.1fK", float64(n)/1000)
			}
			return fmt.Sprintf("%d", n)
		},
	}
}

// writeJSONIndex writes the collection index as JSON
func writeJSONIndex(dir string, index *CollectionIndex) error {
	data, err := json.MarshalIndent(index, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "index.json"), data, 0644)
}

// writeHTMLIndex generates the HTML visual browser
func writeHTMLIndex(dir string, index *CollectionIndex) error {
	tmpl, err := template.New("index").Funcs(getTemplateFuncs()).Parse(htmlTemplate)
	if err != nil {
		return err
	}

	f, err := os.Create(filepath.Join(dir, "index.html"))
	if err != nil {
		return err
	}
	defer func() { _ = f.Close() }()

	return tmpl.Execute(f, index)
}

// generateCollectionIndex creates JSON and HTML indexes for a collection after download.
// It enriches entries with metadata from yt-dlp's .info.json files and generates
// both index.json (machine-readable) and index.html (visual browser) files.
func generateCollectionIndex(collectionDir string, entries []VideoEntry) error {
	// 1. Scan for .info.json files in the directory
	infoFiles, err := filepath.Glob(filepath.Join(collectionDir, "*.info.json"))
	if err != nil {
		return fmt.Errorf("error scanning for info files: %v", err)
	}

	// 2. Build video ID to info map
	infoMap := make(map[string]*YtdlpInfo)
	for _, f := range infoFiles {
		info, err := parseInfoJSON(f)
		if err != nil {
			fmt.Printf("[!] Warning: Failed to parse %s: %v\n", f, err)
			continue
		}
		infoMap[info.ID] = info
	}

	// 3. Create a copy of entries to avoid mutating the input slice
	enrichedEntries := make([]VideoEntry, len(entries))
	copy(enrichedEntries, entries)

	// 4. Enrich entries with metadata
	for i := range enrichedEntries {
		videoID := extractVideoID(enrichedEntries[i].Link)
		enrichedEntries[i].VideoID = videoID

		if info, ok := infoMap[videoID]; ok {
			enrichedEntries[i].Title = info.Title
			enrichedEntries[i].Creator = info.Uploader
			enrichedEntries[i].CreatorID = info.UploaderID
			enrichedEntries[i].UploadDate = info.UploadDate
			enrichedEntries[i].Description = info.Description
			enrichedEntries[i].Duration = info.Duration
			enrichedEntries[i].ViewCount = info.ViewCount
			enrichedEntries[i].LikeCount = info.LikeCount
			enrichedEntries[i].ThumbnailURL = info.Thumbnail
			enrichedEntries[i].Downloaded = true

			// Determine the local filename from the info
			if info.Filename != "" {
				enrichedEntries[i].LocalFilename = filepath.Base(info.Filename)
			}

			// Check for thumbnail file (try common extensions)
			baseWithoutExt := strings.TrimSuffix(info.Filename, filepath.Ext(info.Filename))
			for _, ext := range []string{".jpg", ".webp", ".png"} {
				thumbPath := baseWithoutExt + ext
				if _, err := os.Stat(filepath.Join(collectionDir, filepath.Base(thumbPath))); err == nil {
					enrichedEntries[i].ThumbnailFile = filepath.Base(thumbPath)
					break
				}
			}
		} else {
			enrichedEntries[i].Downloaded = false
			enrichedEntries[i].DownloadError = "Video not downloaded or metadata unavailable"
		}
	}

	// 5. Create index struct
	index := CollectionIndex{
		Name:        filepath.Base(collectionDir),
		GeneratedAt: time.Now().Format("2006-01-02 15:04:05"),
		TotalVideos: len(enrichedEntries),
		Videos:      enrichedEntries,
	}

	// Count downloaded/failed
	for _, e := range enrichedEntries {
		if e.Downloaded {
			index.Downloaded++
		} else {
			index.Failed++
		}
	}

	// 5. Write JSON index
	if err := writeJSONIndex(collectionDir, &index); err != nil {
		return fmt.Errorf("error writing JSON index: %v", err)
	}

	// 6. Generate HTML index
	if err := writeHTMLIndex(collectionDir, &index); err != nil {
		return fmt.Errorf("error writing HTML index: %v", err)
	}

	return nil
}

// getEntriesForCollection filters video entries for a specific collection
func getEntriesForCollection(entries []VideoEntry, collection string) []VideoEntry {
	var result []VideoEntry
	for _, e := range entries {
		if sanitizeCollectionName(e.Collection) == collection {
			result = append(result, e)
		}
	}
	return result
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
		OutputName:           "fav_videos.txt",
	}

	flatStructure := flag.Bool("flat-structure", false, "Disable collection organization (use flat directory structure)")
	noThumbnails := flag.Bool("no-thumbnails", false, "Skip thumbnail download (faster, less storage)")
	indexOnly := flag.Bool("index-only", false, "Regenerate indexes from existing .info.json files without downloading")
	help := flag.Bool("help", false, "Show help message")
	h := flag.Bool("h", false, "Show help message")

	flag.Parse()

	if *help || *h {
		printUsage()
		os.Exit(0)
	}

	config.OrganizeByCollection = !*flatStructure
	config.SkipThumbnails = *noThumbnails
	config.IndexOnly = *indexOnly

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
	fmt.Println("  --no-thumbnails      Skip thumbnail download (faster, less storage)")
	fmt.Println("  --index-only         Regenerate indexes from existing .info.json files")
	fmt.Println("  --help, -h           Show this help message")
	fmt.Println("\nExamples:")
	fmt.Println("  1) Double-click (no arguments) if 'user_data_tiktok.json' is in the same folder.")
	fmt.Printf("  2) Or drag & drop a JSON file onto '%s' to specify a different JSON file.\n", exeName)
	fmt.Printf("  3) Or run from command line: %s path\\to\\my_tiktok_data.json\n", exeName)
	fmt.Printf("  4) Use flat structure: %s --flat-structure\n", exeName)
	fmt.Printf("  5) Skip thumbnails: %s --no-thumbnails\n", exeName)
	fmt.Printf("  6) Regenerate index only: %s --index-only\n", exeName)
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

	// Handle --index-only mode: regenerate indexes without downloading
	if config.IndexOnly {
		fmt.Println("[*] Index-only mode: regenerating indexes from existing .info.json files")

		// Still need to ask about liked videos to know which collections to process
		fmt.Print("[*] Would you like to include 'Liked' videos as well? (y/n, default is 'n'): ")
		scanner := bufio.NewScanner(os.Stdin)
		scanner.Scan()
		input := strings.TrimSpace(strings.ToLower(scanner.Text()))
		if input == "y" || input == "yes" {
			config.IncludeLiked = true
		}

		// Parse JSON to get video entries
		videoEntries, err := parseFavoriteVideosFromFile(config.JSONFile, config.IncludeLiked)
		if err != nil {
			fmt.Printf("[!!!] Error parsing JSON: %v\n", err)
			os.Exit(1)
		}

		fmt.Printf("[*] Loaded %d video entries from '%s'\n", len(videoEntries), config.JSONFile)

		if config.OrganizeByCollection {
			// Regenerate indexes for each collection
			collections := make(map[string]bool)
			for _, entry := range videoEntries {
				collections[sanitizeCollectionName(entry.Collection)] = true
			}
			for collection := range collections {
				collectionEntries := getEntriesForCollection(videoEntries, collection)
				if err := generateCollectionIndex(collection, collectionEntries); err != nil {
					fmt.Printf("[!] Warning: Failed to generate index for %s: %v\n", collection, err)
				} else {
					fmt.Printf("[*] Generated index.html and index.json for %s\n", collection)
				}
			}
		} else {
			// Regenerate index for flat structure
			dir, err := filepath.Abs(".")
			if err != nil {
				dir = "."
			}
			if err := generateCollectionIndex(dir, videoEntries); err != nil {
				fmt.Printf("[!] Warning: Failed to generate index: %v\n", err)
			} else {
				fmt.Println("[*] Generated index.html and index.json")
			}
		}
		return
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
		ytDlpCmd := fmt.Sprintf("%syt-dlp.exe -a \"%s\" --output \"%%(upload_date)s_%%(id)s_%%(title).50B.%%(ext)s\" --write-info-json --write-thumbnail", psPrefix, config.OutputName)
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
				// Use collection-specific filename
				collectionFilename := getOutputFilename(collection)
				collectionOutputName := filepath.Join(collection, collectionFilename)
				fmt.Printf("[*] Processing collection: %s\n", collection)
				runYtdlp(psPrefix, collectionOutputName, config.OrganizeByCollection, config.SkipThumbnails)

				// Generate index after download completes
				collectionEntries := getEntriesForCollection(videoEntries, collection)
				if err := generateCollectionIndex(collection, collectionEntries); err != nil {
					fmt.Printf("[!] Warning: Failed to generate index for %s: %v\n", collection, err)
				} else {
					fmt.Printf("[*] Generated index.html and index.json for %s\n", collection)
				}
			}
		} else {
			runYtdlp(psPrefix, config.OutputName, config.OrganizeByCollection, config.SkipThumbnails)

			// Generate index for flat structure in current directory
			dir, err := filepath.Abs(".")
			if err != nil {
				dir = "."
			}
			if err := generateCollectionIndex(dir, videoEntries); err != nil {
				fmt.Printf("[!] Warning: Failed to generate index: %v\n", err)
			} else {
				fmt.Println("[*] Generated index.html and index.json")
			}
		}
	}
}
