// Package api fala com a API REST da Jataí (/api/v1).
package api

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Client struct {
	BaseURL    string
	Token      string
	UserAgent  string
	HTTP       *http.Client
	RetryDelay time.Duration
	Debug      io.Writer
}

// New exige https; http só para localhost (desenvolvimento).
func New(baseURL, token, userAgent string) (*Client, error) {
	u, err := url.Parse(baseURL)
	if err != nil || u.Host == "" {
		return nil, fmt.Errorf("endereço da API inválido: %q", baseURL)
	}
	host := u.Hostname()
	local := host == "localhost" || host == "127.0.0.1" || host == "::1"
	if u.Scheme != "https" && (u.Scheme != "http" || !local) {
		return nil, fmt.Errorf("a API precisa de https (só localhost aceita http): %q", baseURL)
	}
	return &Client{
		BaseURL: strings.TrimRight(baseURL, "/"), Token: token, UserAgent: userAgent,
		HTTP: defaultHTTPClient(), RetryDelay: time.Second,
	}, nil
}

// Sem timeout total: um upload de 500 MB pode demorar. O limite fica na
// conexão e na espera pela resposta depois do envio.
func defaultHTTPClient() *http.Client {
	return &http.Client{Transport: &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
		TLSHandshakeTimeout:   30 * time.Second,
		ResponseHeaderTimeout: 5 * time.Minute,
	}}
}

type request struct {
	method        string
	path          string
	query         url.Values
	body          io.Reader
	contentType   string
	contentLength int64
}

func (c *Client) Get(ctx context.Context, path string, query url.Values, out any) error {
	var err error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			if werr := wait(ctx, c.RetryDelay*time.Duration(attempt)); werr != nil {
				return werr
			}
		}
		var data []byte
		data, _, err = c.do(ctx, request{method: http.MethodGet, path: path, query: query})
		if err == nil {
			return decode(data, out)
		}
		if !IsNetwork(err) {
			return err
		}
	}
	return err
}

func (c *Client) GetRaw(ctx context.Context, path string, query url.Values) ([]byte, string, error) {
	return c.do(ctx, request{method: http.MethodGet, path: path, query: query})
}

func (c *Client) Post(ctx context.Context, path string, body, out any) error {
	return c.sendJSON(ctx, http.MethodPost, path, body, out)
}

func (c *Client) Put(ctx context.Context, path string, body, out any) error {
	return c.sendJSON(ctx, http.MethodPut, path, body, out)
}

func (c *Client) Patch(ctx context.Context, path string, body, out any) error {
	return c.sendJSON(ctx, http.MethodPatch, path, body, out)
}

func (c *Client) Delete(ctx context.Context, path string, out any) error {
	return c.sendJSON(ctx, http.MethodDelete, path, nil, out)
}

func (c *Client) sendJSON(ctx context.Context, method, path string, body, out any) error {
	req := request{method: method, path: path}
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return err
		}
		req.body, req.contentType, req.contentLength = bytes.NewReader(data), "application/json", int64(len(data))
	}
	data, _, err := c.do(ctx, req)
	if err != nil {
		return err
	}
	return decode(data, out)
}

func (c *Client) do(ctx context.Context, r request) ([]byte, string, error) {
	target := c.BaseURL + r.path
	if len(r.query) > 0 {
		target += "?" + r.query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, r.method, target, r.body)
	if err != nil {
		return nil, "", err
	}
	if r.contentLength > 0 {
		req.ContentLength = r.contentLength
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.UserAgent)
	if r.contentType != "" {
		req.Header.Set("Content-Type", r.contentType)
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}
	c.debugf("→ %s %s", r.method, target)
	resp, err := c.httpClient().Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, "", ctx.Err()
		}
		if errors.Is(err, ErrUnsafeRedirect) {
			return nil, "", ErrUnsafeRedirect
		}
		return nil, "", &networkError{err: err}
	}
	defer resp.Body.Close()
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, "", &networkError{err: err}
	}
	c.debugf("← %d %s", resp.StatusCode, target)
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, "", newError(resp.StatusCode, data)
	}
	return data, resp.Header.Get("Content-Type"), nil
}

// ErrUnsafeRedirect: a API mandou seguir para http ou para outro endereço; a
// requisição para ali, e o Authorization não vai junto.
var ErrUnsafeRedirect = errors.New("a API do Jataí redirecionou para um endereço inseguro ou de outro servidor; " +
	"a requisição foi interrompida para proteger sua credencial. Confira JATAI_API_URL")

// httpClient é uma cópia rasa do cliente HTTP com a regra de redirecionamento
// da CLI, valendo também para um cliente injetado (testes, MCP).
func (c *Client) httpClient() *http.Client {
	hc := *c.HTTP
	next := hc.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if err := safeRedirect(req, via); err != nil {
			return err
		}
		if next != nil {
			return next(req, via)
		}
		return nil
	}
	return &hc
}

// safeRedirect só segue para o mesmo host, sem trocar https por http; e no
// máximo 10 vezes, como o padrão do net/http.
func safeRedirect(req *http.Request, via []*http.Request) error {
	first := via[0].URL
	if req.URL.Host != first.Host || (first.Scheme == "https" && req.URL.Scheme != "https") {
		return ErrUnsafeRedirect
	}
	if len(via) >= 10 {
		return errors.New("a API do Jataí redirecionou vezes demais")
	}
	return nil
}

func decode(data []byte, out any) error {
	if out == nil || len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	return json.Unmarshal(data, out)
}

func (c *Client) debugf(format string, args ...any) {
	if c.Debug != nil {
		fmt.Fprintf(c.Debug, "[debug] "+format+"\n", args...)
	}
}

func wait(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type networkError struct{ err error }

func (e *networkError) Error() string { return "falha de conexão com a API: " + e.err.Error() }
func (e *networkError) Unwrap() error { return e.err }

func IsNetwork(err error) bool {
	var n *networkError
	return errors.As(err, &n)
}
