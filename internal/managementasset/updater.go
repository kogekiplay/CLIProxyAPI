package managementasset

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/httpfetch"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/util"
	sdkconfig "github.com/router-for-me/CLIProxyAPI/v7/sdk/config"
	log "github.com/sirupsen/logrus"
	"golang.org/x/sync/singleflight"
)

const (
	defaultManagementReleaseURL  = "https://api.github.com/repos/kogekiplay/Cli-Proxy-API-Management-Center/releases/latest"
	defaultManagementFallbackURL = "https://github.com/kogekiplay/Cli-Proxy-API-Management-Center/releases/latest/download/management.html"
	managementAssetName          = "management.html"
	managementBundleAssetName    = "management-bundle.tar.gz"
	managementAssetsDirName      = "management-assets"
	managementBundleHashName     = ".management-bundle.sha256"
	httpUserAgent                = "CLIProxyAPI-management-updater"
	managementSyncMinInterval    = 30 * time.Second
	updateCheckInterval          = 3 * time.Hour
	maxAssetDownloadSize         = 50 << 20 // 50 MB safety limit for management asset downloads
	maxBundleExtractSize         = 100 << 20
	retainedAssetVersions        = 3
)

// ManagementFileName exposes the control panel asset filename.
const ManagementFileName = managementAssetName

// ManagementAssetsDirName exposes the directory used by the split control-panel bundle.
const ManagementAssetsDirName = managementAssetsDirName

var (
	lastUpdateCheckMu   sync.Mutex
	lastUpdateCheckTime time.Time
	currentConfigPtr    atomic.Pointer[config.Config]
	schedulerOnce       sync.Once
	schedulerConfigPath atomic.Value
	sfGroup             singleflight.Group
)

// SetCurrentConfig stores the latest configuration snapshot for management asset decisions.
func SetCurrentConfig(cfg *config.Config) {
	if cfg == nil {
		currentConfigPtr.Store(nil)
		return
	}
	currentConfigPtr.Store(cfg)
}

// StartAutoUpdater launches a background goroutine that periodically ensures the management asset is up to date.
// It respects the disable-control-panel flag on every iteration and supports hot-reloaded configurations.
func StartAutoUpdater(ctx context.Context, configFilePath string) {
	configFilePath = strings.TrimSpace(configFilePath)
	if configFilePath == "" {
		log.Debug("management asset auto-updater skipped: empty config path")
		return
	}

	schedulerConfigPath.Store(configFilePath)

	schedulerOnce.Do(func() {
		go runAutoUpdater(ctx)
	})
}

func runAutoUpdater(ctx context.Context) {
	if ctx == nil {
		ctx = context.Background()
	}

	ticker := time.NewTicker(updateCheckInterval)
	defer ticker.Stop()

	runOnce := func() {
		cfg := currentConfigPtr.Load()
		if reason, skip := autoUpdateSkipReason(cfg); skip {
			log.Debugf("management asset auto-updater skipped: %s", reason)
			return
		}

		configPath, _ := schedulerConfigPath.Load().(string)
		staticDir := StaticDir(configPath)
		EnsureLatestManagementHTML(ctx, staticDir, cfg.ProxyURL, cfg.RemoteManagement.PanelGitHubRepository)
	}

	runOnce()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			runOnce()
		}
	}
}

func autoUpdateSkipReason(cfg *config.Config) (string, bool) {
	if cfg == nil {
		return "config not yet available", true
	}
	if cfg.Home.Enabled {
		return "cluster mode enabled", true
	}
	if cfg.RemoteManagement.DisableControlPanel {
		return "control panel disabled", true
	}
	if cfg.RemoteManagement.DisableAutoUpdatePanel {
		return "disable-auto-update-panel is enabled", true
	}
	return "", false
}

func newHTTPClient(proxyURL string) *http.Client {
	client := &http.Client{Timeout: 15 * time.Second}

	sdkCfg := &sdkconfig.SDKConfig{ProxyURL: strings.TrimSpace(proxyURL)}
	util.SetProxy(sdkCfg, client)

	return client
}

type releaseAsset struct {
	Name               string `json:"name"`
	URL                string `json:"url"`
	BrowserDownloadURL string `json:"browser_download_url"`
	Digest             string `json:"digest"`
}

type releaseResponse struct {
	Assets []releaseAsset `json:"assets"`
}

type releaseAssets struct {
	bundle         *releaseAsset
	bundleHash     string
	standalone     *releaseAsset
	standaloneHash string
}

