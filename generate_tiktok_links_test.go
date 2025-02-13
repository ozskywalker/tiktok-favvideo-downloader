package main

import (
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
	// If the request is going to github.com, rewrite to the test server
	if strings.Contains(req.URL.Host, "github.com") {
		// e.g. original: https://api.github.com/repos/yt-dlp/...
		// we rewrite to: ts.URL/repos/yt-dlp/...
		newURL := r.host + req.URL.Path
		req.URL.Scheme = "http"
		req.URL.Host = strings.TrimPrefix(r.host, "http://")
		req.URL, _ = req.URL.Parse(newURL)
	}
	return r.rt.RoundTrip(req)
}

// // TestRunYtdlp is more of an integration test. We won't do a full test of exec.Command
// // (that can be tricky to automate). But at least ensure it doesn’t panic.
// func TestRunYtdlp(t *testing.T) {
// 	defer func() {
// 		if r := recover(); r != nil {
// 			t.Errorf("runYtdlp panicked: %v", r)
// 		}
// 	}()

// 	// We won't actually run yt-dlp in a real test. We'll do a quick check
// 	// that it tries to run the command. You could do advanced stubbing of exec.Command
// 	// if needed, but that's beyond the scope here.
// 	runYtdlp(".\\", "fake_list.txt")
// 	// If we reach here without panic, consider it "passed" in a basic sense.
// }
