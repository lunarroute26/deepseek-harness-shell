package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

type unusedBaseProvider struct{}

type timeoutBaseProvider struct {
	unusedBaseProvider
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

type interruptedReader struct {
	reader io.Reader
}

type terminalErrorReader struct {
	payload []byte
	read    bool
}

func (r *interruptedReader) Read(buffer []byte) (int, error) {
	n, err := r.reader.Read(buffer)
	if err == io.EOF {
		return n, io.ErrUnexpectedEOF
	}
	return n, err
}

func (r *interruptedReader) Close() error { return nil }

func (r *terminalErrorReader) Read(buffer []byte) (int, error) {
	if r.read {
		return 0, io.EOF
	}
	r.read = true
	return copy(buffer, r.payload), io.ErrUnexpectedEOF
}

func (unusedBaseProvider) Name() string { return "github" }

func (unusedBaseProvider) Check(
	context.Context,
	updater.CheckRequest,
) (*updater.Release, error) {
	panic("unexpected Check call")
}

func (unusedBaseProvider) Download(
	context.Context,
	*updater.Release,
	io.Writer,
	func(int64, int64),
) error {
	panic("unexpected Download call")
}

func (timeoutBaseProvider) Check(
	ctx context.Context,
	_ updater.CheckRequest,
) (*updater.Release, error) {
	<-ctx.Done()
	return nil, ctx.Err()
}

func TestResumableGitHubProviderLimitsVersionChecks(t *testing.T) {
	provider := newResumableGitHubProvider(
		timeoutBaseProvider{},
		&http.Client{},
		20*time.Millisecond,
		time.Second,
	)
	_, err := provider.Check(context.Background(), updater.CheckRequest{})
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("Check() error = %v, want context deadline exceeded", err)
	}
}

func TestResumableGitHubProviderContinuesInterruptedDownload(t *testing.T) {
	payload := bytes.Repeat([]byte("deepseek-harness-shell-update\n"), 8192)
	split := len(payload) / 2
	var requests atomic.Int32
	var resumedAt atomic.Int64

	client := &http.Client{Transport: roundTripFunc(func(r *http.Request) (*http.Response, error) {
		if got := r.Header.Get("Accept-Encoding"); got != "identity" {
			return nil, fmt.Errorf("Accept-Encoding = %q, want identity", got)
		}
		request := requests.Add(1)
		if request == 1 {
			return &http.Response{
				StatusCode:    http.StatusOK,
				ContentLength: int64(len(payload)),
				Header:        make(http.Header),
				Body: &interruptedReader{
					reader: bytes.NewReader(payload[:split]),
				},
				Request: r,
			}, nil
		}

		var offset int64
		if _, err := fmt.Sscanf(r.Header.Get("Range"), "bytes=%d-", &offset); err != nil {
			return nil, fmt.Errorf("resume Range %q: %w", r.Header.Get("Range"), err)
		}
		resumedAt.Store(offset)
		header := make(http.Header)
		header.Set(
			"Content-Range",
			fmt.Sprintf("bytes %d-%d/%d", offset, len(payload)-1, len(payload)),
		)
		return &http.Response{
			StatusCode:    http.StatusPartialContent,
			ContentLength: int64(len(payload)) - offset,
			Header:        header,
			Body:          io.NopCloser(bytes.NewReader(payload[offset:])),
			Request:       r,
		}, nil
	})}

	provider := newResumableGitHubProvider(
		unusedBaseProvider{},
		client,
		time.Second,
		time.Second,
	)
	provider.maxRetries = 2
	provider.retryDelay = 0
	release := &updater.Release{
		Artifact: updater.Artifact{Size: int64(len(payload))},
		Metadata: map[string]any{"github.asset.url": "https://example.test/update.zip"},
	}

	var downloaded bytes.Buffer
	var finalProgress int64
	err := provider.Download(
		context.Background(),
		release,
		&downloaded,
		func(written, _ int64) { finalProgress = written },
	)
	if err != nil {
		t.Fatalf("Download() error: %v", err)
	}
	if !bytes.Equal(downloaded.Bytes(), payload) {
		t.Fatalf("downloaded payload differs: got %d bytes, want %d", downloaded.Len(), len(payload))
	}
	if requests.Load() != 2 {
		t.Fatalf("request count = %d, want 2", requests.Load())
	}
	if resumedAt.Load() != int64(split) {
		t.Fatalf("resume offset = %d, want %d", resumedAt.Load(), split)
	}
	if finalProgress != int64(len(payload)) {
		t.Fatalf("final progress = %d, want %d", finalProgress, len(payload))
	}
}

func TestParseContentRange(t *testing.T) {
	start, total, err := parseContentRange("bytes 1024-2047/4096")
	if err != nil {
		t.Fatalf("parseContentRange() error: %v", err)
	}
	if start != 1024 || total != 4096 {
		t.Fatalf("parseContentRange() = (%d, %d), want (1024, 4096)", start, total)
	}
}

func TestCopyDownloadResponseAcceptsCompleteBodyWithTerminalError(t *testing.T) {
	payload := []byte("complete update")
	var downloaded bytes.Buffer
	written, retryable, err := copyDownloadResponse(
		context.Background(),
		io.NopCloser(&terminalErrorReader{payload: payload}),
		&downloaded,
		0,
		int64(len(payload)),
		0,
		nil,
	)
	if err != nil {
		t.Fatalf("copyDownloadResponse() error: %v", err)
	}
	if retryable {
		t.Fatal("copyDownloadResponse() marked a complete body retryable")
	}
	if written != int64(len(payload)) || !bytes.Equal(downloaded.Bytes(), payload) {
		t.Fatalf("copyDownloadResponse() wrote %d bytes, want %d", written, len(payload))
	}
}