// StaticDir resolves the directory that stores the management control panel asset.
func StaticDir(configFilePath string) string {
	if override := strings.TrimSpace(os.Getenv("MANAGEMENT_STATIC_PATH")); override != "" {
		cleaned := filepath.Clean(override)
		if strings.EqualFold(filepath.Base(cleaned), managementAssetName) {
			return filepath.Dir(cleaned)
		}
		return cleaned
	}

	if writable := util.WritablePath(); writable != "" {
		return filepath.Join(writable, "static")
	}

	configFilePath = strings.TrimSpace(configFilePath)
	if configFilePath == "" {
		return ""
	}

	base := filepath.Dir(configFilePath)
	fileInfo, err := os.Stat(configFilePath)
	if err == nil {
		if fileInfo.IsDir() {
			base = configFilePath
		}
	}

	return filepath.Join(base, "static")
}

// FilePath resolves the absolute path to the management control panel asset.
func FilePath(configFilePath string) string {
	return FilePathFor(configFilePath, ManagementFileName)
}

// FilePathFor resolves the absolute path to a named control-panel asset.
// stored in the configured management static directory.
func FilePathFor(configFilePath string, name string) string {
	if override := strings.TrimSpace(os.Getenv("MANAGEMENT_STATIC_PATH")); override != "" {
		cleaned := filepath.Clean(override)
		if strings.EqualFold(filepath.Base(cleaned), managementAssetName) {
			cleaned = filepath.Dir(cleaned)
		}
		return filepath.Join(cleaned, name)
	}

	dir := StaticDir(configFilePath)
	if dir == "" {
		return ""
	}
	return filepath.Join(dir, name)
}

// EnsureLatestManagementHTML checks the latest management.html asset and updates the local copy when needed.
// It coalesces concurrent sync attempts and returns whether the asset exists after the sync attempt.
func EnsureLatestManagementHTML(ctx context.Context, staticDir string, proxyURL string, panelRepository string) bool {
	if ctx == nil {
		ctx = context.Background()
	}

	staticDir = strings.TrimSpace(staticDir)
	if staticDir == "" {
		log.Debug("management asset sync skipped: empty static directory")
		return false
	}
	localPath := filepath.Join(staticDir, managementAssetName)

	_, _, _ = sfGroup.Do(localPath, func() (interface{}, error) {
		lastUpdateCheckMu.Lock()
		now := time.Now()
		timeSinceLastAttempt := now.Sub(lastUpdateCheckTime)
		if !lastUpdateCheckTime.IsZero() && timeSinceLastAttempt < managementSyncMinInterval {
			lastUpdateCheckMu.Unlock()
			log.Debugf(
				"management asset sync skipped by throttle: last attempt %v ago (interval %v)",
				timeSinceLastAttempt.Round(time.Second),
				managementSyncMinInterval,
			)
			return nil, nil
		}
		lastUpdateCheckTime = now
		lastUpdateCheckMu.Unlock()

		localFileMissing := false
		if _, errStat := os.Stat(localPath); errStat != nil {
			if errors.Is(errStat, os.ErrNotExist) {
				localFileMissing = true
			} else {
				log.WithError(errStat).Debug("failed to stat local management asset")
			}
		}

		if errMkdirAll := os.MkdirAll(staticDir, 0o755); errMkdirAll != nil {
			log.WithError(errMkdirAll).Warn("failed to prepare static directory for management asset")
			return nil, nil
		}

		releaseURL := resolveReleaseURL(panelRepository)
		client := newHTTPClient(proxyURL)

		assets, err := fetchLatestAssets(ctx, client, releaseURL)
		if err != nil {
			if localFileMissing {
				log.WithError(err).Warn("failed to fetch latest management release information, trying fallback page")
				if ensureFallbackManagementHTML(ctx, client, localPath) {
					return nil, nil
				}
				return nil, nil
			}
			log.WithError(err).Warn("failed to fetch latest management release information")
			return nil, nil
		}

		if assets.bundle != nil {
			if err = syncManagementBundle(ctx, client, staticDir, assets.bundle, assets.bundleHash); err == nil {
				return nil, nil
			}
			log.WithError(err).Warn("failed to update management bundle, trying standalone control panel")
			_ = os.Remove(filepath.Join(staticDir, managementBundleHashName))
		}

		if assets.standalone != nil {
			if err = syncStandaloneManagementHTML(ctx, client, localPath, assets.standalone, assets.standaloneHash); err == nil {
				return nil, nil
			}
			log.WithError(err).Warn("failed to update standalone management control panel")
		}

		if localFileMissing {
			log.Warn("latest management release did not yield a usable control panel, trying fallback page")
			_ = ensureFallbackManagementHTML(ctx, client, localPath)
		}
		return nil, nil
	})

	_, err := os.Stat(localPath)
	return err == nil
}

