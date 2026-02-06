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
		regexp.MustCompile(`/photo/(\d+)`),
		regexp.MustCompile(`/v/(\d+)`),
	}

	// Pre-compiled regex patterns for parsing yt-dlp and gallery-dl output
	ytdlpErrorPattern     = regexp.MustCompile(`ERROR:\s*\[TikTok\]\s*(\d+):\s*(.+)`)
	progressLinePattern   = regexp.MustCompile(`\[download\] Downloading item (\d+) of (\d+)`)
	gallerySuccessPattern = regexp.MustCompile(`^#\d+`)
	galleryErrorPattern   = regexp.MustCompile(`(?i)error|failed|\[error\]`)
	galleryVideoIDPattern = regexp.MustCompile(`(\d{19})`)
)

// VideoEntry represents a video or photo with its collection information and metadata
type VideoEntry struct {
	// From TikTok JSON export
	Link       string `json:"link"`
	Date       string `json:"favorited_date"` // When user favorited/liked
	Collection string `json:"collection"`     // "favorites" or "liked"

	// Derived from URL
	VideoID     string `json:"video_id"`
	ContentType string `json:"content_type,omitempty"` // "video" or "photo" (detected at download time)

	// From yt-dlp/gallery-dl metadata (populated after download)
	Title         string `json:"title,omitempty"`
	Creator       string `json:"creator,omitempty"`
	CreatorID     string `json:"creator_id,omitempty"`
	UploadDate    string `json:"upload_date,omitempty"`
	Description   string `json:"description,omitempty"`
	Duration      int    `json:"duration,omitempty"` // 0 for photos
	ViewCount     int64  `json:"view_count,omitempty"`
	LikeCount     int64  `json:"like_count,omitempty"`
	ThumbnailURL  string `json:"thumbnail_url,omitempty"`
	ThumbnailFile string `json:"thumbnail_file,omitempty"`

	// Photo-specific fields
	ImageCount int      `json:"image_count,omitempty"` // Number of images in slideshow
	ImageFiles []string `json:"image_files,omitempty"` // List of local image filenames
	AudioFile  string   `json:"audio_file,omitempty"`  // Audio track filename (for slideshows)

	// Download status
	Downloaded    bool   `json:"downloaded"`
	LocalFilename string `json:"local_filename,omitempty"` // For videos; first image for photos
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
	TotalSkipped   int
	// Photo-specific tracking
	TotalPhotosAttempted int
	TotalPhotosSuccess   int
	TotalPhotosFailed    int
}

// CollectionResult tracks results for a single collection
type CollectionResult struct {
	Name           string
	ContentType    string // "video", "photo", or "" (legacy/mixed)
	Attempted      int
	Success        int
	Failed         int
	Skipped        int
	FailureDetails []FailureDetail
}

// FailureDetail contains information about a failed download
type FailureDetail struct {
	VideoID      string
	VideoURL     string
	ErrorMessage string
	ErrorType    ErrorType
	ContentType  string // "video" or "photo"
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
	SkippedCount   int
	InitialSkipped int
}

