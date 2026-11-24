package node

import (
	"archive/zip"
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

func stagePackage(path, expected string) (root string, cleanup func(), result error) {
	if !regexp.MustCompile(`^[0-9a-fA-F]{64}$`).MatchString(expected) {
		return "", nil, errors.New("需要可信 SHA256 摘要")
	}
	actual, err := sha(path)
	if err != nil || !strings.EqualFold(actual, expected) {
		return "", nil, errors.New("安装包 SHA256 校验失败")
	}
	z, err := zip.OpenReader(path)
	if err != nil {
		return "", nil, err
	}
	defer z.Close()
	if len(z.File) > 1000 {
		return "", nil, errors.New("安装包文件过多")
	}
	total := uint64(0)
	seen := map[string]bool{}
	for _, e := range z.File {
		total += e.UncompressedSize64
		name := e.Name
		if total > 1<<30 || filepath.IsAbs(name) || strings.ContainsAny(name, "\\:") || filepath.ToSlash(filepath.Clean(name)) != name || name == ".." || strings.HasPrefix(name, "../") || seen[name] || e.FileInfo().Mode()&os.ModeSymlink != 0 {
			return "", nil, errors.New("安装包含非法路径、重复或过大文件")
		}
		seen[name] = true
	}
	root, err = os.MkdirTemp("", "netgo-node-package-")
	if err != nil {
		return "", nil, err
	}
	cleanup = func() { _ = os.RemoveAll(root) }
	defer func() {
		if result != nil {
			cleanup()
		}
	}()
	for _, e := range z.File {
		target := filepath.Join(root, e.Name)
		if e.FileInfo().IsDir() {
			if err = os.MkdirAll(target, 0750); err != nil {
				return root, cleanup, err
			}
			continue
		}
		if err = os.MkdirAll(filepath.Dir(target), 0750); err != nil {
			return root, cleanup, err
		}
		stream, e2 := e.Open()
		if e2 != nil {
			return root, cleanup, e2
		}
		mode := os.FileMode(0644)
		if e.FileInfo().Mode()&0111 != 0 {
			mode = 0755
		}
		file, e2 := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
		if e2 != nil {
			stream.Close()
			return root, cleanup, e2
		}
		_, e2 = io.Copy(file, stream)
		stream.Close()
		file.Close()
		if e2 != nil {
			return root, cleanup, e2
		}
	}
	if err = verifyPackage(root); err != nil {
		return root, cleanup, err
	}
	return root, cleanup, nil
}
func verifyPackage(root string) error {
	path := filepath.Join(root, "SHA256SUMS")
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	scanner := bufio.NewScanner(file)
	listed := map[string]bool{}
	for scanner.Scan() {
		parts := strings.Split(scanner.Text(), "  ")
		if len(parts) != 2 || !regexp.MustCompile(`^[0-9a-f]{64}$`).MatchString(parts[0]) || parts[1] == "SHA256SUMS" || filepath.IsAbs(parts[1]) || strings.ContainsAny(parts[1], "\\:") || filepath.ToSlash(filepath.Clean(parts[1])) != parts[1] || strings.HasPrefix(parts[1], "../") || listed[parts[1]] {
			return errors.New("非法安装包摘要清单")
		}
		listed[parts[1]] = true
		actual, e := sha(filepath.Join(root, parts[1]))
		if e != nil || actual != parts[0] {
			return errors.New("安装包文件摘要不匹配")
		}
	}
	if err = scanner.Err(); err != nil {
		return err
	}
	return filepath.WalkDir(root, func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			return nil
		}
		name, _ := filepath.Rel(root, path)
		name = filepath.ToSlash(name)
		if name != "SHA256SUMS" && !listed[name] {
			return errors.New("安装包有未列出的文件")
		}
		return nil
	})
}
func (m Manager) releasePackage(version string) (source string, cleanup func(), result error) {
	if version != "latest" && !regexp.MustCompile(`^[0-9]+\.[0-9]+\.[0-9]+(-[A-Za-z0-9.-]+)?$`).MatchString(version) {
		return "", nil, errors.New("无效版本号")
	}
	endpoint := "https://api.github.com/repos/sck03/NetGo/releases/latest"
	if version != "latest" {
		endpoint = "https://api.github.com/repos/sck03/NetGo/releases/tags/v" + version
	}
	response, err := http.Get(endpoint)
	if err != nil {
		return "", nil, err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return "", nil, errors.New("尚无可下载 Release；可从 GitHub Actions 下载对应平台包并运行包内安装脚本")
	}
	var release struct {
		Tag    string `json:"tag_name"`
		Assets []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&release); err != nil {
		return "", nil, err
	}
	version = strings.TrimPrefix(release.Tag, "v")
	name := "NetGo-" + version + "-linux-amd64.zip"
	if m.Role == "vps" {
		name = "NetGo-" + version + "-vps-linux-amd64.zip"
	}
	urls := map[string]string{}
	for _, asset := range release.Assets {
		urls[asset.Name] = asset.URL
	}
	if urls[name] == "" || urls["SHA256SUMS"] == "" {
		return "", nil, errors.New("该 Release 缺少此平台的校验包")
	}
	temp, err := os.MkdirTemp("", "netgo-release-")
	if err != nil {
		return "", nil, err
	}
	defer os.RemoveAll(temp)
	sumFile := filepath.Join(temp, "SHA256SUMS")
	if err = download(urls["SHA256SUMS"], sumFile, 1<<20); err != nil {
		return "", nil, err
	}
	b, err := os.ReadFile(sumFile)
	if err != nil {
		return "", nil, err
	}
	expected := ""
	for _, line := range strings.Split(string(b), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && fields[1] == name {
			if expected != "" {
				return "", nil, errors.New("重复安装包摘要")
			}
			expected = fields[0]
		}
	}
	archive := filepath.Join(temp, name)
	if err = download(urls[name], archive, 512<<20); err != nil {
		return "", nil, err
	}
	return stagePackage(archive, expected)
}
func download(url, path string, max int64) error {
	if !strings.HasPrefix(url, "https://") {
		return errors.New("下载地址必须是 HTTPS")
	}
	client := &http.Client{Timeout: 10 * time.Minute, CheckRedirect: func(r *http.Request, via []*http.Request) error {
		if r.URL.Scheme != "https" || len(via) > 5 {
			return errors.New("下载重定向不安全")
		}
		return nil
	}}
	response, err := client.Get(url)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	if response.StatusCode != 200 {
		return fmt.Errorf("下载返回 HTTP %d", response.StatusCode)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	defer file.Close()
	written, err := io.Copy(file, io.LimitReader(response.Body, max+1))
	if err != nil {
		return err
	}
	if written > max {
		return errors.New("下载文件过大")
	}
	return nil
}
