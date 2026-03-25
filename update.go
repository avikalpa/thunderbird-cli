package main

import (
	"archive/tar"
	"compress/gzip"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"golang.org/x/mod/semver"
)

func runUpdate(checkOnly bool) error {
	rel, err := checkForLatestRelease()
	if err != nil {
		return err
	}
	currentSemver := normalizeSemver(version)
	latestSemver := normalizeSemver(rel.TagName)
	fmt.Printf("Current version: %s\n", version)
	fmt.Printf("Latest release:  %s\n", rel.TagName)
	switch {
	case currentSemver != "" && latestSemver != "" && semver.Compare(currentSemver, latestSemver) > 0:
		fmt.Println("Current build is newer than the latest published release.")
		return nil
	case currentSemver != "" && latestSemver != "" && semver.Compare(currentSemver, latestSemver) == 0:
		fmt.Println("Already up to date.")
		return nil
	case strings.TrimPrefix(rel.TagName, "v") == strings.TrimPrefix(version, "v"):
		fmt.Println("Already up to date.")
		return nil
	}
	if checkOnly {
		fmt.Println("Update available.")
		return nil
	}
	if runtime.GOOS == "windows" {
		return fmt.Errorf("tb update is not supported on Windows yet; download the latest release archive manually")
	}
	assetName := releaseAssetName(rel.TagName)
	var assetURL string
	for _, asset := range rel.Assets {
		if asset.Name == assetName {
			assetURL = asset.URL
			break
		}
	}
	if assetURL == "" {
		return fmt.Errorf("release %s does not contain asset %s", rel.TagName, assetName)
	}
	exe, err := os.Executable()
	if err != nil {
		return err
	}
	fmt.Printf("Downloading %s\n", assetName)
	return replaceBinaryFromTarGz(assetURL, exe)
}

func normalizeSemver(v string) string {
	v = strings.TrimSpace(v)
	if v == "" {
		return ""
	}
	if !strings.HasPrefix(v, "v") {
		v = "v" + v
	}
	if !semver.IsValid(v) {
		return ""
	}
	return v
}

func releaseAssetName(tag string) string {
	return fmt.Sprintf("thunderbird-cli_%s_%s.tar.gz", strings.TrimPrefix(tag, "v"), releaseTarget())
}

func releaseTarget() string {
	switch runtime.GOOS + "/" + runtime.GOARCH {
	case "linux/amd64":
		return "linux_x86_64"
	case "linux/arm64":
		return "linux_arm64"
	case "darwin/amd64":
		return "darwin_x86_64"
	case "darwin/arm64":
		return "darwin_arm64"
	default:
		return runtime.GOOS + "_" + runtime.GOARCH
	}
}

func replaceBinaryFromTarGz(url, exePath string) error {
	client := &http.Client{Timeout: 60 * time.Second}
	resp, err := client.Get(url)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("download failed: %s %s", resp.Status, strings.TrimSpace(string(body)))
	}
	gzr, err := gzip.NewReader(resp.Body)
	if err != nil {
		return err
	}
	defer gzr.Close()
	tr := tar.NewReader(gzr)
	tmp := exePath + ".tmp"
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if filepath.Base(hdr.Name) != "tb" {
			continue
		}
		out, err := os.OpenFile(tmp, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
		if err != nil {
			return err
		}
		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			return err
		}
		if err := out.Close(); err != nil {
			return err
		}
		return os.Rename(tmp, exePath)
	}
	return fmt.Errorf("downloaded archive did not contain tb binary")
}
