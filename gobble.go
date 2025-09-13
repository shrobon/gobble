package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"
)

const (
	defaultParallelism  = 16
	contentLengthHeader = "Content-Length"
	rangeHeader         = "Range"
	acceptRangesHeader  = "Accept-Ranges"
)

func closeFile(f *os.File) {
	err := f.Close()

	if err != nil {
		panic(err)
	}
}

type DownloadJob struct {
	begin int
	end   int
	part  int
}

type FileStats struct {
	fileSize            int
	isParallelSupported bool
	maxParallelism      int
}

func NewProgressTracker(totalSize int64) *ProgressTracker {
	pt := &ProgressTracker{
		totalSize: totalSize,
		done:      make(chan struct{}),
	}

	go pt.Run()
	return pt
}

// Exponential backoff with jitter
func backoff(attempt int) {
	base := time.Duration(1<<attempt) * time.Second
	jitter := time.Duration(rand.Intn(1000)) * time.Millisecond
	sleep := base + jitter
	fmt.Printf("⏳ Backing off for %v...\n", sleep)
	time.Sleep(sleep)
}

func detectMaxConnections(url string) int {
	low, high := 1, defaultParallelism
	maxConns := 1
	attempts := 1

	for low <= high {
		mid := low + (high-low)/2
		fmt.Printf("🔍 Testing %d connections...\n", mid)

		ok, rateLimited := testParallelRequest(url, mid)

		if rateLimited {
			fmt.Println("⚠️ Rate-limited (429). Backing off...")
			backoff(attempts)
			attempts++
			high = mid - 1
			continue
		}

		attempts = 1 // reset attempts on success or non-rate-limit failure

		if ok {
			maxConns = mid
			low = mid + 1
		} else {
			high = mid - 1
		}
	}

	fmt.Printf("✅ Max safe parallel connections: %d\n", maxConns)
	return maxConns
}

func testParallelRequest(url string, maxConns int) (ok bool, rateLimited bool) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	wg := sync.WaitGroup{}
	wg.Add(maxConns)

	errChan := make(chan int, maxConns)
	for i := 0; i < maxConns; i++ {
		go func(i int) {
			defer wg.Done()

			start := int64(i * 100)
			end := start + 99

			req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", start, end))
			resp, err := http.DefaultClient.Do(req)
			if resp != nil {
				defer resp.Body.Close()
			}

			if err != nil {
				errChan <- 500 // generic server error
				return
			}

			if resp.StatusCode == http.StatusTooManyRequests {
				errChan <- 429
				return
			}

			if resp.StatusCode != http.StatusPartialContent {
				errChan <- resp.StatusCode
				return
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	for code := range errChan {
		if code == 429 {
			return false, true // failed due to rate limit
		}
		if code != 0 {
			return false, false // failed for another reason
		}
	}

	return true, false
}

func downloadPart(ctx context.Context, url string, output string, job DownloadJob, wg *sync.WaitGroup, tracker *ProgressTracker) {
	defer wg.Done()

	req, _ := http.NewRequestWithContext(ctx, "GET", url, nil)
	req.Header.Set(rangeHeader, fmt.Sprintf("bytes=%d-%d", job.begin, job.end))

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			fmt.Printf("🚫 Part %d download canceled due to context cancellation.\n", job.part)
			return
		}
		panic(err)
	}
	defer res.Body.Close()

	partFile, err := os.OpenFile(output, os.O_WRONLY, 0666)
	if err != nil {
		fmt.Printf("Error opening file: %v\n", err)
		return
	}
	defer closeFile(partFile)

	partFile.Seek(int64(job.begin), 0)
	// _, err = io.Copy(partFile, res.Body) // TODO check if using zero copy internally
	// if err != nil {
	// 	panic(err)
	// }

	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		select {
		case <-ctx.Done():
			return

		default:
			n, err := res.Body.Read(buf)
			if n > 0 {
				_, wErr := partFile.Write(buf[:n])
				if wErr != nil {
					fmt.Printf("Error writing to file: %v\n", wErr)
					return
				}
				tracker.Add(int64(n))
			}

			if err == io.EOF {
				break
			}

			if err != nil {
				// Return without printing if the error is due to context cancellation
				if ctx.Err() != nil {
					return
				}
				fmt.Printf("Error reading from body: %v\n", err)
				return
			}
		}
	}
}