func syncManagementBundle(ctx context.Context, client *http.Client, staticDir string, asset *releaseAsset, remoteHash string) error {
	hashPath := filepath.Join(staticDir, managementBundleHashName)
	localHash := readHashMarker(hashPath)
	if remoteHash != "" && localHash != "" && strings.EqualFold(remoteHash, localHash) {
		if managementBundleInstalled(staticDir) {
			log.Debug("management bundle is already up to date")
			return nil
		}
	}

	data, downloadedHash, err := downloadReleaseAsset(ctx, client, asset)
	if err != nil {
		return err
	}
	if remoteHash != "" && !strings.EqualFold(remoteHash, downloadedHash) {
		return fmt.Errorf("management bundle digest mismatch: expected %s got %s", remoteHash, downloadedHash)
	}
	if localHash != "" && strings.EqualFold(localHash, downloadedHash) {
		if managementBundleInstalled(staticDir) {
			log.Debug("management bundle is already up to date")
			return nil
		}
	}

	if err = installManagementBundle(staticDir, data); err != nil {
		return fmt.Errorf("install management bundle: %w", err)
	}
	if err = atomicWriteFile(hashPath, []byte(downloadedHash+"\n")); err != nil {
		return fmt.Errorf("write management bundle hash: %w", err)
	}

	log.Infof("management bundle updated successfully (hash=%s)", downloadedHash)
	return nil
}

func managementBundleInstalled(staticDir string) bool {
	info, err := os.Stat(filepath.Join(staticDir, managementAssetName))
	return err == nil && info.Mode().IsRegular() && directoryHasRegularFile(filepath.Join(staticDir, managementAssetsDirName))
}

func syncStandaloneManagementHTML(ctx context.Context, client *http.Client, localPath string, asset *releaseAsset, remoteHash string) error {
	localHash, err := fileSHA256(localPath)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			log.WithError(err).Debug("failed to read local management asset hash")
		}
		localHash = ""
	}
	if remoteHash != "" && localHash != "" && strings.EqualFold(remoteHash, localHash) {
		log.Debug("standalone management asset is already up to date")
		return nil
	}

	data, downloadedHash, err := downloadReleaseAsset(ctx, client, asset)
	if err != nil {
		return err
	}
	if remoteHash != "" && !strings.EqualFold(remoteHash, downloadedHash) {
		return fmt.Errorf("management asset digest mismatch: expected %s got %s", remoteHash, downloadedHash)
	}
	if err = atomicWriteFile(localPath, data); err != nil {
		return fmt.Errorf("write management asset: %w", err)
	}
	_ = os.Remove(filepath.Join(filepath.Dir(localPath), managementBundleHashName))

	log.Infof("standalone management asset updated successfully (hash=%s)", downloadedHash)
	return nil
}

func readHashMarker(path string) string {
	data, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return strings.ToLower(strings.TrimSpace(string(data)))
}

func ensureFallbackManagementHTML(ctx context.Context, client *http.Client, localPath string) bool {
	data, downloadedHash, err := downloadAsset(ctx, client, defaultManagementFallbackURL)
	if err != nil {
		log.WithError(err).Warn("failed to download fallback management control panel page")
		return false
	}

	log.Warnf("management asset downloaded from fallback URL without digest verification (hash=%s) — "+
		"enable verified GitHub updates by keeping disable-auto-update-panel set to false", downloadedHash)

	if err = atomicWriteFile(localPath, data); err != nil {
		log.WithError(err).Warn("failed to persist fallback management control panel page")
		return false
	}
	_ = os.Remove(filepath.Join(filepath.Dir(localPath), managementBundleHashName))

	log.Infof("management asset updated from fallback page successfully (hash=%s)", downloadedHash)
	return true
}

