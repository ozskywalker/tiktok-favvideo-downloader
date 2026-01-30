package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Test utility helpers to reduce code duplication

// setupTestDir creates a temporary directory for testing and returns a cleanup function
func setupTestDir(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir, err := os.MkdirTemp("", "test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	return tmpDir, func() { _ = os.RemoveAll(tmpDir) }
}

// setupTestDirWithChdir creates a temporary directory, changes to it, and returns cleanup function
func setupTestDirWithChdir(t *testing.T) (string, func()) {
	t.Helper()
	tmpDir, cleanup := setupTestDir(t)

	oldCwd, err := os.Getwd()
	if err != nil {
		cleanup()
		t.Fatalf("failed to get working directory: %v", err)
	}

	if err := os.Chdir(tmpDir); err != nil {
		cleanup()
		t.Fatalf("failed to chdir: %v", err)
	}

	return tmpDir, func() {
		_ = os.Chdir(oldCwd)
		cleanup()
	}
}

// createMockInfoJSON creates a mock .info.json file for testing
func createMockInfoJSON(t *testing.T, dir, videoID, title, filename string) {
	t.Helper()
	infoJSON := fmt.Sprintf(`{
		"id": "%s",
		"title": "%s",
		"uploader": "TestUser",
		"uploader_id": "testuser123",
		"upload_date": "20260129",
		"description": "Test description",
		"duration": 45,
		"view_count": 1500000,
		"like_count": 50000,
		"thumbnail": "https://example.com/thumb.jpg",
		"filename": "%s"
	}`, videoID, title, filename)

	infoPath := filepath.Join(dir, filename+".info.json")
	if err := os.WriteFile(infoPath, []byte(infoJSON), 0644); err != nil {
		t.Fatalf("failed to write info.json: %v", err)
	}
}

// createMockVideoFile creates a mock video file for testing
func createMockVideoFile(t *testing.T, dir, filename string) {
	t.Helper()
	videoPath := filepath.Join(dir, filename)
	if err := os.WriteFile(videoPath, []byte("fake video data"), 0644); err != nil {
		t.Fatalf("failed to write video file: %v", err)
	}
}

// readCollectionIndex reads and parses a collection index.json file
func readCollectionIndex(t *testing.T, dir string) *CollectionIndex {
	t.Helper()
	indexPath := filepath.Join(dir, "index.json")
	data, err := os.ReadFile(indexPath)
	if err != nil {
		t.Fatalf("failed to read index.json: %v", err)
	}

	var index CollectionIndex
	if err := json.Unmarshal(data, &index); err != nil {
		t.Fatalf("failed to parse index.json: %v", err)
	}
	return &index
}

// TestIsRunningInPowershell checks if isRunningInPowershell returns
// true/false based on the environment variable. We manipulate the environment.
func TestIsRunningInPowershell(t *testing.T) {
	// Backup original PSModulePath
	originalPSModulePath := os.Getenv("PSModulePath")
	defer func() { _ = os.Setenv("PSModulePath", originalPSModulePath) }()

	// Case 1: Contains "PowerShell"
	_ = os.Setenv("PSModulePath", "C:\\Windows\\PowerShell\\Modules")
	if !isRunningInPowershell() {
		t.Error("expected isRunningInPowershell to return true when PSModulePath contains 'PowerShell'")
	}

	// Case 2: Does not contain "PowerShell"
	_ = os.Setenv("PSModulePath", "SomeRandomPath")
	if isRunningInPowershell() {
		t.Error("expected isRunningInPowershell to return false when PSModulePath does NOT contain 'PowerShell'")
	}
}

// TestGetExeName is pretty straightforward: we just ensure it returns
// some non-empty string.
func TestGetExeName(t *testing.T) {
	exeName := getExeName()
	if exeName == "" {
		t.Error("expected getExeName to return a non-empty string")
	}
}

// TestParseFavoriteVideosFromFile verifies that we can parse JSON data correctly.
func TestParseFavoriteVideosFromFile(t *testing.T) {
	// Create a temporary JSON file
	tmpFile, err := os.CreateTemp("", "testdata_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer func() { _ = os.Remove(tmpFile.Name()) }()

	// Write JSON data that includes both favorited and liked videos
	jsonContent := `{
		"Likes and Favorites": {
			"Favorite Videos": {
				"FavoriteVideoList": [
					{"Link": "https://www.tiktok.com/@someone/video/1"},
					{"Link": "https://www.tiktok.com/@someone/video/2"}
				]
			},
			"Like List": {
				"ItemFavoriteList": [
					{"date": "2023-01-01", "link": "https://www.tiktok.com/@someone/liked/1"},
					{"date": "2023-01-02", "link": "https://www.tiktok.com/@someone/liked/2"}
				]
			}
		}
	}`
	if _, err := tmpFile.WriteString(jsonContent); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	_ = tmpFile.Close()

	// Test case: only favorited videos
	videoEntries, err := parseFavoriteVideosFromFile(tmpFile.Name(), false)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if len(videoEntries) != 2 {
		t.Errorf("expected 2 favorited video entries, got %d", len(videoEntries))
	}
	if videoEntries[0].Link != "https://www.tiktok.com/@someone/video/1" {
		t.Errorf("unexpected first favorited link: %s", videoEntries[0].Link)
	}
	if videoEntries[0].Collection != "favorites" {
		t.Errorf("unexpected first collection: %s", videoEntries[0].Collection)
	}
	if videoEntries[1].Link != "https://www.tiktok.com/@someone/video/2" {
		t.Errorf("unexpected second favorited link: %s", videoEntries[1].Link)
	}
	if videoEntries[1].Collection != "favorites" {
		t.Errorf("unexpected second collection: %s", videoEntries[1].Collection)
	}

	// Test case: favorited and liked videos
	videoEntries, err = parseFavoriteVideosFromFile(tmpFile.Name(), true)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if len(videoEntries) != 4 {
		t.Errorf("expected 4 total video entries, got %d", len(videoEntries))
	}
	if videoEntries[2].Link != "https://www.tiktok.com/@someone/liked/1" {
		t.Errorf("unexpected third link: %s", videoEntries[2].Link)
	}
	if videoEntries[2].Collection != "liked" {
		t.Errorf("unexpected third collection: %s", videoEntries[2].Collection)
	}
	if videoEntries[3].Link != "https://www.tiktok.com/@someone/liked/2" {
		t.Errorf("unexpected fourth link: %s", videoEntries[3].Link)
	}
	if videoEntries[3].Collection != "liked" {
		t.Errorf("unexpected fourth collection: %s", videoEntries[3].Collection)
	}
}

// TestWriteFavoriteVideosToFile checks that we write URLs to file properly.
func TestWriteFavoriteVideosToFile(t *testing.T) {
	// Create a temp output file
	tmpOut, err := os.CreateTemp("", "fav_videos_*.txt")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	outputName := tmpOut.Name()
	_ = tmpOut.Close()
	defer func() { _ = os.Remove(outputName) }()

	// We'll write these URLs
	urls := []string{"https://abc", "https://def", "https://xyz"}

	// Convert URLs to VideoEntries for testing
	videoEntries := make([]VideoEntry, len(urls))
	for i, url := range urls {
		videoEntries[i] = VideoEntry{Link: url, Collection: "test"}
	}

	// Perform the write (flat structure for this test)
	if err := writeFavoriteVideosToFile(videoEntries, outputName, false); err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	// Verify the contents
	content, err := os.ReadFile(outputName)
	if err != nil {
		t.Fatalf("failed to read output file: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(string(content)), "\n")
	if len(lines) != 3 {
		t.Errorf("expected 3 lines, got %d", len(lines))
	}
	if lines[0] != "https://abc" {
		t.Errorf("unexpected first line: %s", lines[0])
	}
}

// TestGetOrDownloadYtdlp tests the function that checks for yt-dlp.exe and downloads it if missing.
// We mock the HTTP calls with httptest.
func TestGetOrDownloadYtdlp(t *testing.T) {
	// 1. Create a temp directory to run our test so we don't pollute the real workspace
	tmpDir, err := os.MkdirTemp("", "ytdlp_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }() // cleanup
	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}

	// Instead of defer os.Chdir(oldCwd):
	defer func() {
		if err := os.Chdir(oldCwd); err != nil {
			t.Fatalf("failed to revert to original working dir: %v", err)
		}
	}()

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir to %s: %v", tmpDir, err)
	}

	exeName := "yt-dlp.exe"

	// 2. Test scenario where file already exists
	// Create a dummy file to simulate existing exe
	if err := os.WriteFile(exeName, []byte("dummy data"), 0644); err != nil {
		t.Fatalf("failed to create dummy exe file: %v", err)
	}

	client := http.DefaultClient // not actually used for this scenario
	if err := getOrDownloadYtdlp(client, exeName); err != nil {
		t.Errorf("expected nil error when file already exists, got %v", err)
	}

	// 3. Remove the file to force a download scenario
	_ = os.Remove(exeName)

	// Create a mock release JSON
	mockReleaseJSON := `{
        "assets": [
            {
                "name": "yt-dlp.exe",
                "browser_download_url": "http://example.com/yt-dlp.exe"
            }
        ]
    }`

	// Create a test server that serves our mock release JSON,
	// as well as the "download" for the exe file.
	downloadHandler := http.NewServeMux()
	downloadHandler.HandleFunc("/repos/yt-dlp/yt-dlp/releases/latest", func(w http.ResponseWriter, r *http.Request) {
		if _, err := w.Write([]byte(mockReleaseJSON)); err != nil {
			t.Fatalf("failed to write mock release JSON: %v", err)
		}
	})
	downloadHandler.HandleFunc("/yt-dlp.exe", func(w http.ResponseWriter, r *http.Request) {
		// Return some fake exe content
		if _, err := w.Write([]byte("fake exe bytes")); err != nil {
			t.Fatalf("failed to write fake exe bytes: %v", err)
		}
	})
	ts := httptest.NewServer(downloadHandler)
	defer ts.Close()

	// We need a custom client that rewrites the URL to our test server
	customClient := &http.Client{
		Transport: &rewriterRoundTripper{
			rt:   http.DefaultTransport,
			host: ts.URL, // e.g. http://127.0.0.1:12345
		},
	}

	// Now call getOrDownloadYtdlp again, which should attempt a download
	if err := getOrDownloadYtdlp(customClient, exeName); err != nil {
		t.Errorf("expected nil error on download scenario, got %v", err)
	}

	// Finally, check that our "exe" was downloaded
	if _, err := os.Stat(exeName); os.IsNotExist(err) {
		t.Errorf("expected %s to exist after download, but it doesn't", exeName)
	}
}

// rewriterRoundTripper rewrites GitHub URLs to our test server’s host.
type rewriterRoundTripper struct {
	rt   http.RoundTripper
	host string
}

func (r *rewriterRoundTripper) RoundTrip(req *http.Request) (*http.Response, error) {
	// If the request is going to github.com OR example.com, rewrite to the test server
	if strings.Contains(req.URL.Host, "github.com") || strings.Contains(req.URL.Host, "example.com") {
		// e.g. original: https://api.github.com/repos/yt-dlp/...
		// we rewrite to: ts.URL/repos/yt-dlp/...
		newURL := r.host + req.URL.Path
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(r.host, "http://")
		req.URL, _ = req.URL.Parse(newURL)
	}
	return r.rt.RoundTrip(req)
}

// MockCommandRunner for testing command execution
type MockCommandRunner struct {
	ShouldFail bool
	Commands   []MockCommand
}

type MockCommand struct {
	Name string
	Args []string
}

func (m *MockCommandRunner) Run(name string, args ...string) error {
	m.Commands = append(m.Commands, MockCommand{Name: name, Args: args})
	if m.ShouldFail {
		return fmt.Errorf("mock command failed")
	}
	return nil
}

// TestRunYtdlpWithRunner tests the runYtdlp function with mocked command execution
func TestRunYtdlpWithRunner(t *testing.T) {
	tests := []struct {
		name                 string
		psPrefix             string
		outputName           string
		organizeByCollection bool
		skipThumbnails       bool
		shouldFail           bool
		expectCmd            string
		expectArgs           []string
	}{
		{
			name:                 "successful execution without powershell prefix",
			psPrefix:             "",
			outputName:           "test_videos.txt",
			organizeByCollection: false,
			skipThumbnails:       false,
			shouldFail:           false,
			expectCmd:            "yt-dlp.exe",
			expectArgs:           []string{"-a", "test_videos.txt", "--output", "%(upload_date)s_%(id)s_%(title).50B.%(ext)s", "--write-info-json", "--write-thumbnail"},
		},
		{
			name:                 "successful execution with powershell prefix",
			psPrefix:             ".\\",
			outputName:           "fav_videos.txt",
			organizeByCollection: false,
			skipThumbnails:       false,
			shouldFail:           false,
			expectCmd:            ".\\yt-dlp.exe",
			expectArgs:           []string{"-a", "fav_videos.txt", "--output", "%(upload_date)s_%(id)s_%(title).50B.%(ext)s", "--write-info-json", "--write-thumbnail"},
		},
		{
			name:                 "command execution failure",
			psPrefix:             "",
			outputName:           "videos.txt",
			organizeByCollection: false,
			skipThumbnails:       false,
			shouldFail:           true,
			expectCmd:            "yt-dlp.exe",
			expectArgs:           []string{"-a", "videos.txt", "--output", "%(upload_date)s_%(id)s_%(title).50B.%(ext)s", "--write-info-json", "--write-thumbnail"},
		},
		{
			name:                 "collection organized output goes to subdirectory",
			psPrefix:             "",
			outputName:           filepath.Join("favorites", "fav_videos.txt"),
			organizeByCollection: true,
			skipThumbnails:       false,
			shouldFail:           false,
			expectCmd:            "yt-dlp.exe",
			expectArgs:           []string{"-a", filepath.Join("favorites", "fav_videos.txt"), "--output", filepath.Join("favorites", "%(upload_date)s_%(id)s_%(title).50B.%(ext)s"), "--write-info-json", "--write-thumbnail"},
		},
		{
			name:                 "skip thumbnails omits --write-thumbnail flag",
			psPrefix:             "",
			outputName:           "test_videos.txt",
			organizeByCollection: false,
			skipThumbnails:       true,
			shouldFail:           false,
			expectCmd:            "yt-dlp.exe",
			expectArgs:           []string{"-a", "test_videos.txt", "--output", "%(upload_date)s_%(id)s_%(title).50B.%(ext)s", "--write-info-json"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &MockCommandRunner{ShouldFail: tt.shouldFail}

			// Capture output for verification
			runYtdlpWithRunner(mockRunner, tt.psPrefix, tt.outputName, tt.organizeByCollection, tt.skipThumbnails)

			// Verify command was called correctly
			if len(mockRunner.Commands) != 1 {
				t.Errorf("expected 1 command execution, got %d", len(mockRunner.Commands))
				return
			}

			cmd := mockRunner.Commands[0]
			if cmd.Name != tt.expectCmd {
				t.Errorf("expected command %q, got %q", tt.expectCmd, cmd.Name)
			}

			if len(cmd.Args) != len(tt.expectArgs) {
				t.Errorf("expected %d args, got %d", len(tt.expectArgs), len(cmd.Args))
				return
			}

			for i, arg := range tt.expectArgs {
				if cmd.Args[i] != arg {
					t.Errorf("expected arg[%d] %q, got %q", i, arg, cmd.Args[i])
				}
			}
		})
	}
}

// TestParseFavoriteVideosFromFileErrorScenarios tests various error conditions
func TestParseFavoriteVideosFromFileErrorScenarios(t *testing.T) {
	tests := []struct {
		name         string
		jsonContent  string
		includeLiked bool
		expectError  bool
	}{
		{
			name:         "malformed JSON",
			jsonContent:  `{"Likes and Favorites": {"Favorite Videos": {`,
			includeLiked: false,
			expectError:  true,
		},
		{
			name:         "missing Likes and Favorites field",
			jsonContent:  `{"NotLikes and Favorites": {}}`,
			includeLiked: false,
			expectError:  false, // Should not error, just return empty slice
		},
		{
			name:         "missing Favorite Videos field",
			jsonContent:  `{"Likes and Favorites": {"NotFavoriteVideos": {}}}`,
			includeLiked: false,
			expectError:  false,
		},
		{
			name:         "empty favorite videos list",
			jsonContent:  `{"Likes and Favorites": {"Favorite Videos": {"FavoriteVideoList": []}}}`,
			includeLiked: false,
			expectError:  false,
		},
		{
			name:         "missing Link field in favorite video",
			jsonContent:  `{"Likes and Favorites": {"Favorite Videos": {"FavoriteVideoList": [{"NotLink": "test"}]}}}`,
			includeLiked: false,
			expectError:  false,
		},
		{
			name: "unicode characters in URLs",
			jsonContent: `{
				"Likes and Favorites": {
					"Favorite Videos": {
						"FavoriteVideoList": [
							{"Link": "https://www.tiktok.com/@用户/video/123"}
						]
					}
				}
			}`,
			includeLiked: false,
			expectError:  false,
		},
		{
			name: "very long URL",
			jsonContent: fmt.Sprintf(`{
				"Likes and Favorites": {
					"Favorite Videos": {
						"FavoriteVideoList": [
							{"Link": "https://www.tiktok.com/%s"}
						]
					}
				}
			}`, strings.Repeat("a", 2000)),
			includeLiked: false,
			expectError:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create temporary file
			tmpFile, err := os.CreateTemp("", "test_*.json")
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			if _, err := tmpFile.WriteString(tt.jsonContent); err != nil {
				t.Fatalf("failed to write to temp file: %v", err)
			}
			_ = tmpFile.Close()

			_, err = parseFavoriteVideosFromFile(tmpFile.Name(), tt.includeLiked)
			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			} else if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestParseFavoriteVideosFromFileNotFound tests file not found scenario
func TestParseFavoriteVideosFromFileNotFound(t *testing.T) {
	_, err := parseFavoriteVideosFromFile("nonexistent_file.json", false)
	if err == nil {
		t.Error("expected error for non-existent file")
	}
}

// TestWriteFavoriteVideosToFileErrorScenarios tests write error conditions
func TestWriteFavoriteVideosToFileErrorScenarios(t *testing.T) {
	tests := []struct {
		name     string
		urls     []string
		filename string
	}{
		{
			name:     "empty URL list",
			urls:     []string{},
			filename: "empty_test.txt",
		},
		{
			name:     "single URL",
			urls:     []string{"https://test.com"},
			filename: "single_test.txt",
		},
		{
			name:     "URLs with unicode characters",
			urls:     []string{"https://www.tiktok.com/@用户/video/123", "https://test.com/café"},
			filename: "unicode_test.txt",
		},
		{
			name:     "very long URLs",
			urls:     []string{fmt.Sprintf("https://test.com/%s", strings.Repeat("long", 500))},
			filename: "long_url_test.txt",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpFile, err := os.CreateTemp("", tt.filename)
			if err != nil {
				t.Fatalf("failed to create temp file: %v", err)
			}
			_ = tmpFile.Close()
			defer func() { _ = os.Remove(tmpFile.Name()) }()

			// Convert URLs to VideoEntries
			videoEntries := make([]VideoEntry, len(tt.urls))
			for i, url := range tt.urls {
				videoEntries[i] = VideoEntry{Link: url, Collection: "test"}
			}

			err = writeFavoriteVideosToFile(videoEntries, tmpFile.Name(), false)
			if err != nil {
				t.Errorf("unexpected error: %v", err)
			}

			// Verify content
			content, err := os.ReadFile(tmpFile.Name())
			if err != nil {
				t.Fatalf("failed to read output file: %v", err)
			}

			lines := strings.Split(strings.TrimSpace(string(content)), "\n")
			if len(tt.urls) == 0 {
				if string(content) != "" {
					t.Error("expected empty file for empty URL list")
				}
			} else {
				if len(lines) != len(tt.urls) {
					t.Errorf("expected %d lines, got %d", len(tt.urls), len(lines))
				}
			}
		})
	}
}

// TestGetOrDownloadYtdlpErrorScenarios tests network and download error conditions
func TestGetOrDownloadYtdlpErrorScenarios(t *testing.T) {
	tests := []struct {
		name          string
		serverHandler func(w http.ResponseWriter, r *http.Request)
		expectError   bool
	}{
		{
			name: "GitHub API returns 404",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(http.StatusNotFound)
			},
			expectError: true,
		},
		{
			name: "GitHub API returns invalid JSON",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte("invalid json"))
			},
			expectError: true,
		},
		{
			name: "No yt-dlp.exe asset found",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				_, _ = w.Write([]byte(`{"assets": [{"name": "other.exe", "browser_download_url": "http://example.com/other.exe"}]}`))
			},
			expectError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "ytdlp_error_test")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			oldCwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("failed to get working directory: %v", err)
			}
			defer func() { _ = os.Chdir(oldCwd) }()

			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("failed to chdir: %v", err)
			}

			server := httptest.NewServer(http.HandlerFunc(tt.serverHandler))
			defer server.Close()

			customClient := &http.Client{
				Transport: &rewriterRoundTripper{
					rt:   http.DefaultTransport,
					host: server.URL,
				},
			}

			err = getOrDownloadYtdlp(customClient, "yt-dlp.exe")
			if tt.expectError && err == nil {
				t.Error("expected error but got none")
			} else if !tt.expectError && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestPrintUsage tests the usage printing function
