# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## Collection Organization Feature

### Default Behavior
By default, the application organizes downloaded videos into collection-based subdirectories:
- `favorites/` - Contains favorited videos with their URL list file
- `liked/` - Contains liked videos with their URL list file

### Usage Examples
```bash
# Default: Collection organization enabled
tiktok-favvideo-downloader.exe

# Disable collection organization (flat structure)
tiktok-favvideo-downloader.exe --flat-structure

# Use with custom JSON file
tiktok-favvideo-downloader.exe my_data.json

# Combine flags and custom file
tiktok-favvideo-downloader.exe --flat-structure my_data.json

# Skip thumbnail downloads (faster, less storage)
tiktok-favvideo-downloader.exe --no-thumbnails

# Regenerate indexes without re-downloading videos
tiktok-favvideo-downloader.exe --index-only

# Use cookies from file for age-restricted videos
tiktok-favvideo-downloader.exe --cookies cookies.txt

# Extract cookies from Chrome browser
tiktok-favvideo-downloader.exe --cookies-from-browser chrome

# Combine with other flags
tiktok-favvideo-downloader.exe --cookies-from-browser firefox --no-thumbnails

# Disable resume functionality (force re-download all videos)
tiktok-favvideo-downloader.exe --disable-resume

# Disable progress bar (use traditional line-by-line output)
tiktok-favvideo-downloader.exe --no-progress-bar
```

### Real-Time Progress Bar (New!)

**By default, the application displays a live progress bar during downloads showing:**
- Current progress: "Downloading favorites (87/92)"
- Visual progress bar: "████████████░░░ 94.6%"
- Success and failure counts in real-time
- Colored output (green for success, red for failures)

**Example output:**
```
Downloading favorites (87/92) | ████████████░░░ 94.6% | Success: 85 | Failed: 2
```

**How It Works:**
- Automatically enabled when terminal supports ANSI escape codes
- Parses yt-dlp's "[download] Downloading item X of Y" progress messages
- Updates progress bar in real-time without cluttering output
- Non-progress messages (errors, warnings) are still displayed
- Progress bar auto-clears when download completes

**Disabling the Progress Bar:**
Use `--no-progress-bar` flag to revert to traditional line-by-line output:
```bash
tiktok-favvideo-downloader.exe --no-progress-bar
```

This is useful for:
- Piped output or redirected logs
- Terminals that don't support ANSI codes
- Debugging or viewing full yt-dlp output
- Running in background or automated scripts

**Terminal Compatibility:**
- Automatically detects ANSI support (Windows Terminal, ConEmu, modern terminals)
- Auto-disables on: piped output, old Command Prompt, non-terminal environments
- No configuration needed - works out of the box on supported terminals

### Resume Download Functionality

**By default, the application automatically resumes downloads and skips already-downloaded videos.**

This prevents:
- Wasted bandwidth from re-downloading existing videos
- IP rate-limiting from excessive requests to TikTok
- Unnecessary storage usage

**How It Works**:
- Uses yt-dlp's `--download-archive` flag to maintain a list of downloaded video IDs
- Archive files are created automatically:
  - Collection mode: `favorites/download_archive.txt`, `liked/download_archive.txt`
  - Flat mode: `download_archive.txt` in root directory
- Each archive file contains one line per video: `tiktok <video_id>`
- Videos in the archive are automatically skipped on subsequent runs
- Partial downloads (`.part` files) are automatically resumed via `--continue` flag

**Archive File Management**:
- Archive files are created automatically on first run
- Safe to manually edit - remove a line to force re-download of that specific video
- Safe to delete entire archive file to force full re-download of all videos
- Compatible with yt-dlp's standard archive format

**Disabling Resume**:
Use `--disable-resume` flag to force re-download of all videos (ignores archive):
```bash
tiktok-favvideo-downloader.exe --disable-resume
```

This is useful for:
- Forcing re-download of all videos (e.g., after format changes)
- Testing download functionality
- Replacing corrupted or incomplete downloads