func installManagementBundle(staticDir string, data []byte) error {
	tempDir, err := os.MkdirTemp(staticDir, ".management-bundle-*")
	if err != nil {
		return err
	}
	defer func() {
		_ = os.RemoveAll(tempDir)
	}()

	if err = extractManagementBundle(data, tempDir); err != nil {
		return err
	}

	htmlData, err := os.ReadFile(filepath.Join(tempDir, managementAssetName))
	if err != nil {
		return fmt.Errorf("read bundled management page: %w", err)
	}
	currentVersions, err := mergeManagementAssets(
		filepath.Join(tempDir, managementAssetsDirName),
		filepath.Join(staticDir, managementAssetsDirName),
	)
	if err != nil {
		return err
	}

	if err = atomicWriteFile(filepath.Join(staticDir, managementAssetName), htmlData); err != nil {
		return fmt.Errorf("activate bundled management page: %w", err)
	}
	cleanupStaleAssetVersions(filepath.Join(staticDir, managementAssetsDirName), currentVersions)
	return nil
}

func extractManagementBundle(data []byte, destination string) error {
	gzipReader, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return fmt.Errorf("open management bundle gzip stream: %w", err)
	}
	defer func() {
		_ = gzipReader.Close()
	}()

	tarReader := tar.NewReader(gzipReader)
	var extractedSize int64
	seenFiles := make(map[string]struct{})
	for {
		header, errNext := tarReader.Next()
		if errors.Is(errNext, io.EOF) {
			break
		}
		if errNext != nil {
			return fmt.Errorf("read management bundle: %w", errNext)
		}

		cleanName := path.Clean(strings.TrimSpace(header.Name))
		if cleanName == "." || path.IsAbs(cleanName) || cleanName == ".." || strings.HasPrefix(cleanName, "../") {
			return fmt.Errorf("invalid management bundle path %q", header.Name)
		}
		if cleanName != managementAssetName && cleanName != managementAssetsDirName && !strings.HasPrefix(cleanName, managementAssetsDirName+"/") {
			return fmt.Errorf("unexpected management bundle path %q", header.Name)
		}

		targetPath := filepath.Join(destination, filepath.FromSlash(cleanName))
		relativePath, errRel := filepath.Rel(destination, targetPath)
		if errRel != nil || relativePath == ".." || strings.HasPrefix(relativePath, ".."+string(filepath.Separator)) {
			return fmt.Errorf("management bundle path escapes destination: %q", header.Name)
		}

		switch header.Typeflag {
		case tar.TypeDir:
			if err = os.MkdirAll(targetPath, 0o755); err != nil {
				return err
			}
		case tar.TypeReg, tar.TypeRegA:
			if header.Size < 0 || header.Size > maxBundleExtractSize {
				return fmt.Errorf("management bundle file %q is too large", header.Name)
			}
			extractedSize += header.Size
			if extractedSize > maxBundleExtractSize {
				return fmt.Errorf("management bundle expands beyond %d bytes", maxBundleExtractSize)
			}
			if _, duplicate := seenFiles[cleanName]; duplicate {
				return fmt.Errorf("duplicate management bundle file %q", header.Name)
			}
			seenFiles[cleanName] = struct{}{}
			if err = os.MkdirAll(filepath.Dir(targetPath), 0o755); err != nil {
				return err
			}
			file, errCreate := os.OpenFile(targetPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
			if errCreate != nil {
				return errCreate
			}
			written, errCopy := io.CopyN(file, tarReader, header.Size)
			errClose := file.Close()
			if errCopy != nil {
				return errCopy
			}
			if errClose != nil {
				return errClose
			}
			if written != header.Size {
				return fmt.Errorf("short management bundle file %q", header.Name)
			}
		default:
			return fmt.Errorf("unsupported management bundle entry %q", header.Name)
		}
	}

	if info, errStat := os.Stat(filepath.Join(destination, managementAssetName)); errStat != nil || !info.Mode().IsRegular() {
		return fmt.Errorf("management bundle is missing %s", managementAssetName)
	}
	assetsPath := filepath.Join(destination, managementAssetsDirName)
	if !directoryHasRegularFile(assetsPath) {
		return fmt.Errorf("management bundle is missing static assets")
	}
	return nil
}

func directoryHasRegularFile(root string) bool {
	found := false
	errFound := errors.New("management asset found")
	_ = filepath.Walk(root, func(_ string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if info.Mode().IsRegular() {
			found = true
			return errFound
		}
		return nil
	})
	return found
}