// ProgressRenderer handles ANSI-based progress display
type ProgressRenderer struct {
	enabled     bool      // false if terminal doesn't support ANSI or user disabled it
	lastLineLen int       // track last line length for proper clearing
	writer      io.Writer // where to write output (defaults to os.Stdout)
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

// ToolConfig defines how to manage an external tool (yt-dlp, gallery-dl, etc.)
type ToolConfig struct {
	Name            string                               // Display name (e.g., "yt-dlp")
	ExeName         string                               // Executable filename (e.g., "yt-dlp.exe")
	GitHubRepo      string                               // GitHub repo path (e.g., "yt-dlp/yt-dlp")
	GetVersion      func(exePath string) (string, error) // Get local version
	CompareVersions func(local, remote string) int       // Compare versions
	SelfUpdate      func(exePath string) error           // Self-update command (nil if unsupported)
	StripVPrefix    bool                                 // Strip "v" prefix from GitHub release tag
}

// backupExe backs up the current executable to .old
func backupExe(exeName string) error {
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

// downloadLatestRelease downloads the latest version of a tool from GitHub releases
func downloadLatestRelease(client *http.Client, tool *ToolConfig) error {
	fmt.Printf("[*] Downloading the latest %s release from GitHub...\n", tool.Name)

	releaseURL := fmt.Sprintf("https://api.github.com/repos/%s/releases/latest", tool.GitHubRepo)
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

	var downloadURL string
	for _, asset := range release.Assets {
		if strings.EqualFold(asset.Name, tool.ExeName) {
			downloadURL = asset.BrowserDownloadURL
			break
		}
	}
	if downloadURL == "" {
		return fmt.Errorf("could not find %s in the latest release assets", tool.ExeName)
	}

	fmt.Printf("[*] Downloading %s...\n", downloadURL)

	out, err := os.Create(tool.ExeName)
	if err != nil {
		return fmt.Errorf("error creating %s: %v", tool.ExeName, err)
	}
	defer func() { _ = out.Close() }()

	downloadResp, err := client.Get(downloadURL)
	if err != nil {
		return fmt.Errorf("failed to download %s: %v", tool.ExeName, err)
	}
	defer func() { _ = downloadResp.Body.Close() }()

	if _, err := io.Copy(out, downloadResp.Body); err != nil {
		return fmt.Errorf("failed to write %s to disk: %v", tool.ExeName, err)
	}

	fmt.Printf("[*] Successfully downloaded %s\n", tool.Name)
	return nil
}

// getLatestVersion fetches the latest version of a tool from GitHub releases
func getLatestVersion(client *http.Client, tool *ToolConfig) (string, error) {
	checkRedirect := client.CheckRedirect
	client.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		return http.ErrUseLastResponse
	}
	defer func() { client.CheckRedirect = checkRedirect }()

	url := fmt.Sprintf("https://github.com/%s/releases/latest", tool.GitHubRepo)
	resp, err := client.Get(url)
	if err != nil {
		return "", fmt.Errorf("failed to fetch GitHub releases: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusFound && resp.StatusCode != http.StatusMovedPermanently {
		return "", fmt.Errorf("unexpected response status: %d", resp.StatusCode)
	}

	location := resp.Header.Get("Location")
	if location == "" {
		return "", fmt.Errorf("no redirect location in response")
	}

	parts := strings.Split(location, "/tag/")
	if len(parts) != 2 {
		return "", fmt.Errorf("unexpected redirect URL format: %s", location)
	}

	version := strings.TrimSpace(parts[1])
	if tool.StripVPrefix {
		version = strings.TrimPrefix(version, "v")
	}
	if version == "" {
		return "", fmt.Errorf("empty version in redirect URL")
	}

	return version, nil
}

// getOrDownloadTool checks if a tool is present, offers updates, or downloads it
func getOrDownloadTool(client *http.Client, tool *ToolConfig) error {
	if _, err := os.Stat(tool.ExeName); err == nil {
		localVersion, err := tool.GetVersion(tool.ExeName)
		if err != nil {
			fmt.Printf("[!] Warning: Could not get local %s version: %v\n", tool.Name, err)
			fmt.Printf("[*] Found %s in the current directory. Continuing with existing version.\n", tool.ExeName)
			return nil
		}

		latestVersion, err := getLatestVersion(client, tool)
		if err != nil {
			fmt.Printf("[!] Warning: Could not check for %s updates: %v\n", tool.Name, err)
			fmt.Printf("[*] Found %s (version %s). Continuing with existing version.\n", tool.ExeName, localVersion)
			return nil
		}

		if tool.CompareVersions(localVersion, latestVersion) < 0 {
			fmt.Printf("[*] %s: Current version: %s, Latest version: %s\n", tool.Name, localVersion, latestVersion)
			if promptForUpdate() {
				// Try self-update first if supported
				if tool.SelfUpdate != nil {
					if err := tool.SelfUpdate(tool.ExeName); err != nil {
						fmt.Printf("[!] Self-update failed: %v\n", err)
						fmt.Printf("[*] Trying manual download as fallback...\n")
					} else {
						return nil
					}
				}

				// Manual download (backup + download)
				if err := backupExe(tool.ExeName); err != nil {
					return fmt.Errorf("backup failed: %v", err)
				}

				if err := downloadLatestRelease(client, tool); err != nil {
					fmt.Printf("[!] Download failed: %v\n", err)
					fmt.Printf("[*] Attempting to restore backup...\n")
					if restoreErr := os.Rename(tool.ExeName+".old", tool.ExeName); restoreErr != nil {
						return fmt.Errorf("download failed and could not restore backup: %v (restore error: %v)", err, restoreErr)
					}
					fmt.Printf("[*] Backup restored. Continuing with existing version.\n")
					return nil
				}
			} else {
				fmt.Printf("[*] Continuing with existing %s (version %s).\n", tool.ExeName, localVersion)
			}
		} else {
			fmt.Printf("[*] Found %s (version %s) - up to date.\n", tool.ExeName, localVersion)
		}
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("error checking for existing %s: %v", tool.ExeName, err)
	}

	fmt.Printf("[*] %s not found. Downloading the latest release from GitHub...\n", tool.ExeName)
	return downloadLatestRelease(client, tool)
}

// getYtdlpVersion runs yt-dlp --version and returns the version string (e.g., "2026.01.29")
func getYtdlpVersion(exePath string) (string, error) {
	// Use explicit relative path for Go 1.19+ security (cannot run executables from current dir without ./)
	cmd := exec.Command("."+string(filepath.Separator)+exePath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to run %s --version: %v", exePath, err)
	}
	version := strings.TrimSpace(string(output))
	if version == "" {
		return "", fmt.Errorf("yt-dlp --version returned empty output")
	}
	return version, nil
}

// compareVersions compares two yt-dlp version strings in YYYY.MM.DD format
// Returns: -1 if local < remote (needs update), 0 if equal, 1 if local > remote
func compareVersions(local, remote string) int {
	// Parse versions - yt-dlp uses YYYY.MM.DD format
	// Simple string comparison works since format is consistent and zero-padded
	if local == remote {
		return 0
	}
	if local < remote {
		return -1
	}
	return 1
}

// updateYtdlp runs yt-dlp --update to self-update the binary
func updateYtdlp(exePath string) error {
	fmt.Println("[*] Running yt-dlp --update...")
	// Use explicit relative path for Go 1.19+ security
	cmd := exec.Command("."+string(filepath.Separator)+exePath, "--update")
	cmd.Stdout = os.Stdout
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("yt-dlp --update failed: %v", err)
	}
	return nil
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

// getGalleryDlVersion runs gallery-dl --version and returns the version string
func getGalleryDlVersion(exePath string) (string, error) {
	// Use explicit relative path for Go 1.19+ security
	cmd := exec.Command("."+string(filepath.Separator)+exePath, "--version")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("failed to run %s --version: %v", exePath, err)
	}
	// gallery-dl outputs "gallery-dl X.Y.Z" so extract version number
	version := strings.TrimSpace(string(output))
	// Parse "gallery-dl X.Y.Z" format
	parts := strings.Fields(version)
	if len(parts) >= 2 {
		return parts[len(parts)-1], nil
	}
	if version == "" {
		return "", fmt.Errorf("gallery-dl --version returned empty output")
	}
	return version, nil
}

// compareGalleryDlVersions compares two gallery-dl version strings in X.Y.Z format
// Returns: -1 if local < remote (needs update), 0 if equal, 1 if local > remote
func compareGalleryDlVersions(local, remote string) int {
	// Parse semantic versions (X.Y.Z format)
	localParts := strings.Split(local, ".")
	remoteParts := strings.Split(remote, ".")

	// Compare each component numerically
	for i := 0; i < len(localParts) && i < len(remoteParts); i++ {
		l, _ := strconv.Atoi(localParts[i])
		r, _ := strconv.Atoi(remoteParts[i])
		if l < r {
			return -1
		}
		if l > r {
			return 1
		}
	}

	// If all compared parts are equal, longer version is greater
	if len(localParts) < len(remoteParts) {
		return -1
	}
	if len(localParts) > len(remoteParts) {
		return 1
	}
	return 0
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
//   - https://www.tiktok.com/@user/photo/7600559584901647646
//   - https://m.tiktok.com/v/7600559584901647646.html
func extractVideoID(url string) string {
	for _, re := range videoIDPatterns {
		if matches := re.FindStringSubmatch(url); len(matches) > 1 {
			return matches[1]
		}
	}
	return ""
}

// ContentTypeCache represents the persistent cache for content type detection results.
// This avoids re-detecting video vs photo for URLs on subsequent runs.
type ContentTypeCache struct {
	Version int               `json:"version"`
	Types   map[string]string `json:"types"` // URL -> "video" or "photo"
}

// loadContentTypeCache reads the content type cache from disk.
// Returns an empty cache (not error) if the file doesn't exist or is corrupt.
func loadContentTypeCache(cacheFilePath string) map[string]string {
	data, err := os.ReadFile(cacheFilePath)
	if err != nil {
		return make(map[string]string)
	}

	var cache ContentTypeCache
	if err := json.Unmarshal(data, &cache); err != nil {
		fmt.Printf("[!] Warning: content type cache file is corrupt, starting fresh\n")
		return make(map[string]string)
	}

	if cache.Types == nil {
		return make(map[string]string)
	}

	return cache.Types
}

// saveContentTypeCache writes the content type cache to disk.
func saveContentTypeCache(cacheFilePath string, types map[string]string) error {
	cache := ContentTypeCache{
		Version: 1,
		Types:   types,
	}

	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		return fmt.Errorf("failed to marshal content type cache: %v", err)
	}

	if err := os.WriteFile(cacheFilePath, data, 0644); err != nil {
		return fmt.Errorf("failed to write content type cache: %v", err)
	}

	return nil
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
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			fmt.Fprintf(os.Stderr, "Warning: failed to close archive file: %v\n", closeErr)
		}
	}()

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

// identifyFailedEntries compares the download archive before and after a yt-dlp run
// to determine which entries succeeded (newly downloaded), which were already downloaded,
// and which failed (not in archive after run).
func identifyFailedEntries(entries []VideoEntry, archiveBefore, archiveAfter map[string]bool) (succeeded, alreadyDownloaded, failed []VideoEntry) {
	for _, entry := range entries {
		videoID := extractVideoID(entry.Link)
		if videoID == "" {
			failed = append(failed, entry)
			continue
		}
		if archiveBefore[videoID] {
			alreadyDownloaded = append(alreadyDownloaded, entry)
		} else if archiveAfter[videoID] {
			succeeded = append(succeeded, entry)
		} else {
			failed = append(failed, entry)
		}
	}
	return
}

// inferContentTypeFromFiles determines content type by checking what files exist on disk
// for a given video ID. Returns "video" if .info.json exists, "photo" if other .json
// metadata exists (excluding .info.json and index.json), or "" if nothing found.
func inferContentTypeFromFiles(dir string, videoID string) string {
	if videoID == "" {
		return ""
	}

	// Check for yt-dlp .info.json files (indicates video)
	infoPattern := filepath.Join(dir, fmt.Sprintf("*%s*.info.json", videoID))
	infoMatches, _ := filepath.Glob(infoPattern)
	if len(infoMatches) > 0 {
		return "video"
	}

	// Check for video files directly (mp4, webm, etc.)
	for _, ext := range []string{"mp4", "mkv", "webm", "mov"} {
		videoPattern := filepath.Join(dir, fmt.Sprintf("*%s*.%s", videoID, ext))
		videoMatches, _ := filepath.Glob(videoPattern)
		if len(videoMatches) > 0 {
			return "video"
		}
	}

	// Check for photo files (jpg, png, webp with this video ID but no .info.json)
	for _, ext := range []string{"jpg", "jpeg", "png", "webp"} {
		photoPattern := filepath.Join(dir, fmt.Sprintf("*%s*.%s", videoID, ext))
		photoMatches, _ := filepath.Glob(photoPattern)
		if len(photoMatches) > 0 {
			return "photo"
		}
	}

	// Check for gallery-dl metadata .json files (not .info.json, not index.json)
	jsonPattern := filepath.Join(dir, fmt.Sprintf("*%s*.json", videoID))
	jsonMatches, _ := filepath.Glob(jsonPattern)
	for _, match := range jsonMatches {
		base := filepath.Base(match)
		if !strings.HasSuffix(base, ".info.json") && base != "index.json" {
			return "photo"
		}
	}

	return ""
}

