package middleware

import (
	"bufio"
	"bytes"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/interfaces"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/logging"
)

func TestExtractRequestBodyPrefersOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	wrapper := &ResponseWriterWrapper{
		requestInfo: &RequestInfo{Body: []byte("original-body")},
	}

	body := wrapper.extractRequestBody(c)
	if string(body) != "original-body" {
		t.Fatalf("request body = %q, want %q", string(body), "original-body")
	}

	c.Set(requestBodyOverrideContextKey, []byte("override-body"))
	body = wrapper.extractRequestBody(c)
	if string(body) != "override-body" {
		t.Fatalf("request body = %q, want %q", string(body), "override-body")
	}
}

func TestExtractRequestBodySupportsStringOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	wrapper := &ResponseWriterWrapper{body: &bytes.Buffer{}}
	c.Set(requestBodyOverrideContextKey, "override-as-string")

	body := wrapper.extractRequestBody(c)
	if string(body) != "override-as-string" {
		t.Fatalf("request body = %q, want %q", string(body), "override-as-string")
	}
}

func TestExtractResponseBodyPrefersOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	wrapper := &ResponseWriterWrapper{body: &bytes.Buffer{}}
	wrapper.body.WriteString("original-response")

	body := wrapper.extractResponseBody(c)
	if string(body) != "original-response" {
		t.Fatalf("response body = %q, want %q", string(body), "original-response")
	}

	c.Set(responseBodyOverrideContextKey, []byte("override-response"))
	body = wrapper.extractResponseBody(c)
	if string(body) != "override-response" {
		t.Fatalf("response body = %q, want %q", string(body), "override-response")
	}

	body[0] = 'X'
	if got := wrapper.extractResponseBody(c); string(got) != "override-response" {
		t.Fatalf("response override should be cloned, got %q", string(got))
	}
}

func TestExtractResponseBodySupportsStringOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	wrapper := &ResponseWriterWrapper{}
	c.Set(responseBodyOverrideContextKey, "override-response-as-string")

	body := wrapper.extractResponseBody(c)
	if string(body) != "override-response-as-string" {
		t.Fatalf("response body = %q, want %q", string(body), "override-response-as-string")
	}
}

func TestExtractBodyOverrideClonesBytes(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	override := []byte("body-override")
	c.Set(requestBodyOverrideContextKey, override)

	body := extractBodyOverride(c, requestBodyOverrideContextKey)
	if !bytes.Equal(body, override) {
		t.Fatalf("body override = %q, want %q", string(body), string(override))
	}

	body[0] = 'X'
	if !bytes.Equal(override, []byte("body-override")) {
		t.Fatalf("override mutated: %q", string(override))
	}
}

func TestExtractWebsocketTimelineUsesOverride(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	wrapper := &ResponseWriterWrapper{}
	if got := wrapper.extractWebsocketTimeline(c); got != nil {
		t.Fatalf("expected nil websocket timeline, got %q", string(got))
	}

	c.Set(websocketTimelineOverrideContextKey, []byte("timeline"))
	body := wrapper.extractWebsocketTimeline(c)
	if string(body) != "timeline" {
		t.Fatalf("websocket timeline = %q, want %q", string(body), "timeline")
	}
}

