package common

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"strings"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
)

func HttpClient() *http.Client {
	return &http.Client{
		Transport: otelhttp.NewTransport(http.DefaultTransport),
	}
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
		parsedURL, parseErr := neturl.Parse(url)
		if parseErr != nil {
			return nil, parseErr
		}
		q := parsedURL.Query()
		for k, v := range httpConfig.QueryParams {
			q.Set(k, v)
		}
		parsedURL.RawQuery = q.Encode()
		url = parsedURL.String()
	}
	request, err := http.NewRequest(method, url, nil)
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
	if httpConfig.Body != nil {
		defer func() {
			if cerr := httpConfig.Body.Close(); cerr != nil {
				slog.Error("Failed to close HTTP request body:", "error", cerr)
			}
		}()
		request.Body = httpConfig.Body
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