func mergeManagementAssets(sourceRoot string, destinationRoot string) ([]string, error) {
	entries, err := os.ReadDir(sourceRoot)
	if err != nil {
		return nil, fmt.Errorf("read bundled management assets: %w", err)
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("management bundle contains no static assets")
	}
	if err = os.MkdirAll(destinationRoot, 0o755); err != nil {
		return nil, err
	}

	currentVersions := make([]string, 0, len(entries))
	for _, entry := range entries {
		sourcePath := filepath.Join(sourceRoot, entry.Name())
		destinationPath := filepath.Join(destinationRoot, entry.Name())
		if entry.IsDir() {
			currentVersions = append(currentVersions, entry.Name())
			if err = mergeManagementAssetDirectory(sourcePath, destinationPath); err != nil {
				return nil, err
			}
			continue
		}
		if entry.Type().IsRegular() {
			if err = copyManagementAssetIfMissing(sourcePath, destinationPath); err != nil {
				return nil, err
			}
			continue
		}
		return nil, fmt.Errorf("unsupported bundled management asset %q", entry.Name())
	}
	return currentVersions, nil
}

func mergeManagementAssetDirectory(sourceRoot string, destinationRoot string) error {
	return filepath.Walk(sourceRoot, func(sourcePath string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		relativePath, errRel := filepath.Rel(sourceRoot, sourcePath)
		if errRel != nil {
			return errRel
		}
		destinationPath := filepath.Join(destinationRoot, relativePath)
		if info.IsDir() {
			return os.MkdirAll(destinationPath, 0o755)
		}
		if !info.Mode().IsRegular() {
			return fmt.Errorf("unsupported bundled management asset %q", relativePath)
		}
		return copyManagementAssetIfMissing(sourcePath, destinationPath)
	})
}

func copyManagementAssetIfMissing(sourcePath string, destinationPath string) error {
	if _, err := os.Stat(destinationPath); err == nil {
		return nil
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(destinationPath), 0o755); err != nil {
		return err
	}

	source, err := os.Open(sourcePath)
	if err != nil {
		return err
	}
	defer func() {
		_ = source.Close()
	}()

	destination, err := os.OpenFile(destinationPath, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o644)
	if errors.Is(err, os.ErrExist) {
		return nil
	}
	if err != nil {
		return err
	}
	removeOnFailure := true
	defer func() {
		_ = destination.Close()
		if removeOnFailure {
			_ = os.Remove(destinationPath)
		}
	}()

	if _, err = io.Copy(destination, source); err != nil {
		return err
	}
	if err = destination.Close(); err != nil {
		return err
	}
	removeOnFailure = false
	return nil
}

func cleanupStaleAssetVersions(assetsRoot string, currentVersions []string) {
	if len(currentVersions) == 0 {
		return
	}
	current := make(map[string]struct{}, len(currentVersions))
	for _, name := range currentVersions {
		current[name] = struct{}{}
	}

	type versionEntry struct {
		name    string
		modTime time.Time
	}
	entries, err := os.ReadDir(assetsRoot)
	if err != nil {
		return
	}
	stale := make([]versionEntry, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if _, active := current[entry.Name()]; active {
			continue
		}
		info, errInfo := entry.Info()
		if errInfo != nil {
			continue
		}
		stale = append(stale, versionEntry{name: entry.Name(), modTime: info.ModTime()})
	}
	sort.Slice(stale, func(i, j int) bool {
		return stale[i].modTime.After(stale[j].modTime)
	})
	keepOld := retainedAssetVersions - len(current)
	if keepOld < 0 {
		keepOld = 0
	}
	if keepOld > len(stale) {
		keepOld = len(stale)
	}
	for _, entry := range stale[keepOld:] {
		if errRemove := os.RemoveAll(filepath.Join(assetsRoot, entry.name)); errRemove != nil {
			log.WithError(errRemove).Debugf("failed to remove stale management asset version %s", entry.name)
		}
	}
}

func resolveReleaseURL(repo string) string {
	repo = strings.TrimSpace(repo)
	if repo == "" {
		return defaultManagementReleaseURL
	}

	parsed, err := url.Parse(repo)
	if err != nil || parsed.Host == "" {
		return defaultManagementReleaseURL
	}

	host := strings.ToLower(parsed.Host)
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")

	if host == "api.github.com" {
		if !strings.HasSuffix(strings.ToLower(parsed.Path), "/releases/latest") {
			parsed.Path = parsed.Path + "/releases/latest"
		}
		return parsed.String()
	}

	if host == "github.com" {
		parts := strings.Split(strings.Trim(parsed.Path, "/"), "/")
		if len(parts) >= 2 && parts[0] != "" && parts[1] != "" {
			repoName := strings.TrimSuffix(parts[1], ".git")
			return fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", parts[0], repoName)
		}
	}

	return defaultManagementReleaseURL
}

