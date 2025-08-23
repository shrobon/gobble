package main

import (
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"sync"
	"time"
)

const (
	defaultParallelism  = 10
	contentLengthHeader = "Content-Length"
	rangeHeader         = "Range"
	acceptRangesHeader  = "Accept-Ranges"
)

func closeFile(f *os.File) {
	//fmt.Println("closing file handler...")
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

func downloadPart(url string, output string, job DownloadJob, wg *sync.WaitGroup, tracker *ProgressTracker) {
	defer wg.Done()

	req, _ := http.NewRequest("GET", url, nil)
	req.Header.Set(rangeHeader, fmt.Sprintf("bytes=%d-%d", job.begin, job.end))

	res, err := http.DefaultClient.Do(req)
	if err != nil {
		panic(err)
	}
	defer res.Body.Close()

	partFile, err := os.OpenFile(output, os.O_WRONLY, 0666)
	if err != nil {
		panic(err)
	}
	defer closeFile(partFile)

	partFile.Seek(int64(job.begin), 0)
	// _, err = io.Copy(partFile, res.Body) // TODO check if using zero copy internally
	// if err != nil {
	// 	panic(err)
	// }

	buf := make([]byte, 32*1024) // 32KB buffer
	for {
		n, err := res.Body.Read(buf)
		if n > 0 {
			_, wErr := partFile.Write(buf[:n])
			if wErr != nil {
				panic(wErr)
			}
			tracker.Add(int64(n))
		}

		if err == io.EOF {
			break
		}

		if err != nil {
			panic(err)
		}
	}
	//fmt.Printf("Downloaded bytes %d-%d\n", job.begin, job.end)
}

func download(url string, output string, stats FileStats, tracker *ProgressTracker) error {

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
		go downloadPart(url, output, job, &wg, tracker)
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
		maxParallelism = defaultParallelism // TODO
	}

	fileSize, _ = strconv.Atoi(resp.Header.Get(contentLengthHeader))

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
	return
}

func main() {
	// Defining available input params
	url := flag.String("url", "", "URL to download")
	// parallelism := flag.Int("parallelism", 0, "# parallel download threads")
	flag.Parse()

	if *url == "" {
		fmt.Println("Please provide a URL with -url")
		flag.PrintDefaults()
		os.Exit(1)
	}

	start := time.Now()
	stats := getStats(*url)
	fmt.Printf("File size: %d MB, Parallelism: %d\n", stats.fileSize/1024/1024, stats.maxParallelism)
	prepareOutputFile("downloaded_file", stats.fileSize)

	tracker := NewProgressTracker(int64(stats.fileSize))
	download(*url, "downloaded_file", stats, tracker)

	duration := time.Since(start)
	fmt.Printf("Total time taken %s\n", duration)
}
