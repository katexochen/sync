package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

type httpClient struct {
	c *http.Client
}

func newHTTPClient() *httpClient {
	return &httpClient{
		c: &http.Client{},
	}
}

func (c *httpClient) RequestJSON(ctx context.Context, url string, body, resp any) error {
	if body == http.NoBody {
		return c.GetJSON(ctx, url, resp)
	}
	return c.PostJSON(ctx, url, body, resp)
}

func (c *httpClient) Get(ctx context.Context, url string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	_, err = c.c.Do(req)
	return err
}

func (c *httpClient) GetJSON(ctx context.Context, url string, resp any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, http.NoBody)
	if err != nil {
		return fmt.Errorf("creating request: %w", err)
	}
	res, err := c.c.Do(req)
	if err != nil {
		return fmt.Errorf("performing request: %w", err)
	}
	defer res.Body.Close()
	if err := json.NewDecoder(res.Body).Decode(resp); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}

func (c *httpClient) PostJSON(ctx context.Context, url string, body, resp any) error {
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
	if err := json.NewDecoder(res.Body).Decode(resp); err != nil {
		return fmt.Errorf("decoding response: %w", err)
	}
	return nil
}