func TestPrintUsage(t *testing.T) {
	// Since printUsage writes to stdout, we can't easily capture it
	// But we can at least ensure it doesn't panic
	defer func() {
		if r := recover(); r != nil {
			t.Errorf("printUsage panicked: %v", r)
		}
	}()

	printUsage()
}

// TestIntegrationWorkflow tests the complete workflow end-to-end
func TestIntegrationWorkflow(t *testing.T) {
	// Create temporary directory for test
	tmpDir, err := os.MkdirTemp("", "integration_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	defer func() { _ = os.Chdir(oldCwd) }()

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	// Create test JSON file with comprehensive TikTok data
	testJSON := `{
		"Likes and Favorites": {
			"Favorite Videos": {
				"FavoriteVideoList": [
					{"Link": "https://www.tiktok.com/@user1/video/123"},
					{"Link": "https://www.tiktok.com/@user2/video/456"}
				]
			},
			"Like List": {
				"ItemFavoriteList": [
					{"date": "2023-01-01", "link": "https://www.tiktok.com/@user3/video/789"},
					{"date": "2023-01-02", "link": "https://www.tiktok.com/@user4/video/101"}
				]
			}
		}
	}`

	jsonFile := "test_user_data_tiktok.json"
	if err := os.WriteFile(jsonFile, []byte(testJSON), 0644); err != nil {
		t.Fatalf("failed to write test JSON: %v", err)
	}

	tests := []struct {
		name         string
		includeLiked bool
		expectedURLs int
	}{
		{
			name:         "favorites only",
			includeLiked: false,
			expectedURLs: 2,
		},
		{
			name:         "favorites and liked",
			includeLiked: true,
			expectedURLs: 4,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Parse JSON
			videoEntries, err := parseFavoriteVideosFromFile(jsonFile, tt.includeLiked)
			if err != nil {
				t.Fatalf("failed to parse JSON: %v", err)
			}

			if len(videoEntries) != tt.expectedURLs {
				t.Errorf("expected %d video entries, got %d", tt.expectedURLs, len(videoEntries))
			}

			// Write to output file
			outputFile := fmt.Sprintf("test_output_%s.txt", tt.name)
			if err := writeFavoriteVideosToFile(videoEntries, outputFile, false); err != nil {
				t.Fatalf("failed to write URLs: %v", err)
			}

			// Verify output file
			content, err := os.ReadFile(outputFile)
			if err != nil {
				t.Fatalf("failed to read output file: %v", err)
			}

			lines := strings.Split(strings.TrimSpace(string(content)), "\n")
			if len(lines) != tt.expectedURLs {
				t.Errorf("expected %d lines in output, got %d", tt.expectedURLs, len(lines))
			}

			// Verify URLs are correct
			for i, entry := range videoEntries {
				if lines[i] != entry.Link {
					t.Errorf("expected line %d to be %q, got %q", i, entry.Link, lines[i])
				}
			}
		})
	}
}