func download(ctx context.Context, url string, output string, stats FileStats, tracker *ProgressTracker) error {

	var jobs []DownloadJob
	partSize := stats.fileSize / stats.maxParallelism

	for i := 0; i < stats.maxParallelism; i++ {
		begin := i * partSize
		end := begin + partSize - 1

		if i == stats.maxParallelism-1 { // last chunk
			end = stats.fileSize - 1
		}

		jobs = append(jobs, DownloadJob{begin, end, i + 1})
	}

	var wg sync.WaitGroup
	for _, job := range jobs {
		wg.Add(1)
		go downloadPart(ctx, url, output, job, &wg, tracker)
	}

	wg.Wait()
	tracker.Finish()
	return nil
}

func getStatsFallback(url string) FileStats {
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		panic(err) // It is a possibility that the URL is malformed
	}

	req.Header.Set(rangeHeader, "bytes=0-1")
	resp, err := http.DefaultClient.Do(req)

	if err != nil {
		panic(err)
	}
	defer resp.Body.Close()

	fileSize, _ := strconv.Atoi(resp.Header.Get(contentLengthHeader))
	fmt.Printf("Status code: %d\n", resp.StatusCode)

	if resp.StatusCode != http.StatusPartialContent {
		fmt.Println("Server does not support range requests. Using 1 thread for download")

		return FileStats{
			fileSize:            fileSize,
			maxParallelism:      1,
			isParallelSupported: false,
		}
	} else {
		fmt.Printf("Server supports range requests. Using %d threads for download\n", defaultParallelism)
		return FileStats{
			fileSize:            fileSize,
			maxParallelism:      defaultParallelism,
			isParallelSupported: true,
		}
	}
}

func getStats(url string) FileStats {
	/* Note: Some webservers may not have HEAD configured and may outright reject it
	If this happens, fallback on determining if parallelism is supported using Get request
	*/
	var fileSize int
	isParallelSupported := false
	maxParallelism := 1

	resp, err := http.Head(url)
	fileSize, _ = strconv.Atoi(resp.Header.Get(contentLengthHeader))
	fmt.Println("HEAD Status code:", resp.StatusCode)

	if err != nil || resp.StatusCode != http.StatusOK {
		// check with a get request with range headers
		return getStatsFallback(url)
	}

	/*
		resp.Body is a network stream (an open TCP connection + buffers)
		1. Resource cleanup - Prevent "too many open files"
		2. Connection reuse
		3. Proper memory release - buffers allocated for the response wont be freed until GC runs
	*/
	defer resp.Body.Close()

	if resp.Header.Get(acceptRangesHeader) == "bytes" {
		isParallelSupported = true
		maxParallelism = detectMaxConnections(url)
	}

	return FileStats{
		fileSize:            fileSize,
		maxParallelism:      maxParallelism,
		isParallelSupported: isParallelSupported,
	}
}

// Create the file for storing the download content
func prepareOutputFile(fileName string, fileSize int) {
	out, err := os.Create(fileName)
	if err != nil {
		panic(err)
	}
	defer closeFile(out)

	// Guarantee the downloaded file has the correct size upfront and makes parallel writes safe.
	out.Truncate(int64(fileSize))
}

func setupSignals(cancel context.CancelFunc) {
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-sigChan
		fmt.Println("\n❗ Received interrupt. Exiting...")
		cancel()
	}()
}

func main() {
	start := time.Now()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	setupSignals(cancel)

	link := flag.String("url", "", "URL to download")
	filename := flag.String("o", "", "filename")
	flag.Parse()

	if *link == "" {
		fmt.Println("Please provide a URL with -url")
		flag.PrintDefaults()
		os.Exit(1)
	}

	downloadFile, err := buildDownloadPath(*filename, *link)
	if err != nil {
		panic(err)
	}

	stats := getStats(*link)
	fmt.Printf("File size: %d MB, Parallelism: %d\n", stats.fileSize/1024/1024, stats.maxParallelism)
	prepareOutputFile(downloadFile, stats.fileSize)

	tracker := NewProgressTracker(int64(stats.fileSize))
	download(ctx, *link, downloadFile, stats, tracker)

	if ctx.Err() != nil {
		fmt.Println("Download cancelled.")
		os.Remove(downloadFile) // clean up partial file
		duration := time.Since(start)
		fmt.Printf("Total time taken before interruption: %s\n", duration)
	} else {
		duration := time.Since(start)
		fmt.Printf("Total time taken %s\n", duration)
	}
}