func TestResponseWriterSkipsCaptureForSuccessfulResponseWhenLoggingDisabled(t *testing.T) {
	underlying := newBenchmarkGinWriter()
	wrapper := NewResponseWriterWrapper(underlying, &testRequestLogger{}, &RequestInfo{})
	wrapper.logOnErrorOnly = true
	underlying.Header().Set("Content-Type", "text/event-stream")

	wrapper.WriteHeader(http.StatusOK)
	if _, err := wrapper.Write([]byte("data: ok\n\n")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if wrapper.body != nil {
		t.Fatal("successful response allocated a body capture while logging was disabled")
	}
	if wrapper.headers != nil || wrapper.headersCaptured {
		t.Fatal("successful response captured headers while logging was disabled")
	}
}

func TestResponseWriterCapturesErrorResponseWhenLoggingDisabled(t *testing.T) {
	underlying := newBenchmarkGinWriter()
	wrapper := NewResponseWriterWrapper(underlying, &testRequestLogger{}, &RequestInfo{})
	wrapper.logOnErrorOnly = true
	underlying.Header().Set("Content-Type", "application/json")

	wrapper.WriteHeader(http.StatusBadRequest)
	if _, err := wrapper.WriteString(`{"error":"bad request"}`); err != nil {
		t.Fatalf("WriteString: %v", err)
	}

	if wrapper.body == nil || wrapper.body.String() != `{"error":"bad request"}` {
		t.Fatalf("captured body = %q", wrapper.body)
	}
	if got := wrapper.headers["Content-Type"]; len(got) != 1 || got[0] != "application/json" {
		t.Fatalf("captured Content-Type = %#v", got)
	}
}

func TestResponseWriterCapturesHeadersOnceBeforeCommit(t *testing.T) {
	underlying := newBenchmarkGinWriter()
	wrapper := NewResponseWriterWrapper(underlying, &testRequestLogger{enabled: true}, &RequestInfo{})
	underlying.Header().Set("X-Test", "before")

	wrapper.WriteHeader(http.StatusOK)
	underlying.Header().Set("X-Test", "after")
	if _, err := wrapper.Write([]byte("ok")); err != nil {
		t.Fatalf("Write: %v", err)
	}

	if got := wrapper.headers["X-Test"]; len(got) != 1 || got[0] != "before" {
		t.Fatalf("captured X-Test = %#v, want pre-commit value", got)
	}
}

func BenchmarkResponseWriterDisabledStreaming(b *testing.B) {
	underlying := newBenchmarkGinWriter()
	underlying.Header().Set("Content-Type", "text/event-stream")
	logger := &testRequestLogger{}
	requestInfo := &RequestInfo{}
	chunk := []byte("data: {\"type\":\"response.output_text.delta\",\"delta\":\"hello\"}\n\n")

	b.ReportAllocs()
	b.ResetTimer()
	for range b.N {
		underlying.reset()
		wrapper := NewResponseWriterWrapper(underlying, logger, requestInfo)
		wrapper.logOnErrorOnly = true
		wrapper.WriteHeader(http.StatusOK)
		for range 100 {
			_, _ = wrapper.Write(chunk)
		}
	}
}

func TestFinalizeStreamingWritesAPIWebsocketTimeline(t *testing.T) {
	gin.SetMode(gin.TestMode)
	recorder := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(recorder)

	streamWriter := &testStreamingLogWriter{}
	wrapper := &ResponseWriterWrapper{
		ResponseWriter: c.Writer,
		logger:         &testRequestLogger{enabled: true},
		requestInfo: &RequestInfo{
			URL:       "/v1/responses",
			Method:    "POST",
			Headers:   map[string][]string{"Content-Type": {"application/json"}},
			RequestID: "req-1",
			Timestamp: time.Date(2026, time.April, 1, 12, 0, 0, 0, time.UTC),
		},
		isStreaming:  true,
		streamWriter: streamWriter,
	}

	c.Set("API_WEBSOCKET_TIMELINE", []byte("Timestamp: 2026-04-01T12:00:00Z\nEvent: api.websocket.request\n{}"))

	if err := wrapper.Finalize(c); err != nil {
		t.Fatalf("Finalize error: %v", err)
	}
	if string(streamWriter.apiWebsocketTimeline) != "Timestamp: 2026-04-01T12:00:00Z\nEvent: api.websocket.request\n{}" {
		t.Fatalf("stream writer websocket timeline = %q", string(streamWriter.apiWebsocketTimeline))
	}
	if !streamWriter.closed {
		t.Fatal("expected stream writer to be closed")
	}
}

type testRequestLogger struct {
	enabled bool
}

func (l *testRequestLogger) LogRequest(string, string, map[string][]string, []byte, int, map[string][]string, []byte, []byte, []byte, []byte, []byte, []*interfaces.ErrorMessage, string, time.Time, time.Time) error {
	return nil
}

func (l *testRequestLogger) LogStreamingRequest(string, string, map[string][]string, []byte, string) (logging.StreamingLogWriter, error) {
	return &testStreamingLogWriter{}, nil
}

func (l *testRequestLogger) IsEnabled() bool {
	return l.enabled
}

type testStreamingLogWriter struct {
	apiWebsocketTimeline []byte
	closed               bool
}

type benchmarkGinWriter struct {
	header http.Header
	status int
	size   int
}

func newBenchmarkGinWriter() *benchmarkGinWriter {
	return &benchmarkGinWriter{header: make(http.Header)}
}

func (w *benchmarkGinWriter) reset() {
	w.status = 0
	w.size = 0
}

func (w *benchmarkGinWriter) Header() http.Header { return w.header }

func (w *benchmarkGinWriter) Write(data []byte) (int, error) {
	w.WriteHeaderNow()
	w.size += len(data)
	return len(data), nil
}

func (w *benchmarkGinWriter) WriteString(data string) (int, error) {
	w.WriteHeaderNow()
	w.size += len(data)
	return len(data), nil
}

func (w *benchmarkGinWriter) WriteHeader(statusCode int) {
	if w.status == 0 {
		w.status = statusCode
	}
}

func (w *benchmarkGinWriter) WriteHeaderNow() {
	if w.status == 0 {
		w.status = http.StatusOK
	}
}

func (w *benchmarkGinWriter) Status() int {
	if w.status == 0 {
		return http.StatusOK
	}
	return w.status
}

func (w *benchmarkGinWriter) Size() int     { return w.size }
func (w *benchmarkGinWriter) Written() bool { return w.status != 0 }
func (w *benchmarkGinWriter) Flush()        { w.WriteHeaderNow() }
func (w *benchmarkGinWriter) CloseNotify() <-chan bool {
	return make(chan bool)
}
func (w *benchmarkGinWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	return nil, nil, errors.New("hijack is not supported")
}
func (w *benchmarkGinWriter) Pusher() http.Pusher { return nil }

func (w *testStreamingLogWriter) WriteChunkAsync([]byte) {}

func (w *testStreamingLogWriter) WriteStatus(int, map[string][]string) error {
	return nil
}

func (w *testStreamingLogWriter) WriteAPIRequest([]byte) error {
	return nil
}

func (w *testStreamingLogWriter) WriteAPIResponse([]byte) error {
	return nil
}

func (w *testStreamingLogWriter) WriteAPIWebsocketTimeline(apiWebsocketTimeline []byte) error {
	w.apiWebsocketTimeline = bytes.Clone(apiWebsocketTimeline)
	return nil
}

func (w *testStreamingLogWriter) SetFirstChunkTimestamp(time.Time) {}

func (w *testStreamingLogWriter) Close() error {
	w.closed = true
	return nil
}