// TestMainFunctionArguments tests main function with different argument scenarios
func TestMainFunctionArguments(t *testing.T) {
	// This is challenging to test directly since main() calls os.Exit and has interactive prompts
	// Instead, we'll test the core logic that main() uses

	tests := []struct {
		name     string
		args     []string
		jsonFile string
		setup    func(t *testing.T, dir string) // setup function to create necessary files
	}{
		{
			name:     "help flag",
			args:     []string{"program", "-h"},
			jsonFile: "",
			setup:    func(t *testing.T, dir string) {}, // No setup needed for help
		},
		{
			name:     "help flag long",
			args:     []string{"program", "--help"},
			jsonFile: "",
			setup:    func(t *testing.T, dir string) {},
		},
		{
			name:     "custom JSON file path",
			args:     []string{"program", "custom_data.json"},
			jsonFile: "custom_data.json",
			setup: func(t *testing.T, dir string) {
				testJSON := `{"Likes and Favorites": {"Favorite Videos": {"FavoriteVideoList": [{"Link": "https://test.com"}]}}}`
				if err := os.WriteFile("custom_data.json", []byte(testJSON), 0644); err != nil {
					t.Fatalf("failed to create custom JSON: %v", err)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tmpDir, err := os.MkdirTemp("", "main_test")
			if err != nil {
				t.Fatalf("failed to create temp dir: %v", err)
			}
			defer func() { _ = os.RemoveAll(tmpDir) }()

			oldCwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("failed to get working directory: %v", err)
			}
			defer func() { _ = os.Chdir(oldCwd) }()

			if err := os.Chdir(tmpDir); err != nil {
				t.Fatalf("failed to chdir: %v", err)
			}

			// Setup test environment
			tt.setup(t, tmpDir)

			// Test argument parsing logic that main() uses
			var jsonFile string
			if len(tt.args) > 1 {
				if tt.args[1] == "-h" || tt.args[1] == "--help" {
					// Help case - just ensure printUsage doesn't panic
					defer func() {
						if r := recover(); r != nil {
							t.Errorf("printUsage panicked: %v", r)
						}
					}()
					printUsage()
					return
				}
				jsonFile = tt.args[1]
			} else {
				jsonFile = "user_data_tiktok.json"
			}

			// Test file existence check logic
			if tt.jsonFile != "" {
				if _, err := os.Stat(jsonFile); os.IsNotExist(err) {
					t.Errorf("expected JSON file to exist: %s", jsonFile)
				}

				// Test that we can parse the file
				_, err := parseFavoriteVideosFromFile(jsonFile, false)
				if err != nil {
					t.Errorf("failed to parse JSON file: %v", err)
				}
			}
		})
	}
}

// TestEdgeCasesAndBoundaries tests various edge cases and boundary conditions
func TestEdgeCasesAndBoundaries(t *testing.T) {
	t.Run("very large JSON file", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "large_test_*.json")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer func() { _ = os.Remove(tmpFile.Name()) }()

		// Create JSON with many entries
		var videoList []string
		for i := 0; i < 1000; i++ {
			videoList = append(videoList, fmt.Sprintf(`{"Link": "https://www.tiktok.com/@user%d/video/%d"}`, i, i))
		}

		largeJSON := fmt.Sprintf(`{
			"Likes and Favorites": {
				"Favorite Videos": {
					"FavoriteVideoList": [%s]
				}
			}
		}`, strings.Join(videoList, ","))

		if _, err := tmpFile.WriteString(largeJSON); err != nil {
			t.Fatalf("failed to write large JSON: %v", err)
		}
		_ = tmpFile.Close()

		urls, err := parseFavoriteVideosFromFile(tmpFile.Name(), false)
		if err != nil {
			t.Errorf("failed to parse large JSON: %v", err)
		}

		if len(urls) != 1000 {
			t.Errorf("expected 1000 URLs, got %d", len(urls))
		}
	})

	t.Run("empty JSON structure", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "empty_test_*.json")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer func() { _ = os.Remove(tmpFile.Name()) }()

		emptyJSON := `{}`
		if _, err := tmpFile.WriteString(emptyJSON); err != nil {
			t.Fatalf("failed to write empty JSON: %v", err)
		}
		_ = tmpFile.Close()

		urls, err := parseFavoriteVideosFromFile(tmpFile.Name(), false)
		if err != nil {
			t.Errorf("unexpected error for empty JSON: %v", err)
		}

		if len(urls) != 0 {
			t.Errorf("expected 0 URLs for empty JSON, got %d", len(urls))
		}
	})

	t.Run("concurrent file access", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "concurrent_test_*.json")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer func() { _ = os.Remove(tmpFile.Name()) }()

		testJSON := `{"Likes and Favorites": {"Favorite Videos": {"FavoriteVideoList": [{"Link": "https://test.com"}]}}}`
		if _, err := tmpFile.WriteString(testJSON); err != nil {
			t.Fatalf("failed to write test JSON: %v", err)
		}
		_ = tmpFile.Close()

		// Simulate concurrent access
		done := make(chan bool, 2)
		for i := 0; i < 2; i++ {
			go func() {
				defer func() { done <- true }()
				_, err := parseFavoriteVideosFromFile(tmpFile.Name(), false)
				if err != nil {
					t.Errorf("concurrent access failed: %v", err)
				}
			}()
		}

		// Wait for both goroutines
		<-done
		<-done
	})

	t.Run("special characters in filenames", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "special_chars_test")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		oldCwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get working directory: %v", err)
		}
		defer func() { _ = os.Chdir(oldCwd) }()

		if err := os.Chdir(tmpDir); err != nil {
			t.Fatalf("failed to chdir: %v", err)
		}

		// Test filenames with spaces and special characters (Windows-safe)
		testFiles := []string{
			"test file with spaces.txt",
			"test-file-with-dashes.txt",
			"test_file_with_underscores.txt",
		}

		urls := []string{"https://test1.com", "https://test2.com"}

		// Convert URLs to VideoEntries
		videoEntries := make([]VideoEntry, len(urls))
		for i, url := range urls {
			videoEntries[i] = VideoEntry{Link: url, Collection: "test"}
		}

		for _, filename := range testFiles {
			err := writeFavoriteVideosToFile(videoEntries, filename, false)
			if err != nil {
				t.Errorf("failed to write file with special chars %q: %v", filename, err)
				continue
			}

			// Verify file was created and contains correct content
			content, err := os.ReadFile(filename)
			if err != nil {
				t.Errorf("failed to read file %q: %v", filename, err)
				continue
			}

			lines := strings.Split(strings.TrimSpace(string(content)), "\n")
			if len(lines) != len(urls) {
				t.Errorf("file %q: expected %d lines, got %d", filename, len(urls), len(lines))
			}
		}
	})
}

