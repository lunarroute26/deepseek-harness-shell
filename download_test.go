package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/wailsapp/wails/v3/pkg/application"
)

func TestParseDSHBaseURL(t *testing.T) {
	valid, err := parseDSHBaseURL("http://127.0.0.1:62341")
	if err != nil {
		t.Fatal(err)
	}
	if got := valid.String(); got != "http://127.0.0.1:62341" {
		t.Fatalf("base URL = %q", got)
	}

	for _, candidate := range []string{
		"https://127.0.0.1:62341",
		"http://localhost:62341",
		"http://127.0.0.1",
		"http://user@127.0.0.1:62341",
		"http://127.0.0.1:62341?unexpected=true",
	} {
		t.Run(candidate, func(t *testing.T) {
			if _, err := parseDSHBaseURL(candidate); err == nil {
				t.Fatalf("parseDSHBaseURL(%q) succeeded", candidate)
			}
		})
	}
}

func TestValidateDownloadRequest(t *testing.T) {
	baseURL, err := parseDSHBaseURL("http://127.0.0.1:62341")
	if err != nil {
		t.Fatal(err)
	}
	valid := "http://127.0.0.1:62341/api/session.export?sessionId=session-1&includeDescendants=true"
	parsed, filename, err := validateDownloadRequest(baseURL, valid, "dsh-session-session-1.zip")
	if err != nil {
		t.Fatal(err)
	}
	if parsed.String() != valid || filename != "dsh-session-session-1.zip" {
		t.Fatalf("validated request = (%q, %q)", parsed, filename)
	}
	_, fallback, err := validateDownloadRequest(
		baseURL,
		"http://127.0.0.1:62341/api/session.export?sessionId=a%2Fb&includeDescendants=true",
		"",
	)
	if err != nil || fallback != "dsh-session-a_b.zip" {
		t.Fatalf("fallback filename = %q, error = %v", fallback, err)
	}

	for name, candidate := range map[string]string{
		"wrong port":       "http://127.0.0.1:62342/api/session.export?sessionId=a&includeDescendants=true",
		"wrong path":       "http://127.0.0.1:62341/api/other?sessionId=a&includeDescendants=true",
		"missing session":  "http://127.0.0.1:62341/api/session.export?includeDescendants=true",
		"no descendants":   "http://127.0.0.1:62341/api/session.export?sessionId=a&includeDescendants=false",
		"unexpected query": "http://127.0.0.1:62341/api/session.export?sessionId=a&includeDescendants=true&target=x",
		"fragment":         "http://127.0.0.1:62341/api/session.export?sessionId=a&includeDescendants=true#x",
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, err := validateDownloadRequest(baseURL, candidate, "download.zip"); err == nil {
				t.Fatalf("validateDownloadRequest(%q) succeeded", candidate)
			}
		})
	}
}

func TestDownloadFilenameSanitising(t *testing.T) {
	for _, test := range []struct {
		input string
		want  string
	}{
		{"../../report.zip", "report.zip"},
		{"..\\..\\report.zip", "report.zip"},
		{"session:a/b", "b.zip"},
		{"report.ZIP", "report.ZIP"},
		{" report ", "report.zip"},
		{"", ""},
	} {
		if got := sanitiseZipFilename(test.input); got != test.want {
			t.Errorf("sanitiseZipFilename(%q) = %q, want %q", test.input, got, test.want)
		}
	}
	if got := ensureZipDestination(filepath.Join("tmp", "archive")); got != filepath.Join("tmp", "archive.zip") {
		t.Fatalf("ensureZipDestination() = %q", got)
	}
}

func TestDownloadMessageOriginAllowed(t *testing.T) {
	baseURL, err := parseDSHBaseURL("http://127.0.0.1:62341")
	if err != nil {
		t.Fatal(err)
	}
	for name, info := range map[string]*application.OriginInfo{
		"mac origin": {
			Origin:      "http://127.0.0.1:62341",
			IsMainFrame: true,
		},
		"linux URL": {
			Origin: "http://127.0.0.1:62341/session/1",
		},
		"windows sources": {
			Origin:    "http://127.0.0.1:62341/session/1",
			TopOrigin: "http://127.0.0.1:62341/",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if !downloadMessageOriginAllowed(info, baseURL) {
				t.Fatal("matching origin was rejected")
			}
		})
	}
	for name, info := range map[string]*application.OriginInfo{
		"nil":        nil,
		"empty":      {},
		"wrong port": {Origin: "http://127.0.0.1:62342"},
		"mixed": {
			Origin:    "http://127.0.0.1:62341",
			TopOrigin: "https://example.com",
		},
	} {
		t.Run(name, func(t *testing.T) {
			if downloadMessageOriginAllowed(info, baseURL) {
				t.Fatal("untrusted origin was allowed")
			}
		})
	}
}

