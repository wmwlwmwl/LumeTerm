package runtimeinstaller

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	goruntime "runtime"
	"strings"
	"time"

	runtimeenv "lumeterm/module/runtimeenv"
)

const uvLatestReleaseAPIURL = "https://api.github.com/repos/astral-sh/uv/releases/latest"

var uvGitHubMirrorPrefixes = []string{
	"https://ghproxy.net/",
	"https://gh-proxy.com/",
	"https://proxy.gitwarp.top/",
}

type gitHubRelease struct {
	TagName string               `json:"tag_name"`
	Assets  []gitHubReleaseAsset `json:"assets"`
}

type gitHubReleaseAsset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

func InstallRuntimeEnvironment(programDirectory string, settings runtimeenv.Settings, language string) (runtimeenv.Status, error) {
	normalized := runtimeenv.NormalizeSettings(settings)
	switch normalized.EnvironmentType {
	case runtimeenv.DefaultEnvironmentType:
		return installUV(programDirectory, normalized, language)
	default:
		return runtimeenv.Status{}, fmt.Errorf("unsupported runtime environment: %s", normalized.EnvironmentType)
	}
}

func installUV(programDirectory string, settings runtimeenv.Settings, language string) (runtimeenv.Status, error) {
	installRoot := runtimeenv.ResolveTargetPath(programDirectory, settings)
	if strings.TrimSpace(installRoot) == "" {
		return runtimeenv.Status{}, fmt.Errorf("runtime environment install root is empty")
	}
	if err := os.MkdirAll(installRoot, 0o755); err != nil {
		return runtimeenv.Status{}, err
	}
	currentStatus := runtimeenv.DetectStatus(programDirectory, settings)
	if currentStatus.Ready && isManagedBinaryUnderRoot(currentStatus.BinaryPath, installRoot) {
		return currentStatus, nil
	}

	client := &http.Client{Timeout: 2 * time.Minute}
	release, version, err := fetchLatestUVRelease(client, language)
	if err != nil {
		return runtimeenv.Status{}, err
	}

	versionDir := filepath.Join(installRoot, version)
	if binaryPath, err := findBinaryInTree(versionDir, uvBinaryFileName()); err == nil && binaryPath != "" {
		if err := runtimeenv.SaveManagedState(programDirectory, settings, version, binaryPath); err != nil {
			return runtimeenv.Status{}, err
		}
		return runtimeenv.DetectStatus(programDirectory, settings), nil
	}

	assetName, err := resolveUVAssetName()
	if err != nil {
		return runtimeenv.Status{}, err
	}
	asset, err := findReleaseAsset(release.Assets, assetName)
	if err != nil {
		return runtimeenv.Status{}, err
	}
	checksumAsset, _ := findReleaseAsset(release.Assets, assetName+".sha256")

	workDir, err := os.MkdirTemp(installRoot, ".uv-install-*")
	if err != nil {
		return runtimeenv.Status{}, err
	}
	defer os.RemoveAll(workDir)

	archivePath := filepath.Join(workDir, asset.Name)
	if err := downloadFile(client, asset.BrowserDownloadURL, archivePath, language); err != nil {
		return runtimeenv.Status{}, err
	}
	if checksumAsset != nil {
		if err := verifyDownloadedFile(client, checksumAsset.BrowserDownloadURL, archivePath, language); err != nil {
			return runtimeenv.Status{}, err
		}
	}

	stageDir := filepath.Join(workDir, "stage")
	if err := os.MkdirAll(stageDir, 0o755); err != nil {
		return runtimeenv.Status{}, err
	}
	if strings.HasSuffix(strings.ToLower(asset.Name), ".zip") {
		if err := extractZipArchive(archivePath, stageDir); err != nil {
			return runtimeenv.Status{}, err
		}
	} else {
		if err := extractTarGzArchive(archivePath, stageDir); err != nil {
			return runtimeenv.Status{}, err
		}
	}

	stageBinaryPath, err := findBinaryInTree(stageDir, uvBinaryFileName())
	if err != nil {
		return runtimeenv.Status{}, err
	}
	if err := os.RemoveAll(versionDir); err != nil && !os.IsNotExist(err) {
		return runtimeenv.Status{}, err
	}
	if err := os.Rename(stageDir, versionDir); err != nil {
		return runtimeenv.Status{}, err
	}
	finalBinaryPath := filepath.Join(versionDir, strings.TrimPrefix(strings.TrimPrefix(stageBinaryPath, stageDir), string(os.PathSeparator)))
	if err := runtimeenv.SaveManagedState(programDirectory, settings, version, finalBinaryPath); err != nil {
		return runtimeenv.Status{}, err
	}

	finalStatus := runtimeenv.DetectStatus(programDirectory, settings)
	if !finalStatus.Ready {
		return runtimeenv.Status{}, fmt.Errorf("uv installation finished but binary is not ready")
	}
	return finalStatus, nil
}