// TestCollectionOrganization tests the new collection organization features
func TestCollectionOrganization(t *testing.T) {
	// Test sanitizeCollectionName function
	t.Run("sanitize_collection_names", func(t *testing.T) {
		tests := []struct {
			input    string
			expected string
		}{
			{"favorites", "favorites"},
			{"liked videos", "liked videos"},
			{"my<collection>", "my_collection_"},
			{"test/collection\\name", "test_collection_name"},
			{"  collection.  ", "collection"},
			{"", "unknown"},
			{"collection:with|special*chars", "collection_with_special_chars"},
		}

		for _, tt := range tests {
			result := sanitizeCollectionName(tt.input)
			if result != tt.expected {
				t.Errorf("sanitizeCollectionName(%q) = %q, want %q", tt.input, result, tt.expected)
			}
		}
	})

	// Test createCollectionDirectories function
	t.Run("create_collection_directories", func(t *testing.T) {
		// Create a temporary directory for testing
		tmpDir, err := os.MkdirTemp("", "collection_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		// Change to temp directory
		originalDir, _ := os.Getwd()
		defer func() { _ = os.Chdir(originalDir) }()
		_ = os.Chdir(tmpDir)

		videoEntries := []VideoEntry{
			{Link: "https://test1.com", Collection: "favorites"},
			{Link: "https://test2.com", Collection: "liked"},
			{Link: "https://test3.com", Collection: "favorites"},
			{Link: "https://test4.com", Collection: "custom collection"},
		}

		// Test with organization enabled
		err = createCollectionDirectories(videoEntries, true)
		if err != nil {
			t.Errorf("createCollectionDirectories failed: %v", err)
		}

		// Check if directories were created
		expectedDirs := []string{"favorites", "liked", "custom collection"}
		for _, dir := range expectedDirs {
			if _, err := os.Stat(dir); os.IsNotExist(err) {
				t.Errorf("expected directory %q to be created", dir)
			}
		}

		// Test with organization disabled
		_ = os.RemoveAll("favorites")
		_ = os.RemoveAll("liked")
		_ = os.RemoveAll("custom collection")

		err = createCollectionDirectories(videoEntries, false)
		if err != nil {
			t.Errorf("createCollectionDirectories failed: %v", err)
		}

		// Check that no directories were created
		for _, dir := range expectedDirs {
			if _, err := os.Stat(dir); !os.IsNotExist(err) {
				t.Errorf("directory %q should not be created when organization is disabled", dir)
			}
		}
	})

	// Test writeFavoriteVideosToFile with collection organization
	t.Run("write_videos_with_collection_organization", func(t *testing.T) {
		// Create a temporary directory for testing
		tmpDir, err := os.MkdirTemp("", "collection_write_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		// Change to temp directory
		originalDir, _ := os.Getwd()
		defer func() { _ = os.Chdir(originalDir) }()
		_ = os.Chdir(tmpDir)

		videoEntries := []VideoEntry{
			{Link: "https://fav1.com", Collection: "favorites"},
			{Link: "https://fav2.com", Collection: "favorites"},
			{Link: "https://liked1.com", Collection: "liked"},
			{Link: "https://liked2.com", Collection: "liked"},
		}

		// Test with collection organization enabled
		// Note: outputName is ignored when organizing by collection - each collection uses its own filename
		err = writeFavoriteVideosToFile(videoEntries, "ignored.txt", true)
		if err != nil {
			t.Errorf("writeFavoriteVideosToFile with organization failed: %v", err)
		}

		// Check if collection directories and files were created with collection-specific filenames
		favoritesFile := filepath.Join("favorites", "fav_videos.txt")
		likedFile := filepath.Join("liked", "liked_videos.txt")

		if _, err := os.Stat(favoritesFile); os.IsNotExist(err) {
			t.Errorf("expected favorites file %q to be created", favoritesFile)
		}

		if _, err := os.Stat(likedFile); os.IsNotExist(err) {
			t.Errorf("expected liked file %q to be created", likedFile)
		}

		// Verify content of favorites file
		favContent, err := os.ReadFile(favoritesFile)
		if err != nil {
			t.Errorf("failed to read favorites file: %v", err)
		}
		favLines := strings.Split(strings.TrimSpace(string(favContent)), "\n")
		if len(favLines) != 2 {
			t.Errorf("expected 2 lines in favorites file, got %d", len(favLines))
		}
		if favLines[0] != "https://fav1.com" || favLines[1] != "https://fav2.com" {
			t.Errorf("favorites file content incorrect: %v", favLines)
		}

		// Verify content of liked file
		likedContent, err := os.ReadFile(likedFile)
		if err != nil {
			t.Errorf("failed to read liked file: %v", err)
		}
		likedLines := strings.Split(strings.TrimSpace(string(likedContent)), "\n")
		if len(likedLines) != 2 {
			t.Errorf("expected 2 lines in liked file, got %d", len(likedLines))
		}
		if likedLines[0] != "https://liked1.com" || likedLines[1] != "https://liked2.com" {
			t.Errorf("liked file content incorrect: %v", likedLines)
		}
	})
}

// TestExtractVideoID tests the video ID extraction from TikTok URLs
func TestExtractVideoID(t *testing.T) {
	tests := []struct {
		name     string
		url      string
		expected string
	}{
		{
			name:     "standard tiktokv share URL",
			url:      "https://www.tiktokv.com/share/video/7600559584901647646/",
			expected: "7600559584901647646",
		},
		{
			name:     "tiktok user video URL",
			url:      "https://www.tiktok.com/@user123/video/7600559584901647646",
			expected: "7600559584901647646",
		},
		{
			name:     "mobile tiktok v URL",
			url:      "https://m.tiktok.com/v/7600559584901647646.html",
			expected: "7600559584901647646",
		},
		{
			name:     "URL with query params",
			url:      "https://www.tiktok.com/@user/video/1234567890?is_from_webapp=1",
			expected: "1234567890",
		},
		{
			name:     "invalid URL no video ID",
			url:      "https://www.tiktok.com/@user/profile",
			expected: "",
		},
		{
			name:     "empty URL",
			url:      "",
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := extractVideoID(tt.url)
			if result != tt.expected {
				t.Errorf("extractVideoID(%q) = %q, want %q", tt.url, result, tt.expected)
			}
		})
	}
}

// TestGetOutputFilename tests collection-specific filename generation
func TestGetOutputFilename(t *testing.T) {
	tests := []struct {
		collection string
		expected   string
	}{
		{"favorites", "fav_videos.txt"},
		{"liked", "liked_videos.txt"},
		{"other", "fav_videos.txt"},
		{"", "fav_videos.txt"},
	}

	for _, tt := range tests {
		t.Run(tt.collection, func(t *testing.T) {
			result := getOutputFilename(tt.collection)
			if result != tt.expected {
				t.Errorf("getOutputFilename(%q) = %q, want %q", tt.collection, result, tt.expected)
			}
		})
	}
}

// TestParseInfoJSON tests parsing of yt-dlp info.json files
func TestParseInfoJSON(t *testing.T) {
	t.Run("valid info json", func(t *testing.T) {
		// Create temp file with valid JSON
		tmpFile, err := os.CreateTemp("", "info_*.json")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer func() { _ = os.Remove(tmpFile.Name()) }()

		infoJSON := `{
			"id": "7600559584901647646",
			"title": "Test Video Title",
			"uploader": "TestUser",
			"uploader_id": "testuser123",
			"upload_date": "20260129",
			"description": "Test description",
			"duration": 45,
			"view_count": 1500000,
			"like_count": 50000,
			"thumbnail": "https://example.com/thumb.jpg",
			"filename": "20260129_7600559584901647646_Test_Video.mp4"
		}`

		if _, err := tmpFile.WriteString(infoJSON); err != nil {
			t.Fatalf("failed to write to temp file: %v", err)
		}
		_ = tmpFile.Close()

		info, err := parseInfoJSON(tmpFile.Name())
		if err != nil {
			t.Fatalf("parseInfoJSON failed: %v", err)
		}

		if info.ID != "7600559584901647646" {
			t.Errorf("expected ID '7600559584901647646', got %q", info.ID)
		}
		if info.Title != "Test Video Title" {
			t.Errorf("expected Title 'Test Video Title', got %q", info.Title)
		}
		if info.Duration != 45 {
			t.Errorf("expected Duration 45, got %d", info.Duration)
		}
		if info.ViewCount != 1500000 {
			t.Errorf("expected ViewCount 1500000, got %d", info.ViewCount)
		}
	})

	t.Run("invalid json", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "invalid_*.json")
		if err != nil {
			t.Fatalf("failed to create temp file: %v", err)
		}
		defer func() { _ = os.Remove(tmpFile.Name()) }()

		if _, err := tmpFile.WriteString("not valid json"); err != nil {
			t.Fatalf("failed to write to temp file: %v", err)
		}
		_ = tmpFile.Close()

		_, err = parseInfoJSON(tmpFile.Name())
		if err == nil {
			t.Error("expected error for invalid JSON, got nil")
		}
	})

	t.Run("file not found", func(t *testing.T) {
		_, err := parseInfoJSON("nonexistent_file.json")
		if err == nil {
			t.Error("expected error for nonexistent file, got nil")
		}
	})
}

