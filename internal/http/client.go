package http

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type Client struct {
	c *http.Client
}

type httpStatusCodeError struct {
	StatusCode int
}

func (e *httpStatusCodeError) Error() string {
	return fmt.Sprintf("status code %d", e.StatusCode)
}

func NewClient() *Client {
	return &Client{
		c: &http.Client{},
	}
}

func (c *Client) RequestJSON(ctx context.Context, url string, body, resp any) error {
	if body == http.NoBody {
		return c.GetJSON(ctx, url, resp)
	}
	return c.PostJSON(ctx, url, body, resp)
}

func (c *Client) Get(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	res, err := c.c.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return &httpStatusCodeError{StatusCode: res.StatusCode}
	}
	return nil
}

func (c *Client) GetJSON(ctx context.Context, url string, resp any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	res, err := c.c.Do(req)
	if err != nil {
		return fmt.Errorf("performing request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return &httpStatusCodeError{StatusCode: res.StatusCode}
	}
	if err := json.NewDecoder(res.Body).Decode(resp); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

func (c *Client) PostJSON(ctx context.Context, url string, body, resp any) error {
	bodyJSON, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("marshaling request body: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(bodyJSON))
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := c.c.Do(req)
	if err != nil {
		return fmt.Errorf("performing request: %w", err)
	}
	defer res.Body.Close()
	if res.StatusCode != http.StatusOK {
		return &httpStatusCodeError{StatusCode: res.StatusCode}
	}
	if err := json.NewDecoder(res.Body).Decode(resp); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