func uvInstallerUsesChineseMirror(language string) bool {
	normalizedLanguage := strings.ToLower(strings.TrimSpace(language))
	return strings.HasPrefix(normalizedLanguage, "zh")
}

func buildUVCandidateURLs(sourceURL string, language string) []string {
	trimmedSourceURL := strings.TrimSpace(sourceURL)
	if trimmedSourceURL == "" {
		return nil
	}
	candidates := make([]string, 0, len(uvGitHubMirrorPrefixes)+1)
	seen := make(map[string]struct{}, len(uvGitHubMirrorPrefixes)+1)
	appendCandidate := func(value string) {
		trimmedValue := strings.TrimSpace(value)
		if trimmedValue == "" {
			return
		}
		if _, ok := seen[trimmedValue]; ok {
			return
		}
		seen[trimmedValue] = struct{}{}
		candidates = append(candidates, trimmedValue)
	}
	if uvInstallerUsesChineseMirror(language) && (strings.Contains(trimmedSourceURL, "github.com") || strings.Contains(trimmedSourceURL, "api.github.com")) {
		for _, prefix := range uvGitHubMirrorPrefixes {
			appendCandidate(prefix + trimmedSourceURL)
		}
	}
	appendCandidate(trimmedSourceURL)
	return candidates
}

func fetchLatestUVRelease(client *http.Client, language string) (gitHubRelease, string, error) {
	candidateURLs := buildUVCandidateURLs(uvLatestReleaseAPIURL, language)
	var lastErr error
	for _, candidateURL := range candidateURLs {
		request, err := http.NewRequest(http.MethodGet, candidateURL, nil)
		if err != nil {
			lastErr = err
			continue
		}
		request.Header.Set("Accept", "application/vnd.github+json")
		request.Header.Set("User-Agent", "LumeTerm")
		response, err := client.Do(request)
		if err != nil {
			lastErr = err
			continue
		}
		if response.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("failed to fetch uv latest release: %s", response.Status)
			response.Body.Close()
			continue
		}
		var release gitHubRelease
		if err := json.NewDecoder(response.Body).Decode(&release); err != nil {
			lastErr = err
			response.Body.Close()
			continue
		}
		response.Body.Close()
		version := strings.TrimSpace(strings.TrimPrefix(release.TagName, "v"))
		if version == "" {
			lastErr = fmt.Errorf("uv latest release version is empty")
			continue
		}
		return release, version, nil
	}
	if lastErr != nil {
		return gitHubRelease{}, "", lastErr
	}
	return gitHubRelease{}, "", fmt.Errorf("failed to fetch uv latest release")
}

func findReleaseAsset(assets []gitHubReleaseAsset, name string) (*gitHubReleaseAsset, error) {
	for index := range assets {
		if strings.EqualFold(strings.TrimSpace(assets[index].Name), strings.TrimSpace(name)) {
			return &assets[index], nil
		}
	}
	return nil, fmt.Errorf("uv release asset not found: %s", name)
}

func resolveUVAssetName() (string, error) {
	switch goruntime.GOOS {
	case "windows":
		switch goruntime.GOARCH {
		case "amd64":
			return "uv-x86_64-pc-windows-msvc.zip", nil
		case "arm64":
			return "uv-aarch64-pc-windows-msvc.zip", nil
		case "386":
			return "uv-i686-pc-windows-msvc.zip", nil
		}
	case "darwin":
		switch goruntime.GOARCH {
		case "amd64":
			return "uv-x86_64-apple-darwin.tar.gz", nil
		case "arm64":
			return "uv-aarch64-apple-darwin.tar.gz", nil
		}
	case "linux":
		libcVariant := detectLinuxLibcVariant()
		switch goruntime.GOARCH {
		case "amd64":
			return fmt.Sprintf("uv-x86_64-unknown-linux-%s.tar.gz", libcVariant), nil
		case "386":
			return fmt.Sprintf("uv-i686-unknown-linux-%s.tar.gz", libcVariant), nil
		case "arm64":
			return fmt.Sprintf("uv-aarch64-unknown-linux-%s.tar.gz", libcVariant), nil
		case "arm":
			if libcVariant == "musl" {
				return "uv-armv7-unknown-linux-musleabihf.tar.gz", nil
			}
			return "uv-armv7-unknown-linux-gnueabihf.tar.gz", nil
		case "riscv64":
			return fmt.Sprintf("uv-riscv64gc-unknown-linux-%s.tar.gz", libcVariant), nil
		case "ppc64le":
			return "uv-powerpc64le-unknown-linux-gnu.tar.gz", nil
		case "s390x":
			return "uv-s390x-unknown-linux-gnu.tar.gz", nil
		}
	}
	return "", fmt.Errorf("unsupported uv platform: %s/%s", goruntime.GOOS, goruntime.GOARCH)
}