// TestGetEntriesForCollection tests filtering video entries by collection
func TestGetEntriesForCollection(t *testing.T) {
	entries := []VideoEntry{
		{Link: "https://fav1.com", Collection: "favorites"},
		{Link: "https://fav2.com", Collection: "favorites"},
		{Link: "https://liked1.com", Collection: "liked"},
		{Link: "https://liked2.com", Collection: "liked"},
		{Link: "https://other.com", Collection: "other"},
	}

	t.Run("filter favorites", func(t *testing.T) {
		result := getEntriesForCollection(entries, "favorites")
		if len(result) != 2 {
			t.Errorf("expected 2 favorites, got %d", len(result))
		}
	})

	t.Run("filter liked", func(t *testing.T) {
		result := getEntriesForCollection(entries, "liked")
		if len(result) != 2 {
			t.Errorf("expected 2 liked, got %d", len(result))
		}
	})

	t.Run("filter nonexistent", func(t *testing.T) {
		result := getEntriesForCollection(entries, "nonexistent")
		if len(result) != 0 {
			t.Errorf("expected 0 entries, got %d", len(result))
		}
	})
}

// TestGenerateCollectionIndex tests the index generation functionality
func TestGenerateCollectionIndex(t *testing.T) {
	t.Run("generates index files with metadata enrichment", func(t *testing.T) {
		// Create temp directory
		tmpDir, err := os.MkdirTemp("", "collection_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		// Create mock .info.json file
		infoJSON := `{
			"id": "7600559584901647646",
			"title": "Test Video Title",
			"uploader": "TestUser",
			"uploader_id": "testuser123",
			"upload_date": "20260129",
			"description": "Test description",
			"duration": 45,
			"view_count": 1500000,
			"like_count": 50000,
			"thumbnail": "https://example.com/thumb.jpg",
			"filename": "20260129_7600559584901647646_Test_Video.mp4"
		}`
		infoPath := filepath.Join(tmpDir, "20260129_7600559584901647646_Test_Video.info.json")
		if err := os.WriteFile(infoPath, []byte(infoJSON), 0644); err != nil {
			t.Fatalf("failed to write info.json: %v", err)
		}

		// Create the actual video file (required for download verification)
		videoPath := filepath.Join(tmpDir, "20260129_7600559584901647646_Test_Video.mp4")
		if err := os.WriteFile(videoPath, []byte("fake video data"), 0644); err != nil {
			t.Fatalf("failed to write video file: %v", err)
		}

		// Create video entries
		entries := []VideoEntry{
			{
				Link:       "https://www.tiktok.com/@user/video/7600559584901647646",
				Date:       "2026-01-29",
				Collection: "favorites",
			},
			{
				Link:       "https://www.tiktok.com/@user/video/9999999999999999999",
				Date:       "2026-01-28",
				Collection: "favorites",
			},
		}

		// Store original values to verify no mutation
		originalLink0 := entries[0].Link
		originalTitle0 := entries[0].Title

		// Generate index
		err = generateCollectionIndex(tmpDir, entries)
		if err != nil {
			t.Fatalf("generateCollectionIndex failed: %v", err)
		}

		// Verify index.json was created
		indexJSONPath := filepath.Join(tmpDir, "index.json")
		if _, err := os.Stat(indexJSONPath); os.IsNotExist(err) {
			t.Error("index.json was not created")
		}

		// Verify index.html was created
		indexHTMLPath := filepath.Join(tmpDir, "index.html")
		if _, err := os.Stat(indexHTMLPath); os.IsNotExist(err) {
			t.Error("index.html was not created")
		}

		// Read and verify index.json content
		indexData, err := os.ReadFile(indexJSONPath)
		if err != nil {
			t.Fatalf("failed to read index.json: %v", err)
		}

		var index CollectionIndex
		if err := json.Unmarshal(indexData, &index); err != nil {
			t.Fatalf("failed to parse index.json: %v", err)
		}

		// Verify index structure
		if index.TotalVideos != 2 {
			t.Errorf("expected TotalVideos=2, got %d", index.TotalVideos)
		}
		if index.Downloaded != 1 {
			t.Errorf("expected Downloaded=1, got %d", index.Downloaded)
		}
		if index.Failed != 1 {
			t.Errorf("expected Failed=1, got %d", index.Failed)
		}

		// Verify first video was enriched with metadata
		if len(index.Videos) != 2 {
			t.Fatalf("expected 2 videos, got %d", len(index.Videos))
		}
		if index.Videos[0].Title != "Test Video Title" {
			t.Errorf("expected Title 'Test Video Title', got %q", index.Videos[0].Title)
		}
		if index.Videos[0].Creator != "TestUser" {
			t.Errorf("expected Creator 'TestUser', got %q", index.Videos[0].Creator)
		}
		if !index.Videos[0].Downloaded {
			t.Error("expected first video to be marked as downloaded")
		}

		// Verify second video marked as failed
		if index.Videos[1].Downloaded {
			t.Error("expected second video to be marked as failed")
		}

		// Verify original entries were NOT mutated
		if entries[0].Link != originalLink0 {
			t.Errorf("original entry Link was mutated")
		}
		if entries[0].Title != originalTitle0 {
			t.Errorf("original entry Title was mutated: expected %q, got %q", originalTitle0, entries[0].Title)
		}
	})

	t.Run("handles empty collection", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "empty_collection_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		entries := []VideoEntry{}

		err = generateCollectionIndex(tmpDir, entries)
		if err != nil {
			t.Fatalf("generateCollectionIndex failed on empty collection: %v", err)
		}

		// Verify index files were still created
		if _, err := os.Stat(filepath.Join(tmpDir, "index.json")); os.IsNotExist(err) {
			t.Error("index.json was not created for empty collection")
		}
		if _, err := os.Stat(filepath.Join(tmpDir, "index.html")); os.IsNotExist(err) {
			t.Error("index.html was not created for empty collection")
		}
	})

	t.Run("handles missing info.json gracefully", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "no_info_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		entries := []VideoEntry{
			{
				Link:       "https://www.tiktok.com/@user/video/1234567890",
				Collection: "favorites",
			},
		}

		err = generateCollectionIndex(tmpDir, entries)
		if err != nil {
			t.Fatalf("generateCollectionIndex failed: %v", err)
		}

		// Read index.json and verify the entry is marked as failed
		indexData, err := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		if err != nil {
			t.Fatalf("failed to read index.json: %v", err)
		}

		var index CollectionIndex
		if err := json.Unmarshal(indexData, &index); err != nil {
			t.Fatalf("failed to parse index.json: %v", err)
		}

		if index.Downloaded != 0 {
			t.Errorf("expected Downloaded=0, got %d", index.Downloaded)
		}
		if index.Failed != 1 {
			t.Errorf("expected Failed=1, got %d", index.Failed)
		}
	})
}

// TestWriteHTMLIndex tests the HTML template rendering
func TestWriteHTMLIndex(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "html_test_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	index := &CollectionIndex{
		Name:        "test_collection",
		GeneratedAt: "2026-01-29 12:00:00",
		TotalVideos: 2,
		Downloaded:  1,
		Failed:      1,
		Videos: []VideoEntry{
			{
				VideoID:    "123456",
				Title:      "Test Video",
				Creator:    "TestUser",
				Downloaded: true,
			},
			{
				VideoID:    "789012",
				Title:      "Failed Video",
				Downloaded: false,
			},
		},
	}

	err = writeHTMLIndex(tmpDir, index)
	if err != nil {
		t.Fatalf("writeHTMLIndex failed: %v", err)
	}

	// Verify file was created
	htmlPath := filepath.Join(tmpDir, "index.html")
	if _, err := os.Stat(htmlPath); os.IsNotExist(err) {
		t.Fatal("index.html was not created")
	}

	// Read and verify content contains expected elements
	content, err := os.ReadFile(htmlPath)
	if err != nil {
		t.Fatalf("failed to read index.html: %v", err)
	}

	contentStr := string(content)
	if !strings.Contains(contentStr, "test_collection") {
		t.Error("HTML doesn't contain collection name")
	}
	if !strings.Contains(contentStr, "Test Video") {
		t.Error("HTML doesn't contain video title")
	}
	if !strings.Contains(contentStr, "TestUser") {
		t.Error("HTML doesn't contain creator name")
	}
}