### Collection Directory Structure
```
project-folder/
├── tiktok-favvideo-downloader.exe
├── user_data_tiktok.json
│
├── favorites/                                        # Favorited videos collection
│   ├── fav_videos.txt                               # URL list (yt-dlp compatible)
│   ├── download_archive.txt                         # Resume tracking (skips downloaded videos)
│   ├── index.json                                   # Machine-readable metadata index
│   ├── index.html                                   # Visual browser (open in Chrome)
│   ├── 20260129_7600559584901647646_Funny_Cat.mp4   # Video file
│   ├── 20260129_7600559584901647646_Funny_Cat.info.json  # yt-dlp metadata
│   ├── 20260129_7600559584901647646_Funny_Cat.jpg   # Thumbnail
│   └── ...
│
└── liked/                                           # Liked videos collection (if opted in)
    ├── liked_videos.txt                             # Note: different filename for liked
    ├── download_archive.txt                         # Resume tracking for liked videos
    ├── index.json
    ├── index.html
    └── ...
```

### Video Metadata & Indexing Feature

After downloading videos, the application generates:

1. **`index.html`** - Visual browser with:
   - Thumbnail grid view
   - Search by title, creator, or description
   - Filter by download status (All/Downloaded/Failed)
   - Click-to-play video modal
   - Dark theme, works offline

2. **`index.json`** - Machine-readable index with:
   - Video metadata (title, creator, duration, views, etc.)
   - Favorited dates from TikTok export
   - Download status and local filenames
   - Original TikTok URLs

### Filename Format
Videos are downloaded with the format: `%(upload_date)s_%(id)s_%(title).50B.%(ext)s`

Example: `20260129_7600559584901647646_Funny_Cat_Video_Title.mp4`

This includes:
- Upload date for chronological sorting
- Video ID for uniqueness
- Truncated title (50 bytes) for identification

### Download Session Reporting

After each download session completes, the application provides comprehensive reporting:

1. **Console Summary** - Displays at the end of each session:
   - Session duration
   - Total videos attempted, successful, and failed
   - Per-collection breakdown (when using collection organization)
   - Reference to `results.txt` for detailed failure information

2. **`results.txt` File** - Appends detailed session results to a log file in the root directory:
   - Session timestamp and duration
   - Summary statistics (attempted/success/failed counts)
   - Detailed failure list with:
     - Video ID and URL
     - Error type (IP Blocked, Authentication Required, Not Available, Network Timeout, Other)
     - Full error message from yt-dlp
   - Troubleshooting tips specific to encountered error types
   - Multiple sessions are preserved with clear separators

Example console output:
```
================================================================================
                        DOWNLOAD SESSION SUMMARY
================================================================================
Duration: 15m 32s
Total Videos Attempted: 127
  ✓ Successfully Downloaded: 119
  ✗ Failed: 8

Collection Breakdown:
  favorites:
    Attempted: 92  |  Success: 87  |  Failed: 5
  liked:
    Attempted: 35  |  Success: 32  |  Failed: 3

For detailed failure information, see results.txt
================================================================================
```

Example `results.txt` entry:
```
================================================================================
TikTok Video Downloader - Session Results
Generated: 2026-01-30 14:35:22
Duration: 15m 32s
================================================================================

SUMMARY
=======
Total Videos Attempted: 127
Successfully Downloaded: 119
Failed: 8

FAILED DOWNLOADS
================

Collection: favorites (5 failures)
--------------------------------------------------

1. Video ID: 7600559584901647646
   URL: https://www.tiktok.com/@user/video/7600559584901647646
   Error Type: IP Blocked
   Error: Your IP address is blocked from accessing this post

2. Video ID: 7600559584901647647
   URL: https://www.tiktok.com/@user/video/7600559584901647647
   Error Type: Authentication Required
   Error: This post may not be comfortable for some audiences. Log in for access

TROUBLESHOOTING TIPS
====================
IP Blocked (3 videos):
  - Your IP may be rate-limited by TikTok
  - Try again after waiting 30-60 minutes
  - Consider using a VPN or different network

Authentication Required (2 videos):
  - These videos require login to view
  - You may need to download manually while logged in
```