func fetchLatestAssets(ctx context.Context, client *http.Client, releaseURL string) (*releaseAssets, error) {
	if strings.TrimSpace(releaseURL) == "" {
		releaseURL = defaultManagementReleaseURL
	}

	headers := map[string]string{
		"Accept":     "application/vnd.github+json",
		"User-Agent": httpUserAgent,
	}
	gitURL := strings.ToLower(strings.TrimSpace(os.Getenv("GITSTORE_GIT_URL")))
	if tok := strings.TrimSpace(os.Getenv("GITSTORE_GIT_TOKEN")); tok != "" && strings.Contains(gitURL, "github.com") {
		headers["Authorization"] = "Bearer " + tok
	}

	data, err := httpfetch.GetBytes(ctx, client, releaseURL, headers, 0)
	if err != nil {
		return nil, fmt.Errorf("fetch release: %w", err)
	}

	var release releaseResponse
	if err = json.Unmarshal(data, &release); err != nil {
		return nil, fmt.Errorf("decode release response: %w", err)
	}

	assets := &releaseAssets{}
	for i := range release.Assets {
		asset := &release.Assets[i]
		switch {
		case strings.EqualFold(asset.Name, managementBundleAssetName):
			assets.bundle = asset
			assets.bundleHash = parseDigest(asset.Digest)
		case strings.EqualFold(asset.Name, managementAssetName):
			assets.standalone = asset
			assets.standaloneHash = parseDigest(asset.Digest)
		}
	}
	if assets.bundle == nil && assets.standalone == nil {
		return nil, fmt.Errorf("management assets %s and %s not found in latest release", managementBundleAssetName, managementAssetName)
	}
	return assets, nil
}

func fetchLatestAsset(ctx context.Context, client *http.Client, releaseURL string) (*releaseAsset, string, error) {
	assets, err := fetchLatestAssets(ctx, client, releaseURL)
	if err != nil {
		return nil, "", err
	}
	if assets.standalone == nil {
		return nil, "", fmt.Errorf("management asset %s not found in latest release", managementAssetName)
	}
	return assets.standalone, assets.standaloneHash, nil
}

func downloadAsset(ctx context.Context, client *http.Client, downloadURL string) ([]byte, string, error) {
	if strings.TrimSpace(downloadURL) == "" {
		return nil, "", fmt.Errorf("empty download url")
	}

	data, err := httpfetch.GetBytes(ctx, client, downloadURL, map[string]string{"User-Agent": httpUserAgent}, maxAssetDownloadSize)
	if err != nil {
		return nil, "", fmt.Errorf("download asset: %w", err)
	}

	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:]), nil
}

func downloadReleaseAsset(ctx context.Context, client *http.Client, asset *releaseAsset) ([]byte, string, error) {
	if asset == nil {
		return nil, "", fmt.Errorf("missing release asset")
	}
	if strings.TrimSpace(asset.URL) != "" {
		data, err := httpfetch.GetBytes(ctx, client, asset.URL, map[string]string{
			"Accept":     "application/octet-stream",
			"User-Agent": httpUserAgent,
		}, maxAssetDownloadSize)
		if err == nil {
			sum := sha256.Sum256(data)
			return data, hex.EncodeToString(sum[:]), nil
		}
		log.WithError(err).Warn("failed to download management asset through GitHub API, trying browser download URL")
	}
	return downloadAsset(ctx, client, asset.BrowserDownloadURL)
}

func fileSHA256(path string) (string, error) {
	file, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer func() {
		_ = file.Close()
	}()

	h := sha256.New()
	if _, err = io.Copy(h, file); err != nil {
		return "", err
	}

	return hex.EncodeToString(h.Sum(nil)), nil
}

func atomicWriteFile(path string, data []byte) error {
	tmpFile, err := os.CreateTemp(filepath.Dir(path), "management-*.html")
	if err != nil {
		return err
	}

	tmpName := tmpFile.Name()
	defer func() {
		_ = tmpFile.Close()
		_ = os.Remove(tmpName)
	}()

	if _, err = tmpFile.Write(data); err != nil {
		return err
	}

	if err = tmpFile.Chmod(0o644); err != nil {
		return err
	}

	if err = tmpFile.Close(); err != nil {
		return err
	}

	if err = os.Rename(tmpName, path); err != nil {
		return err
	}

	return nil
}

func parseDigest(digest string) string {
	digest = strings.TrimSpace(digest)
	if digest == "" {
		return ""
	}

	if idx := strings.Index(digest, ":"); idx >= 0 {
		digest = digest[idx+1:]
	}

	return strings.ToLower(strings.TrimSpace(digest))
}