func detectLinuxLibcVariant() string {
	if goruntime.GOOS != "linux" {
		return "gnu"
	}
	if _, err := os.Stat("/etc/alpine-release"); err == nil {
		return "musl"
	}
	patterns := []string{
		"/lib/ld-musl-*",
		"/lib64/ld-musl-*",
		"/usr/lib/ld-musl-*",
		"/usr/lib64/ld-musl-*",
	}
	for _, pattern := range patterns {
		matches, err := filepath.Glob(pattern)
		if err == nil && len(matches) > 0 {
			return "musl"
		}
	}
	return "gnu"
}

func uvBinaryFileName() string {
	if goruntime.GOOS == "windows" {
		return "uv.exe"
	}
	return "uv"
}

func downloadFile(client *http.Client, sourceURL string, destinationPath string, language string) error {
	candidateURLs := buildUVCandidateURLs(sourceURL, language)
	var lastErr error
	for _, candidateURL := range candidateURLs {
		if err := downloadFileOnce(client, candidateURL, destinationPath); err == nil {
			return nil
		} else {
			lastErr = err
			_ = os.Remove(destinationPath)
		}
	}
	if lastErr != nil {
		return lastErr
	}
	return fmt.Errorf("failed to download %s", sourceURL)
}

func downloadFileOnce(client *http.Client, sourceURL string, destinationPath string) error {
	response, err := client.Get(sourceURL)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to download %s: %s", sourceURL, response.Status)
	}
	file, err := os.Create(destinationPath)
	if err != nil {
		return err
	}
	defer file.Close()
	if _, err := io.Copy(file, response.Body); err != nil {
		return err
	}
	return file.Close()
}

func verifyDownloadedFile(client *http.Client, checksumURL string, filePath string, language string) error {
	checksumFields, err := downloadChecksumFields(client, checksumURL, language)
	if err != nil {
		return err
	}
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	hash := sha256.New()
	if _, err := io.Copy(hash, file); err != nil {
		return err
	}
	actualChecksum := hex.EncodeToString(hash.Sum(nil))
	if !strings.EqualFold(actualChecksum, checksumFields[0]) {
		return fmt.Errorf("checksum mismatch: expected %s, got %s", checksumFields[0], actualChecksum)
	}
	return nil
}

