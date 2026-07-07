package update

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"runtime"
	"strings"
	"time"

	"github.com/minio/selfupdate"
	"golang.org/x/mod/semver"
)

const (
	repoOwner = "akiker233"
	repoName  = "telegram-mail-bot"
)

// Release 对应 GitHub Releases API 返回的最新发布信息（仅保留用到的字段）。
type Release struct {
	TagName string  `json:"tag_name"`
	Assets  []Asset `json:"assets"`
}

// Asset 是 Release 下的一个附件（压缩包或 checksums.txt）。
type Asset struct {
	Name               string `json:"name"`
	BrowserDownloadURL string `json:"browser_download_url"`
}

// 网络请求与实际替换二进制的步骤抽成变量，便于测试时替换为假实现。
var (
	fetchLatestRelease = fetchLatestReleaseHTTP
	fetchAsset         = fetchAssetHTTP
	applyBinary        = applyBinaryReal
)

// CheckVersion 查询 GitHub 最新版本。如果比 currentVersion 新则返回新版本号，
// 一样新则返回空字符串，出错时返回 error。
func CheckVersion(currentVersion string, client *http.Client) (string, error) {
	if currentVersion == "" {
		return "", fmt.Errorf("当前是开发版本")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	release, err := fetchLatestRelease(ctx, client)
	if err != nil {
		return "", err
	}

	if semver.Compare(release.TagName, currentVersion) <= 0 {
		return "", nil
	}
	return release.TagName, nil
}

// Run 是 `./mailbot update` 的主流程：检查 GitHub 上的最新版本，如果比当前版本新，
// 下载对应平台的压缩包、校验 SHA256、解压取出二进制并替换掉当前正在运行的程序。
func Run(currentVersion string, client *http.Client) error {
	if currentVersion == "" {
		return fmt.Errorf("当前是开发版本，无法自动更新，请手动从 Release 页面下载")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	release, err := fetchLatestRelease(ctx, client)
	if err != nil {
		return err
	}

	if semver.Compare(release.TagName, currentVersion) <= 0 {
		fmt.Println("已是最新版本")
		return nil
	}

	assetName := fmt.Sprintf("mailbot_%s_%s.%s", runtime.GOOS, runtime.GOARCH, archiveExt())
	asset := findAsset(release.Assets, assetName)
	if asset == nil {
		return fmt.Errorf("未找到适用于当前平台（%s/%s）的更新包", runtime.GOOS, runtime.GOARCH)
	}

	checksumsAsset := findAsset(release.Assets, "checksums.txt")
	if checksumsAsset == nil {
		return fmt.Errorf("未找到 checksums.txt，无法校验更新包完整性")
	}

	checksumsData, err := fetchAsset(ctx, client, checksumsAsset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("update: 下载 checksums.txt 失败: %w", err)
	}
	expectedSum, err := parseChecksum(string(checksumsData), assetName)
	if err != nil {
		return err
	}

	archiveData, err := fetchAsset(ctx, client, asset.BrowserDownloadURL)
	if err != nil {
		return fmt.Errorf("update: 下载更新包失败: %w", err)
	}

	actualSum := sha256.Sum256(archiveData)
	if hex.EncodeToString(actualSum[:]) != expectedSum {
		return fmt.Errorf("update: 更新包校验和不匹配，已中止更新")
	}

	binary, err := extractBinary(archiveData, runtime.GOOS)
	if err != nil {
		return err
	}

	if err := applyBinary(binary); err != nil {
		return err
	}

	fmt.Printf("已更新到 %s，请重新启动程序\n", release.TagName)
	return nil
}

func archiveExt() string {
	if runtime.GOOS == "windows" {
		return "zip"
	}
	return "tar.gz"
}

func findAsset(assets []Asset, name string) *Asset {
	for i := range assets {
		if assets[i].Name == name {
			return &assets[i]
		}
	}
	return nil
}

// httpClientOrDefault 在 client 为空时返回 http.DefaultClient，保证请求始终有可用客户端。
func httpClientOrDefault(client *http.Client) *http.Client {
	if client == nil {
		return http.DefaultClient
	}
	return client
}

// parseChecksum 从 checksums.txt（`sha256sum` 输出格式：`<hash>  <filename>`）里取出指定文件名对应的哈希值。
func parseChecksum(checksums, filename string) (string, error) {
	for _, line := range strings.Split(checksums, "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) != 2 {
			continue
		}
		if fields[1] == filename || strings.TrimPrefix(fields[1], "*") == filename {
			return fields[0], nil
		}
	}
	return "", fmt.Errorf("update: checksums.txt 中未找到 %s 的校验和", filename)
}

func fetchLatestReleaseHTTP(ctx context.Context, client *http.Client) (*Release, error) {
	url := fmt.Sprintf("https://api.github.com/repos/%s/%s/releases/latest", repoOwner, repoName)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: 构造请求失败: %w", err)
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := httpClientOrDefault(client).Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: 请求 GitHub Release 失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: 请求 GitHub Release 失败，状态码 %d", resp.StatusCode)
	}

	var release Release
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return nil, fmt.Errorf("update: 解析 GitHub Release 响应失败: %w", err)
	}
	return &release, nil
}

func fetchAssetHTTP(ctx context.Context, client *http.Client, url string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("update: 构造请求失败: %w", err)
	}

	resp, err := httpClientOrDefault(client).Do(req)
	if err != nil {
		return nil, fmt.Errorf("update: 下载失败: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("update: 下载失败，状态码 %d", resp.StatusCode)
	}

	return io.ReadAll(resp.Body)
}

// applyBinaryReal 用新二进制替换掉当前正在运行的程序。
// 完整性已经在 Run 里通过 checksums.txt 校验过压缩包本身，这里不再重复传 selfupdate.Options.Checksum
// （该字段校验的是传入 Apply 的字节内容，也就是解压后的二进制，而不是压缩包，两者哈希不同，混用会导致每次都校验失败）。
func applyBinaryReal(binary []byte) error {
	err := selfupdate.Apply(bytes.NewReader(binary), selfupdate.Options{})
	if err != nil {
		if rerr := selfupdate.RollbackError(err); rerr != nil {
			return fmt.Errorf("update: 回滚失败，文件系统可能处于不一致状态: %w", rerr)
		}
		return fmt.Errorf("update: 应用更新失败: %w", err)
	}
	return nil
}
