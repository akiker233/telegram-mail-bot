package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"runtime"
	"testing"
)

// withFakes 临时替换网络请求和实际替换二进制的三个包级变量，测试结束后自动还原。
func withFakes(t *testing.T, release func(ctx context.Context, client *http.Client) (*Release, error), asset func(ctx context.Context, client *http.Client, url string) ([]byte, error), apply func(binary []byte) error) *bool {
	t.Helper()
	origRelease, origAsset, origApply := fetchLatestRelease, fetchAsset, applyBinary
	applyCalled := false

	fetchLatestRelease = release
	fetchAsset = asset
	applyBinary = func(binary []byte) error {
		applyCalled = true
		if apply != nil {
			return apply(binary)
		}
		return nil
	}

	t.Cleanup(func() {
		fetchLatestRelease = origRelease
		fetchAsset = origAsset
		applyBinary = origApply
	})

	return &applyCalled
}

func TestRunEmptyVersionErrors(t *testing.T) {
	applyCalled := withFakes(t, nil, nil, nil)

	err := Run("", http.DefaultClient)
	if err == nil {
		t.Fatal("expected error for empty current version, got nil")
	}
	if *applyCalled {
		t.Error("applyBinary should not be called when current version is empty")
	}
}

func TestRunAlreadyLatestVersion(t *testing.T) {
	applyCalled := withFakes(t, func(ctx context.Context, client *http.Client) (*Release, error) {
		return &Release{TagName: "v1.1.0"}, nil
	}, nil, nil)

	err := Run("v1.1.0", http.DefaultClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if *applyCalled {
		t.Error("applyBinary should not be called when already on latest version")
	}
}

func TestRunNewVersionAvailableAppliesUpdate(t *testing.T) {
	binaryContent := []byte("fake binary content")
	archiveData := buildArchiveForCurrentPlatform(t, binaryContent)
	assetName := fmt.Sprintf("mailbot_%s_%s.%s", runtime.GOOS, runtime.GOARCH, archiveExt())
	sum := sha256.Sum256(archiveData)
	checksums := fmt.Sprintf("%s  %s\n", hex.EncodeToString(sum[:]), assetName)

	applyCalled := withFakes(t, func(ctx context.Context, client *http.Client) (*Release, error) {
		return &Release{
			TagName: "v1.1.0",
			Assets: []Asset{
				{Name: assetName, BrowserDownloadURL: "https://example.com/" + assetName},
				{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
			},
		}, nil
	}, func(ctx context.Context, client *http.Client, url string) ([]byte, error) {
		if url == "https://example.com/checksums.txt" {
			return []byte(checksums), nil
		}
		return archiveData, nil
	}, func(binary []byte) error {
		if string(binary) != string(binaryContent) {
			t.Errorf("applyBinary got unexpected content: %q", binary)
		}
		return nil
	})

	if err := Run("v1.0.0", http.DefaultClient); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !*applyCalled {
		t.Error("expected applyBinary to be called when a new version is available")
	}
}

func TestRunPassesCustomClientToFetchers(t *testing.T) {
	customClient := &http.Client{Timeout: 456}
	var gotReleaseClient, gotAssetClient *http.Client

	applyCalled := withFakes(t, func(ctx context.Context, client *http.Client) (*Release, error) {
		gotReleaseClient = client
		return &Release{TagName: "v1.1.0"}, nil
	}, func(ctx context.Context, client *http.Client, url string) ([]byte, error) {
		gotAssetClient = client
		return []byte("checksum"), nil
	}, nil)

	// 版本相同，applyBinary 不会被调用，但 fetchLatestRelease 仍会被调用一次。
	_ = Run("v1.1.0", customClient)
	if gotReleaseClient != customClient {
		t.Errorf("fetchLatestRelease did not receive custom client")
	}
	if *applyCalled {
		t.Error("applyBinary should not be called when already on latest version")
	}
	_ = gotAssetClient
}

func TestRunChecksumMismatchAbortsWithoutApply(t *testing.T) {
	archiveData := buildArchiveForCurrentPlatform(t, []byte("fake binary content"))
	assetName := fmt.Sprintf("mailbot_%s_%s.%s", runtime.GOOS, runtime.GOARCH, archiveExt())
	wrongChecksums := fmt.Sprintf("%s  %s\n", "0000000000000000000000000000000000000000000000000000000000000000", assetName)

	applyCalled := withFakes(t, func(ctx context.Context, client *http.Client) (*Release, error) {
		return &Release{
			TagName: "v1.1.0",
			Assets: []Asset{
				{Name: assetName, BrowserDownloadURL: "https://example.com/" + assetName},
				{Name: "checksums.txt", BrowserDownloadURL: "https://example.com/checksums.txt"},
			},
		}, nil
	}, func(ctx context.Context, client *http.Client, url string) ([]byte, error) {
		if url == "https://example.com/checksums.txt" {
			return []byte(wrongChecksums), nil
		}
		return archiveData, nil
	}, nil)

	err := Run("v1.0.0", http.DefaultClient)
	if err == nil {
		t.Fatal("expected error on checksum mismatch, got nil")
	}
	if *applyCalled {
		t.Error("applyBinary should not be called when checksum verification fails")
	}
}

func TestRunMissingAssetErrors(t *testing.T) {
	applyCalled := withFakes(t, func(ctx context.Context, client *http.Client) (*Release, error) {
		return &Release{TagName: "v1.1.0"}, nil
	}, nil, nil)

	err := Run("v1.0.0", http.DefaultClient)
	if err == nil {
		t.Fatal("expected error when no matching asset is found, got nil")
	}
	if *applyCalled {
		t.Error("applyBinary should not be called when no matching asset is found")
	}
}

func TestCheckVersionPassesCustomClient(t *testing.T) {
	customClient := &http.Client{Timeout: 789}
	var gotClient *http.Client

	origRelease := fetchLatestRelease
	fetchLatestRelease = func(ctx context.Context, client *http.Client) (*Release, error) {
		gotClient = client
		return &Release{TagName: "v1.1.0"}, nil
	}
	defer func() { fetchLatestRelease = origRelease }()

	_, _ = CheckVersion("v1.0.0", customClient)
	if gotClient != customClient {
		t.Error("CheckVersion did not pass custom client to fetchLatestRelease")
	}
}

// buildArchiveForCurrentPlatform 按当前运行平台构造一个压缩包（zip 或 tar.gz），
// 内含指定内容的二进制文件，用于模拟从 GitHub 下载到的更新包。
func buildArchiveForCurrentPlatform(t *testing.T, content []byte) []byte {
	t.Helper()
	if runtime.GOOS == "windows" {
		return buildZip(t, "mailbot.exe", content)
	}
	return buildTarGz(t, "mailbot", content)
}
