package main

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

// EstimateDownloadBandwidth estimates the download bandwidth by timing the download of a file from a given URL.
// It returns the estimated bandwidth in megabits per second (Mbps) and any error that occurred.
func EstimateDownloadBandwidth(url string) (float64, error) {
	// A request to a reliable server with a predictable file size is ideal.
	// We make a HEAD request first to get the content length without downloading the full file.
	// This helps to check if the URL is valid and if the size can be determined.
	resp, err := http.Head(url)
	if err != nil {
		return 0, fmt.Errorf("failed to get URL head: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return 0, fmt.Errorf("server returned non-200 status code: %d", resp.StatusCode)
	}

	contentLength := resp.ContentLength
	if contentLength <= 0 {
		return 0, fmt.Errorf("could not determine content length for URL: %s", url)
	}

	fmt.Printf("Starting download of %.2f MB file from %s...\n", float64(contentLength)/(1024*1024), url)

	// Start the timer
	start := time.Now()

	// Perform the actual download
	downloadResp, err := http.Get(url)
	if err != nil {
		return 0, fmt.Errorf("failed to get URL: %v", err)
	}
	defer downloadResp.Body.Close()

	// Read the entire body to ensure the full file is downloaded and timed
	_, err = io.Copy(io.Discard, downloadResp.Body)
	if err != nil {
		return 0, fmt.Errorf("failed to read response body: %v", err)
	}

	// Stop the timer
	elapsed := time.Since(start)

	// Calculate the bandwidth in bytes per second
	bytesPerSecond := float64(contentLength) / elapsed.Seconds()

	// Convert bytes/s to megabits/s (Mbps)
	// 1 byte = 8 bits
	// 1 megabit = 1,000,000 bits
	// Mbps = (bytes/s * 8) / 1,000,000
	mbps := (bytesPerSecond * 8) / 1_000_000

	return mbps, nil
}

func main() {
	// Using a link to a large file, such as a well-known Linux distribution ISO, is a good test.
	// This example uses a test file from a public test server.
	testURL := "http://speedtest.newark.linode.com/1GB-newark.bin"

	bandwidth, err := EstimateDownloadBandwidth(testURL)
	if err != nil {
		log.Fatalf("Error estimating bandwidth: %v", err)
	}

	// Convert Mbps to Gbps for a more appropriate unit if the speed is high
	if bandwidth >= 1000 {
		fmt.Printf("Estimated download bandwidth: %.2f Gbps\n", bandwidth/1000)
	} else {
		fmt.Printf("Estimated download bandwidth: %.2f Mbps\n", bandwidth)
	}
}
