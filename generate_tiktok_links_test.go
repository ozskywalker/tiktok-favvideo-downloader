package main

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

// TestIsRunningInPowershell checks if isRunningInPowershell returns
// true/false based on the environment variable. We manipulate the environment.
func TestIsRunningInPowershell(t *testing.T) {
	// Backup original PSModulePath
	originalPSModulePath := os.Getenv("PSModulePath")
	defer os.Setenv("PSModulePath", originalPSModulePath)

	// Case 1: Contains "PowerShell"
	os.Setenv("PSModulePath", "C:\\Windows\\PowerShell\\Modules")
	if !isRunningInPowershell() {
		t.Error("expected isRunningInPowershell to return true when PSModulePath contains 'PowerShell'")
	}

	// Case 2: Does not contain "PowerShell"
	os.Setenv("PSModulePath", "SomeRandomPath")
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
	defer os.Remove(tmpFile.Name())

	// Write JSON data that includes both favorited and liked videos
	jsonContent := `{
		"Activity": {
			"Favorite Videos": {
				"FavoriteVideoList": [
					{"Link": "https://www.tiktok.com/@someone/video/1"},
					{"Link": "https://www.tiktok.com/@someone/video/2"}
				]
			},
			"Like List": {
				"ItemFavoriteList": [
					{"Date": "2023-01-01", "Link": "https://www.tiktok.com/@someone/liked/1"},
					{"Date": "2023-01-02", "Link": "https://www.tiktok.com/@someone/liked/2"}
				]
			}
		}
	}`
	if _, err := tmpFile.WriteString(jsonContent); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	// Test case: only favorited videos
	videoURLs, err := parseFavoriteVideosFromFile(tmpFile.Name(), false)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if len(videoURLs) != 2 {
		t.Errorf("expected 2 favorited video links, got %d", len(videoURLs))
	}
	if videoURLs[0] != "https://www.tiktok.com/@someone/video/1" {
		t.Errorf("unexpected first favorited link: %s", videoURLs[0])
	}
	if videoURLs[1] != "https://www.tiktok.com/@someone/video/2" {
		t.Errorf("unexpected second favorited link: %s", videoURLs[1])
	}

	// Test case: favorited and liked videos
	videoURLs, err = parseFavoriteVideosFromFile(tmpFile.Name(), true)
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}
	if len(videoURLs) != 4 {
		t.Errorf("expected 4 total video links, got %d", len(videoURLs))
	}
	if videoURLs[2] != "https://www.tiktok.com/@someone/liked/1" {
		t.Errorf("unexpected third link: %s", videoURLs[2])
	}
	if videoURLs[3] != "https://www.tiktok.com/@someone/liked/2" {
		t.Errorf("unexpected fourth link: %s", videoURLs[3])
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
	tmpOut.Close()
	defer os.Remove(outputName)

	// We'll write these URLs
	urls := []string{"https://abc", "https://def", "https://xyz"}

	// Perform the write
	if err := writeFavoriteVideosToFile(urls, outputName); err != nil {
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
	defer os.RemoveAll(tmpDir) // cleanup
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
	os.Remove(exeName)

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
		name       string
		psPrefix   string
		outputName string
		shouldFail bool
		expectCmd  string
		expectArgs []string
	}{
		{
			name:       "successful execution without powershell prefix",
			psPrefix:   "",
			outputName: "test_videos.txt",
			shouldFail: false,
			expectCmd:  "yt-dlp.exe",
			expectArgs: []string{"-a", "test_videos.txt", "--output", "%(upload_date)s_%(uploader_id)s.%(ext)s"},
		},
		{
			name:       "successful execution with powershell prefix",
			psPrefix:   ".\\",
			outputName: "fav_videos.txt",
			shouldFail: false,
			expectCmd:  ".\\yt-dlp.exe",
			expectArgs: []string{"-a", "fav_videos.txt", "--output", "%(upload_date)s_%(uploader_id)s.%(ext)s"},
		},
		{
			name:       "command execution failure",
			psPrefix:   "",
			outputName: "videos.txt",
			shouldFail: true,
			expectCmd:  "yt-dlp.exe",
			expectArgs: []string{"-a", "videos.txt", "--output", "%(upload_date)s_%(uploader_id)s.%(ext)s"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mockRunner := &MockCommandRunner{ShouldFail: tt.shouldFail}

			// Capture output for verification
			runYtdlpWithRunner(mockRunner, tt.psPrefix, tt.outputName)

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
			jsonContent:  `{"Activity": {"Favorite Videos": {`,
			includeLiked: false,
			expectError:  true,
		},
		{
			name:         "missing Activity field",
			jsonContent:  `{"NotActivity": {}}`,
			includeLiked: false,
			expectError:  false, // Should not error, just return empty slice
		},
		{
			name:         "missing Favorite Videos field",
			jsonContent:  `{"Activity": {"NotFavoriteVideos": {}}}`,
			includeLiked: false,
			expectError:  false,
		},
		{
			name:         "empty favorite videos list",
			jsonContent:  `{"Activity": {"Favorite Videos": {"FavoriteVideoList": []}}}`,
			includeLiked: false,
			expectError:  false,
		},
		{
			name:         "missing Link field in favorite video",
			jsonContent:  `{"Activity": {"Favorite Videos": {"FavoriteVideoList": [{"NotLink": "test"}]}}}`,
			includeLiked: false,
			expectError:  false,
		},
		{
			name: "unicode characters in URLs",
			jsonContent: `{
				"Activity": {
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
				"Activity": {
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
			defer os.Remove(tmpFile.Name())

			if _, err := tmpFile.WriteString(tt.jsonContent); err != nil {
				t.Fatalf("failed to write to temp file: %v", err)
			}
			tmpFile.Close()

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
			tmpFile.Close()
			defer os.Remove(tmpFile.Name())

			err = writeFavoriteVideosToFile(tt.urls, tmpFile.Name())
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
				w.Write([]byte("invalid json"))
			},
			expectError: true,
		},
		{
			name: "No yt-dlp.exe asset found",
			serverHandler: func(w http.ResponseWriter, r *http.Request) {
				w.Write([]byte(`{"assets": [{"name": "other.exe", "browser_download_url": "http://example.com/other.exe"}]}`))
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
			defer os.RemoveAll(tmpDir)

			oldCwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("failed to get working directory: %v", err)
			}
			defer os.Chdir(oldCwd)

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
	defer os.RemoveAll(tmpDir)

	oldCwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("failed to get working directory: %v", err)
	}
	defer os.Chdir(oldCwd)

	if err := os.Chdir(tmpDir); err != nil {
		t.Fatalf("failed to chdir: %v", err)
	}

	// Create test JSON file with comprehensive TikTok data
	testJSON := `{
		"Activity": {
			"Favorite Videos": {
				"FavoriteVideoList": [
					{"Link": "https://www.tiktok.com/@user1/video/123"},
					{"Link": "https://www.tiktok.com/@user2/video/456"}
				]
			},
			"Like List": {
				"ItemFavoriteList": [
					{"Date": "2023-01-01", "Link": "https://www.tiktok.com/@user3/video/789"},
					{"Date": "2023-01-02", "Link": "https://www.tiktok.com/@user4/video/101"}
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
			urls, err := parseFavoriteVideosFromFile(jsonFile, tt.includeLiked)
			if err != nil {
				t.Fatalf("failed to parse JSON: %v", err)
			}

			if len(urls) != tt.expectedURLs {
				t.Errorf("expected %d URLs, got %d", tt.expectedURLs, len(urls))
			}

			// Write to output file
			outputFile := fmt.Sprintf("test_output_%s.txt", tt.name)
			if err := writeFavoriteVideosToFile(urls, outputFile); err != nil {
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
			for i, url := range urls {
				if lines[i] != url {
					t.Errorf("expected line %d to be %q, got %q", i, url, lines[i])
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
				testJSON := `{"Activity": {"Favorite Videos": {"FavoriteVideoList": [{"Link": "https://test.com"}]}}}`
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
			defer os.RemoveAll(tmpDir)

			oldCwd, err := os.Getwd()
			if err != nil {
				t.Fatalf("failed to get working directory: %v", err)
			}
			defer os.Chdir(oldCwd)

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
		defer os.Remove(tmpFile.Name())

		// Create JSON with many entries
		var videoList []string
		for i := 0; i < 1000; i++ {
			videoList = append(videoList, fmt.Sprintf(`{"Link": "https://www.tiktok.com/@user%d/video/%d"}`, i, i))
		}

		largeJSON := fmt.Sprintf(`{
			"Activity": {
				"Favorite Videos": {
					"FavoriteVideoList": [%s]
				}
			}
		}`, strings.Join(videoList, ","))

		if _, err := tmpFile.WriteString(largeJSON); err != nil {
			t.Fatalf("failed to write large JSON: %v", err)
		}
		tmpFile.Close()

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
		defer os.Remove(tmpFile.Name())

		emptyJSON := `{}`
		if _, err := tmpFile.WriteString(emptyJSON); err != nil {
			t.Fatalf("failed to write empty JSON: %v", err)
		}
		tmpFile.Close()

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
		defer os.Remove(tmpFile.Name())

		testJSON := `{"Activity": {"Favorite Videos": {"FavoriteVideoList": [{"Link": "https://test.com"}]}}}`
		if _, err := tmpFile.WriteString(testJSON); err != nil {
			t.Fatalf("failed to write test JSON: %v", err)
		}
		tmpFile.Close()

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
		defer os.RemoveAll(tmpDir)

		oldCwd, err := os.Getwd()
		if err != nil {
			t.Fatalf("failed to get working directory: %v", err)
		}
		defer os.Chdir(oldCwd)

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

		for _, filename := range testFiles {
			err := writeFavoriteVideosToFile(urls, filename)
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