**Error Types:**
- **IP Blocked** - Your IP address is rate-limited or blocked by TikTok
- **Authentication Required** - Video requires login to access (age-restricted content)
- **Not Available** - Video deleted, private, or region-locked
- **Network Timeout** - Connection issues or timeouts
- **Other** - Miscellaneous errors not matching known patterns

## Development Commands

### Building
```bash
# Build for current platform
go build -o tiktok-favvideo-downloader.exe .

# Build for Windows x86-64 with version
GOOS=windows GOARCH=amd64 go build -ldflags="-X 'main.version=v1.0.0'" -o tiktok-favvideo-downloader-x86_64.exe .

# Build for Windows ARM64 with version
GOOS=windows GOARCH=arm64 go build -ldflags="-X 'main.version=v1.0.0'" -o tiktok-favvideo-downloader-ARM64.exe .
```

### Testing
```bash
# Run all tests
go test -v ./...

# Run tests with coverage
go test -cover -v ./...

# Run specific test function
go test -v -run TestFunctionName

# Run tests in parallel
go test -v -parallel 4 ./...
```

### Code Quality
```bash
# Format code (required before commits)
go fmt ./...

# Run static analysis (required)
go vet ./...

# Run linter (automatically checked in CI)
golangci-lint run
```

### Dependencies
```bash
# Download dependencies
go mod download

# Update dependencies
go get -u ./...

# Clean up modules
go mod tidy
```

## Architecture

### Project Structure
This is a single-package Go application (`package main`) that downloads TikTok favorite/liked videos using yt-dlp. The main components are:

- **Main executable**: `generate_tiktok_links.go` - Core application logic
- **Tests**: `generate_tiktok_links_test.go` - Comprehensive test suite with 64.7% coverage
- **Templates**: `templates/index.html` - Embedded HTML template for visual browser (via `//go:embed`)
- **No external dependencies**: Pure Go standard library implementation (uses `embed` package)

### Key Components

1. **JSON Data Parsing**: Parses TikTok's `user_data_tiktok.json` export file
   - `Data` struct defines the expected JSON structure
   - `parseFavoriteVideosFromFile()` extracts video entries with collection metadata
   - `VideoEntry` struct contains Link, Date, Collection, and extended metadata fields

2. **Collection Organization**: Organizes videos by collection type (enabled by default)
   - `sanitizeCollectionName()` ensures collection names are valid directory names
   - `createCollectionDirectories()` creates subdirectories for each collection
   - `writeFavoriteVideosToFile()` writes videos to collection-specific files
   - `getOutputFilename()` returns collection-specific filenames (fav_videos.txt vs liked_videos.txt)
   - Supports `--flat-structure` flag to disable organization

3. **yt-dlp Integration**: Downloads and manages the yt-dlp executable
   - `getOrDownloadYtdlp()` automatically downloads latest yt-dlp.exe from GitHub if not present
   - `runYtdlp()` executes yt-dlp with multiple flags:
     - `--write-info-json` - Save metadata for each video
     - `--write-thumbnail` - Download thumbnails (optional via `--no-thumbnails`)
     - `--download-archive` - Track downloaded videos for resume functionality (default)
     - `--no-overwrites` - Skip re-downloading existing files (default)
     - `--continue` - Resume partial downloads (default)
   - Supports `--no-thumbnails` flag to skip thumbnail downloads
   - Supports `--cookies` and `--cookies-from-browser` flags for age-restricted videos
   - Supports `--disable-resume` flag to force re-download all videos
   - Supports `--no-progress-bar` flag to disable real-time progress display
   - New filename format includes video ID and truncated title

