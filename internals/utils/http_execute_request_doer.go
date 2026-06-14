package utils

import (
	"bytes"
	"fmt"
	"io"
	"net/http"

	uwuHttp "github.com/Laky-64/http"
)

// ExecuteRequestDoer adapts github.com/Laky-64/http to the generated HttpRequestDoer interface.
type ExecuteRequestDoer struct{}

// Do executes an http.Request via httpx.ExecuteRequest and maps the result back to *http.Response.
func (d *ExecuteRequestDoer) Do(req *http.Request) (*http.Response, error) {
	if req == nil {
		return nil, fmt.Errorf("nil request")
	}

	options := []uwuHttp.RequestOption{
		uwuHttp.Method(req.Method),
	}

	if req.Body != nil {
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		_ = req.Body.Close()
		if len(body) > 0 {
			options = append(options, uwuHttp.Body(body))
		}
	}

	if len(req.Header) > 0 {
		headers := make(map[string]string, len(req.Header))
		for k, v := range req.Header {
			if len(v) > 0 {
				headers[k] = v[0]
			}
		}
		options = append(options, uwuHttp.Headers(headers))
	}

	result, err := uwuHttp.ExecuteRequest(req.URL.String(), options...)
	if err != nil {
		return nil, err
	}

	resp := &http.Response{
		StatusCode: result.StatusCode,
		Status:     fmt.Sprintf("%d %s", result.StatusCode, http.StatusText(result.StatusCode)),
		Header:     http.Header(result.Headers),
		Body:       io.NopCloser(bytes.NewReader(result.Body)),
		Request:    req,
	}

	if resp.Header == nil {
		resp.Header = make(http.Header)
	}

	resp.ContentLength = int64(len(result.Body))
	return resp, nil
}
