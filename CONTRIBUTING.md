# Contributing to TikTok Favorite Video Downloader

Thank you for considering contributing to this project! This document outlines the development workflow, testing requirements, and release process.

## Development Setup

### Prerequisites
- **Go 1.25.1 or later** (required for building)
- Git for version control
- A Windows machine for testing (or WSL/cross-compilation for non-Windows development)

### Getting Started
```bash
# Clone the repository
git clone https://github.com/ozskywalker/tiktok-favvideo-downloader.git
cd tiktok-favvideo-downloader

# Install dependencies
go mod download

# Run tests to verify setup
go test -v ./...

# Build locally
go build -o tiktok-favvideo-downloader.exe .
```

## Development Workflow

### Branch Structure
- **`main`** - Stable code, ready for release
- **`dev`** - Active development branch
- **Feature branches** - For larger features, create branches from `dev`

### Workflow Steps
1. **Development**: Work on the `dev` branch or create feature branches
2. **Testing**: Ensure all tests pass and maintain/improve coverage
3. **Pull Request**: Create PR from `dev` → `main` when ready
4. **Code Review**: PRs are automatically tested via GitHub Actions
5. **Release**: Create git tags from `main` to trigger releases

### Commit Types

**Changelog-visible types (should appear in releases):**
- **`feat:`** - New features (appears in "Features" section)
- **`fix:`** - Bug fixes (appears in "Bug fixes" section)  
- **`sec:`** - Security-related changes (appears in "Security" section)
- **`perf:`** - Performance improvements (appears in "Performance" section)

**Internal types (should be filtered out from releases):**
- **`docs:`** - Documentation changes
- **`test:`** - Test changes
- **`build:`** - Build system changes (CI/CD, workflows, linting, releases)
- **`ci:`** - CI/CD changes (synonym for `build:`)
- **`refactor:`** - Code refactoring
- **`style:`** - Code style changes

### Code Quality Standards
```bash
# Format code (required)
go fmt ./...

# Run static analysis (required)
go vet ./...

# Run tests with coverage (aim for >55%)
go test -cover -v ./...

# Run linter (automatically checked in CI)
golangci-lint run
```

## Testing Requirements

### Test Coverage
- **Current coverage**: 56.6%
- **Minimum requirement**: Don't decrease existing coverage
- **Goal**: Improve coverage for new features

### Test Categories
1. **Unit Tests**: Individual function testing
2. **Integration Tests**: End-to-end workflow testing
3. **Error Scenarios**: Network failures, malformed data, file system errors
4. **Edge Cases**: Large files, unicode characters, concurrent access

### Running Tests
```bash
# Run all tests
go test -v ./...

# Run tests with coverage report
go test -cover -v ./...

# Run specific test function
go test -v -run TestFunctionName

# Run tests in parallel
go test -v -parallel 4 ./...
```

### Writing Tests
- All new functions should have corresponding tests
- Use table-driven tests for multiple test cases
- Mock external dependencies (HTTP calls, file system, command execution)
- Test both success and failure scenarios
- Include edge cases and boundary conditions

Example test structure:
```go
func TestYourFunction(t *testing.T) {
    tests := []struct {
        name        string
        input       InputType
        expected    ExpectedType
        expectError bool
    }{
        {"success case", validInput, expectedOutput, false},
        {"error case", invalidInput, nil, true},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            result, err := YourFunction(tt.input)
            if tt.expectError && err == nil {
                t.Error("expected error but got none")
            }
            // ... additional assertions
        })
    }
}
```

## Release Process

### Version Management
This project uses **semantic versioning** (e.g., `v1.2.3`):
- **Major** (`v2.0.0`): Breaking changes
- **Minor** (`v1.2.0`): New features, backward compatible
- **Patch** (`v1.2.1`): Bug fixes, backward compatible

### Creating a Release

1. **Prepare for Release**
   ```bash
   # Ensure you're on main branch
   git checkout main
   git pull origin main

   # Verify everything works
   go test -v ./...
   go vet ./...
   ```

2. **Create and Push Tag**
   ```bash
   # Create new tag (replace with your version)
   git tag v1.2.0

   # Push tag to trigger release
   git push origin v1.2.0
   ```

3. **Automated Release Process**
   - GitHub Actions automatically triggers on tag push
   - Runs full test suite
   - Builds Windows x86-64 and ARM64 binaries
   - Creates GitHub release with binaries attached
   - Version is injected into binary via build flags

### What Gets Released
- `tiktok-favvideo-downloader-x86_64.exe` (Windows Intel/AMD)
- `tiktok-favvideo-downloader-ARM64.exe` (Windows ARM)
- `README.md` documentation

## GitHub Actions Workflows

### CI Workflow (`check-every-devpush-and-mainPR.yml`)
**Triggers**:
- Every push to `dev` branch
- Pull requests targeting `main`

**Actions**:
- Sets up Go 1.25.1
- Downloads dependencies
- Runs linting (`golangci-lint`)
- Runs full test suite

### Release Workflow (`release-on-tag.yml`)
**Triggers**:
- Any git tag push (pattern: `*`)

**Actions**:
- Runs `go vet` and tests
- Builds cross-platform binaries
- Creates GitHub release with assets

## Common Tasks

### Adding a New Feature
1. Create feature branch from `dev`
2. Implement feature with tests
3. Ensure tests pass and coverage is maintained
4. Create PR to `main`
5. Address review feedback
6. Merge after approval

### Fixing a Bug
1. Create fix on `dev` branch
2. Add/update tests to cover the bug case
3. Verify fix resolves issue
4. Create PR to `main`

### Updating Dependencies
```bash
# Update all dependencies
go get -u ./...

# Update specific dependency
go get -u github.com/specific/package

# Tidy up modules
go mod tidy
```

## Getting Help

- **Issues**: [GitHub Issues](https://github.com/ozskywalker/tiktok-favvideo-downloader/issues)
- **Questions**: Open a GitHub Discussion or Issue
- **Bug Reports**: Include steps to reproduce, expected vs actual behavior

## Code of Conduct

- Be respectful and constructive in all interactions
- Focus on the technical aspects of contributions
- Help maintain a welcoming environment for all contributors

---

Thank you for contributing! Your efforts help make this tool better for everyone. 🚀