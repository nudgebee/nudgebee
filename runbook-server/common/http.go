package common

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func HttpClient() *http.Client {
	return &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}
}

type httpConfig struct {
	QueryParams map[string]string
	Headers     map[string]string
	Context     context.Context
	Body        io.ReadCloser
	timeout     time.Duration
}

type HttpOption interface {
	apply(*httpConfig)
}

type httpQueryParam struct {
	param map[string]string
}

func (h httpQueryParam) apply(c *httpConfig) {
	c.QueryParams = h.param
}

func HttpWithQueryParams(params map[string]string) HttpOption {
	return httpQueryParam{param: params}
}

type httpHeader struct {
	param map[string]string
}

func (h httpHeader) apply(c *httpConfig) {
	c.Headers = h.param
}

func HttpWithHeaders(params map[string]string) HttpOption {
	return httpHeader{param: params}
}

type httpTimeout struct {
	timeout time.Duration
}

func (h httpTimeout) apply(c *httpConfig) {
	c.timeout = h.timeout
}

func HttpWithTimeout(timeout time.Duration) HttpOption {
	return httpTimeout{timeout: timeout}
}

type httpContext struct {
	context context.Context
}

func (h httpContext) apply(c *httpConfig) {
	c.Context = h.context
}

func HttpWithContext(c context.Context) HttpOption {
	return httpContext{context: c}
}

type httpBody struct {
	body io.ReadCloser
}

func (h httpBody) apply(c *httpConfig) {
	c.Body = h.body
}

func HttpWithBody(c io.ReadCloser) HttpOption {
	return httpBody{body: c}
}

func HttpWithJsonBody(c any) HttpOption {
	data, err := MarshalJson(c)
	if err != nil {
		slog.Error("http: unable to marshal json body", "error", err)
		return httpBody{body: nil}
	}

	return httpBody{body: io.NopCloser(bytes.NewReader(data))}
}

func HttpWithStringBody(c string) HttpOption {
	return httpBody{body: io.NopCloser(strings.NewReader(c))}
}

func HttpWithFormUrlEncodedBody(c any) HttpOption {
	formData := neturl.Values{}
	if m, ok := c.(map[string]string); ok {
		for key, value := range m {
			formData.Set(key, value)
		}
	} else {
		slog.Error("http: invalid type for form data", "type", fmt.Sprintf("%T", c))
		return httpBody{body: nil}
	}
	data := []byte(formData.Encode())
	return httpBody{body: io.NopCloser(bytes.NewReader(data))}
}

func httpExecuteRequest(method string, url string, options ...HttpOption) (resp *http.Response, err error) {
	httpConfig := &httpConfig{}
	for _, option := range options {
		option.apply(httpConfig)
	}
	if httpConfig.QueryParams != nil {
		params := neturl.Values{}
		for k, v := range httpConfig.QueryParams {
			params.Add(k, v)
		}
		url = url + "?" + params.Encode()
	}
	request, err := http.NewRequest(method, url, httpConfig.Body)
	if err != nil {
		return nil, err
	}

	if httpConfig.Context == nil {
		httpConfig.Context = context.Background()
	}

	request = request.WithContext(httpConfig.Context)
	if httpConfig.Headers != nil {
		for k, v := range httpConfig.Headers {
			request.Header.Add(k, v)
		}
	}

	client := *HttpClient() // Create a copy of the client
	if httpConfig.timeout > 0 {
		client.Timeout = httpConfig.timeout
	}

	return client.Do(request)
}

func HttpGet(url string, options ...HttpOption) (resp *http.Response, err error) {
	return httpExecuteRequest("GET", url, options...)
}

func HttpPost(url string, options ...HttpOption) (resp *http.Response, err error) {
	return httpExecuteRequest("POST", url, options...)
}

func HttpPut(url string, options ...HttpOption) (resp *http.Response, err error) {
	return httpExecuteRequest("PUT", url, options...)
}

func HttpDelete(url string, options ...HttpOption) (resp *http.Response, err error) {
	return httpExecuteRequest("DELETE", url, options...)
}

func HttpHead(url string, options ...HttpOption) (resp *http.Response, err error) {
	return httpExecuteRequest("HEAD", url, options...)
}

func HttpPatch(url string, options ...HttpOption) (resp *http.Response, err error) {
	return httpExecuteRequest("PATCH", url, options...)
}
