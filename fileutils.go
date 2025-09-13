package main

import (
	"fmt"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"
)

func buildDownloadPath(userInputPath string, link string) (string, error) {
	parsedUrl, err := url.Parse(link)
	if err != nil {
		return "", fmt.Errorf("invalid URL: %w", err)
	}

	extractedFilename := path.Base(parsedUrl.Path)

	if userInputPath == "" {
		homeDir, _ := os.UserHomeDir()
		downloadPath := filepath.Join(homeDir, "Downloads", "gobble", extractedFilename)
		fmt.Printf("[INFO] No output file location provided. Using default location as %s\n", downloadPath)
		return ensureUniquePath(downloadPath), nil
	}

	resolvedPath, err := expandTilde(userInputPath)

	if err != nil {
		return "", fmt.Errorf("unable to expand the `~` in path: %w", err)
	}

	info, err := os.Stat(resolvedPath)

	if err == nil {
		// Path exists
		if info.IsDir() {
			downloadPath := filepath.Join(resolvedPath, extractedFilename)
			fmt.Printf("[INFO] Provided path is a directory. Appending filename to make: %s\n", downloadPath)
			return ensureUniquePath(downloadPath), nil
		} else {
			fmt.Printf("[INFO] Using provided output file location %s\n", resolvedPath)
			return ensureUniquePath(resolvedPath), nil
		}
	} else if os.IsNotExist(err) {
		fmt.Printf("[INFO] Provided path does not exist. Creating new file at: %s\n", resolvedPath)
		return resolvedPath, nil
	} else {
		return "", fmt.Errorf("invalid path provided: %w", err)
	}
}

func expandTilde(p string) (string, error) {
	if strings.HasPrefix(p, "~/") || p == "~" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}

		if p == "~" {
			return homeDir, nil
		}

		return filepath.Join(homeDir, p[2:]), nil
	}

	return p, nil
}

func ensureUniquePath(path string) string {
	info, err := os.Stat(path)

	// Check if there is a file with the same name already exists
	// If yes, add some redundancy
	if err == nil && !info.IsDir() {
		dir := filepath.Dir(path)
		ext := filepath.Ext(path)
		base := strings.TrimSuffix(filepath.Base(path), ext)
		newPath := filepath.Join(dir, fmt.Sprintf("%s_%d%s", base, time.Now().UnixMilli(), ext))
		fmt.Printf("[INFO] File already exists at '%s'. Renaming to '%s' to avoid overwriting.\n", path, newPath)
		return newPath
	}

	return path
}
