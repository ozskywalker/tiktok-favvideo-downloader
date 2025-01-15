package main

import (
	"io/ioutil"
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
	tmpFile, err := ioutil.TempFile("", "testdata_*.json")
	if err != nil {
		t.Fatalf("failed to create temp file: %v", err)
	}
	defer os.Remove(tmpFile.Name())

	// Write some minimal JSON data to the file
	jsonContent := `{
        "Activity": {
            "Favorite Videos": {
                "FavoriteVideoList": [
                    {"Link": "https://www.tiktok.com/@someone/video/1"},
                    {"Link": "https://www.tiktok.com/@someone/video/2"}
                ]
            }
        }
    }`
	if _, err := tmpFile.WriteString(jsonContent); err != nil {
		t.Fatalf("failed to write to temp file: %v", err)
	}
	tmpFile.Close()

	// Parse the file
	videoURLs, err := parseFavoriteVideosFromFile(tmpFile.Name())
	if err != nil {
		t.Errorf("expected no error, got %v", err)
	}

	if len(videoURLs) != 2 {
		t.Errorf("expected 2 video links, got %d", len(videoURLs))
	}
	if videoURLs[0] != "https://www.tiktok.com/@someone/video/1" {
		t.Errorf("unexpected first link: %s", videoURLs[0])
	}
	if videoURLs[1] != "https://www.tiktok.com/@someone/video/2" {
		t.Errorf("unexpected second link: %s", videoURLs[1])
	}
}

// TestWriteFavoriteVideosToFile checks that we write URLs to file properly.
func TestWriteFavoriteVideosToFile(t *testing.T) {
	// Create a temp output file
	tmpOut, err := ioutil.TempFile("", "fav_videos_*.txt")
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
	content, err := ioutil.ReadFile(outputName)
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
	tmpDir, err := ioutil.TempDir("", "ytdlp_test")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir) // cleanup
	oldCwd, _ := os.Getwd()
	defer os.Chdir(oldCwd)

	os.Chdir(tmpDir)

	exeName := "yt-dlp.exe"

	// 2. Test scenario where file already exists
	// Create a dummy file to simulate existing exe
	if err := ioutil.WriteFile(exeName, []byte("dummy data"), 0644); err != nil {
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
		w.Write([]byte(mockReleaseJSON))
	})
	downloadHandler.HandleFunc("/yt-dlp.exe", func(w http.ResponseWriter, r *http.Request) {
		// Return some fake exe content
		w.Write([]byte("fake exe bytes"))
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
		req.URL.Path = req.URL.Path
		req.URL.RawQuery = req.URL.RawQuery
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
