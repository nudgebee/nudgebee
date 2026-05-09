package common

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	netUrl "net/url"
	"strings"
	"time"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

const (
	RetryAttempts    = 5
	MaxRetryDuration = RetryAttempts * time.Minute
	InitialBackoff   = time.Second
)

var HttpClientInternal = &http.Client{Transport: otelhttp.NewTransport(http.DefaultTransport)}

func HttpClient() *http.Client {
	return HttpClientInternal
}

type httpConfig struct {
	QueryParams map[string]string
	Headers     map[string]string
	Context     context.Context
	Body        io.ReadCloser
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
	data, err := json.Marshal(c)
	if err != nil {
		slog.Error("http: unable to marshal json body", "error", err)
		return httpBody{body: nil}
	}

	return httpBody{body: io.NopCloser(bytes.NewReader(data))}
}

func HttpWithStringBody(c string) HttpOption {
	return httpBody{body: io.NopCloser(strings.NewReader(c))}
}

func httpExecuteRequest(method string, url string, options ...HttpOption) (resp *http.Response, err error) {
	httpConfig := &httpConfig{}
	for _, option := range options {
		option.apply(httpConfig)
	}
	if httpConfig.QueryParams != nil {
		params := netUrl.Values{}
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

	return HttpClient().Do(request)
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