// TestFormatDuration tests the duration formatting function
func TestFormatDuration(t *testing.T) {
	funcs := getTemplateFuncs()
	formatDuration := funcs["formatDuration"].(func(int) string)

	tests := []struct {
		seconds  int
		expected string
	}{
		{0, "0:00"},
		{5, "0:05"},
		{59, "0:59"},
		{60, "1:00"},
		{65, "1:05"},
		{125, "2:05"},
		{3600, "60:00"},
		{3661, "61:01"},
	}

	for _, tt := range tests {
		result := formatDuration(tt.seconds)
		if result != tt.expected {
			t.Errorf("formatDuration(%d) = %q, want %q", tt.seconds, result, tt.expected)
		}
	}
}

// TestFormatNumber tests the number formatting function
func TestFormatNumber(t *testing.T) {
	funcs := getTemplateFuncs()
	formatNumber := funcs["formatNumber"].(func(int64) string)

	tests := []struct {
		number   int64
		expected string
	}{
		{0, "0"},
		{999, "999"},
		{1000, "1.0K"},
		{1500, "1.5K"},
		{10000, "10.0K"},
		{999999, "1000.0K"},
		{1000000, "1.0M"},
		{1500000, "1.5M"},
		{10000000, "10.0M"},
	}

	for _, tt := range tests {
		result := formatNumber(tt.number)
		if result != tt.expected {
			t.Errorf("formatNumber(%d) = %q, want %q", tt.number, result, tt.expected)
		}
	}
}

// TestParseFlags tests the new CLI flag parsing functionality
func TestParseFlags(t *testing.T) {
	// Save original command line args
	originalArgs := os.Args
	defer func() { os.Args = originalArgs }()

	tests := []struct {
		name                   string
		args                   []string
		expectedJSONFile       string
		expectedOrganization   bool
		expectedSkipThumbnails bool
		expectedIndexOnly      bool
	}{
		{
			name:                   "default_settings",
			args:                   []string{"program"},
			expectedJSONFile:       "user_data_tiktok.json",
			expectedOrganization:   true,
			expectedSkipThumbnails: false,
			expectedIndexOnly:      false,
		},
		{
			name:                   "flat_structure_flag",
			args:                   []string{"program", "--flat-structure"},
			expectedJSONFile:       "user_data_tiktok.json",
			expectedOrganization:   false,
			expectedSkipThumbnails: false,
			expectedIndexOnly:      false,
		},
		{
			name:                   "custom_json_file",
			args:                   []string{"program", "custom_data.json"},
			expectedJSONFile:       "custom_data.json",
			expectedOrganization:   true,
			expectedSkipThumbnails: false,
			expectedIndexOnly:      false,
		},
		{
			name:                   "flat_structure_with_custom_file",
			args:                   []string{"program", "--flat-structure", "custom_data.json"},
			expectedJSONFile:       "custom_data.json",
			expectedOrganization:   false,
			expectedSkipThumbnails: false,
			expectedIndexOnly:      false,
		},
		{
			name:                   "no_thumbnails_flag",
			args:                   []string{"program", "--no-thumbnails"},
			expectedJSONFile:       "user_data_tiktok.json",
			expectedOrganization:   true,
			expectedSkipThumbnails: true,
			expectedIndexOnly:      false,
		},
		{
			name:                   "index_only_flag",
			args:                   []string{"program", "--index-only"},
			expectedJSONFile:       "user_data_tiktok.json",
			expectedOrganization:   true,
			expectedSkipThumbnails: false,
			expectedIndexOnly:      true,
		},
		{
			name:                   "all_flags_combined",
			args:                   []string{"program", "--flat-structure", "--no-thumbnails", "--index-only", "custom.json"},
			expectedJSONFile:       "custom.json",
			expectedOrganization:   false,
			expectedSkipThumbnails: true,
			expectedIndexOnly:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Set up command line arguments
			os.Args = tt.args

			// Reset flag package state
			flag.CommandLine = flag.NewFlagSet(os.Args[0], flag.ExitOnError)

			config := parseFlags()

			if config.JSONFile != tt.expectedJSONFile {
				t.Errorf("expected JSONFile %q, got %q", tt.expectedJSONFile, config.JSONFile)
			}

			if config.OrganizeByCollection != tt.expectedOrganization {
				t.Errorf("expected OrganizeByCollection %v, got %v", tt.expectedOrganization, config.OrganizeByCollection)
			}

			if config.SkipThumbnails != tt.expectedSkipThumbnails {
				t.Errorf("expected SkipThumbnails %v, got %v", tt.expectedSkipThumbnails, config.SkipThumbnails)
			}

			if config.IndexOnly != tt.expectedIndexOnly {
				t.Errorf("expected IndexOnly %v, got %v", tt.expectedIndexOnly, config.IndexOnly)
			}
		})
	}
}

// TestIndexOnlyMode tests the --index-only workflow that regenerates indexes without downloading
func TestIndexOnlyMode(t *testing.T) {
	t.Run("index-only with collection organization", func(t *testing.T) {
		// Create temp directory
		tmpDir, err := os.MkdirTemp("", "index_only_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		oldCwd, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldCwd) }()
		_ = os.Chdir(tmpDir)

		// Create collections directory structure
		_ = os.Mkdir("favorites", 0755)

		// Create test JSON file
		jsonContent := `{
			"Likes and Favorites": {
				"Favorite Videos": {
					"FavoriteVideoList": [
						{"Link": "https://www.tiktok.com/@user/video/7600559584901647646", "Date": "2026-01-29"}
					]
				}
			}
		}`
		jsonFile := "user_data_tiktok.json"
		if err := os.WriteFile(jsonFile, []byte(jsonContent), 0644); err != nil {
			t.Fatalf("failed to write test JSON: %v", err)
		}

		// Create mock .info.json file in favorites directory
		infoJSON := `{
			"id": "7600559584901647646",
			"title": "Test Video",
			"uploader": "TestUser",
			"uploader_id": "testuser123",
			"upload_date": "20260129",
			"description": "Test description",
			"duration": 45,
			"view_count": 1500000,
			"like_count": 50000,
			"thumbnail": "https://example.com/thumb.jpg",
			"filename": "20260129_7600559584901647646_Test_Video.mp4"
		}`
		infoPath := filepath.Join("favorites", "20260129_7600559584901647646_Test_Video.info.json")
		if err := os.WriteFile(infoPath, []byte(infoJSON), 0644); err != nil {
			t.Fatalf("failed to write info.json: %v", err)
		}

		// Create the actual video file
		videoPath := filepath.Join("favorites", "20260129_7600559584901647646_Test_Video.mp4")
		if err := os.WriteFile(videoPath, []byte("fake video data"), 0644); err != nil {
			t.Fatalf("failed to write video file: %v", err)
		}

		// Parse video entries
		videoEntries, err := parseFavoriteVideosFromFile(jsonFile, false)
		if err != nil {
			t.Fatalf("parseFavoriteVideosFromFile failed: %v", err)
		}

		// Simulate --index-only mode: regenerate indexes for each collection
		collections := make(map[string]bool)
		for _, entry := range videoEntries {
			collections[sanitizeCollectionName(entry.Collection)] = true
		}

		for collection := range collections {
			collectionEntries := getEntriesForCollection(videoEntries, collection)
			if err := generateCollectionIndex(collection, collectionEntries); err != nil {
				t.Fatalf("generateCollectionIndex failed: %v", err)
			}
		}

		// Verify index files were created
		indexJSONPath := filepath.Join("favorites", "index.json")
		if _, err := os.Stat(indexJSONPath); os.IsNotExist(err) {
			t.Error("index.json was not created in favorites directory")
		}

		indexHTMLPath := filepath.Join("favorites", "index.html")
		if _, err := os.Stat(indexHTMLPath); os.IsNotExist(err) {
			t.Error("index.html was not created in favorites directory")
		}

		// Verify index content
		indexData, err := os.ReadFile(indexJSONPath)
		if err != nil {
			t.Fatalf("failed to read index.json: %v", err)
		}

		var index CollectionIndex
		if err := json.Unmarshal(indexData, &index); err != nil {
			t.Fatalf("failed to parse index.json: %v", err)
		}

		if index.Downloaded != 1 {
			t.Errorf("expected 1 downloaded video, got %d", index.Downloaded)
		}
		if index.Failed != 0 {
			t.Errorf("expected 0 failed videos, got %d", index.Failed)
		}
	})

	t.Run("index-only with flat structure", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "index_only_flat_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		oldCwd, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldCwd) }()
		_ = os.Chdir(tmpDir)

		// Create test JSON file
		jsonContent := `{
			"Likes and Favorites": {
				"Favorite Videos": {
					"FavoriteVideoList": [
						{"Link": "https://www.tiktok.com/@user/video/1234567890"}
					]
				}
			}
		}`
		jsonFile := "user_data_tiktok.json"
		if err := os.WriteFile(jsonFile, []byte(jsonContent), 0644); err != nil {
			t.Fatalf("failed to write test JSON: %v", err)
		}

		// Create mock .info.json file in current directory (flat structure)
		infoJSON := `{
			"id": "1234567890",
			"title": "Flat Structure Video",
			"uploader": "FlatUser",
			"filename": "20260129_1234567890_Flat_Video.mp4"
		}`
		if err := os.WriteFile("20260129_1234567890_Flat_Video.info.json", []byte(infoJSON), 0644); err != nil {
			t.Fatalf("failed to write info.json: %v", err)
		}

		// Parse and generate index for flat structure
		videoEntries, err := parseFavoriteVideosFromFile(jsonFile, false)
		if err != nil {
			t.Fatalf("parseFavoriteVideosFromFile failed: %v", err)
		}

		dir, err := filepath.Abs(".")
		if err != nil {
			dir = "."
		}

		if err := generateCollectionIndex(dir, videoEntries); err != nil {
			t.Fatalf("generateCollectionIndex failed: %v", err)
		}

		// Verify files created in current directory
		if _, err := os.Stat("index.json"); os.IsNotExist(err) {
			t.Error("index.json was not created in current directory")
		}
		if _, err := os.Stat("index.html"); os.IsNotExist(err) {
			t.Error("index.html was not created in current directory")
		}
	})

	t.Run("index-only with no existing info files", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "index_only_empty_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		oldCwd, _ := os.Getwd()
		defer func() { _ = os.Chdir(oldCwd) }()
		_ = os.Chdir(tmpDir)

		// Create collections directory
		_ = os.Mkdir("favorites", 0755)

		// Create test JSON file
		jsonContent := `{
			"Likes and Favorites": {
				"Favorite Videos": {
					"FavoriteVideoList": [
						{"Link": "https://www.tiktok.com/@user/video/9999999999"}
					]
				}
			}
		}`
		jsonFile := "user_data_tiktok.json"
		if err := os.WriteFile(jsonFile, []byte(jsonContent), 0644); err != nil {
			t.Fatalf("failed to write test JSON: %v", err)
		}

		// Don't create any .info.json files - simulate no downloads yet
		videoEntries, err := parseFavoriteVideosFromFile(jsonFile, false)
		if err != nil {
			t.Fatalf("parseFavoriteVideosFromFile failed: %v", err)
		}

		collectionEntries := getEntriesForCollection(videoEntries, "favorites")
		if err := generateCollectionIndex("favorites", collectionEntries); err != nil {
			t.Fatalf("generateCollectionIndex failed: %v", err)
		}

		// Verify index shows all videos as failed
		indexData, err := os.ReadFile(filepath.Join("favorites", "index.json"))
		if err != nil {
			t.Fatalf("failed to read index.json: %v", err)
		}

		var index CollectionIndex
		if err := json.Unmarshal(indexData, &index); err != nil {
			t.Fatalf("failed to parse index.json: %v", err)
		}

		if index.Downloaded != 0 {
			t.Errorf("expected 0 downloaded videos, got %d", index.Downloaded)
		}
		if index.Failed != 1 {
			t.Errorf("expected 1 failed video, got %d", index.Failed)
		}
	})
}

