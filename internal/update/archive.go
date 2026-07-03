package update

import (
	"archive/tar"
	"archive/zip"
	"bytes"
	"compress/gzip"
	"fmt"
	"io"
	"path"
)

// extractBinary 从下载的压缩包（zip 或 tar.gz）内容中取出 mailbot/mailbot.exe 二进制的原始字节。
// windows 平台的发布包是 zip，其余平台是 tar.gz（见 release.yml 打包逻辑）。
func extractBinary(data []byte, goos string) ([]byte, error) {
	binName := "mailbot"
	if goos == "windows" {
		binName = "mailbot.exe"
	}

	if goos == "windows" {
		return extractFromZip(data, binName)
	}
	return extractFromTarGz(data, binName)
}

func extractFromZip(data []byte, binName string) ([]byte, error) {
	r, err := zip.NewReader(bytes.NewReader(data), int64(len(data)))
	if err != nil {
		return nil, fmt.Errorf("update: 解析 zip 失败: %w", err)
	}

	for _, f := range r.File {
		if path.Base(f.Name) != binName {
			continue
		}
		rc, err := f.Open()
		if err != nil {
			return nil, fmt.Errorf("update: 读取 zip 内文件失败: %w", err)
		}
		defer rc.Close()
		return io.ReadAll(rc)
	}

	return nil, fmt.Errorf("update: 压缩包内未找到 %s", binName)
}

func extractFromTarGz(data []byte, binName string) ([]byte, error) {
	gz, err := gzip.NewReader(bytes.NewReader(data))
	if err != nil {
		return nil, fmt.Errorf("update: 解析 gzip 失败: %w", err)
	}
	defer gz.Close()

	tr := tar.NewReader(gz)
	for {
		header, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("update: 解析 tar 失败: %w", err)
		}
		if path.Base(header.Name) != binName {
			continue
		}
		return io.ReadAll(tr)
	}

	return nil, fmt.Errorf("update: 压缩包内未找到 %s", binName)
}
