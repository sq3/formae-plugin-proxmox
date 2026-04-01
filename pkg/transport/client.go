// SPDX-License-Identifier: Apache-2.0

package transport

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

// Client wraps HTTP calls to the Proxmox VE REST API
type Client struct {
	baseURL    string
	tokenID    string
	secret     string
	httpClient *http.Client
}

// ClientConfig holds connection parameters for the Proxmox API
type ClientConfig struct {
	ApiUrl   string
	TokenID  string
	Secret   string
	Insecure bool
}

// NewClient creates a new Proxmox API client
func NewClient(cfg *ClientConfig) (*Client, error) {
	if cfg == nil {
		return nil, fmt.Errorf("config is nil")
	}
	if cfg.ApiUrl == "" {
		return nil, fmt.Errorf("API URL is required")
	}
	if cfg.TokenID == "" || cfg.Secret == "" {
		return nil, fmt.Errorf("token ID and secret are required")
	}

	// Normalize base URL: strip trailing slash, ensure /api2/json suffix
	baseURL := strings.TrimRight(cfg.ApiUrl, "/")
	if !strings.HasSuffix(baseURL, "/api2/json") {
		baseURL += "/api2/json"
	}

	insecure := cfg.Insecure
	if !insecure {
		if v := os.Getenv("PROXMOX_INSECURE_SKIP_VERIFY"); v == "true" || v == "1" {
			insecure = true
		}
	}

	transport := &http.Transport{
		TLSClientConfig: &tls.Config{
			InsecureSkipVerify: insecure,
		},
	}

	return &Client{
		baseURL: baseURL,
		tokenID: cfg.TokenID,
		secret:  cfg.Secret,
		httpClient: &http.Client{
			Transport: transport,
			Timeout:   30 * time.Second,
		},
	}, nil
}

// Response represents a parsed Proxmox API response
type Response struct {
	StatusCode int
	Data       map[string]interface{}
	DataArray  []interface{}
	RawData    json.RawMessage
}

// Get performs a GET request
func (c *Client) Get(ctx context.Context, path string) (*Response, error) {
	return c.do(ctx, "GET", path, nil)
}

// Post performs a POST request with form-encoded body
func (c *Client) Post(ctx context.Context, path string, params map[string]interface{}) (*Response, error) {
	return c.do(ctx, "POST", path, params)
}

// Put performs a PUT request with form-encoded body
func (c *Client) Put(ctx context.Context, path string, params map[string]interface{}) (*Response, error) {
	return c.do(ctx, "PUT", path, params)
}

// Delete performs a DELETE request
func (c *Client) Delete(ctx context.Context, path string) (*Response, error) {
	return c.do(ctx, "DELETE", path, nil)
}

// WaitForTask polls a task UPID until it completes
func (c *Client) WaitForTask(ctx context.Context, node string, upid string) error {
	taskPath := fmt.Sprintf("/nodes/%s/tasks/%s/status", node, url.PathEscape(upid))

	maxWait := 5 * time.Minute
	startTime := time.Now()
	pollInterval := 2 * time.Second

	for {
		if time.Since(startTime) > maxWait {
			return fmt.Errorf("task timed out after %v: %s", maxWait, upid)
		}

		time.Sleep(pollInterval)

		resp, err := c.Get(ctx, taskPath)
		if err != nil {
			return fmt.Errorf("failed to poll task status: %w", err)
		}

		status, _ := resp.Data["status"].(string)
		switch status {
		case "stopped":
			// Check if task completed successfully
			exitStatus, _ := resp.Data["exitstatus"].(string)
			if exitStatus == "OK" {
				return nil
			}
			return fmt.Errorf("task failed with exit status: %s", exitStatus)
		case "running":
			// Continue polling
		default:
			// Unknown status, continue polling
		}

		// Exponential backoff, max 15s
		pollInterval = pollInterval * 2
		if pollInterval > 15*time.Second {
			pollInterval = 15 * time.Second
		}
	}
}

func (c *Client) do(ctx context.Context, method string, path string, params map[string]interface{}) (*Response, error) {
	fullURL := c.baseURL + path

	var body io.Reader
	if params != nil && (method == "POST" || method == "PUT") {
		form := url.Values{}
		for k, v := range params {
			form.Set(k, fmt.Sprintf("%v", v))
		}
		body = strings.NewReader(form.Encode())
	}

	req, err := http.NewRequestWithContext(ctx, method, fullURL, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	// API token authentication
	req.Header.Set("Authorization", fmt.Sprintf("PVEAPIToken=%s=%s", c.tokenID, c.secret))

	if body != nil {
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, &Error{
			Code:       ErrorCodeUnknown,
			Message:    fmt.Sprintf("request failed: %v", err),
			Underlying: err,
		}
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	// Check for HTTP errors
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := string(respBody)
		// Try to extract error message from JSON
		var envelope map[string]interface{}
		if json.Unmarshal(respBody, &envelope) == nil {
			if errors, ok := envelope["errors"]; ok {
				errJSON, _ := json.Marshal(errors)
				message = string(errJSON)
			} else if msg, ok := envelope["message"].(string); ok {
				message = msg
			}
		}
		return nil, &Error{
			Code:     ClassifyHTTPStatus(resp.StatusCode),
			Message:  message,
			HTTPCode: resp.StatusCode,
		}
	}

	// Parse response - Proxmox wraps everything in {"data": ...}
	result := &Response{StatusCode: resp.StatusCode}

	if len(respBody) == 0 {
		return result, nil
	}

	var envelope struct {
		Data json.RawMessage `json:"data"`
	}
	if err := json.Unmarshal(respBody, &envelope); err != nil {
		return nil, fmt.Errorf("failed to parse response: %w", err)
	}

	if len(envelope.Data) == 0 {
		return result, nil
	}

	result.RawData = envelope.Data

	// Try parsing data as a string (e.g., UPID from task creation)
	var strData string
	if json.Unmarshal(envelope.Data, &strData) == nil {
		result.Data = map[string]interface{}{"value": strData}
		return result, nil
	}

	// Try parsing as object
	var obj map[string]interface{}
	if json.Unmarshal(envelope.Data, &obj) == nil {
		result.Data = obj
		return result, nil
	}

	// Try parsing as array
	var arr []interface{}
	if json.Unmarshal(envelope.Data, &arr) == nil {
		result.DataArray = arr
		return result, nil
	}

	return result, nil
}