func downloadChecksumFields(client *http.Client, checksumURL string, language string) ([]string, error) {
	candidateURLs := buildUVCandidateURLs(checksumURL, language)
	var lastErr error
	for _, candidateURL := range candidateURLs {
		checksumFields, err := downloadChecksumFieldsOnce(client, candidateURL)
		if err == nil {
			return checksumFields, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, fmt.Errorf("failed to download checksum %s", checksumURL)
}

func downloadChecksumFieldsOnce(client *http.Client, checksumURL string) ([]string, error) {
	response, err := client.Get(checksumURL)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to download checksum %s: %s", checksumURL, response.Status)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return nil, err
	}
	checksumFields := strings.Fields(strings.TrimSpace(string(body)))
	if len(checksumFields) == 0 {
		return nil, fmt.Errorf("checksum file is empty")
	}
	return checksumFields, nil
}

func extractZipArchive(archivePath string, destinationDir string) error {
	reader, err := zip.OpenReader(archivePath)
	if err != nil {
		return err
	}
	defer reader.Close()
	cleanDestinationDir := filepath.Clean(destinationDir)
	for _, file := range reader.File {
		targetPath := filepath.Join(cleanDestinationDir, filepath.FromSlash(file.Name))
		cleanTargetPath := filepath.Clean(targetPath)
		if cleanTargetPath != cleanDestinationDir && !strings.HasPrefix(cleanTargetPath, cleanDestinationDir+string(os.PathSeparator)) {
			return fmt.Errorf("zip entry escapes destination: %s", file.Name)
		}
		if file.FileInfo().IsDir() {
			if err := os.MkdirAll(cleanTargetPath, 0o755); err != nil {
				return err
			}
			continue
		}
		if err := os.MkdirAll(filepath.Dir(cleanTargetPath), 0o755); err != nil {
			return err
		}
		sourceFile, err := file.Open()
		if err != nil {
			return err
		}
		targetFile, err := os.OpenFile(cleanTargetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, file.Mode())
		if err != nil {
			sourceFile.Close()
			return err
		}
		if _, err := io.Copy(targetFile, sourceFile); err != nil {
			targetFile.Close()
			sourceFile.Close()
			return err
		}
		if err := targetFile.Close(); err != nil {
			sourceFile.Close()
			return err
		}
		if err := sourceFile.Close(); err != nil {
			return err
		}
	}
	return nil
}

func extractTarGzArchive(archivePath string, destinationDir string) error {
	archiveFile, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer archiveFile.Close()
	gzipReader, err := gzip.NewReader(archiveFile)
	if err != nil {
		return err
	}
	defer gzipReader.Close()
	tarReader := tar.NewReader(gzipReader)
	cleanDestinationDir := filepath.Clean(destinationDir)
	for {
		header, err := tarReader.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		targetPath := filepath.Join(cleanDestinationDir, filepath.FromSlash(header.Name))
		cleanTargetPath := filepath.Clean(targetPath)
		if cleanTargetPath != cleanDestinationDir && !strings.HasPrefix(cleanTargetPath, cleanDestinationDir+string(os.PathSeparator)) {
			return fmt.Errorf("tar entry escapes destination: %s", header.Name)
		}
		switch header.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(cleanTargetPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if err := os.MkdirAll(filepath.Dir(cleanTargetPath), 0o755); err != nil {
				return err
			}
			targetFile, err := os.OpenFile(cleanTargetPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, os.FileMode(header.Mode))
			if err != nil {
				return err
			}
			if _, err := io.Copy(targetFile, tarReader); err != nil {
				targetFile.Close()
				return err
			}
			if err := targetFile.Close(); err != nil {
				return err
			}
		case tar.TypeSymlink:
			// 校验符号链接目标解析后仍落在解压目录内，防止恶意包借链接写出界
			linkDest := filepath.Clean(filepath.Join(filepath.Dir(cleanTargetPath), filepath.FromSlash(header.Linkname)))
			if linkDest != cleanDestinationDir && !strings.HasPrefix(linkDest, cleanDestinationDir+string(os.PathSeparator)) {
				return fmt.Errorf("tar symlink escapes destination: %s", header.Name)
			}
			if err := os.MkdirAll(filepath.Dir(cleanTargetPath), 0o755); err != nil {
				return err
			}
			if err := os.Symlink(header.Linkname, cleanTargetPath); err != nil && !os.IsExist(err) {
				return err
			}
		}
	}
}

func findBinaryInTree(rootDir string, binaryName string) (string, error) {
	rootDir = filepath.Clean(strings.TrimSpace(rootDir))
	if rootDir == "" {
		return "", fmt.Errorf("binary root is empty")
	}
	var binaryPath string
	err := filepath.WalkDir(rootDir, func(currentPath string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		if strings.EqualFold(entry.Name(), binaryName) {
			binaryPath = currentPath
			return io.EOF
		}
		return nil
	})
	if err != nil && err != io.EOF {
		return "", err
	}
	if strings.TrimSpace(binaryPath) == "" {
		return "", fmt.Errorf("uv binary not found in extracted archive")
	}
	if absolutePath, absErr := filepath.Abs(binaryPath); absErr == nil {
		binaryPath = absolutePath
	}
	return filepath.Clean(binaryPath), nil
}

func isManagedBinaryUnderRoot(binaryPath string, installRoot string) bool {
	cleanBinaryPath := filepath.Clean(strings.TrimSpace(binaryPath))
	cleanInstallRoot := filepath.Clean(strings.TrimSpace(installRoot))
	if cleanBinaryPath == "" || cleanInstallRoot == "" {
		return false
	}
	if cleanBinaryPath == cleanInstallRoot {
		return true
	}
	return strings.HasPrefix(cleanBinaryPath, cleanInstallRoot+string(os.PathSeparator))
}