// TestWriteJSONIndexErrors tests error handling in writeJSONIndex
func TestWriteJSONIndexErrors(t *testing.T) {
	t.Run("marshal error with invalid data", func(t *testing.T) {
		// Create temp directory
		tmpDir, err := os.MkdirTemp("", "json_error_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		// Create an index with data that will marshal successfully
		// (JSON marshaling in Go is very permissive, so we test the happy path)
		index := &CollectionIndex{
			Name:        "test",
			GeneratedAt: "2026-01-29",
			TotalVideos: 1,
			Videos: []VideoEntry{
				{
					Link:  "https://test.com",
					Title: "Test",
				},
			},
		}

		err = writeJSONIndex(tmpDir, index)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}

		// Verify file was created
		if _, err := os.Stat(filepath.Join(tmpDir, "index.json")); os.IsNotExist(err) {
			t.Error("index.json was not created")
		}
	})

	t.Run("write error with invalid directory", func(t *testing.T) {
		// Try to write to a non-existent directory
		index := &CollectionIndex{
			Name:   "test",
			Videos: []VideoEntry{},
		}

		err := writeJSONIndex("/nonexistent/directory/path", index)
		if err == nil {
			t.Error("expected error when writing to invalid directory, got nil")
		}
	})

	t.Run("write error with read-only directory", func(t *testing.T) {
		// Skip on Windows where read-only directory permissions work differently
		if strings.Contains(strings.ToLower(os.Getenv("OS")), "windows") {
			t.Skip("Skipping read-only directory test on Windows")
		}

		// Create temp directory
		tmpDir, err := os.MkdirTemp("", "readonly_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() {
			// Restore write permissions before cleanup
			_ = os.Chmod(tmpDir, 0755)
			_ = os.RemoveAll(tmpDir)
		}()

		// Make directory read-only
		if err := os.Chmod(tmpDir, 0555); err != nil {
			t.Skipf("Cannot set read-only permissions on this platform: %v", err)
		}

		index := &CollectionIndex{
			Name:   "test",
			Videos: []VideoEntry{},
		}

		err = writeJSONIndex(tmpDir, index)
		if err == nil {
			t.Error("expected error when writing to read-only directory, got nil")
		}
	})
}

// TestWriteHTMLIndexErrors tests error handling in writeHTMLIndex
func TestWriteHTMLIndexErrors(t *testing.T) {
	t.Run("template execution with valid data", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "html_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		index := &CollectionIndex{
			Name:        "test",
			GeneratedAt: "2026-01-29",
			TotalVideos: 1,
			Downloaded:  1,
			Videos: []VideoEntry{
				{
					VideoID:    "123",
					Title:      "Test Video",
					Downloaded: true,
				},
			},
		}

		err = writeHTMLIndex(tmpDir, index)
		if err != nil {
			t.Errorf("expected no error, got %v", err)
		}
	})

	t.Run("write error with invalid directory", func(t *testing.T) {
		index := &CollectionIndex{
			Name:   "test",
			Videos: []VideoEntry{},
		}

		err := writeHTMLIndex("/nonexistent/directory/path", index)
		if err == nil {
			t.Error("expected error when writing to invalid directory, got nil")
		}
	})

	t.Run("template execution with special characters", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "html_special_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		// Test with special HTML characters (should be auto-escaped by Go templates)
		index := &CollectionIndex{
			Name:        "test <script>alert('xss')</script>",
			GeneratedAt: "2026-01-29",
			Videos: []VideoEntry{
				{
					Title:       "<script>alert('xss')</script>",
					Description: "Test & special chars < > \" '",
					Creator:     "User<tag>",
				},
			},
		}

		err = writeHTMLIndex(tmpDir, index)
		if err != nil {
			t.Errorf("expected no error with special characters, got %v", err)
		}

		// Verify HTML was created and special chars are escaped
		content, err := os.ReadFile(filepath.Join(tmpDir, "index.html"))
		if err != nil {
			t.Fatalf("failed to read HTML: %v", err)
		}

		htmlStr := string(content)
		// Go templates auto-escape, so script tags should be escaped
		if strings.Contains(htmlStr, "<script>alert") && !strings.Contains(htmlStr, "&lt;script&gt;") {
			t.Error("HTML special characters were not properly escaped")
		}
	})
}

// TestVideoIDValidation tests that missing video IDs are properly logged and handled
func TestVideoIDValidation(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "video_id_validation_*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer func() { _ = os.RemoveAll(tmpDir) }()

	// Create entries with invalid URLs (no video ID)
	entries := []VideoEntry{
		{
			Link:       "https://www.tiktok.com/@user/profile", // Invalid - no video ID
			Collection: "favorites",
		},
		{
			Link:       "https://invalid-url", // Invalid - no video ID
			Collection: "favorites",
		},
		{
			Link:       "https://www.tiktok.com/@user/video/1234567890", // Valid
			Collection: "favorites",
		},
	}

	// Generate index - should warn about invalid URLs
	err = generateCollectionIndex(tmpDir, entries)
	if err != nil {
		t.Fatalf("generateCollectionIndex failed: %v", err)
	}

	// Read and verify index
	indexData, err := os.ReadFile(filepath.Join(tmpDir, "index.json"))
	if err != nil {
		t.Fatalf("failed to read index.json: %v", err)
	}

	var index CollectionIndex
	if err := json.Unmarshal(indexData, &index); err != nil {
		t.Fatalf("failed to parse index.json: %v", err)
	}

	// Check that invalid URLs are marked as failed with appropriate error
	invalidCount := 0
	for _, v := range index.Videos {
		if v.VideoID == "" {
			invalidCount++
			if v.Downloaded {
				t.Error("expected video with no ID to be marked as not downloaded")
			}
			if !strings.Contains(v.DownloadError, "Invalid URL format") {
				t.Errorf("expected error message about invalid URL, got: %s", v.DownloadError)
			}
		}
	}

	if invalidCount != 2 {
		t.Errorf("expected 2 videos with invalid IDs, got %d", invalidCount)
	}

	// Check counts
	if index.Failed != 3 {
		t.Errorf("expected 3 failed videos (2 invalid URLs + 1 missing metadata), got %d", index.Failed)
	}
}

