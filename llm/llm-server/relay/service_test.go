package relay

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"nudgebee/llm/config"
)

type MockRequestHandler func(request string) (int, string, error)

func MockRelayServer(handler MockRequestHandler) (*httptest.Server, error) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, err := io.ReadAll(r.Body)
		if err != nil {
			if _, err := fmt.Fprintln(w, `{"message": "error"}`); err != nil {
				// Log the error, but don't return it as it's a test file
				fmt.Printf("Error writing to response writer: %v\n", err)
			}
			w.WriteHeader(http.StatusInternalServerError)
		}
		stringBody := string(body)

		status, response, err := handler(stringBody)
		if err != nil {
			if _, err := fmt.Fprintln(w, `{"message": "error"}`); err != nil {
				// Log the error, but don't return it as it's a test file
				fmt.Printf("Error writing to response writer: %v\n", err)
			}
			w.WriteHeader(http.StatusInternalServerError)
		}
		if response != "" {
			if _, err := fmt.Fprintln(w, response); err != nil {
				// Log the error, but don't return it as it's a test file
				fmt.Printf("Error writing to response writer: %v\n", err)
			}
		}
		w.WriteHeader(status)
	}))

	config.Config.RelayServerEndpoint = ts.URL
	return ts, nil
}
