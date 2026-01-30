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
```

### Collection Directory Structure
```
project-folder/
├── tiktok-favvideo-downloader.exe
├── user_data_tiktok.json
│
├── favorites/                                        # Favorited videos collection
│   ├── fav_videos.txt                               # URL list (yt-dlp compatible)
│   ├── index.json                                   # Machine-readable metadata index
│   ├── index.html                                   # Visual browser (open in Chrome)
│   ├── 20260129_7600559584901647646_Funny_Cat.mp4   # Video file
│   ├── 20260129_7600559584901647646_Funny_Cat.info.json  # yt-dlp metadata
│   ├── 20260129_7600559584901647646_Funny_Cat.jpg   # Thumbnail
│   └── ...
│
└── liked/                                           # Liked videos collection (if opted in)
    ├── liked_videos.txt                             # Note: different filename for liked
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
   - `runYtdlp()` executes yt-dlp with `--write-info-json` and optional `--write-thumbnail` flags
   - Supports `--no-thumbnails` flag to skip thumbnail downloads
   - New filename format includes video ID and truncated title

4. **Video Metadata & Indexing**: Generates browsable indexes after download
   - `YtdlpInfo` struct for parsing yt-dlp's .info.json files
   - `CollectionIndex` struct for the complete collection metadata
   - `extractVideoID()` parses video IDs from TikTok URLs
   - `parseInfoJSON()` reads yt-dlp metadata files
   - `generateCollectionIndex()` creates index.json and index.html after download
   - `getEntriesForCollection()` filters entries by collection name
   - HTML template with search, filter, and embedded video player

5. **CLI Flag Parsing**: Command-line argument handling
   - `parseFlags()` handles `--flat-structure`, `--no-thumbnails`, `--index-only`, `--help` flags
   - `Config` struct stores application configuration
   - Supports positional arguments for custom JSON file paths
   - `--index-only` mode regenerates indexes from existing .info.json files without downloading

6. **Cross-platform Command Execution**: Handles PowerShell vs Command Prompt differences
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