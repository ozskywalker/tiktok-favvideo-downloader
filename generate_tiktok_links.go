package main

import (
	"bufio"
	"encoding/json"
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
				Date string `json:"Date"`
				Link string `json:"Link"`
			} `json:"ItemFavoriteList"`
		} `json:"Like List"`
	} `json:"Activity"`
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

// parseFavoriteVideosFromFile reads the given JSON file and returns the list of favorite video URLs.
func parseFavoriteVideosFromFile(jsonFile string, includeLiked bool) ([]string, error) {
	file, err := os.Open(filepath.Clean(jsonFile))
	if err != nil {
		return nil, fmt.Errorf("error opening JSON file: %v", err)
	}
	defer file.Close()

	var data Data
	if err := json.NewDecoder(file).Decode(&data); err != nil {
		return nil, fmt.Errorf("error parsing JSON: %v", err)
	}

	videoURLs := make([]string, 0)

	// Always add favorited videos
	for _, item := range data.Activity.FavoriteVideos.FavoriteVideoList {
		videoURLs = append(videoURLs, item.Link)
	}

	// Add liked videos if the user requested them
	if includeLiked {
		for _, item := range data.Activity.LikedVideos.ItemFavoriteList {
			videoURLs = append(videoURLs, item.Link)
		}
	}

	return videoURLs, nil
}

// writeFavoriteVideosToFile writes the video URLs into the given output file.
func writeFavoriteVideosToFile(videoURLs []string, outputName string) error {
	outFile, err := os.Create(outputName)
	if err != nil {
		return fmt.Errorf("[!!!] Error creating %s: %v", outputName, err)
	}
	defer outFile.Close()

	for _, url := range videoURLs {
		_, writeErr := outFile.WriteString(url + "\n")
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
func runYtdlp(psPrefix, outputName string) {
	runYtdlpWithRunner(&RealCommandRunner{}, psPrefix, outputName)
}

// runYtdlpWithRunner allows dependency injection for testing
func runYtdlpWithRunner(runner CommandRunner, psPrefix, outputName string) {
	fmt.Println("[*] Running yt-dlp now...")
	cmdStr := fmt.Sprintf("%syt-dlp.exe", psPrefix)
	if err := runner.Run(cmdStr, "-a", outputName, "--output", "%(upload_date)s_%(uploader_id)s.%(ext)s"); err != nil {
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

// printUsage prints basic usage info for this program.
func printUsage() {
	exeName := getExeName()

	fmt.Println("\nUsage:")
	fmt.Printf("  %s [optional path to user_data_tiktok.json]\n", exeName)
	fmt.Println("\nExamples:")
	fmt.Println("  1) Double-click (no arguments) if 'user_data_tiktok.json' is in the same folder.")
	fmt.Printf("  2) Or drag & drop a JSON file onto '%s' to specify a different JSON file.\n", exeName)
	fmt.Printf("  3) Or run from command line: %s path\\to\\my_tiktok_data.json\n", exeName)
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

	// Default JSON file
	jsonFile := "user_data_tiktok.json"

	// If an argument is given, treat that as the path to the JSON
	if len(os.Args) > 1 {
		// If user passes -h or --help, just print usage.
		if os.Args[1] == "-h" || os.Args[1] == "--help" {
			printUsage()
			return
		}
		jsonFile = os.Args[1]
	}

	// Check if JSON file exists before proceeding
	if _, err := os.Stat(jsonFile); os.IsNotExist(err) {
		fmt.Printf("[!!!] Error: JSON file '%s' does not exist.\n", jsonFile)
		printUsage()
		os.Exit(1)
	}

	// Attempt to get or download yt-dlp.exe
	if err := getOrDownloadYtdlp(http.DefaultClient, "yt-dlp.exe"); err != nil {
		fmt.Printf("[!] Warning: %v\n", err)
		// Not exiting here so you can still generate fav_videos.txt if needed
	}

	includeLiked := false
	fmt.Print("[*] Would you like to include 'Liked' videos as well? (y/n, default is 'n'): ")
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Scan()
	input := strings.TrimSpace(strings.ToLower(scanner.Text()))
	// Update includeLiked to true if the input is "y"
	if input == "y" || input == "yes" {
		includeLiked = true
	}

	// Extract video URLs
	videoURLs, err := parseFavoriteVideosFromFile(jsonFile, includeLiked)
	if err != nil {
		fmt.Printf("[!!!] Error parsing JSON. Are you sure '%s' is valid JSON?\n", jsonFile)
		fmt.Printf("Details: %v\n", err)
		os.Exit(1)
	}

	fmt.Printf("[*] Successfully loaded %d favorite video entries from '%s'\n", len(videoURLs), jsonFile)

	// Write them to fav_videos.txt
	outputName := "fav_videos.txt"
	if err := writeFavoriteVideosToFile(videoURLs, outputName); err != nil {
		fmt.Println(err)
		os.Exit(1)
	}
	fmt.Printf("[*] Extracted %d video URLs to '%s'.\n", len(videoURLs), outputName)

	// Construct the recommended yt-dlp command
	psPrefix := ""
	if isRunningInPowershell() {
		psPrefix = ".\\"
	}
	ytDlpCmd := fmt.Sprintf("%syt-dlp.exe -a \"%s\" --output \"%%(upload_date)s_%%(uploader_id)s.%%(ext)s\"", psPrefix, outputName)

	fmt.Println("[*] Done! You can now run yt-dlp like this:")
	fmt.Printf("  %s\n", ytDlpCmd)

	// Offer to run the command automatically
	fmt.Print("\n*** Would you like me to run yt-dlp for you instead? (y/n): ")
	answer := bufio.NewReader(os.Stdin)
	response, _ := answer.ReadString('\n')
	response = strings.TrimSpace(strings.ToLower(response))
	if response == "y" || response == "yes" {
		runYtdlp(psPrefix, outputName)
	}
}