func TestBridgeScriptTargetsDetachedAnchorDownloads(t *testing.T) {
	script := bridgeScriptForOrigin("http://127.0.0.1:62341")
	for _, expected := range []string{
		"HTMLAnchorElement.prototype.click",
		"/api/session.export",
		"dsh-shell:download-request",
		"http://127.0.0.1:62341",
	} {
		if !strings.Contains(script, expected) {
			t.Fatalf("bridge script does not contain %q", expected)
		}
	}
}

func TestStreamDownloadReplacesDestination(t *testing.T) {
	payload := strings.Repeat("session-log-data\n", 4096)
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		if request.URL.Path != "/api/session.export" {
			return nil, fmt.Errorf("unexpected path: %s", request.URL.Path)
		}
		return testHTTPResponse(http.StatusOK, payload, int64(len(payload))), nil
	})}

	destination := filepath.Join(t.TempDir(), "session.zip")
	if err := os.WriteFile(destination, []byte("old"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceURL, err := url.Parse("http://127.0.0.1:62341/api/session.export?sessionId=a&includeDescendants=true")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	var lastWritten, lastTotal int64
	err = streamDownload(context.Background(), client, sourceURL, destination, func(written, total int64) {
		mu.Lock()
		lastWritten, lastTotal = written, total
		mu.Unlock()
	})
	if err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != payload {
		t.Fatal("downloaded file content differs")
	}
	mu.Lock()
	if lastWritten != int64(len(payload)) || lastTotal != int64(len(payload)) {
		t.Fatalf("last progress = (%d, %d)", lastWritten, lastTotal)
	}
	mu.Unlock()
	assertNoDownloadParts(t, filepath.Dir(destination))
}

func TestStreamDownloadFailurePreservesExistingFile(t *testing.T) {
	client := &http.Client{Transport: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
		return testHTTPResponse(http.StatusServiceUnavailable, "export unavailable", -1), nil
	})}
	destination := filepath.Join(t.TempDir(), "session.zip")
	if err := os.WriteFile(destination, []byte("existing"), 0o600); err != nil {
		t.Fatal(err)
	}
	sourceURL, _ := url.Parse("http://127.0.0.1:62341/api/session.export")
	err := streamDownload(context.Background(), client, sourceURL, destination, nil)
	if err == nil || !strings.Contains(err.Error(), "HTTP 503") {
		t.Fatalf("streamDownload() error = %v", err)
	}
	content, err := os.ReadFile(destination)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "existing" {
		t.Fatalf("existing destination was changed to %q", content)
	}
	assertNoDownloadParts(t, filepath.Dir(destination))
}

func TestStreamDownloadCancellationRemovesTemporaryFile(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	client := &http.Client{Transport: roundTripFunc(func(request *http.Request) (*http.Response, error) {
		reader, writer := io.Pipe()
		go func() {
			_, _ = writer.Write([]byte(strings.Repeat("x", 4096)))
			<-request.Context().Done()
			_ = writer.CloseWithError(request.Context().Err())
		}()
		return &http.Response{
			StatusCode:    http.StatusOK,
			Status:        "200 OK",
			Header:        make(http.Header),
			Body:          reader,
			ContentLength: -1,
			Request:       request,
		}, nil
	})}
	destination := filepath.Join(t.TempDir(), "cancelled.zip")
	sourceURL, _ := url.Parse("http://127.0.0.1:62341/api/session.export")
	err := streamDownload(ctx, client, sourceURL, destination, func(written, _ int64) {
		if written > 0 {
			cancel()
		}
	})
	if err == nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("streamDownload() error = %v, want context cancellation", err)
	}
	if _, err := os.Stat(destination); !os.IsNotExist(err) {
		t.Fatalf("cancelled destination exists: %v", err)
	}
	assertNoDownloadParts(t, filepath.Dir(destination))
}

func testHTTPResponse(status int, body string, contentLength int64) *http.Response {
	return &http.Response{
		StatusCode:    status,
		Status:        fmt.Sprintf("%d %s", status, http.StatusText(status)),
		Header:        make(http.Header),
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: contentLength,
	}
}

func assertNoDownloadParts(t *testing.T, directory string) {
	t.Helper()
	matches, err := filepath.Glob(filepath.Join(directory, "*.part"))
	if err != nil {
		t.Fatal(err)
	}
	if len(matches) != 0 {
		t.Fatalf("temporary download files remain: %v", matches)
	}
}
