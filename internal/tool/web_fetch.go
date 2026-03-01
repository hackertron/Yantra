package tool

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/hackertron/Yantra/internal/types"
)

const (
	webFetchTimeout    = 30 * time.Second
	webFetchMaxBody    = 1024 * 1024 // 1 MB
)

type webFetchTool struct{}

func NewWebFetch() types.Tool { return &webFetchTool{} }

func (t *webFetchTool) Name() string        { return "web_fetch" }
func (t *webFetchTool) SafetyTier() types.SafetyTier { return types.SideEffecting }
func (t *webFetchTool) Timeout() time.Duration       { return webFetchTimeout }

func (t *webFetchTool) Description() string {
	return "Fetch a URL via HTTP and return the status code and response body."
}

func (t *webFetchTool) Decl() types.FunctionDecl {
	return types.FunctionDecl{
		Name:        t.Name(),
		Description: t.Description(),
		Parameters: Schema(
			Prop{Name: "url", Type: TypeString, Description: "URL to fetch", Required: true},
			Prop{Name: "method", Type: TypeString, Description: "HTTP method (default GET)", Required: false, Enum: []string{"GET", "POST", "PUT", "DELETE", "PATCH", "HEAD"}},
			Prop{Name: "body", Type: TypeString, Description: "Request body (for POST/PUT/PATCH)", Required: false},
			Prop{Name: "headers", Type: TypeString, Description: "Headers as JSON object string", Required: false},
		),
	}
}

func (t *webFetchTool) Execute(ctx context.Context, input json.RawMessage, execCtx types.ToolExecutionContext) (string, error) {
	var args struct {
		URL     string `json:"url"`
		Method  string `json:"method"`
		Body    string `json:"body"`
		Headers string `json:"headers"`
	}
	if err := json.Unmarshal(input, &args); err != nil {
		return "", fmt.Errorf("invalid input: %w", err)
	}

	method := args.Method
	if method == "" {
		method = "GET"
	}

	var bodyReader io.Reader
	if args.Body != "" {
		bodyReader = io.NopCloser(
			io.LimitReader(
				readerFromString(args.Body),
				webFetchMaxBody,
			),
		)
	}

	req, err := http.NewRequestWithContext(ctx, method, args.URL, bodyReader)
	if err != nil {
		return "", fmt.Errorf("invalid request: %w", err)
	}

	// Parse and apply custom headers.
	if args.Headers != "" {
		var headers map[string]string
		if err := json.Unmarshal([]byte(args.Headers), &headers); err != nil {
			return "", fmt.Errorf("invalid headers JSON: %w", err)
		}
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}

	client := &http.Client{
		Timeout: webFetchTimeout,
	}
	resp, err := client.Do(req)
	if err != nil {
		return "", fmt.Errorf("fetch error: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(io.LimitReader(resp.Body, webFetchMaxBody))
	if err != nil {
		return "", fmt.Errorf("read body error: %w", err)
	}

	return fmt.Sprintf("status: %d\n\n%s", resp.StatusCode, string(body)), nil
}

type stringReader struct {
	s string
	i int
}

func readerFromString(s string) io.Reader {
	return &stringReader{s: s}
}

func (r *stringReader) Read(p []byte) (int, error) {
	if r.i >= len(r.s) {
		return 0, io.EOF
	}
	n := copy(p, r.s[r.i:])
	r.i += n
	return n, nil
}