// TestThumbnailDetection tests thumbnail file detection with various extensions and edge cases
func TestThumbnailDetection(t *testing.T) {
	t.Run("detects jpg thumbnail", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "thumb_jpg_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		// Create .info.json
		infoJSON := `{
			"id": "123456",
			"title": "Test",
			"filename": "20260129_123456_Test.mp4"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_123456_Test.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Create .jpg thumbnail
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_123456_Test.jpg"), []byte("fake image"), 0644); err != nil {
			t.Fatal(err)
		}

		// Create video file
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_123456_Test.mp4"), []byte("fake video"), 0644); err != nil {
			t.Fatal(err)
		}

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/123456"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatal(err)
		}

		indexData, _ := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		var index CollectionIndex
		_ = json.Unmarshal(indexData, &index)

		if index.Videos[0].ThumbnailFile != "20260129_123456_Test.jpg" {
			t.Errorf("expected .jpg thumbnail, got %q", index.Videos[0].ThumbnailFile)
		}
	})

	t.Run("prioritizes extensions in order", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "thumb_priority_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		infoJSON := `{
			"id": "789012",
			"title": "Test",
			"filename": "20260129_789012_Test.mp4"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_789012_Test.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Create multiple thumbnail formats
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_789012_Test.jpg"), []byte("jpg"), 0644)
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_789012_Test.webp"), []byte("webp"), 0644)
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_789012_Test.png"), []byte("png"), 0644)
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_789012_Test.mp4"), []byte("video"), 0644)

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/789012"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatal(err)
		}

		indexData, _ := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		var index CollectionIndex
		_ = json.Unmarshal(indexData, &index)

		// Should pick .jpg first (first in priority list)
		if index.Videos[0].ThumbnailFile != "20260129_789012_Test.jpg" {
			t.Errorf("expected .jpg to be prioritized, got %q", index.Videos[0].ThumbnailFile)
		}
	})

	t.Run("handles uppercase extensions", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "thumb_upper_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		infoJSON := `{
			"id": "345678",
			"title": "Test",
			"filename": "20260129_345678_Test.mp4"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_345678_Test.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Create uppercase extension thumbnail (note: on case-insensitive file systems like Windows,
		// this may be found as lowercase, which is acceptable behavior)
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_345678_Test.JPG"), []byte("image"), 0644)
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_345678_Test.mp4"), []byte("video"), 0644)

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/345678"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatal(err)
		}

		indexData, _ := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		var index CollectionIndex
		_ = json.Unmarshal(indexData, &index)

		// Accept either .JPG or .jpg depending on file system case sensitivity
		thumbFile := index.Videos[0].ThumbnailFile
		if thumbFile != "20260129_345678_Test.JPG" && thumbFile != "20260129_345678_Test.jpg" {
			t.Errorf("expected .JPG or .jpg thumbnail, got %q", thumbFile)
		}
	})

	t.Run("handles missing thumbnail", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "thumb_missing_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		infoJSON := `{
			"id": "999888",
			"title": "Test",
			"filename": "20260129_999888_Test.mp4"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_999888_Test.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Create video but NO thumbnail
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_999888_Test.mp4"), []byte("video"), 0644)

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/999888"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatal(err)
		}

		indexData, _ := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		var index CollectionIndex
		_ = json.Unmarshal(indexData, &index)

		if index.Videos[0].ThumbnailFile != "" {
			t.Errorf("expected no thumbnail, got %q", index.Videos[0].ThumbnailFile)
		}
	})
}

// TestPartialDownloadHandling tests detection of partial downloads and missing video files
func TestPartialDownloadHandling(t *testing.T) {
	t.Run("detects partial download with .part file", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "partial_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		infoJSON := `{
			"id": "111222",
			"title": "Partial Download",
			"filename": "20260129_111222_Partial.mp4"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_111222_Partial.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Create .part file (partial download)
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_111222_Partial.mp4.part"), []byte("partial"), 0644); err != nil {
			t.Fatal(err)
		}

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/111222"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatal(err)
		}

		indexData, _ := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		var index CollectionIndex
		_ = json.Unmarshal(indexData, &index)

		if index.Videos[0].Downloaded {
			t.Error("expected video with .part file to be marked as not downloaded")
		}
		if !strings.Contains(index.Videos[0].DownloadError, "incomplete") {
			t.Errorf("expected 'incomplete' error message, got: %s", index.Videos[0].DownloadError)
		}
		if index.Failed != 1 {
			t.Errorf("expected 1 failed video, got %d", index.Failed)
		}
	})

	t.Run("detects missing video file with metadata only", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "metadata_only_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		infoJSON := `{
			"id": "333444",
			"title": "Metadata Only",
			"filename": "20260129_333444_Metadata.mp4"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_333444_Metadata.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Don't create the video file - only .info.json exists
		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/333444"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatal(err)
		}

		indexData, _ := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		var index CollectionIndex
		_ = json.Unmarshal(indexData, &index)

		if index.Videos[0].Downloaded {
			t.Error("expected video with missing file to be marked as not downloaded")
		}
		if !strings.Contains(index.Videos[0].DownloadError, "missing") {
			t.Errorf("expected 'missing' error message, got: %s", index.Videos[0].DownloadError)
		}
	})

	t.Run("detects incomplete metadata", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "incomplete_meta_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		// Info without filename field
		infoJSON := `{
			"id": "555666",
			"title": "No Filename"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_555666_Test.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/555666"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatal(err)
		}

		indexData, _ := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		var index CollectionIndex
		_ = json.Unmarshal(indexData, &index)

		if index.Videos[0].Downloaded {
			t.Error("expected video with incomplete metadata to be marked as not downloaded")
		}
		if !strings.Contains(index.Videos[0].DownloadError, "incomplete") {
			t.Errorf("expected 'incomplete' error message, got: %s", index.Videos[0].DownloadError)
		}
	})

	t.Run("successful download with all files present", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "success_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		infoJSON := `{
			"id": "777888",
			"title": "Complete Download",
			"filename": "20260129_777888_Complete.mp4"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_777888_Complete.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}

		// Create complete video file
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_777888_Complete.mp4"), []byte("complete video"), 0644); err != nil {
			t.Fatal(err)
		}

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/777888"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatal(err)
		}

		indexData, _ := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		var index CollectionIndex
		_ = json.Unmarshal(indexData, &index)

		if !index.Videos[0].Downloaded {
			t.Error("expected complete video to be marked as downloaded")
		}
		if index.Videos[0].DownloadError != "" {
			t.Errorf("expected no error, got: %s", index.Videos[0].DownloadError)
		}
		if index.Downloaded != 1 {
			t.Errorf("expected 1 downloaded video, got %d", index.Downloaded)
		}
	})
}

// TestSpecialCharactersInIndex tests handling of special characters in various metadata fields
func TestSpecialCharactersInIndex(t *testing.T) {
	t.Run("handles emoji in titles and descriptions", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "emoji_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		infoJSON := `{
			"id": "9988776655",
			"title": "🎉 Fun Video 🎊 Party Time! 🥳",
			"description": "Testing emoji 😀😃😄 support",
			"uploader": "User👨‍💻",
			"filename": "20260129_9988776655_Fun.mp4"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_9988776655_Fun.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_9988776655_Fun.mp4"), []byte("video"), 0644)

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/9988776655"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatal(err)
		}

		// Verify JSON can be parsed
		indexData, _ := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		var index CollectionIndex
		if err := json.Unmarshal(indexData, &index); err != nil {
			t.Fatalf("failed to parse index with emoji: %v", err)
		}

		if !strings.Contains(index.Videos[0].Title, "🎉") {
			t.Error("emoji should be preserved in JSON")
		}

		// Verify HTML can be read and contains emoji
		htmlData, _ := os.ReadFile(filepath.Join(tmpDir, "index.html"))
		if !strings.Contains(string(htmlData), "🎉") {
			t.Error("emoji should be preserved in HTML")
		}
	})

	t.Run("escapes HTML injection attempts", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "xss_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		infoJSON := `{
			"id": "1122334455",
			"title": "<script>alert('xss')</script><img src=x onerror=alert('xss')>",
			"description": "<iframe src='javascript:alert(1)'>",
			"uploader": "</title><script>alert('xss')</script>",
			"filename": "20260129_1122334455_Test.mp4"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_1122334455_Test.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_1122334455_Test.mp4"), []byte("video"), 0644)

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/1122334455"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatal(err)
		}

		htmlData, _ := os.ReadFile(filepath.Join(tmpDir, "index.html"))
		htmlStr := string(htmlData)

		// Verify HTML tags are escaped
		if strings.Contains(htmlStr, "<script>alert('xss')</script>") {
			t.Error("script tags should be escaped in HTML output")
		}
		if strings.Contains(htmlStr, "&lt;script&gt;") || strings.Contains(htmlStr, "&#") {
			// Good - HTML is escaped
		} else {
			t.Log("Warning: HTML escaping method may have changed")
		}
	})

	t.Run("handles very long titles and descriptions", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "long_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		longTitle := strings.Repeat("A", 5000)
		longDesc := strings.Repeat("B", 10000)
		infoJSON := fmt.Sprintf(`{
			"id": "6677889900",
			"title": "%s",
			"description": "%s",
			"filename": "20260129_6677889900_Test.mp4"
		}`, longTitle, longDesc)
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_6677889900_Test.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_6677889900_Test.mp4"), []byte("video"), 0644)

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/6677889900"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatalf("should handle very long strings: %v", err)
		}

		// Verify both files were created
		if _, err := os.Stat(filepath.Join(tmpDir, "index.json")); err != nil {
			t.Error("index.json should be created even with very long strings")
		}
		if _, err := os.Stat(filepath.Join(tmpDir, "index.html")); err != nil {
			t.Error("index.html should be created even with very long strings")
		}
	})

	t.Run("handles unicode RTL text", func(t *testing.T) {
		tmpDir, err := os.MkdirTemp("", "rtl_test_*")
		if err != nil {
			t.Fatalf("failed to create temp dir: %v", err)
		}
		defer func() { _ = os.RemoveAll(tmpDir) }()

		// Arabic and Hebrew text (right-to-left)
		infoJSON := `{
			"id": "2233445566",
			"title": "مرحبا بك في تيك توك",
			"description": "שלום עולם",
			"uploader": "مستخدم",
			"filename": "20260129_2233445566_Test.mp4"
		}`
		if err := os.WriteFile(filepath.Join(tmpDir, "20260129_2233445566_Test.info.json"), []byte(infoJSON), 0644); err != nil {
			t.Fatal(err)
		}
		_ = os.WriteFile(filepath.Join(tmpDir, "20260129_2233445566_Test.mp4"), []byte("video"), 0644)

		entries := []VideoEntry{{Link: "https://www.tiktok.com/@user/video/2233445566"}}
		if err := generateCollectionIndex(tmpDir, entries); err != nil {
			t.Fatalf("should handle RTL text: %v", err)
		}

		indexData, _ := os.ReadFile(filepath.Join(tmpDir, "index.json"))
		var index CollectionIndex
		if err := json.Unmarshal(indexData, &index); err != nil {
			t.Fatalf("failed to parse index with RTL text: %v", err)
		}

		if !strings.Contains(index.Videos[0].Title, "مرحبا") {
			t.Error("RTL text should be preserved")
		}
	})
}
