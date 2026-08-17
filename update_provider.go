package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/wailsapp/wails/v3/pkg/updater"
)

const defaultUpdateDownloadRetries = 4

type resumableGitHubProvider struct {
	base         updater.Provider
	client       *http.Client
	checkTimeout time.Duration
	idleTimeout  time.Duration
	maxRetries   int
	retryDelay   time.Duration
}

func newResumableGitHubProvider(
	base updater.Provider,
	client *http.Client,
	checkTimeout time.Duration,
	idleTimeout time.Duration,
) *resumableGitHubProvider {
	return &resumableGitHubProvider{
		base:         base,
		client:       client,
		checkTimeout: checkTimeout,
		idleTimeout:  idleTimeout,
		maxRetries:   defaultUpdateDownloadRetries,
		retryDelay:   time.Second,
	}
}

func (p *resumableGitHubProvider) Name() string {
	return p.base.Name()
}

func (p *resumableGitHubProvider) Check(
	ctx context.Context,
	req updater.CheckRequest,
) (*updater.Release, error) {
	if p.checkTimeout <= 0 {
		return p.base.Check(ctx, req)
	}
	checkCtx, cancel := context.WithTimeout(ctx, p.checkTimeout)
	defer cancel()
	return p.base.Check(checkCtx, req)
}

func (p *resumableGitHubProvider) Download(
	ctx context.Context,
	rel *updater.Release,
	dst io.Writer,
	onProgress func(written, total int64),
) error {
	if rel == nil || rel.Metadata == nil {
		return errors.New("github: release missing metadata")
	}
	url, ok := rel.Metadata["github.asset.url"].(string)
	if !ok || url == "" {
		return errors.New("github: release metadata missing asset URL")
	}

	total := rel.Artifact.Size
	written := int64(0)
	for attempt := 0; ; attempt++ {
		resp, responseTotal, retryable, err := p.openDownload(ctx, url, written)
		if responseTotal > 0 {
			total = responseTotal
		}
		if err == nil {
			written, retryable, err = copyDownloadResponse(
				ctx,
				resp.Body,
				dst,
				written,
				total,
				p.idleTimeout,
				onProgress,
			)
			_ = resp.Body.Close()
		}
		if err == nil {
			return nil
		}
		if !retryable || attempt >= p.maxRetries {
			return fmt.Errorf("github: download: %w", err)
		}
		if err := waitForDownloadRetry(ctx, p.retryDelay, attempt); err != nil {
			return fmt.Errorf("github: download: %w", err)
		}
	}
}

func (p *resumableGitHubProvider) openDownload(
	ctx context.Context,
	url string,
	offset int64,
) (*http.Response, int64, bool, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, 0, false, err
	}
	req.Header.Set("Accept", "application/octet-stream")
	req.Header.Set("Accept-Encoding", "identity")
	req.Header.Set("User-Agent", "deepseek-harness-shell-updater")
	if offset > 0 {
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
	}

	resp, err := p.client.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, 0, false, ctx.Err()
		}
		return nil, 0, true, err
	}

	if resp.StatusCode == http.StatusOK && offset == 0 {
		total := resp.ContentLength
		if total < 0 {
			total = 0
		}
		return resp, total, true, nil
	}
	if resp.StatusCode == http.StatusPartialContent {
		start, total, err := parseContentRange(resp.Header.Get("Content-Range"))
		if err != nil || start != offset {
			_ = resp.Body.Close()
			return nil, 0, false, fmt.Errorf(
				"invalid Content-Range %q for offset %d",
				resp.Header.Get("Content-Range"),
				offset,
			)
		}
		return resp, total, true, nil
	}

	_ = resp.Body.Close()
	retryable := resp.StatusCode == http.StatusRequestTimeout ||
		resp.StatusCode == http.StatusTooManyRequests ||
		resp.StatusCode >= http.StatusInternalServerError
	if offset > 0 && resp.StatusCode == http.StatusOK {
		retryable = false
		return nil, 0, retryable, errors.New("server ignored the resume Range request")
	}
	return nil, 0, retryable, fmt.Errorf("HTTP %d", resp.StatusCode)
}

func parseContentRange(value string) (start int64, total int64, err error) {
	if !strings.HasPrefix(value, "bytes ") {
		return 0, 0, errors.New("missing bytes prefix")
	}
	rangeAndTotal := strings.SplitN(strings.TrimPrefix(value, "bytes "), "/", 2)
	if len(rangeAndTotal) != 2 {
		return 0, 0, errors.New("missing total")
	}
	positions := strings.SplitN(rangeAndTotal[0], "-", 2)
	if len(positions) != 2 {
		return 0, 0, errors.New("missing range end")
	}
	start, err = strconv.ParseInt(positions[0], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	total, err = strconv.ParseInt(rangeAndTotal[1], 10, 64)
	if err != nil {
		return 0, 0, err
	}
	if start < 0 || total <= start {
		return 0, 0, errors.New("invalid range bounds")
	}
	return start, total, nil
}

func copyDownloadResponse(
	ctx context.Context,
	body io.ReadCloser,
	dst io.Writer,
	written int64,
	total int64,
	idleTimeout time.Duration,
	onProgress func(written, total int64),
) (int64, bool, error) {
	buffer := make([]byte, 64*1024)
	for {
		n, readErr, idleExpired := readWithIdleTimeout(body, buffer, idleTimeout)
		if n > 0 {
			writtenNow, writeErr := dst.Write(buffer[:n])
			written += int64(writtenNow)
			if writeErr != nil {
				return written, false, writeErr
			}
			if writtenNow != n {
				return written, false, io.ErrShortWrite
			}
			if onProgress != nil {
				onProgress(written, total)
			}
			if total > 0 {
				if written == total {
					return written, false, nil
				}
				if written > total {
					return written, false, fmt.Errorf(
						"download exceeds declared size: got %d, want %d",
						written,
						total,
					)
				}
			}
		}
		if readErr == io.EOF {
			if total > 0 && written != total {
				return written, true, io.ErrUnexpectedEOF
			}
			return written, false, nil
		}
		if readErr != nil {
			if ctx.Err() != nil {
				return written, false, ctx.Err()
			}
			if idleExpired {
				return written, true, fmt.Errorf("no download data received for %s", idleTimeout)
			}
			return written, true, readErr
		}
	}
}

func readWithIdleTimeout(
	body io.ReadCloser,
	buffer []byte,
	idleTimeout time.Duration,
) (n int, err error, idleExpired bool) {
	if idleTimeout <= 0 {
		n, err = body.Read(buffer)
		return n, err, false
	}

	fired := make(chan struct{})
	timer := time.AfterFunc(idleTimeout, func() {
		close(fired)
		_ = body.Close()
	})
	n, err = body.Read(buffer)
	if timer.Stop() {
		return n, err, false
	}
	<-fired
	return n, err, true
}

func waitForDownloadRetry(ctx context.Context, baseDelay time.Duration, attempt int) error {
	if baseDelay <= 0 {
		return ctx.Err()
	}
	delay := baseDelay * time.Duration(1<<min(attempt, 5))
	timer := time.NewTimer(delay)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