4. **Real-Time Progress Bar**: Live download progress visualization
   - `ProgressState` struct tracks current download progress (current index, total, success/failure counts)
   - `ProgressRenderer` struct handles ANSI-based progress display with color codes
   - `parseProgressLine()` extracts progress from yt-dlp's "[download] Downloading item X of Y" messages
   - `supportsANSI()` detects terminal ANSI support (Windows Terminal, ConEmu, standard terminals)
   - `renderProgress()` displays live progress bar using ANSI escape codes (\r for line rewrite, color codes)
   - `RealCommandRunner` performs line-by-line output processing via `bufio.Scanner`
   - Auto-disables on piped output or terminals without ANSI support
   - Progress bar updates in real-time without cluttering output with repeated messages

5. **Video Metadata & Indexing**: Generates browsable indexes after download
   - `YtdlpInfo` struct for parsing yt-dlp's .info.json files
   - `CollectionIndex` struct for the complete collection metadata
   - `extractVideoID()` parses video IDs from TikTok URLs
   - `parseInfoJSON()` reads yt-dlp metadata files
   - `generateCollectionIndex()` creates index.json and index.html after download
   - `getEntriesForCollection()` filters entries by collection name
   - HTML template with search, filter, and embedded video player

6. **Download Session Reporting**: Tracks and reports download results
   - `CapturedOutput` struct stores yt-dlp stdout/stderr for parsing
   - `DownloadSession` and `CollectionResult` structs track session statistics
   - `FailureDetail` struct captures per-video error information
   - `parseYtdlpOutput()` extracts error messages from yt-dlp output using regex
   - `categorizeError()` classifies errors into types (IP blocked, auth required, etc.)
   - `printSessionSummary()` displays end-of-session statistics to console
   - `writeResultsFile()` appends detailed results to results.txt with troubleshooting tips
   - Uses `io.MultiWriter` to capture output while still displaying real-time progress

7. **CLI Flag Parsing**: Command-line argument handling
   - `parseFlags()` handles `--flat-structure`, `--no-thumbnails`, `--index-only`, `--disable-resume`, `--no-progress-bar`, `--cookies`, `--cookies-from-browser`, `--help` flags
   - `Config` struct stores application configuration
   - Supports positional arguments for custom JSON file paths
   - `--index-only` mode regenerates indexes from existing .info.json files without downloading
   - `--disable-resume` mode forces re-download of all videos (ignores download archive)
   - `--no-progress-bar` mode disables real-time progress bar (traditional line-by-line output)
   - Cookie validation functions: `validateCookieFile()`, `validateBrowserName()`, `promptForCookies()`

8. **Cross-platform Command Execution**: Handles PowerShell vs Command Prompt differences
   - `isRunningInPowershell()` detects PowerShell environment
   - Adjusts command prefixes accordingly (`.\` for PowerShell)

7. **Version Management**: Uses build-time ldflags to inject version information
   - `version` variable is overridden during builds via `-ldflags="-X 'main.version=...'"`

### Testing Architecture
- Uses dependency injection pattern for external dependencies (HTTP client, command runner)
- `CommandRunner` interface allows mocking of `exec.Command` calls
- HTTP client is passed as parameter to enable mocking of GitHub API calls
- Table-driven tests for comprehensive coverage of edge cases
- Collection organization tests verify directory creation and file organization
- CLI flag parsing tests ensure proper configuration handling

## Release Process

### Branching Strategy
- **main**: Stable code, ready for release
- **dev**: Active development branch
- Feature branches created from `dev`

### Creating Releases
1. Ensure on `main` branch with latest changes
2. Create and push git tag: `git tag v1.2.0 && git push origin v1.2.0`
3. GitHub Actions automatically builds and releases Windows binaries (x86-64 and ARM64)

### CI/CD Workflows
- **Development**: Tests run on every push to `dev` and PRs to `main`
- **Release**: Triggered by any git tag push, builds cross-platform binaries with version injection

## Commit Types
Use these prefixes for commits:
- `feat:` - New features (changelog visible)
- `fix:` - Bug fixes (changelog visible)
- `sec:` - Security changes (changelog visible)
- `perf:` - Performance improvements (changelog visible)
- `docs:`, `test:`, `build:`, `ci:`, `refactor:`, `style:` - Internal changes (filtered from releases)