package main

import (
	"bufio"
	"bytes"
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
	"strconv"
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

// CapturedOutput stores stdout and stderr from yt-dlp
type CapturedOutput struct {
	Stdout   bytes.Buffer
	Stderr   bytes.Buffer
	Combined []string // Line-by-line for parsing
}

// DownloadSession tracks results across all collections
type DownloadSession struct {
	StartTime      time.Time
	EndTime        time.Time
	Collections    []CollectionResult
	TotalAttempted int
	TotalSuccess   int
	TotalFailed    int
}

// CollectionResult tracks results for a single collection
type CollectionResult struct {
	Name           string
	Attempted      int
	Success        int
	Failed         int
	FailureDetails []FailureDetail
}

// FailureDetail contains information about a failed download
type FailureDetail struct {
	VideoID      string
	VideoURL     string
	ErrorMessage string
	ErrorType    ErrorType
}

// ErrorType categorizes common error types
type ErrorType int

const (
	ErrorUnknown ErrorType = iota
	ErrorIPBlocked
	ErrorAuthRequired
	ErrorNotAvailable
	ErrorNetworkTimeout
	ErrorOther
)

// String returns a human-readable description of the error type
func (e ErrorType) String() string {
	switch e {
	case ErrorIPBlocked:
		return "IP Blocked"
	case ErrorAuthRequired:
		return "Authentication Required"
	case ErrorNotAvailable:
		return "Not Available"
	case ErrorNetworkTimeout:
		return "Network Timeout"
	default:
		return "Other Error"
	}
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

// ProgressState tracks real-time download progress for display
type ProgressState struct {
	CollectionName string
	CurrentIndex   int
	TotalVideos    int
	SuccessCount   int
	FailureCount   int
}

// ProgressRenderer handles ANSI-based progress display
type ProgressRenderer struct {
	enabled     bool // false if terminal doesn't support ANSI or user disabled it
	lastLineLen int  // track last line length for proper clearing
}

// Config holds the application configuration
type Config struct {
	OrganizeByCollection bool
	IncludeLiked         bool
	SkipThumbnails       bool
	IndexOnly            bool
	DisableResume        bool // Disable resume functionality (force re-download all videos)
	DisableProgressBar   bool // Disable progress bar (use traditional line-by-line output)
	JSONFile             string
	OutputName           string
	CookieFile           string // Path to Netscape cookies.txt file
	CookieFromBrowser    string // Browser name (chrome, firefox, edge, safari, etc.)
}

// isFileOlderThan30Days checks if a file's modification time is more than 30 days old
func isFileOlderThan30Days(path string) (bool, error) {
	info, err := os.Stat(path)
	if err != nil {
		return false, err
	}

	modTime := info.ModTime()
	thirtyDaysAgo := time.Now().AddDate(0, 0, -30)

	return modTime.Before(thirtyDaysAgo), nil
}

// promptForUpdate asks the user if they want to update yt-dlp.exe
// Returns true if user wants to update (default is yes)
func promptForUpdate() bool {
	fmt.Print("[*] A newer version of yt-dlp may be available. Would you like to download it? (Y/n, default is 'Y'): ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	input := strings.TrimSpace(strings.ToLower(scanner.Text()))

	// Default to yes if input is empty or explicitly yes
	if input == "" || input == "y" || input == "yes" {
		return true
	}

	return false
}

// backupYtdlp backs up the current yt-dlp.exe to yt-dlp.exe.old
// Deletes existing .old file if it exists
func backupYtdlp(exeName string) error {
	oldFileName := exeName + ".old"

	// Delete existing .old file if it exists
	if _, err := os.Stat(oldFileName); err == nil {
		fmt.Printf("[*] Removing old backup file: %s\n", oldFileName)
		if err := os.Remove(oldFileName); err != nil {
			return fmt.Errorf("failed to delete existing %s: %v", oldFileName, err)
		}
	}

	// Rename current exe to .old
	fmt.Printf("[*] Backing up current %s to %s\n", exeName, oldFileName)
	if err := os.Rename(exeName, oldFileName); err != nil {
		return fmt.Errorf("failed to rename %s to %s: %v", exeName, oldFileName, err)
	}

	return nil
}

// downloadLatestYtdlp downloads the latest version of yt-dlp from GitHub
func downloadLatestYtdlp(client *http.Client, exeName string) error {
	fmt.Printf("[*] Downloading the latest release from GitHub...\n")

	// 1. Retrieve the latest release info from GitHub
	releaseURL := "https://api.github.com/repos/yt-dlp/yt-dlp/releases/latest"
	resp, err := client.Get(releaseURL)
	if err != nil {
		return fmt.Errorf("failed to fetch the latest release info: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	var release struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return fmt.Errorf("failed to parse GitHub API release JSON: %v", err)
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
		return fmt.Errorf("could not find %s in the latest release assets", exeName)
	}

	fmt.Printf("[*] Downloading %s...\n", downloadURL)

	// 3. Download the file
	out, err := os.Create(exeName)
	if err != nil {
		return fmt.Errorf("error creating %s: %v", exeName, err)
	}
	defer func() { _ = out.Close() }()

	downloadResp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download %s: %v", exeName, err)
	}
	defer func() { _ = downloadResp.Body.Close() }()

	// 4. Copy the response body to the file
	if _, err := io.Copy(out, downloadResp.Body); err != nil {
		return fmt.Errorf("failed to write %s to disk: %v", exeName, err)
	}

	fmt.Println("[*] Successfully downloaded yt-dlp")
	return nil
}

// getOrDownloadYtdlp checks if yt-dlp.exe is present in the current directory.
// If not, it downloads the latest version from GitHub.
// If it exists but is older than 30 days, prompts user to update.
// Accepts an *http.Client so we can mock the download in tests.
func getOrDownloadYtdlp(client *http.Client, exeName string) error {
	// Check if the file already exists
	if _, err := os.Stat(exeName); err == nil {
		// File exists - check if it's older than 30 days
		isOld, err := isFileOlderThan30Days(exeName)
		if err != nil {
			fmt.Printf("[!] Warning: Could not check file age: %v\n", err)
			fmt.Printf("[*] Found %s in the current directory. Continuing with existing version.\n", exeName)
			return nil
		}

		if isOld {
			// Prompt user for update
			if promptForUpdate() {
				// User wants to update - backup current version
				if err := backupYtdlp(exeName); err != nil {
					return fmt.Errorf("backup failed: %v", err)
				}

				// Download new version
				if err := downloadLatestYtdlp(client, exeName); err != nil {
					// Download failed - try to restore backup
					fmt.Printf("[!] Download failed: %v\n", err)
					fmt.Printf("[*] Attempting to restore backup...\n")
					if restoreErr := os.Rename(exeName+".old", exeName); restoreErr != nil {
						return fmt.Errorf("download failed and could not restore backup: %v (restore error: %v)", err, restoreErr)
					}
					fmt.Printf("[*] Backup restored. Continuing with existing version.\n")
					return nil
				}
			} else {
				fmt.Printf("[*] Continuing with existing %s.\n", exeName)
			}
		} else {
			fmt.Printf("[*] Found %s in the current directory. Skipping download.\n", exeName)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("error checking for existing %s: %v", exeName, err)
	}

	// File doesn't exist - download it
	fmt.Printf("[*] %s not found. Downloading the latest release from GitHub...\n", exeName)
	return downloadLatestYtdlp(client, exeName)
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

// parseArchiveFile reads yt-dlp's download archive file and returns
// a set of video IDs that have been successfully downloaded.
// Archive format: "tiktok <video_id>" per line
// Returns empty map (not error) if file doesn't exist - this is normal for first run.
func parseArchiveFile(archivePath string) (map[string]bool, error) {
	// Check if archive exists
	if _, err := os.Stat(archivePath); os.IsNotExist(err) {
		return make(map[string]bool), nil // Empty archive, not an error
	}

	file, err := os.Open(archivePath)
	if err != nil {
		return nil, fmt.Errorf("failed to open archive file %s: %v", archivePath, err)
	}
	defer file.Close()

	archive := make(map[string]bool)
	scanner := bufio.NewScanner(file)
	lineNum := 0

	for scanner.Scan() {
		lineNum++
		line := strings.TrimSpace(scanner.Text())

		// Skip empty lines
		if line == "" {
			continue
		}

		// Parse "tiktok <video_id>" format
		parts := strings.Fields(line)
		if len(parts) != 2 {
			fmt.Printf("[!] Warning: Malformed archive line %d in %s: %s\n",
				lineNum, archivePath, line)
			continue
		}

		if parts[0] != "tiktok" {
			fmt.Printf("[!] Warning: Unknown platform %s at line %d in %s\n",
				parts[0], lineNum, archivePath)
			continue
		}

		videoID := parts[1]

		// Basic validation: video ID should be numeric
		if _, err := strconv.ParseInt(videoID, 10, 64); err != nil {
			fmt.Printf("[!] Warning: Invalid video ID %s at line %d in %s\n",
				videoID, lineNum, archivePath)
			continue
		}

		archive[videoID] = true
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("error reading archive file %s: %v", archivePath, err)
	}

	return archive, nil
}

// shouldSkipCollection determines if all videos in a collection are already
// downloaded by checking the archive file. Returns true only if 100% of videos
// are in the archive.
//
// Returns:
//   - bool: true if yt-dlp can be skipped (all videos downloaded)
//   - string: informational message for user
//   - error: error parsing archive (caller should fall back to calling yt-dlp)
func shouldSkipCollection(entries []VideoEntry, archivePath string) (bool, string, error) {
	// Empty collection - nothing to download
	if len(entries) == 0 {
		return true, "Empty collection", nil
	}

	// Parse archive file
	archive, err := parseArchiveFile(archivePath)
	if err != nil {
		// Error parsing archive - be conservative, call yt-dlp
		return false, "", err
	}

	// Empty archive - need to download everything
	if len(archive) == 0 {
		msg := fmt.Sprintf("No videos in archive, %d videos need download", len(entries))
		return false, msg, nil
	}

	// Extract video IDs from all entries and check against archive
	var missingIDs []string
	for _, entry := range entries {
		videoID := extractVideoID(entry.Link)

		// If we can't extract video ID, be conservative - don't skip
		if videoID == "" {
			msg := fmt.Sprintf("Could not parse video ID from URL: %s", entry.Link)
			return false, msg, nil
		}

		// Check if video is in archive
		if !archive[videoID] {
			missingIDs = append(missingIDs, videoID)
		}
	}

	// All videos in archive - safe to skip
	if len(missingIDs) == 0 {
		msg := fmt.Sprintf("All %d videos already downloaded", len(entries))
		return true, msg, nil
	}

	// Partial match - need to call yt-dlp
	msg := fmt.Sprintf("%d new videos need download (out of %d total)",
		len(missingIDs), len(entries))
	return false, msg, nil
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
	Run(name string, args ...string) (CapturedOutput, error)
}

// RealCommandRunner implements CommandRunner using exec.Command
type RealCommandRunner struct {
	ProgressRenderer *ProgressRenderer // Optional: if set, renders progress bar
	ProgressState    *ProgressState    // Optional: if set, tracks progress
}

func (r *RealCommandRunner) Run(name string, args ...string) (CapturedOutput, error) {
	cmd := exec.Command(name, args...)

	var stdoutBuf, stderrBuf bytes.Buffer

	// Get stdout and stderr pipes for line-by-line reading
	stdoutPipe, err := cmd.StdoutPipe()
	if err != nil {
		return CapturedOutput{}, err
	}
	stderrPipe, err := cmd.StderrPipe()
	if err != nil {
		return CapturedOutput{}, err
	}

	// Start the command
	if err := cmd.Start(); err != nil {
		return CapturedOutput{}, err
	}

	// Process stdout and stderr line-by-line in goroutines
	done := make(chan bool, 2)

	// Process stdout
	go func() {
		scanner := bufio.NewScanner(stdoutPipe)
		for scanner.Scan() {
			line := scanner.Text()
			stdoutBuf.WriteString(line + "\n") // Capture line

			// Check for progress line if progress rendering is enabled
			if r.ProgressRenderer != nil && r.ProgressState != nil {
				current, total, isProgress, err := parseProgressLine(line)
				if err == nil && isProgress {
					// Update progress state
					r.ProgressState.CurrentIndex = current
					r.ProgressState.TotalVideos = total
					// Render progress bar
					r.ProgressRenderer.renderProgress(r.ProgressState)
					continue // Don't print progress lines when using progress bar
				}

				// Check for skip line (already downloaded videos)
				if isSkipLine(line) {
					// Increment progress for skipped videos
					r.ProgressState.CurrentIndex++
					r.ProgressState.SuccessCount++
					// Render progress bar
					r.ProgressRenderer.renderProgress(r.ProgressState)
					continue // Don't print skip lines when using progress bar
				}

				// Check for error line (failed downloads)
				if isErrorLine(line) {
					// Increment failure count for errors
					r.ProgressState.FailureCount++
					// Render progress bar to update failure count
					r.ProgressRenderer.renderProgress(r.ProgressState)
					// Don't continue here - let the error be printed below
				}

				// Check for verbose line when progress bar is enabled
				if r.ProgressRenderer.enabled && isVerboseLine(line) {
					continue // Don't print verbose lines when using progress bar
				}
			}

			// For non-progress lines or when progress bar is disabled
			if r.ProgressRenderer != nil && r.ProgressRenderer.enabled {
				// Clear progress bar before printing regular line
				r.ProgressRenderer.clearProgress()
			}
			_, _ = fmt.Fprintln(os.Stdout, line) // Ignore errors writing to stdout
			if r.ProgressRenderer != nil && r.ProgressRenderer.enabled {
				// Re-render progress after printing line
				r.ProgressRenderer.renderProgress(r.ProgressState)
			}
		}
		done <- true
	}()

	// Process stderr
	go func() {
		scanner := bufio.NewScanner(stderrPipe)
		for scanner.Scan() {
			line := scanner.Text()
			fmt.Fprintln(os.Stderr, line)      // Display line
			stderrBuf.WriteString(line + "\n") // Capture line
		}
		done <- true
	}()

	// Wait for both goroutines to finish
	<-done
	<-done

	// Wait for command to complete
	err = cmd.Wait()

	// Clear progress bar when command finishes
	if r.ProgressRenderer != nil {
		r.ProgressRenderer.clearProgress()
		fmt.Println() // Add newline after clearing
	}

	// Combine output line-by-line
	combined := combineOutputLines(stdoutBuf.String(), stderrBuf.String())

	return CapturedOutput{
		Stdout:   stdoutBuf,
		Stderr:   stderrBuf,
		Combined: combined,
	}, err
}

// combineOutputLines merges stdout and stderr into a single line-by-line array
func combineOutputLines(stdout, stderr string) []string {
	lines := make([]string, 0)
	lines = append(lines, strings.Split(stdout, "\n")...)
	lines = append(lines, strings.Split(stderr, "\n")...)
	return lines
}

// parseYtdlpOutput extracts failure details from yt-dlp output
// yt-dlp error format: ERROR: [TikTok] VIDEO_ID: error message
func parseYtdlpOutput(lines []string, entries []VideoEntry) []FailureDetail {
	failures := make([]FailureDetail, 0)

	// Build video ID to URL map
	idToURL := make(map[string]string)
	for _, entry := range entries {
		if entry.VideoID != "" {
			idToURL[entry.VideoID] = entry.Link
		}
	}

	// Regex: ERROR: [TikTok] VIDEO_ID: error message
	errorPattern := regexp.MustCompile(`ERROR:\s*\[TikTok\]\s*(\d+):\s*(.+)`)

	for _, line := range lines {
		matches := errorPattern.FindStringSubmatch(line)
		if len(matches) >= 3 {
			videoID := matches[1]
			errorMsg := strings.TrimSpace(matches[2])

			failures = append(failures, FailureDetail{
				VideoID:      videoID,
				VideoURL:     idToURL[videoID],
				ErrorMessage: errorMsg,
				ErrorType:    categorizeError(errorMsg),
			})
		}
	}

	return failures
}

// categorizeError classifies error messages into types
func categorizeError(errorMsg string) ErrorType {
	msgLower := strings.ToLower(errorMsg)

	if strings.Contains(msgLower, "ip address is blocked") {
		return ErrorIPBlocked
	}
	if strings.Contains(msgLower, "log in for access") ||
		strings.Contains(msgLower, "not comfortable for some audiences") {
		return ErrorAuthRequired
	}
	if strings.Contains(msgLower, "not available") ||
		strings.Contains(msgLower, "private video") {
		return ErrorNotAvailable
	}
	if strings.Contains(msgLower, "timeout") ||
		strings.Contains(msgLower, "connection refused") {
		return ErrorNetworkTimeout
	}

	return ErrorOther
}

// parseProgressLine extracts progress information from yt-dlp output
// yt-dlp outputs progress lines like: "[download] Downloading item 5 of 127"
// Returns: (currentIndex, total, isProgressLine, error)
func parseProgressLine(line string) (int, int, bool, error) {
	// Match pattern: [download] Downloading item X of Y
	re := regexp.MustCompile(`\[download\] Downloading item (\d+) of (\d+)`)
	matches := re.FindStringSubmatch(line)

	if len(matches) != 3 {
		return 0, 0, false, nil // Not a progress line
	}

	current, err1 := strconv.Atoi(matches[1])
	total, err2 := strconv.Atoi(matches[2])

	if err1 != nil || err2 != nil {
		return 0, 0, false, fmt.Errorf("failed to parse progress numbers")
	}

	return current, total, true, nil
}

// isSkipLine detects when yt-dlp skips an already-downloaded video
// yt-dlp outputs: "[download] <filename> has already been downloaded" or "has already been recorded in the archive"
// Returns: true if this is a skip message
func isSkipLine(line string) bool {
	return strings.Contains(line, "has already been downloaded") ||
		strings.Contains(line, "has already been recorded in the archive")
}

// isVerboseLine returns true if the line is routine yt-dlp output that can be suppressed
// when progress bar is enabled. These are informational messages that add noise without value.
// ERROR and WARNING messages are never considered verbose and will always be displayed.
func isVerboseLine(line string) bool {
	// Never suppress errors or warnings
	if strings.Contains(line, "ERROR:") || strings.Contains(line, "WARNING:") {
		return false
	}

	verbosePatterns := []string{
		"[generic] Extracting URL:",
		"[generic] ",
		": Downloading webpage",
		"[redirect] Following redirect to",
		"[TikTok] Extracting URL:",
		"[info] ",
		": Downloading 1 format(s):",
		"Video thumbnail is already present",
		"Video metadata is already present",
		"[download] 100%",
	}

	for _, pattern := range verbosePatterns {
		if strings.Contains(line, pattern) {
			return true
		}
	}
	return false
}

// isErrorLine detects when yt-dlp encounters an error during download
// yt-dlp outputs errors like: "ERROR: [TikTok] VIDEO_ID: error message"
// Returns: true if this is an error message
func isErrorLine(line string) bool {
	return strings.Contains(line, "ERROR: [TikTok]")
}

// supportsANSI checks if the terminal supports ANSI escape codes
func supportsANSI() bool {
	// Check if stdout is a terminal (not piped or redirected)
	fileInfo, err := os.Stdout.Stat()
	if err != nil {
		return false
	}

	// If output is piped or redirected, disable ANSI
	if (fileInfo.Mode() & os.ModeCharDevice) == 0 {
		return false
	}

	// Check for TERM environment variable (common on Unix-like systems)
	term := os.Getenv("TERM")
	if term != "" && term != "dumb" {
		return true
	}

	// Check for Windows Terminal or other modern Windows terminals
	// Windows Terminal sets WT_SESSION
	if os.Getenv("WT_SESSION") != "" {
		return true
	}

	// ConEmu sets ConEmuANSI
	if os.Getenv("ConEmuANSI") == "ON" {
		return true
	}

	// Default to false for safety (no progress bar if unsure)
	return false
}

// renderProgress displays a live progress bar using ANSI escape codes
// Format: "Downloading favorites (87/92) | ████████████░░░ 94.6% | Success: 85 | Failed: 2"
func (pr *ProgressRenderer) renderProgress(state *ProgressState) {
	if !pr.enabled {
		return
	}

	// Calculate percentage
	percentage := 0.0
	if state.TotalVideos > 0 {
		percentage = float64(state.CurrentIndex) / float64(state.TotalVideos) * 100
	}

	// Create progress bar (20 characters wide)
	barWidth := 20
	filledWidth := int(float64(barWidth) * percentage / 100)
	if filledWidth > barWidth {
		filledWidth = barWidth
	}

	bar := strings.Repeat("█", filledWidth) + strings.Repeat("░", barWidth-filledWidth)

	// Color codes
	green := "\033[32m"
	red := "\033[31m"
	reset := "\033[0m"

	// Build progress line
	line := fmt.Sprintf("\rDownloading %s (%d/%d) | %s %.1f%% | %sSuccess: %d%s | %sFailed: %d%s",
		state.CollectionName,
		state.CurrentIndex,
		state.TotalVideos,
		bar,
		percentage,
		green,
		state.SuccessCount,
		reset,
		red,
		state.FailureCount,
		reset,
	)

	// Clear previous line if it was longer
	if len(line) < pr.lastLineLen {
		line += strings.Repeat(" ", pr.lastLineLen-len(line))
	}
	pr.lastLineLen = len(line)

	// Print progress (using \r to overwrite current line)
	fmt.Print(line)
}

// clearProgress clears the progress bar line
func (pr *ProgressRenderer) clearProgress() {
	if !pr.enabled || pr.lastLineLen == 0 {
		return
	}

	// Clear line and move to start
	fmt.Print("\r" + strings.Repeat(" ", pr.lastLineLen) + "\r")
	pr.lastLineLen = 0
}

// calculateSessionTotals aggregates totals across all collections
func calculateSessionTotals(collections []CollectionResult) (attempted, success, failed int) {
	for _, col := range collections {
		attempted += col.Attempted
		success += col.Success
		failed += col.Failed
	}
	return
}

// printSessionSummary displays end-of-session summary to console
func printSessionSummary(session *DownloadSession) {
	duration := session.EndTime.Sub(session.StartTime)

	fmt.Println("\n" + strings.Repeat("=", 80))
	fmt.Println("                        DOWNLOAD SESSION SUMMARY")
	fmt.Println(strings.Repeat("=", 80))
	fmt.Printf("Duration: %s\n", formatDuration(int(duration.Seconds())))
	fmt.Printf("Total Videos Attempted: %d\n", session.TotalAttempted)
	fmt.Printf("  ✓ Successfully Downloaded: %d\n", session.TotalSuccess)
	fmt.Printf("  ✗ Failed: %d\n\n", session.TotalFailed)

	if len(session.Collections) > 1 {
		fmt.Println("Collection Breakdown:")
		for _, col := range session.Collections {
			fmt.Printf("  %s:\n", col.Name)
			fmt.Printf("    Attempted: %-4d | Success: %-4d | Failed: %d\n",
				col.Attempted, col.Success, col.Failed)
		}
		fmt.Println()
	}

	if session.TotalFailed > 0 {
		fmt.Println("For detailed failure information, see results.txt")
	}
	fmt.Println(strings.Repeat("=", 80))
}

// formatDuration converts seconds to a human-readable duration string
func formatDuration(seconds int) string {
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}
	minutes := seconds / 60
	secs := seconds % 60
	if minutes < 60 {
		return fmt.Sprintf("%dm %ds", minutes, secs)
	}
	hours := minutes / 60
	mins := minutes % 60
	return fmt.Sprintf("%dh %dm %ds", hours, mins, secs)
}

// writeResultsFile appends session results to results.txt
func writeResultsFile(session *DownloadSession) error {
	resultsPath := "results.txt"

	// Open in append mode, create if doesn't exist
	f, err := os.OpenFile(resultsPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open results.txt: %v", err)
	}
	defer func() { _ = f.Close() }()

	w := bufio.NewWriter(f)
	defer func() { _ = w.Flush() }()

	// Session separator (for multiple sessions in same file)
	_, _ = fmt.Fprintf(w, "\n%s\n", strings.Repeat("=", 80))
	_, _ = fmt.Fprintf(w, "TikTok Video Downloader - Session Results\n")
	_, _ = fmt.Fprintf(w, "Generated: %s\n", session.EndTime.Format("2006-01-02 15:04:05"))
	_, _ = fmt.Fprintf(w, "Duration: %s\n", formatDuration(int(session.EndTime.Sub(session.StartTime).Seconds())))
	_, _ = fmt.Fprintf(w, "%s\n\n", strings.Repeat("=", 80))

	// Summary
	_, _ = fmt.Fprintf(w, "SUMMARY\n")
	_, _ = fmt.Fprintf(w, "=======\n")
	_, _ = fmt.Fprintf(w, "Total Videos Attempted: %d\n", session.TotalAttempted)
	_, _ = fmt.Fprintf(w, "Successfully Downloaded: %d\n", session.TotalSuccess)
	_, _ = fmt.Fprintf(w, "Failed: %d\n\n", session.TotalFailed)

	if session.TotalFailed == 0 {
		_, _ = fmt.Fprintf(w, "All videos downloaded successfully!\n")
		return nil
	}

	// Failed downloads
	_, _ = fmt.Fprintf(w, "FAILED DOWNLOADS\n")
	_, _ = fmt.Fprintf(w, "================\n\n")

	for _, col := range session.Collections {
		if len(col.FailureDetails) == 0 {
			continue
		}

		_, _ = fmt.Fprintf(w, "Collection: %s (%d failures)\n", col.Name, len(col.FailureDetails))
		_, _ = fmt.Fprintf(w, "%s\n\n", strings.Repeat("-", 50))

		for i, failure := range col.FailureDetails {
			_, _ = fmt.Fprintf(w, "%d. Video ID: %s\n", i+1, failure.VideoID)
			_, _ = fmt.Fprintf(w, "   URL: %s\n", failure.VideoURL)
			_, _ = fmt.Fprintf(w, "   Error Type: %s\n", failure.ErrorType.String())
			_, _ = fmt.Fprintf(w, "   Error: %s\n\n", failure.ErrorMessage)
		}
	}

	// Troubleshooting tips
	_, _ = fmt.Fprintf(w, "\nTROUBLESHOOTING TIPS\n")
	_, _ = fmt.Fprintf(w, "====================\n")
	writeTroubleshootingTips(w, session)

	return nil
}

// writeTroubleshootingTips writes context-specific troubleshooting advice
func writeTroubleshootingTips(w *bufio.Writer, session *DownloadSession) {
	// Count error types
	errorCounts := make(map[ErrorType]int)
	for _, col := range session.Collections {
		for _, failure := range col.FailureDetails {
			errorCounts[failure.ErrorType]++
		}
	}

	// Write tips for each encountered error type
	if count := errorCounts[ErrorIPBlocked]; count > 0 {
		_, _ = fmt.Fprintf(w, "IP Blocked (%d videos):\n", count)
		_, _ = fmt.Fprintf(w, "  - Your IP may be rate-limited by TikTok\n")
		_, _ = fmt.Fprintf(w, "  - Try again after waiting 30-60 minutes\n")
		_, _ = fmt.Fprintf(w, "  - Consider using a VPN or different network\n\n")
	}

	if count := errorCounts[ErrorAuthRequired]; count > 0 {
		_, _ = fmt.Fprintf(w, "Authentication Required (%d videos):\n", count)
		_, _ = fmt.Fprintf(w, "  - These videos require login to view (age-restricted content)\n")
		_, _ = fmt.Fprintf(w, "  - Retry with cookies to download these videos:\n")
		_, _ = fmt.Fprintf(w, "    * Use --cookies cookies.txt (Netscape format)\n")
		_, _ = fmt.Fprintf(w, "    * OR use --cookies-from-browser firefox\n")
		_, _ = fmt.Fprintf(w, "  - See: https://github.com/yt-dlp/yt-dlp/wiki/FAQ#how-do-i-pass-cookies-to-yt-dlp\n")
		_, _ = fmt.Fprintf(w, "    NB: cookies-from-browser may not work with Chromium-based browsers, refer to yt-dlp issue 7271 https://github.com/yt-dlp/yt-dlp/issues/7271\n\n")
	}

	if count := errorCounts[ErrorNotAvailable]; count > 0 {
		_, _ = fmt.Fprintf(w, "Not Available (%d videos):\n", count)
		_, _ = fmt.Fprintf(w, "  - Videos may be deleted, private, or region-locked\n")
		_, _ = fmt.Fprintf(w, "  - Check if the video still exists by opening the URL\n\n")
	}

	if count := errorCounts[ErrorNetworkTimeout]; count > 0 {
		_, _ = fmt.Fprintf(w, "Network Timeout (%d videos):\n", count)
		_, _ = fmt.Fprintf(w, "  - Check your internet connection\n")
		_, _ = fmt.Fprintf(w, "  - Retry the download session\n\n")
	}
}

// runYtdlp runs the yt-dlp command for the user
func runYtdlp(psPrefix, outputName string, organizeByCollection, skipThumbnails, disableResume, disableProgressBar bool, cookieFile, cookieFromBrowser string, entries []VideoEntry) (*CollectionResult, error) {
	// Create progress renderer if enabled
	var renderer *ProgressRenderer
	var state *ProgressState
	if !disableProgressBar && supportsANSI() {
		collectionName := filepath.Base(filepath.Dir(outputName))
		if collectionName == "." {
			collectionName = "videos"
		}
		renderer = &ProgressRenderer{enabled: true}
		state = &ProgressState{
			CollectionName: collectionName,
			TotalVideos:    len(entries),
		}
	}

	runner := &RealCommandRunner{
		ProgressRenderer: renderer,
		ProgressState:    state,
	}

	return runYtdlpWithRunner(runner, psPrefix, outputName, organizeByCollection, skipThumbnails, disableResume, cookieFile, cookieFromBrowser, entries)
}

// runYtdlpWithRunner allows dependency injection for testing
func runYtdlpWithRunner(runner CommandRunner, psPrefix, outputName string, organizeByCollection, skipThumbnails, disableResume bool, cookieFile, cookieFromBrowser string, entries []VideoEntry) (*CollectionResult, error) {
	collectionName := filepath.Base(filepath.Dir(outputName))
	if collectionName == "." {
		collectionName = "videos"
	}

	// Pre-check optimization if resume is enabled
	if !disableResume {
		// Calculate archive file path (matches logic below at lines 1159-1165)
		var archivePath string
		if organizeByCollection {
			dir := filepath.Dir(outputName)
			archivePath = filepath.Join(dir, "download_archive.txt")
		} else {
			archivePath = "download_archive.txt"
		}

		// Check if all videos already downloaded
		shouldSkip, msg, err := shouldSkipCollection(entries, archivePath)

		if err != nil {
			// Error parsing archive - log warning but continue with yt-dlp
			fmt.Printf("[!] Warning: Could not parse archive file, proceeding with yt-dlp: %v\n", err)
		} else if shouldSkip {
			// All videos already downloaded - skip yt-dlp entirely
			fmt.Printf("[*] %s collection: %s (skipping yt-dlp)\n",
				collectionName, msg)

			// Return successful result without calling yt-dlp
			return &CollectionResult{
				Name:           collectionName,
				Attempted:      len(entries),
				Failed:         0,
				Success:        len(entries),
				FailureDetails: []FailureDetail{},
			}, nil
		} else {
			// Partial download needed - inform user
			fmt.Printf("[*] %s collection: %s\n", collectionName, msg)
		}
	}

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

	// Add cookie arguments if configured
	if cookieFile != "" {
		args = append(args, "--cookies", cookieFile)
	}
	if cookieFromBrowser != "" {
		args = append(args, "--cookies-from-browser", cookieFromBrowser)
	}

	// Add resume functionality flags unless disabled
	if !disableResume {
		// Calculate archive file path based on organization mode
		var archivePath string
		if organizeByCollection {
			dir := filepath.Dir(outputName)
			archivePath = filepath.Join(dir, "download_archive.txt")
		} else {
			archivePath = "download_archive.txt"
		}

		// Add flags for resume functionality
		args = append(args, "--download-archive", archivePath)
		args = append(args, "--no-overwrites")
		args = append(args, "--continue")
	}

	// Execute and capture output
	output, err := runner.Run(cmdStr, args...)

	// Parse output to extract failures
	failures := parseYtdlpOutput(output.Combined, entries)

	// Build result summary
	result := &CollectionResult{
		Name:           filepath.Base(filepath.Dir(outputName)),
		Attempted:      len(entries),
		Failed:         len(failures),
		Success:        len(entries) - len(failures),
		FailureDetails: failures,
	}

	if err != nil || len(failures) > 0 {
		fmt.Printf("[!] Download completed with %d failures out of %d videos.\n",
			result.Failed, result.Attempted)
	} else {
		fmt.Printf("[*] Successfully downloaded all %d videos.\n", result.Success)
	}

	return result, err
}

// HTML template for the visual index browser
//
//go:embed templates/index.html
var htmlTemplate string

// getTemplateFuncs returns template helper functions for HTML template rendering.
//
// Thread-safety: This function returns a new FuncMap on each call, so it is safe to
// call concurrently from multiple goroutines. The returned FuncMap itself contains
// closures that are stateless and safe for concurrent use within Go's html/template
// package, which handles synchronization internally during template execution.
//
// Note: Currently, the application generates indexes sequentially, but this function
// is designed to support concurrent index generation if needed in the future.
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
func generateCollectionIndex(collectionDir string, entries []VideoEntry, failures []FailureDetail) error {
	collectionName := filepath.Base(collectionDir)
	fmt.Printf("[*] Generating index for %s (%d videos)...\n", collectionName, len(entries))
	// 1. Scan for .info.json files in the directory
	infoFiles, err := filepath.Glob(filepath.Join(collectionDir, "*.info.json"))
	if err != nil {
		return fmt.Errorf("collection %q: error scanning for info files: %v", collectionName, err)
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
	fmt.Printf("[*] Found %d metadata files for %s\n", len(infoMap), collectionName)

	// 3. Build failure map for quick lookup
	failureMap := make(map[string]string)
	for _, f := range failures {
		failureMap[f.VideoID] = f.ErrorMessage
	}

	// 4. Create a copy of entries to avoid mutating the input slice
	enrichedEntries := make([]VideoEntry, len(entries))
	copy(enrichedEntries, entries)

	// 5. Enrich entries with metadata
	for i := range enrichedEntries {
		videoID := extractVideoID(enrichedEntries[i].Link)
		enrichedEntries[i].VideoID = videoID

		// Warn if video ID could not be extracted from URL
		if videoID == "" {
			fmt.Printf("[!] Warning: Could not extract video ID from URL: %s\n", enrichedEntries[i].Link)
			enrichedEntries[i].Downloaded = false
			enrichedEntries[i].DownloadError = "Invalid URL format - could not extract video ID"
			continue
		}

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

			// Determine the local filename from the info (use basename only)
			baseFilename := ""
			if info.Filename != "" {
				baseFilename = filepath.Base(info.Filename)
				enrichedEntries[i].LocalFilename = baseFilename
			}

			// Check if video file actually exists (not just .info.json)
			videoPath := filepath.Join(collectionDir, baseFilename)
			partialPath := videoPath + ".part"

			if _, err := os.Stat(partialPath); err == nil {
				// Partial download exists
				enrichedEntries[i].Downloaded = false
				enrichedEntries[i].DownloadError = "Download incomplete (found .part file)"
			} else if baseFilename != "" {
				if _, err := os.Stat(videoPath); err == nil {
					// Full video file exists
					enrichedEntries[i].Downloaded = true
				} else {
					// Info exists but video file is missing
					enrichedEntries[i].Downloaded = false
					enrichedEntries[i].DownloadError = "Video file missing (metadata only)"
				}
			} else {
				// No filename in metadata
				enrichedEntries[i].Downloaded = false
				enrichedEntries[i].DownloadError = "Metadata incomplete (missing filename)"
			}

			// Check for thumbnail file (try common extensions)
			// Use the base filename (without extension) to search for thumbnails
			if baseFilename != "" {
				baseWithoutExt := strings.TrimSuffix(baseFilename, filepath.Ext(baseFilename))
				for _, ext := range []string{".jpg", ".webp", ".png", ".JPG", ".WEBP", ".PNG"} {
					thumbFilename := baseWithoutExt + ext
					thumbPath := filepath.Join(collectionDir, thumbFilename)
					if _, err := os.Stat(thumbPath); err == nil {
						enrichedEntries[i].ThumbnailFile = thumbFilename
						break
					}
				}
			}
		} else {
			enrichedEntries[i].Downloaded = false
			// Use actual error message if available
			if errMsg, ok := failureMap[videoID]; ok {
				enrichedEntries[i].DownloadError = errMsg
			} else {
				enrichedEntries[i].DownloadError = "Video not downloaded or metadata unavailable"
			}
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
		return fmt.Errorf("collection %q: error writing JSON index: %v", collectionName, err)
	}

	// 6. Generate HTML index
	if err := writeHTMLIndex(collectionDir, &index); err != nil {
		return fmt.Errorf("collection %q: error writing HTML index: %v", collectionName, err)
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
		// If we can't get the path, default to a known name
		return "tiktok-favvideo-downloader.exe"
	}
	// Otherwise, return the filename (base) part of the path
	return filepath.Base(exePath)
}

// validateCookieFile checks if a cookie file exists and is readable
func validateCookieFile(path string) error {
	if path == "" {
		return fmt.Errorf("cookie file path is empty")
	}

	// Check if file exists
	stat, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("cookie file not found: %s", path)
		}
		return fmt.Errorf("error accessing cookie file: %v", err)
	}

	// Check it's not a directory
	if stat.IsDir() {
		return fmt.Errorf("path is a directory, not a file: %s", path)
	}

	// Check if file is readable
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("cannot read cookie file: %v", err)
	}
	defer func() { _ = file.Close() }()

	// Optional: Check if file looks like Netscape cookie format
	scanner := bufio.NewScanner(file)
	if scanner.Scan() {
		firstLine := scanner.Text()
		if !strings.Contains(firstLine, "Netscape HTTP Cookie File") {
			fmt.Println("[!] Warning: File doesn't appear to be in Netscape cookie format")
			fmt.Println("    yt-dlp expects cookies in Netscape format")
		}
	}

	return nil
}

// validateBrowserName checks if a browser name is valid for cookie extraction
func validateBrowserName(browser string) error {
	if browser == "" {
		return fmt.Errorf("browser name is empty")
	}

	validBrowsers := []string{
		"chrome", "firefox", "edge", "safari", "opera",
		"brave", "chromium", "vivaldi",
	}

	browserLower := strings.ToLower(strings.TrimSpace(browser))

	for _, valid := range validBrowsers {
		if browserLower == valid {
			return nil
		}
	}

	return fmt.Errorf("unsupported browser: %s\nValid options: %s",
		browser, strings.Join(validBrowsers, ", "))
}

// promptForCookies interactively asks the user if they want to provide cookies
func promptForCookies(config *Config) error {
	fmt.Print("\n[*] Some videos require authentication to download (age-restricted content).\n")
	fmt.Print("    Would you like to provide cookies for authentication? (y/n, default is 'n'): ")

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	input := strings.TrimSpace(strings.ToLower(scanner.Text()))

	if input != "y" && input != "yes" {
		return nil // User declined
	}

	// Ask for method
	fmt.Println("\n[*] Choose cookie method:")
	fmt.Println("    1) Use cookies.txt file (Netscape format)")
	fmt.Println("    2) Extract from browser (Chrome, Firefox, Edge, etc.)")
	fmt.Print("    Enter choice (1 or 2): ")

	scanner.Scan()
	choice := strings.TrimSpace(scanner.Text())

	switch choice {
	case "1":
		fmt.Print("[*] Enter path to cookies.txt file: ")
		scanner.Scan()
		cookiePath := strings.TrimSpace(scanner.Text())

		if err := validateCookieFile(cookiePath); err != nil {
			return fmt.Errorf("cookie file validation failed: %w", err)
		}

		config.CookieFile = cookiePath
		fmt.Println("[*] Using cookies from file:", cookiePath)

	case "2":
		fmt.Print("[*] Enter browser name (chrome, firefox, edge, safari, etc.): ")
		scanner.Scan()
		browser := strings.TrimSpace(scanner.Text())

		if err := validateBrowserName(browser); err != nil {
			return err
		}

		config.CookieFromBrowser = strings.ToLower(browser)
		fmt.Printf("[*] Will extract cookies from %s browser\n", browser)

	default:
		return fmt.Errorf("invalid choice: %s (expected 1 or 2)", choice)
	}

	return nil
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
	disableResume := flag.Bool("disable-resume", false, "Disable resume functionality (force re-download all videos)")
	noProgressBar := flag.Bool("no-progress-bar", false, "Disable progress bar (use traditional line-by-line output)")
	cookies := flag.String("cookies", "", "Path to Netscape cookies.txt file for authentication")
	cookiesFromBrowser := flag.String("cookies-from-browser", "", "Extract cookies from browser (chrome, firefox, edge, safari, etc.)")
	help := flag.Bool("help", false, "Show help message")
	h := flag.Bool("h", false, "Show help message")

	flag.Parse()

	if *help || *h {
		printUsage()
		os.Exit(0)
	}

	// Check mutual exclusivity of cookie flags
	if *cookies != "" && *cookiesFromBrowser != "" {
		fmt.Println("[!!!] Error: Cannot use both --cookies and --cookies-from-browser")
		os.Exit(1)
	}

	config.OrganizeByCollection = !*flatStructure
	config.SkipThumbnails = *noThumbnails
	config.IndexOnly = *indexOnly
	config.DisableResume = *disableResume
	config.DisableProgressBar = *noProgressBar
	config.CookieFile = *cookies
	config.CookieFromBrowser = *cookiesFromBrowser

	// Validate cookie file if provided
	if config.CookieFile != "" {
		if err := validateCookieFile(config.CookieFile); err != nil {
			fmt.Printf("[!!!] Cookie file validation failed: %v\n", err)
			os.Exit(1)
		}
	}

	// Validate browser name if provided
	if config.CookieFromBrowser != "" {
		if err := validateBrowserName(config.CookieFromBrowser); err != nil {
			fmt.Printf("[!!!] %v\n", err)
			os.Exit(1)
		}
	}

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
	fmt.Println("  --flat-structure           Disable collection organization (use flat directory structure)")
	fmt.Println("  --no-thumbnails            Skip thumbnail download (faster, less storage)")
	fmt.Println("  --index-only               Regenerate indexes from existing .info.json files")
	fmt.Println("  --disable-resume           Disable resume functionality (force re-download all videos)")
	fmt.Println("  --no-progress-bar          Disable progress bar (use traditional line-by-line output)")
	fmt.Println("  --cookies <FILE>           Path to Netscape cookies.txt file for authentication")
	fmt.Println("  --cookies-from-browser <NAME>  Extract cookies from browser (chrome, firefox, edge, etc.)")
	fmt.Println("  --help, -h                 Show this help message")
	fmt.Println("\nExamples:")
	fmt.Println("  1) Double-click (no arguments) if 'user_data_tiktok.json' is in the same folder.")
	fmt.Printf("  2) Or drag & drop a JSON file onto '%s' to specify a different JSON file.\n", exeName)
	fmt.Printf("  3) Or run from command line: %s path\\to\\my_tiktok_data.json\n", exeName)
	fmt.Printf("  4) Use flat structure: %s --flat-structure\n", exeName)
	fmt.Printf("  5) Skip thumbnails: %s --no-thumbnails\n", exeName)
	fmt.Printf("  6) Regenerate index only: %s --index-only\n", exeName)
	fmt.Printf("  7) Force re-download all: %s --disable-resume\n", exeName)
	fmt.Printf("  8) Disable progress bar: %s --no-progress-bar\n", exeName)
	fmt.Printf("  9) Use cookies from file: %s --cookies cookies.txt\n", exeName)
	fmt.Printf("  10) Extract cookies from Chrome: %s --cookies-from-browser chrome\n", exeName)
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
				// No download, so no failure details
				if err := generateCollectionIndex(collection, collectionEntries, nil); err != nil {
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
			// No download, so no failure details
			if err := generateCollectionIndex(dir, videoEntries, nil); err != nil {
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

	// Prompt for cookies if not provided via flags
	if config.CookieFile == "" && config.CookieFromBrowser == "" {
		if err := promptForCookies(config); err != nil {
			fmt.Printf("[!!!] Cookie setup failed: %v\n", err)
			fmt.Println("[*] Continuing without cookies...")
			// Don't exit - continue with download attempt
		}
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
		// Initialize download session tracking
		session := &DownloadSession{
			StartTime:   time.Now(),
			Collections: make([]CollectionResult, 0),
		}

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
				collectionEntries := getEntriesForCollection(videoEntries, collection)

				fmt.Printf("[*] Processing collection: %s\n", collection)
				result, _ := runYtdlp(psPrefix, collectionOutputName, config.OrganizeByCollection, config.SkipThumbnails, config.DisableResume, config.DisableProgressBar, config.CookieFile, config.CookieFromBrowser, collectionEntries)

				// Track session results
				if result != nil {
					session.Collections = append(session.Collections, *result)
				}

				// Generate index after download completes (pass failures for error details)
				var failures []FailureDetail
				if result != nil {
					failures = result.FailureDetails
				}
				if err := generateCollectionIndex(collection, collectionEntries, failures); err != nil {
					fmt.Printf("[!] Warning: Failed to generate index for %s: %v\n", collection, err)
				} else {
					fmt.Printf("[*] Generated index.html and index.json for %s\n", collection)
				}
			}
		} else {
			// Flat structure
			result, _ := runYtdlp(psPrefix, config.OutputName, config.OrganizeByCollection, config.SkipThumbnails, config.DisableResume, config.DisableProgressBar, config.CookieFile, config.CookieFromBrowser, videoEntries)

			// Track session results
			if result != nil {
				session.Collections = append(session.Collections, *result)
			}

			// Generate index for flat structure in current directory
			dir, err := filepath.Abs(".")
			if err != nil {
				dir = "."
			}
			var failures []FailureDetail
			if result != nil {
				failures = result.FailureDetails
			}
			if err := generateCollectionIndex(dir, videoEntries, failures); err != nil {
				fmt.Printf("[!] Warning: Failed to generate index: %v\n", err)
			} else {
				fmt.Println("[*] Generated index.html and index.json")
			}
		}

		// Finalize session
		session.EndTime = time.Now()
		session.TotalAttempted, session.TotalSuccess, session.TotalFailed =
			calculateSessionTotals(session.Collections)

		// Display summary
		printSessionSummary(session)

		// Write results.txt
		if err := writeResultsFile(session); err != nil {
			fmt.Printf("[!] Warning: Failed to write results.txt: %v\n", err)
		}
	}
}