// applyContentTypesFromCache applies cached content types and infers types from files on disk
// for entries that don't have a cached type. Returns the number of entries that remain unknown.
func applyContentTypesFromCache(entries []VideoEntry, cache map[string]string, dir string, organizeByCollection bool) int {
	unknown := 0
	for i := range entries {
		// Already has a type assigned
		if entries[i].ContentType != "" {
			continue
		}

		// Try cache
		if ct, ok := cache[entries[i].Link]; ok {
			entries[i].ContentType = ct
			continue
		}

		// Try inferring from files on disk
		videoID := extractVideoID(entries[i].Link)
		if videoID == "" {
			unknown++
			continue
		}

		lookupDir := dir
		if organizeByCollection {
			lookupDir = sanitizeCollectionName(entries[i].Collection)
		}

		ct := inferContentTypeFromFiles(lookupDir, videoID)
		if ct != "" {
			entries[i].ContentType = ct
			// Update cache for future runs
			cache[entries[i].Link] = ct
		} else {
			unknown++
		}
	}
	return unknown
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

// getVideoOutputFilename returns the video URL list filename for a collection
func getVideoOutputFilename(collection string) string {
	if collection == "liked" {
		return "liked_videos.txt"
	}
	return "fav_videos.txt"
}

// getPhotoOutputFilename returns the photo URL list filename for a collection
func getPhotoOutputFilename(collection string) string {
	if collection == "liked" {
		return "liked_photos.txt"
	}
	return "fav_photos.txt"
}

// separateEntriesByContentType splits entries into videos and photos based on their ContentType field
func separateEntriesByContentType(entries []VideoEntry) (videos []VideoEntry, photos []VideoEntry) {
	for _, entry := range entries {
		if entry.ContentType == "photo" {
			photos = append(photos, entry)
		} else {
			// Default to video if ContentType is empty or "video"
			videos = append(videos, entry)
		}
	}
	return
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
// Separates videos and photos into different files for processing by yt-dlp and gallery-dl respectively.
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

		// Write separate files for each collection, splitting videos and photos
		for collection, entries := range collectionGroups {
			videos, photos := separateEntriesByContentType(entries)

			// Write video URLs
			if len(videos) > 0 {
				videoFilename := getVideoOutputFilename(collection)
				videoOutputName := filepath.Join(collection, videoFilename)
				if err := writeVideoEntriesToFile(videos, videoOutputName); err != nil {
					return err
				}
				fmt.Printf("[*] Extracted %d video URLs to '%s'\n", len(videos), videoOutputName)
			}

			// Write photo URLs
			if len(photos) > 0 {
				photoFilename := getPhotoOutputFilename(collection)
				photoOutputName := filepath.Join(collection, photoFilename)
				if err := writeVideoEntriesToFile(photos, photoOutputName); err != nil {
					return err
				}
				fmt.Printf("[*] Extracted %d photo URLs to '%s'\n", len(photos), photoOutputName)
			}

		}
	} else {
		// Flat structure - separate videos and photos
		videos, photos := separateEntriesByContentType(videoEntries)

		// Write video URLs (use the provided outputName for backward compatibility)
		if len(videos) > 0 {
			if err := writeVideoEntriesToFile(videos, outputName); err != nil {
				return err
			}
			fmt.Printf("[*] Extracted %d video URLs to '%s'\n", len(videos), outputName)
		}

		// Write photo URLs to a separate file in the same directory as the video file
		if len(photos) > 0 {
			dir := filepath.Dir(outputName)
			photoOutputName := filepath.Join(dir, "fav_photos.txt")
			if err := writeVideoEntriesToFile(photos, photoOutputName); err != nil {
				return err
			}
			fmt.Printf("[*] Extracted %d photo URLs to '%s'\n", len(photos), photoOutputName)
		}
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

	// Process output using the extracted function
	// We pass tee readers so we can capture the raw output while processing it
	stdoutTee := io.TeeReader(stdoutPipe, &stdoutBuf)
	stderrTee := io.TeeReader(stderrPipe, &stderrBuf)

	// Note: processOutput now returns just error, as it doesn't build the CapturedOutput
	// We build CapturedOutput here from the buffers
	processErr := processOutput(stdoutTee, stderrTee, os.Stdout, os.Stderr, r.ProgressRenderer, r.ProgressState)

	// Wait for command to complete
	cmdErr := cmd.Wait()

	// Combine output line-by-line
	combined := combineOutputLines(stdoutBuf.String(), stderrBuf.String())

	// Return command error if it failed, otherwise process error
	finalErr := cmdErr
	if finalErr == nil {
		finalErr = processErr
	}

	return CapturedOutput{
		Stdout:   stdoutBuf,
		Stderr:   stderrBuf,
		Combined: combined,
	}, finalErr
}

// processOutput handles reading from stdout/stderr and updating progress
// Separated from Run for testing purposes
func processOutput(stdout, stderr io.Reader, stdoutWriter, stderrWriter io.Writer, renderer *ProgressRenderer, state *ProgressState) error {
	// Process stdout and stderr line-by-line in goroutines
	done := make(chan bool, 2)

	// Process stdout
	go func() {
		scanner := bufio.NewScanner(stdout)
		scanner.Split(scanLinesAndCR)
		for scanner.Scan() {
			line := scanner.Text()

			// Check for progress line if progress rendering is enabled
			if renderer != nil && state != nil {
				current, _, isProgress, err := parseProgressLine(line)
				if err == nil && isProgress {
					// Update progress state
					state.CurrentIndex = state.InitialSkipped + current
					// state.TotalVideos is already set correctly
					// Render progress bar
					renderer.renderProgress(state)
					continue // Don't print progress lines when using progress bar
				}

				// Check for skip line (already downloaded videos)
				if isSkipLine(line) {
					// Increment progress for skipped videos
					state.CurrentIndex++
					state.SkippedCount++
					// Render progress bar
					renderer.renderProgress(state)
					continue // Don't print skip lines when using progress bar
				}

				// Check for error line (failed downloads)
				if isErrorLine(line) {
					// Increment failure count for errors
					state.FailureCount++
					// Don't render here - let it fall through to normal print logic
					// which will clear, print, and re-render properly
				}

				// Check for verbose line when progress bar is enabled
				if renderer.enabled && isVerboseLine(line) {
					continue // Don't print verbose lines when using progress bar
				}
			}

			// For non-progress lines or when progress bar is disabled
			if renderer != nil && renderer.enabled {
				// Clear progress bar before printing regular line
				renderer.clearProgress()
			}
			_, _ = fmt.Fprintln(stdoutWriter, line) // Ignore errors writing to stdout
			if renderer != nil && renderer.enabled {
				// Re-render progress after printing line
				renderer.renderProgress(state)
			}
		}
		if err := scanner.Err(); err != nil {
			_, _ = fmt.Fprintf(stderrWriter, "[!] stdout scanner error: %v\n", err)
		}
		done <- true
	}()

	// Process stderr
	go func() {
		scanner := bufio.NewScanner(stderr)
		scanner.Split(scanLinesAndCR)
		for scanner.Scan() {
			line := scanner.Text()

			// Check for error line (failed downloads) when progress bar is enabled
			if renderer != nil && state != nil {
				if isErrorLine(line) {
					// Increment failure count for errors
					state.FailureCount++
				}
			}

			// Clear progress bar before printing error line
			if renderer != nil && renderer.enabled {
				renderer.clearProgress()
			}
			_, _ = fmt.Fprintln(stderrWriter, line) // Display line
			// Re-render progress bar after printing error line
			if renderer != nil && renderer.enabled {
				renderer.renderProgress(state)
			}
		}
		if err := scanner.Err(); err != nil {
			_, _ = fmt.Fprintf(stderrWriter, "[!] stderr scanner error: %v\n", err)
		}
		done <- true
	}()

	// Wait for both goroutines to finish
	<-done
	<-done

	// Clear progress bar when processing finishes
	if renderer != nil {
		renderer.clearProgress()
		_, _ = fmt.Fprintln(stdoutWriter) // Add newline after clearing
	}

	return nil
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
	for _, line := range lines {
		matches := ytdlpErrorPattern.FindStringSubmatch(line)
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
	matches := progressLinePattern.FindStringSubmatch(line)

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
		"% of ",
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

// scanLinesAndCR is a bufio.SplitFunc that splits on \n, \r\n, or bare \r.
// yt-dlp uses bare \r for in-place percentage updates (e.g., "[download]  50.2% of ~10MiB").
// The default bufio.ScanLines only splits on \n, so bare \r lines accumulate into a single
// token that can exceed the 64KB scanner buffer, silently killing the scanner.
func scanLinesAndCR(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	// Find the earliest \r or \n
	for i := 0; i < len(data); i++ {
		if data[i] == '\n' {
			return i + 1, data[:i], nil
		}
		if data[i] == '\r' {
			// Check for \r\n
			if i+1 < len(data) {
				if data[i+1] == '\n' {
					return i + 2, data[:i], nil
				}
				return i + 1, data[:i], nil
			}
			// \r at end of buffer - if at EOF, return it; otherwise request more data
			if atEOF {
				return len(data), data[:i], nil
			}
			return 0, nil, nil // need more data to check for \r\n
		}
	}
	// No delimiter found
	if atEOF {
		return len(data), data, nil
	}
	return 0, nil, nil // request more data
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

// getWriter returns the output writer, defaulting to os.Stdout
func (pr *ProgressRenderer) getWriter() io.Writer {
	if pr.writer != nil {
		return pr.writer
	}
	return os.Stdout
}

// buildProgressBar creates a progress bar string and percentage from current/total values
func buildProgressBar(current, total, barWidth int) (string, float64) {
	percentage := 0.0
	if total > 0 {
		percentage = float64(current) / float64(total) * 100
	}
	filledWidth := int(float64(barWidth) * percentage / 100)
	if filledWidth > barWidth {
		filledWidth = barWidth
	}
	bar := strings.Repeat("█", filledWidth) + strings.Repeat("░", barWidth-filledWidth)
	return bar, percentage
}

// writeLine writes a carriage-return-prefixed line and pads to clear any previous longer line
func (pr *ProgressRenderer) writeLine(line string) {
	if len(line) < pr.lastLineLen {
		line += strings.Repeat(" ", pr.lastLineLen-len(line))
	}
	pr.lastLineLen = len(line)
	_, _ = fmt.Fprint(pr.getWriter(), line)
}

// renderProgress displays a live progress bar using ANSI escape codes
// Format: "Downloading favorites (87/92) | ████████████░░░ 94.6% | Success: 85 | Failed: 2"
func (pr *ProgressRenderer) renderProgress(state *ProgressState) {
	if !pr.enabled {
		return
	}

	bar, percentage := buildProgressBar(state.CurrentIndex, state.TotalVideos, 20)

	green := "\033[32m"
	yellow := "\033[33m"
	red := "\033[31m"
	reset := "\033[0m"

	line := fmt.Sprintf("\rDownloading %s (%d/%d) | %s %.1f%% | %sSuccess: %d%s | %sSkipped: %d%s | %sFailed: %d%s",
		state.CollectionName,
		state.CurrentIndex, state.TotalVideos,
		bar, percentage,
		green, state.SuccessCount, reset,
		yellow, state.SkippedCount, reset,
		red, state.FailureCount, reset,
	)

	pr.writeLine(line)
}

// clearProgress clears the progress bar line
func (pr *ProgressRenderer) clearProgress() {
	if !pr.enabled || pr.lastLineLen == 0 {
		return
	}
	_, _ = fmt.Fprint(pr.getWriter(), "\r"+strings.Repeat(" ", pr.lastLineLen)+"\r")
	pr.lastLineLen = 0
}

// calculateSessionTotals aggregates totals across all collections
// Returns: attempted, success, failed, skipped, photosAttempted, photosSuccess, photosFailed
func calculateSessionTotals(collections []CollectionResult) (attempted, success, failed, skipped, photosAttempted, photosSuccess, photosFailed int) {
	for _, col := range collections {
		attempted += col.Attempted
		success += col.Success
		failed += col.Failed
		skipped += col.Skipped

		// Track photo-specific stats
		if col.ContentType == "photo" {
			photosAttempted += col.Attempted
			photosSuccess += col.Success
			photosFailed += col.Failed
		}
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

	// Calculate video-only stats (total minus photos)
	videoAttempted := session.TotalAttempted - session.TotalPhotosAttempted
	videoSuccess := session.TotalSuccess - session.TotalPhotosSuccess
	videoFailed := session.TotalFailed - session.TotalPhotosFailed

	// Show video stats
	fmt.Printf("Videos Attempted: %d\n", videoAttempted)
	fmt.Printf("  ✓ Successfully Downloaded: %d\n", videoSuccess)
	fmt.Printf("  - Skipped (Already Downloaded): %d\n", session.TotalSkipped)
	fmt.Printf("  ✗ Failed: %d\n", videoFailed)

	// Show photo stats if any photos were processed
	if session.TotalPhotosAttempted > 0 {
		fmt.Printf("\nPhotos Attempted: %d\n", session.TotalPhotosAttempted)
		fmt.Printf("  ✓ Successfully Downloaded: %d\n", session.TotalPhotosSuccess)
		fmt.Printf("  ✗ Failed: %d\n", session.TotalPhotosFailed)
	}
	fmt.Println()

	if len(session.Collections) > 1 {
		fmt.Println("Collection Breakdown:")
		for _, col := range session.Collections {
			contentType := col.ContentType
			if contentType == "" {
				contentType = "mixed"
			}
			fmt.Printf("  %s (%s):\n", col.Name, contentType)
			fmt.Printf("    Attempted: %-4d | Success: %-4d | Skipped: %-4d | Failed: %d\n",
				col.Attempted, col.Success, col.Skipped, col.Failed)
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
	_, _ = fmt.Fprintf(w, "Skipped: %d\n", session.TotalSkipped)
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
func runYtdlp(psPrefix, outputName string, config *Config, entries []VideoEntry) (*CollectionResult, error) {
	// Create progress renderer if enabled
	var renderer *ProgressRenderer
	var state *ProgressState
	if !config.DisableProgressBar && supportsANSI() {
		collectionName := filepath.Base(filepath.Dir(outputName))
		if collectionName == "." {
			collectionName = "videos"
		}
		renderer = &ProgressRenderer{
			enabled: true,
			writer:  os.Stdout,
		}
		state = &ProgressState{
			CollectionName: collectionName,
			TotalVideos:    len(entries),
		}
	}

	runner := &RealCommandRunner{
		ProgressRenderer: renderer,
		ProgressState:    state,
	}

	return runYtdlpWithRunner(runner, psPrefix, outputName, config, entries)
}

// runYtdlpWithRunner allows dependency injection for testing
func runYtdlpWithRunner(runner CommandRunner, psPrefix, outputName string, config *Config, entries []VideoEntry) (*CollectionResult, error) {
	collectionName := filepath.Base(filepath.Dir(outputName))
	if collectionName == "." {
		collectionName = "videos"
	}

	// Calculate archive file path
	var archivePath string
	if config.OrganizeByCollection {
		dir := filepath.Dir(outputName)
		archivePath = filepath.Join(dir, "download_archive.txt")
	} else {
		archivePath = "download_archive.txt"
	}

	// Optimization: Filter out already downloaded videos if resume is enabled
	videosToDownload := entries
	skippedCount := 0

	if !config.DisableResume {
		archive, err := parseArchiveFile(archivePath)
		if err == nil && len(archive) > 0 {
			var filtered []VideoEntry
			for _, entry := range entries {
				videoID := extractVideoID(entry.Link)
				// If ID found and in archive, skip
				if videoID != "" && archive[videoID] {
					skippedCount++
				} else {
					filtered = append(filtered, entry)
				}
			}
			videosToDownload = filtered
		}
	}

	// Update ProgressState if available
	if realRunner, ok := runner.(*RealCommandRunner); ok && realRunner.ProgressState != nil {
		realRunner.ProgressState.InitialSkipped = skippedCount
		realRunner.ProgressState.SkippedCount = skippedCount
		realRunner.ProgressState.CurrentIndex = skippedCount
		// TotalVideos remains len(entries)
	}

	// If all videos are skipped, we can return early
	if len(videosToDownload) == 0 {
		fmt.Printf("[*] %s collection: All %d videos already downloaded (skipping yt-dlp)\n",
			collectionName, len(entries))

		return &CollectionResult{
			Name:           collectionName,
			Attempted:      len(entries),
			Failed:         0,
			Success:        len(entries), // All considered success (skipped)
			Skipped:        len(entries),
			FailureDetails: []FailureDetail{},
		}, nil
	}

	// If we have skipped some but not all, notify user
	if skippedCount > 0 {
		fmt.Printf("[*] %s collection: %d videos to download (%d skipped)\n",
			collectionName, len(videosToDownload), skippedCount)
	}

	fmt.Println("[*] Running yt-dlp now...")
	cmdStr := fmt.Sprintf("%syt-dlp.exe", psPrefix)

	// Configure output format based on organization preference
	// New format includes video ID and truncated title for better identification
	var outputFormat string
	if config.OrganizeByCollection {
		// Include directory from outputName so videos download to collection folder
		dir := filepath.Dir(outputName)
		outputFormat = filepath.Join(dir, "%(upload_date)s_%(id)s_%(title).50B.%(ext)s")
	} else {
		// Flat structure with new format
		outputFormat = "%(upload_date)s_%(id)s_%(title).50B.%(ext)s"
	}

	// Determine which file to pass to yt-dlp
	targetFile := outputName

	// If we filtered the list, write a temporary file
	if skippedCount > 0 {
		tempFile := outputName + ".partial.txt"
		// Ensure directory exists (should already exist from main, but just in case)
		if config.OrganizeByCollection {
			_ = os.MkdirAll(filepath.Dir(tempFile), 0755)
		}

		if err := writeVideoEntriesToFile(videosToDownload, tempFile); err != nil {
			fmt.Printf("[!] Warning: Failed to create partial list: %v. Using full list.\n", err)
			// Fallback to full list, reset offsets
			if realRunner, ok := runner.(*RealCommandRunner); ok && realRunner.ProgressState != nil {
				realRunner.ProgressState.InitialSkipped = 0
				realRunner.ProgressState.SkippedCount = 0
				realRunner.ProgressState.CurrentIndex = 0
			}
		} else {
			targetFile = tempFile
			defer func() { _ = os.Remove(tempFile) }() // Clean up temp file
		}
	}

	// Build yt-dlp arguments with metadata options
	args := []string{
		"-a", targetFile,
		"--output", outputFormat,
		"--write-info-json", // Save metadata JSON for each video
		"--add-headers",
		"User-Agent:Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/146.0.7671.0 Safari/537.36", // set user-agent for chrome not yt-dlp's default
	}

	// Add thumbnail download unless skipped
	if !config.SkipThumbnails {
		args = append(args, "--write-thumbnail")
		args = append(args, "--convert-thumbnails", "jpg") // Ensure consistent .jpg extension
	}

	// Add cookie arguments if configured
	if config.CookieFile != "" {
		args = append(args, "--cookies", config.CookieFile)
	}
	if config.CookieFromBrowser != "" {
		args = append(args, "--cookies-from-browser", config.CookieFromBrowser)
	}

	// Add resume functionality flags unless disabled
	if !config.DisableResume {
		// Add flags for resume functionality
		args = append(args, "--download-archive", archivePath)
		args = append(args, "--no-overwrites")
		args = append(args, "--continue")
	}

	// Execute and capture output
	output, err := runner.Run(cmdStr, args...)

	// Parse output to extract failures
	failures := parseYtdlpOutput(output.Combined, videosToDownload)

	// Build result summary
	// Get final skipped count from state (includes those skipped by yt-dlp during run)
	finalSkipped := skippedCount
	if realRunner, ok := runner.(*RealCommandRunner); ok && realRunner.ProgressState != nil {
		finalSkipped = realRunner.ProgressState.SkippedCount
	}

	result := &CollectionResult{
		Name:           filepath.Base(filepath.Dir(outputName)),
		Attempted:      len(entries),
		Failed:         len(failures),
		Success:        len(entries) - len(failures) - finalSkipped,
		Skipped:        finalSkipped,
		FailureDetails: failures,
	}

	// Safety check for negative success count
	if result.Success < 0 {
		result.Success = 0
	}

	if err != nil || len(failures) > 0 {
		fmt.Printf("[!] Download completed with %d failures out of %d videos.\n",
			result.Failed, len(videosToDownload))
	} else {
		if skippedCount > 0 {
			fmt.Printf("[*] Successfully downloaded %d new videos.\n", result.Success)
		} else {
			fmt.Printf("[*] Successfully downloaded all %d videos.\n", result.Success)
		}
	}

	return result, err
}

// GalleryDlInfo represents metadata from gallery-dl's .json files
type GalleryDlInfo struct {
	ID          string `json:"id"`
	Description string `json:"description"`
	Uploader    string `json:"author"`
	UploaderID  string `json:"author_id"`
	Date        string `json:"date"`
	Category    string `json:"category"`
	Subcategory string `json:"subcategory"`
	Filename    string `json:"filename"`
	Extension   string `json:"extension"`
	ImageCount  int    `json:"count"`
}

// parseGalleryDlOutput parses gallery-dl output to detect success/failure
// gallery-dl outputs: "#<number> <url>" for successful downloads
// And "ERROR:" or "[error]" for failures
func parseGalleryDlOutput(lines []string, entries []VideoEntry) (success int, failures []FailureDetail) {
	// Build video ID to URL map
	idToURL := make(map[string]string)
	for _, entry := range entries {
		id := extractVideoID(entry.Link)
		if id != "" {
			idToURL[id] = entry.Link
		}
	}

	// Track which video IDs we've seen in success messages
	seenIDs := make(map[string]bool)

	for _, line := range lines {
		// Check for error lines
		if galleryErrorPattern.MatchString(line) {
			// Extract video ID from error line
			if matches := galleryVideoIDPattern.FindStringSubmatch(line); len(matches) > 1 {
				videoID := matches[1]
				if !seenIDs[videoID] {
					seenIDs[videoID] = true
					failures = append(failures, FailureDetail{
						VideoID:      videoID,
						VideoURL:     idToURL[videoID],
						ErrorMessage: line,
						ErrorType:    categorizeError(line),
					})
				}
			}
			continue
		}

		// Check for success lines (starts with #number)
		if gallerySuccessPattern.MatchString(line) {
			// Try to extract video ID from the line
			if matches := galleryVideoIDPattern.FindStringSubmatch(line); len(matches) > 1 {
				seenIDs[matches[1]] = true
			}
			success++
		}
	}

	return success, failures
}

// getPhotoArchivePath returns the path to the photo archive file for a given output directory.
func getPhotoArchivePath(outputDir string, organizeByCollection bool) string {
	if organizeByCollection {
		return filepath.Join(outputDir, "photo_archive.txt")
	}
	return "photo_archive.txt"
}

// appendToPhotoArchive appends successfully downloaded photo IDs to the photo archive file.
func appendToPhotoArchive(archivePath string, entries []VideoEntry) error {
	f, err := os.OpenFile(archivePath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open photo archive for writing: %v", err)
	}
	defer func() { _ = f.Close() }()

	for _, entry := range entries {
		videoID := extractVideoID(entry.Link)
		if videoID != "" {
			if _, err := fmt.Fprintf(f, "tiktok %s\n", videoID); err != nil {
				return fmt.Errorf("failed to write to photo archive: %v", err)
			}
		}
	}
	return nil
}

// runGalleryDl runs gallery-dl to download photo/slideshow posts
func runGalleryDl(psPrefix, outputDir string, config *Config, entries []VideoEntry) (*CollectionResult, error) {
	if len(entries) == 0 {
		return &CollectionResult{
			Name:           filepath.Base(outputDir),
			Attempted:      0,
			Success:        0,
			Failed:         0,
			Skipped:        0,
			FailureDetails: []FailureDetail{},
		}, nil
	}

	collectionName := filepath.Base(outputDir)
	if collectionName == "." {
		collectionName = "photos"
	}

	// Photo archive skip optimization (same concept as yt-dlp's download archive)
	archivePath := getPhotoArchivePath(outputDir, config.OrganizeByCollection)
	photosToDownload := entries
	skippedCount := 0

	if !config.DisableResume {
		archive, err := parseArchiveFile(archivePath)
		if err == nil && len(archive) > 0 {
			var filtered []VideoEntry
			for _, entry := range entries {
				videoID := extractVideoID(entry.Link)
				if videoID != "" && archive[videoID] {
					skippedCount++
				} else {
					filtered = append(filtered, entry)
				}
			}
			photosToDownload = filtered
		}
	}

	// If all photos are skipped, return early
	if len(photosToDownload) == 0 {
		fmt.Printf("[*] %s collection: All %d photos already downloaded (skipping gallery-dl)\n",
			collectionName, len(entries))
		return &CollectionResult{
			Name:           collectionName,
			Attempted:      len(entries),
			Success:        len(entries),
			Failed:         0,
			Skipped:        len(entries),
			FailureDetails: []FailureDetail{},
		}, nil
	}

	if skippedCount > 0 {
		fmt.Printf("[*] %s collection: %d photos to download (%d skipped)\n",
			collectionName, len(photosToDownload), skippedCount)
	}

	fmt.Printf("[*] Running gallery-dl for %d photo posts in %s...\n", len(photosToDownload), collectionName)
	cmdStr := fmt.Sprintf("%sgallery-dl.exe", psPrefix)

	// Write URLs to temp file
	tempFile := filepath.Join(outputDir, "photo_urls_temp.txt")
	if err := writeVideoEntriesToFile(photosToDownload, tempFile); err != nil {
		return nil, fmt.Errorf("failed to create temp URL file: %v", err)
	}
	defer func() { _ = os.Remove(tempFile) }()

	// Build gallery-dl arguments
	// gallery-dl uses different filename formatting than yt-dlp
	// Format: {date:%Y%m%d}_{id}_{title:.50}.{extension}
	filenameFormat := "{date:%Y%m%d}_{id}_{description:.50}.{extension}"

	args := []string{
		"--input-file", tempFile,
		"--directory", outputDir,
		"--filename", filenameFormat,
		"--write-metadata", // Save metadata JSON for each photo
	}

	// Add cookie support
	if config.CookieFile != "" {
		args = append(args, "--cookies", config.CookieFile)
	}
	if config.CookieFromBrowser != "" {
		args = append(args, "--cookies-from-browser", config.CookieFromBrowser)
	}

	// Execute command
	cmd := exec.Command(cmdStr, args...)
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = io.MultiWriter(os.Stdout, &stdoutBuf)
	cmd.Stderr = io.MultiWriter(os.Stderr, &stderrBuf)

	err := cmd.Run()

	// Parse output
	combined := combineOutputLines(stdoutBuf.String(), stderrBuf.String())
	_, failures := parseGalleryDlOutput(combined, photosToDownload)

	// Append succeeded entries to photo archive (for future skip optimization)
	if !config.DisableResume && len(photosToDownload) > len(failures) {
		// Identify succeeded entries by checking which video IDs now have files on disk
		var succeededEntries []VideoEntry
		for _, entry := range photosToDownload {
			videoID := extractVideoID(entry.Link)
			if videoID == "" {
				continue
			}
			// Check if files were downloaded for this ID
			ct := inferContentTypeFromFiles(outputDir, videoID)
			if ct == "photo" {
				succeededEntries = append(succeededEntries, entry)
			}
		}
		if len(succeededEntries) > 0 {
			if archiveErr := appendToPhotoArchive(archivePath, succeededEntries); archiveErr != nil {
				fmt.Printf("[!] Warning: Failed to update photo archive: %v\n", archiveErr)
			}
		}
	}

	result := &CollectionResult{
		Name:           collectionName,
		Attempted:      len(entries),
		Failed:         len(failures),
		Success:        len(entries) - len(failures),
		Skipped:        skippedCount,
		FailureDetails: failures,
	}

	// Safety check
	if result.Success < 0 {
		result.Success = 0
	}
	if result.Skipped < 0 {
		result.Skipped = 0
	}

	if err != nil || len(failures) > 0 {
		fmt.Printf("[!] Photo download completed with %d failures out of %d photos.\n",
			result.Failed, len(photosToDownload))
	} else {
		fmt.Printf("[*] Successfully downloaded all %d photos.\n", result.Success-result.Skipped)
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
// scanPhotoFiles finds downloaded image files and an audio file for a photo post
// by globbing the collection directory for files matching the video ID.
func scanPhotoFiles(collectionDir, videoID string) (imageFiles []string, audioFile string) {
	pattern := filepath.Join(collectionDir, fmt.Sprintf("*%s*", videoID))
	matches, _ := filepath.Glob(pattern)
	for _, match := range matches {
		ext := strings.ToLower(filepath.Ext(match))
		switch ext {
		case ".jpg", ".jpeg", ".png", ".webp":
			imageFiles = append(imageFiles, filepath.Base(match))
		case ".m4a", ".mp3":
			audioFile = filepath.Base(match)
		}
	}
	return
}

// It enriches entries with metadata from yt-dlp's .info.json files and gallery-dl's .json files,
// then generates both index.json (machine-readable) and index.html (visual browser) files.
func generateCollectionIndex(collectionDir string, entries []VideoEntry, failures []FailureDetail) error {
	collectionName := filepath.Base(collectionDir)
	videos, photos := separateEntriesByContentType(entries)
	fmt.Printf("[*] Generating index for %s (%d videos, %d photos)...\n", collectionName, len(videos), len(photos))

	// 1. Scan for yt-dlp .info.json files in the directory
	infoFiles, err := filepath.Glob(filepath.Join(collectionDir, "*.info.json"))
	if err != nil {
		return fmt.Errorf("collection %q: error scanning for info files: %v", collectionName, err)
	}

	// 2. Build video ID to info map from yt-dlp metadata
	infoMap := make(map[string]*YtdlpInfo)
	for _, f := range infoFiles {
		info, err := parseInfoJSON(f)
		if err != nil {
			fmt.Printf("[!] Warning: Failed to parse %s: %v\n", f, err)
			continue
		}
		infoMap[info.ID] = info
	}

	// 3. Scan for gallery-dl .json metadata files (for photos)
	// gallery-dl creates files like: <filename>.json alongside downloaded images
	galleryDlFiles, _ := filepath.Glob(filepath.Join(collectionDir, "*.json"))
	photoInfoMap := make(map[string]*GalleryDlInfo)
	for _, f := range galleryDlFiles {
		// Skip yt-dlp info files
		if strings.HasSuffix(f, ".info.json") || f == filepath.Join(collectionDir, "index.json") {
			continue
		}
		data, err := os.ReadFile(f)
		if err != nil {
			continue
		}
		var info GalleryDlInfo
		if err := json.Unmarshal(data, &info); err != nil {
			continue
		}
		// gallery-dl uses string IDs
		if info.ID != "" {
			photoInfoMap[info.ID] = &info
		}
	}

	totalMetadataFiles := len(infoMap) + len(photoInfoMap)
	fmt.Printf("[*] Found %d metadata files for %s\n", totalMetadataFiles, collectionName)

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

		// Handle photos differently from videos
		if enrichedEntries[i].ContentType == "photo" {
			// Look for gallery-dl metadata
			if info, ok := photoInfoMap[videoID]; ok {
				enrichedEntries[i].Description = info.Description
				enrichedEntries[i].Creator = info.Uploader
				enrichedEntries[i].CreatorID = info.UploaderID
				enrichedEntries[i].ImageCount = info.ImageCount

				// Find downloaded image and audio files for this photo post
				imageFiles, audioFile := scanPhotoFiles(collectionDir, videoID)
				enrichedEntries[i].AudioFile = audioFile
				enrichedEntries[i].ImageFiles = imageFiles
				if len(imageFiles) > 0 {
					enrichedEntries[i].LocalFilename = imageFiles[0]
					enrichedEntries[i].ThumbnailFile = imageFiles[0]
					enrichedEntries[i].Downloaded = true
				} else {
					enrichedEntries[i].Downloaded = false
					if errMsg, ok := failureMap[videoID]; ok {
						enrichedEntries[i].DownloadError = errMsg
					} else {
						enrichedEntries[i].DownloadError = "Photo files not found"
					}
				}
			} else {
				// No gallery-dl metadata, but try to find image files by video ID
				imageFiles, audioFile := scanPhotoFiles(collectionDir, videoID)
				enrichedEntries[i].AudioFile = audioFile
				if len(imageFiles) > 0 {
					enrichedEntries[i].ImageFiles = imageFiles
					enrichedEntries[i].LocalFilename = imageFiles[0]
					enrichedEntries[i].ThumbnailFile = imageFiles[0]
					enrichedEntries[i].Downloaded = true
				} else {
					enrichedEntries[i].Downloaded = false
					if errMsg, ok := failureMap[videoID]; ok {
						enrichedEntries[i].DownloadError = errMsg
					} else {
						enrichedEntries[i].DownloadError = "Photo not downloaded or metadata unavailable"
					}
				}
			}
			continue
		}

		// Handle videos (existing logic)
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
				// Normalize path separators before extracting basename
				// yt-dlp may write Windows-style paths (\) in .info.json even on Unix systems
				// (e.g., if the file was created on Windows and read on Linux, or vice versa)
				normalizedFilename := strings.ReplaceAll(info.Filename, "\\", "/")
				baseFilename = filepath.Base(normalizedFilename)
				enrichedEntries[i].LocalFilename = baseFilename
			} else {
				// Fallback: If filename is not in .info.json, try to find the video file by video ID
				// This handles cases where yt-dlp doesn't populate the filename field
				// Look for files matching the pattern: *_<videoID>_*.mp4 (or other video extensions)
				pattern := filepath.Join(collectionDir, fmt.Sprintf("*_%s_*", videoID))
				matches, err := filepath.Glob(pattern + ".*")
				if err == nil && len(matches) > 0 {
					// Found potential matches - filter for video files (exclude .info.json, .part, .ytdl, etc.)
					for _, match := range matches {
						ext := strings.ToLower(filepath.Ext(match))
						if ext == ".mp4" || ext == ".mkv" || ext == ".webm" || ext == ".mov" {
							baseFilename = filepath.Base(match)
							enrichedEntries[i].LocalFilename = baseFilename
							break
						}
					}
				}
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

		// Apply content types from cache and file inference for proper index generation
		indexCache := loadContentTypeCache("content_types_cache.json")
		unknownCount := applyContentTypesFromCache(videoEntries, indexCache, ".", config.OrganizeByCollection)
		if unknownCount > 0 {
			fmt.Printf("[*] %d entries have unknown content type (no cache or files found)\n", unknownCount)
		}

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

		// Save any updated cache entries from file inference
		if err := saveContentTypeCache("content_types_cache.json", indexCache); err != nil {
			fmt.Printf("[!] Warning: could not save content type cache: %v\n", err)
		}
		return
	}

	// Check if yt-dlp already exists before attempting to get/download
	// If it exists, we'll run it automatically later; if not, we'll ask the user
	ytdlpExistedBefore := false
	if _, err := os.Stat("yt-dlp.exe"); err == nil {
		ytdlpExistedBefore = true
	}

	// Attempt to get or download yt-dlp.exe (handles updates for existing files)
	if err := getOrDownloadTool(http.DefaultClient, &ToolConfig{
		Name:            "yt-dlp",
		ExeName:         "yt-dlp.exe",
		GitHubRepo:      "yt-dlp/yt-dlp",
		GetVersion:      getYtdlpVersion,
		CompareVersions: compareVersions,
		SelfUpdate:      updateYtdlp,
	}); err != nil {
		fmt.Printf("[!] Warning: %v\n", err)
		// Not exiting here so you can still generate fav_videos.txt if needed
	}

	// Also get gallery-dl for photo support
	galleryDlAvailable := false
	if err := getOrDownloadTool(http.DefaultClient, &ToolConfig{
		Name:            "gallery-dl",
		ExeName:         "gallery-dl.exe",
		GitHubRepo:      "mikf/gallery-dl",
		GetVersion:      getGalleryDlVersion,
		CompareVersions: compareGalleryDlVersions,
		StripVPrefix:    true,
	}); err != nil {
		fmt.Printf("[!] Warning: gallery-dl not available, photo posts will be skipped: %v\n", err)
	} else {
		galleryDlAvailable = true
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

	// Apply known content types from cache and file inference (no network requests)
	cacheFilePath := "content_types_cache.json"
	cache := loadContentTypeCache(cacheFilePath)
	unknownCount := applyContentTypesFromCache(videoEntries, cache, ".", config.OrganizeByCollection)

	// Count known videos and photos
	knownVideos, knownPhotos := separateEntriesByContentType(videoEntries)
	cachedVideoCount := 0
	cachedPhotoCount := 0
	for _, e := range knownVideos {
		if e.ContentType == "video" {
			cachedVideoCount++
		}
	}
	cachedPhotoCount = len(knownPhotos)

	if cachedVideoCount+cachedPhotoCount > 0 {
		fmt.Printf("[*] Content types: %d videos, %d photos from cache/files", cachedVideoCount, cachedPhotoCount)
		if unknownCount > 0 {
			fmt.Printf(", %d unknown (will try yt-dlp first)", unknownCount)
		}
		fmt.Println()
	} else if unknownCount > 0 {
		fmt.Printf("[*] No cached content types found. All %d URLs will be sent to yt-dlp first.\n", unknownCount)
	}

	// Write ALL video entries to files (unknowns go in the video file for yt-dlp)
	if err := writeFavoriteVideosToFile(videoEntries, config.OutputName, config.OrganizeByCollection); err != nil {
		fmt.Println(err)
		os.Exit(1)
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

	// If yt-dlp already existed, run automatically; otherwise ask user
	shouldRunYtdlp := false
	if ytdlpExistedBefore {
		// yt-dlp already existed before we started - run automatically
		fmt.Println("\n[*] Starting download with yt-dlp...")
		shouldRunYtdlp = true
	} else if _, err := os.Stat("yt-dlp.exe"); err == nil {
		// yt-dlp was just downloaded by getOrDownloadYtdlp - ask user if they want to run it
		fmt.Print("\n*** yt-dlp.exe was downloaded. Would you like me to run it for you? (y/n): ")
		answer := bufio.NewReader(os.Stdin)
		response, _ := answer.ReadString('\n')
		response = strings.TrimSpace(strings.ToLower(response))
		if response == "y" || response == "yes" {
			shouldRunYtdlp = true
		}
	}

	if shouldRunYtdlp {
		// Initialize download session tracking
		session := &DownloadSession{
			StartTime:   time.Now(),
			Collections: make([]CollectionResult, 0),
		}

		if config.OrganizeByCollection {
			// Run yt-dlp first, then gallery-dl fallback for each collection
			collections := make(map[string]bool)
			for _, entry := range videoEntries {
				collections[sanitizeCollectionName(entry.Collection)] = true
			}
			for collection := range collections {
				collectionEntries := getEntriesForCollection(videoEntries, collection)

				// Separate known-video, known-photo, and unknown entries
				var knownVideoEntries, knownPhotoEntries, unknownEntries []VideoEntry
				for _, entry := range collectionEntries {
					switch entry.ContentType {
					case "video":
						knownVideoEntries = append(knownVideoEntries, entry)
					case "photo":
						knownPhotoEntries = append(knownPhotoEntries, entry)
					default:
						unknownEntries = append(unknownEntries, entry)
					}
				}

				var allFailures []FailureDetail

				// Phase 1: yt-dlp pass (known videos + unknown entries)
				ytdlpEntries := append(knownVideoEntries, unknownEntries...)
				if len(ytdlpEntries) > 0 {
					collectionFilename := getVideoOutputFilename(collection)
					collectionOutputName := filepath.Join(collection, collectionFilename)

					// Write the combined list for yt-dlp
					if err := writeVideoEntriesToFile(ytdlpEntries, collectionOutputName); err != nil {
						fmt.Printf("[!] Error writing URL file: %v\n", err)
						continue
					}

					// Snapshot archive before yt-dlp
					archivePath := filepath.Join(collection, "download_archive.txt")
					archiveBefore, _ := parseArchiveFile(archivePath)

					fmt.Printf("[*] Processing collection: %s (%d videos + %d unknown)\n",
						collection, len(knownVideoEntries), len(unknownEntries))
					result, _ := runYtdlp(psPrefix, collectionOutputName, config, ytdlpEntries)

					// Track session results
					if result != nil {
						result.ContentType = "video"
						session.Collections = append(session.Collections, *result)
						allFailures = append(allFailures, result.FailureDetails...)
					}

					// Phase 1b: Diff archive to identify failed unknowns
					if len(unknownEntries) > 0 {
						archiveAfter, _ := parseArchiveFile(archivePath)
						succeeded, _, failed := identifyFailedEntries(unknownEntries, archiveBefore, archiveAfter)

						// Update cache: succeeded unknowns are videos
						for _, entry := range succeeded {
							cache[entry.Link] = "video"
						}

						// Failed unknowns become candidates for gallery-dl
						if len(failed) > 0 && galleryDlAvailable {
							knownPhotoEntries = append(knownPhotoEntries, failed...)
						} else {
							// No gallery-dl: update cache for failed unknowns that have files on disk
							for _, entry := range failed {
								videoID := extractVideoID(entry.Link)
								if ct := inferContentTypeFromFiles(collection, videoID); ct != "" {
									cache[entry.Link] = ct
								}
							}
						}
					}
				}

				// Phase 2: gallery-dl pass (known photos + failed unknowns from yt-dlp)
				if len(knownPhotoEntries) > 0 && galleryDlAvailable {
					fmt.Printf("[*] Processing collection: %s (%d photos)\n", collection, len(knownPhotoEntries))
					result, _ := runGalleryDl(psPrefix, collection, config, knownPhotoEntries)

					// Track session results
					if result != nil {
						result.ContentType = "photo"
						session.Collections = append(session.Collections, *result)
						allFailures = append(allFailures, result.FailureDetails...)
					}

					// Update cache: entries that gallery-dl succeeded on are photos
					for _, entry := range knownPhotoEntries {
						videoID := extractVideoID(entry.Link)
						if ct := inferContentTypeFromFiles(collection, videoID); ct == "photo" {
							cache[entry.Link] = "photo"
						}
					}
				}

				// Phase 3: Save cache, generate index
				if err := saveContentTypeCache(cacheFilePath, cache); err != nil {
					fmt.Printf("[!] Warning: could not save content type cache: %v\n", err)
				}

				// Re-apply content types to entries for index generation
				for i := range collectionEntries {
					if ct, ok := cache[collectionEntries[i].Link]; ok {
						collectionEntries[i].ContentType = ct
					}
				}

				if err := generateCollectionIndex(collection, collectionEntries, allFailures); err != nil {
					fmt.Printf("[!] Warning: Failed to generate index for %s: %v\n", collection, err)
				} else {
					fmt.Printf("[*] Generated index.html and index.json for %s\n", collection)
				}
			}
		} else {
			// Flat structure - same try-then-fallback approach
			var knownVideoEntries, knownPhotoEntries, unknownEntries []VideoEntry
			for _, entry := range videoEntries {
				switch entry.ContentType {
				case "video":
					knownVideoEntries = append(knownVideoEntries, entry)
				case "photo":
					knownPhotoEntries = append(knownPhotoEntries, entry)
				default:
					unknownEntries = append(unknownEntries, entry)
				}
			}

			var allFailures []FailureDetail

			// Phase 1: yt-dlp pass (known videos + unknown entries)
			ytdlpEntries := append(knownVideoEntries, unknownEntries...)
			if len(ytdlpEntries) > 0 {
				// Write the combined list for yt-dlp
				if err := writeVideoEntriesToFile(ytdlpEntries, config.OutputName); err != nil {
					fmt.Printf("[!] Error writing URL file: %v\n", err)
				} else {
					// Snapshot archive before yt-dlp
					archiveBefore, _ := parseArchiveFile("download_archive.txt")

					result, _ := runYtdlp(psPrefix, config.OutputName, config, ytdlpEntries)

					if result != nil {
						result.ContentType = "video"
						session.Collections = append(session.Collections, *result)
						allFailures = append(allFailures, result.FailureDetails...)
					}

					// Phase 1b: Diff archive to identify failed unknowns
					if len(unknownEntries) > 0 {
						archiveAfter, _ := parseArchiveFile("download_archive.txt")
						succeeded, _, failed := identifyFailedEntries(unknownEntries, archiveBefore, archiveAfter)

						for _, entry := range succeeded {
							cache[entry.Link] = "video"
						}

						if len(failed) > 0 && galleryDlAvailable {
							knownPhotoEntries = append(knownPhotoEntries, failed...)
						} else {
							dir, _ := filepath.Abs(".")
							for _, entry := range failed {
								videoID := extractVideoID(entry.Link)
								if ct := inferContentTypeFromFiles(dir, videoID); ct != "" {
									cache[entry.Link] = ct
								}
							}
						}
					}
				}
			}

			// Phase 2: gallery-dl pass
			if len(knownPhotoEntries) > 0 && galleryDlAvailable {
				fmt.Printf("[*] Processing %d photos with gallery-dl...\n", len(knownPhotoEntries))
				dir, _ := filepath.Abs(".")
				result, _ := runGalleryDl(psPrefix, dir, config, knownPhotoEntries)

				if result != nil {
					result.ContentType = "photo"
					session.Collections = append(session.Collections, *result)
					allFailures = append(allFailures, result.FailureDetails...)
				}

				for _, entry := range knownPhotoEntries {
					videoID := extractVideoID(entry.Link)
					absDir, _ := filepath.Abs(".")
					if ct := inferContentTypeFromFiles(absDir, videoID); ct == "photo" {
						cache[entry.Link] = "photo"
					}
				}
			}

			// Phase 3: Save cache, generate index
			if err := saveContentTypeCache(cacheFilePath, cache); err != nil {
				fmt.Printf("[!] Warning: could not save content type cache: %v\n", err)
			}

			// Re-apply content types to entries for index generation
			for i := range videoEntries {
				if ct, ok := cache[videoEntries[i].Link]; ok {
					videoEntries[i].ContentType = ct
				}
			}

			dir, err := filepath.Abs(".")
			if err != nil {
				dir = "."
			}
			if err := generateCollectionIndex(dir, videoEntries, allFailures); err != nil {
				fmt.Printf("[!] Warning: Failed to generate index: %v\n", err)
			} else {
				fmt.Println("[*] Generated index.html and index.json")
			}
		}

		// Finalize session
		session.EndTime = time.Now()
		session.TotalAttempted, session.TotalSuccess, session.TotalFailed, session.TotalSkipped,
			session.TotalPhotosAttempted, session.TotalPhotosSuccess, session.TotalPhotosFailed =
			calculateSessionTotals(session.Collections)

		// Print summary
		printSessionSummary(session)
		// Write results.txt
		if err := writeResultsFile(session); err != nil {
			fmt.Printf("[!] Warning: Failed to write results.txt: %v\n", err)
		}
	}
}
