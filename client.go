package openrouter

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"strings"
	"time"

	utils "github.com/dedlockdave/go-openrouter/internal"
)

type Client struct {
	config ClientConfig

	requestBuilder utils.RequestBuilder
}

func NewClient(auth, xTitle, httpReferer string) (*Client, error) {
	config, err := DefaultConfig(auth, xTitle, httpReferer)
	if err != nil {
		return nil, err
	}
	return NewClientWithConfig(config), nil
}

func NewClientWithConfig(config ClientConfig) *Client {
	return &Client{
		config:         config,
		requestBuilder: utils.NewRequestBuilder(),
	}
}

const (
	maxRetries     = 3
	initialBackoff = 1 * time.Second
)

var retryableErrors = []string{
	"Overloaded",
	"Internal Server Error",
	"Provider returned error",
}

func shouldRetry(err error) bool {
	if err == nil {
		return false
	}

	errMsg := err.Error()
	for _, retryableErr := range retryableErrors {
		if strings.Contains(errMsg, retryableErr) {
			return true
		}
	}
	return false
}

func (c *Client) sendRequest(req *http.Request, v any) error {
	var lastErr error

	for attempt := 0; attempt <= maxRetries; attempt++ {
		if attempt > 0 {
			// Calculate exponential backoff with jitter
			backoff := float64(initialBackoff) * math.Pow(2, float64(attempt-1))
			jitter := (rand.Float64()*0.5 + 0.5) // 50%-150% of base backoff
			sleepDuration := time.Duration(backoff * jitter)
			time.Sleep(sleepDuration)

			// Clone the request for retry since the original body may have been consumed
			var err error
			req, err = cloneRequest(req)
			if err != nil {
				return fmt.Errorf("failed to clone request for retry: %w", err)
			}
		}

		err := c.doRequest(req, v)
		if err == nil {
			return nil
		}

		// lastErr = err
		// if !shouldRetry(err) {
		// 	return err
		// }

		if attempt < maxRetries {
			log.Printf("Request failed with error: %v. Retrying attempt %d/%d", err, attempt+1, maxRetries)
		}
	}

	return fmt.Errorf("all retry attempts failed, last error: %w", lastErr)
}

func (c *Client) doRequest(req *http.Request, v any) error {
	req.Header.Set("Accept", "application/json; charset=utf-8")

	// Check whether Content-Type is already set, Upload Files API requires
	// Content-Type == multipart/form-data
	contentType := req.Header.Get("Content-Type")
	if contentType == "" {
		req.Header.Set("Content-Type", "application/json; charset=utf-8")
	}

	c.setCommonHeaders(req)

	res, err := c.config.HTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to send request: %w", err)
	}
	defer res.Body.Close()

	// Handle non-200 responses
	if res.StatusCode != http.StatusOK {
		return c.handleErrorResp(res)
	}

	// Check for empty response body
	if res.Body == nil {
		return fmt.Errorf("empty response body")
	}

	bodyBytes, err := io.ReadAll(res.Body)
	if err != nil {
		return fmt.Errorf("failed to read response body: %w", err)
	}

	// Check if response contains an error
	var errorResp ErrorResponse
	if err := json.Unmarshal(bodyBytes, &errorResp); err == nil {
		if errorResp.Error != nil && errorResp.Error.Message != "" {
			return fmt.Errorf("API error: %s", errorResp.Error.Message)
		}
	}

	// Reset the body for subsequent reads
	res.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))

	return decodeResponse(res.Body, v)
}

func (c *Client) setCommonHeaders(req *http.Request) {
	req.Header.Set("HTTP-Referer", c.config.HttpReferer)
	req.Header.Set("X-Title", c.config.XTitle)
	req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", c.config.authToken))
}

func isFailureStatusCode(resp *http.Response) bool {
	return resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusBadRequest
}

func decodeResponse(body io.Reader, v any) error {
	if v == nil {
		return nil
	}

	if result, ok := v.(*string); ok {
		return decodeString(body, result)
	}
	return json.NewDecoder(body).Decode(v)
}

func decodeString(body io.Reader, output *string) error {
	b, err := io.ReadAll(body)
	if err != nil {
		return err
	}
	*output = string(b)
	return nil
}

// fullURL returns full URL for request.
// args[0] is model name, if API type is Azure, model name is required to get deployment name.
func (c *Client) fullURL(suffix string) string {
	return fmt.Sprintf("%s%s", c.config.BaseURL, suffix)
}

func (c *Client) newStreamRequest(
	ctx context.Context,
	method string,
	urlSuffix string,
	body any) (*http.Request, error) {
	req, err := c.requestBuilder.Build(ctx, method, c.fullURL(urlSuffix), body)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set("Cache-Control", "no-cache")
	req.Header.Set("Connection", "keep-alive")

	c.setCommonHeaders(req)
	return req, nil
}

func (c *Client) handleErrorResp(resp *http.Response) error {
	var errRes ErrorResponse

	err := json.NewDecoder(resp.Body).Decode(&errRes)
	if err != nil || errRes.Error == nil {
		reqErr := &RequestError{
			HTTPStatusCode: resp.StatusCode,
			Err:            err,
		}
		if errRes.Error != nil {
			reqErr.Err = errRes.Error
		}
		return reqErr
	}

	errRes.Error.HTTPStatusCode = resp.StatusCode
	return errRes.Error
}

func cloneRequest(req *http.Request) (*http.Request, error) {
	clone := req.Clone(req.Context())

	// If there's a body, we need to clone it
	if req.Body != nil {
		bodyBytes, err := io.ReadAll(req.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to read original request body: %w", err)
		}
		req.Body = io.NopCloser(bytes.NewBuffer(bodyBytes))   // Restore original body
		clone.Body = io.NopCloser(bytes.NewBuffer(bodyBytes)) // Set cloned body
	}

	return clone, nil
}
