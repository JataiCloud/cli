package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

func newTestClient(t *testing.T, h http.HandlerFunc) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	c, err := New(srv.URL, "jat_segredo", "jatai-cli/2.0.0 (test; test)")
	if err != nil {
		t.Fatal(err)
	}
	c.RetryDelay = 0
	return c
}

func TestNewRequiresHTTPSExceptLocalhost(t *testing.T) {
	if _, err := New("http://api.jataicloud.com.br", "", "ua"); err == nil {
		t.Fatal("expected http to be refused")
	}
	for _, ok := range []string{"https://api.jataicloud.com.br", "http://localhost:3000", "http://127.0.0.1:3000", "http://[::1]:3000"} {
		if _, err := New(ok, "", "ua"); err != nil {
			t.Fatalf("%s: %v", ok, err)
		}
	}
}

func TestSendsIdentityAndDecodesJSON(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer jat_segredo" || !strings.HasPrefix(r.UserAgent(), "jatai-cli/2.0.0") {
			t.Errorf("headers = %v", r.Header)
		}
		w.Write([]byte(`{"slug":"loja"}`))
	})
	var out struct{ Slug string }
	if err := c.Get(context.Background(), "/api/v1/apps/loja", nil, &out); err != nil || out.Slug != "loja" {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestErrorMessages(t *testing.T) {
	cases := []struct {
		status int
		body   string
		want   string
	}{
		{401, `{"error":"invalid_token"}`, "Sua sessão expirou. Rode `jatai login`."},
		{403, `{"error":"Só admin do time"}`, "Só admin do time"},
		{404, `{}`, "Recurso não encontrado."},
		{409, `{}`, "Conflito — recurso já existe ou operação em andamento"},
		{422, `{"errors":["Nome em uso","Stack inválida"]}`, "Nome em uso, Stack inválida"},
		{426, `{"error":"Sua CLI está desatualizada: a versão mínima é 2.1.0."}`, "Sua CLI está desatualizada: a versão mínima é 2.1.0."},
		{502, `<html>Bad Gateway</html>`, "Erro na API (502)"},
	}
	for _, tc := range cases {
		c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(tc.status)
			w.Write([]byte(tc.body))
		})
		err := c.Post(context.Background(), "/x", nil, nil)
		var apiErr *Error
		if !errors.As(err, &apiErr) || apiErr.Message != tc.want || !IsStatus(err, tc.status) {
			t.Errorf("status %d: err = %v", tc.status, err)
		}
	}
}

func dropConnections(n int32, calls *int32) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		if atomic.AddInt32(calls, 1) <= n {
			conn, _, _ := w.(http.Hijacker).Hijack()
			conn.Close()
			return
		}
		w.Write([]byte(`{}`))
	}
}

func TestGetRetriesNetworkFailures(t *testing.T) {
	var calls int32
	c := newTestClient(t, dropConnections(2, &calls))
	if err := c.Get(context.Background(), "/x", nil, nil); err != nil {
		t.Fatalf("err = %v", err)
	}
	if n := atomic.LoadInt32(&calls); n != 3 {
		t.Fatalf("calls = %d", n)
	}
}

func TestWritesDoNotRetry(t *testing.T) {
	var calls int32
	c := newTestClient(t, dropConnections(1, &calls))
	err := c.Post(context.Background(), "/x", map[string]string{"a": "b"}, nil)
	if n := atomic.LoadInt32(&calls); !IsNetwork(err) || n != 1 {
		t.Fatalf("err = %v, calls = %d", err, n)
	}
}

func TestUploadSendsMultipartWithLength(t *testing.T) {
	archive := filepath.Join(t.TempDir(), "source.tar.gz")
	os.WriteFile(archive, []byte("conteudo-do-pacote"), 0o600)

	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.ContentLength <= 0 || len(r.TransferEncoding) > 0 {
			t.Errorf("content-length = %d, transfer-encoding = %v", r.ContentLength, r.TransferEncoding)
		}
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Fatal(err)
		}
		file, header, err := r.FormFile("source")
		if err != nil {
			t.Fatal(err)
		}
		data, _ := io.ReadAll(file)
		if r.FormValue("branch") != "main" || string(data) != "conteudo-do-pacote" ||
			header.Header.Get("Content-Type") != "application/gzip" {
			t.Errorf("branch=%q data=%q type=%q", r.FormValue("branch"), data, header.Header.Get("Content-Type"))
		}
		w.WriteHeader(http.StatusCreated)
		w.Write([]byte(`{"id":42}`))
	})
	var out struct{ ID int }
	if err := c.Upload(context.Background(), "/deploys", archive, map[string]string{"branch": "main"}, &out); err != nil || out.ID != 42 {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}

func TestGetRawReturnsBodyAndContentType(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/csv")
		w.Write([]byte("a,b\n1,2\n"))
	})
	data, ct, err := c.GetRaw(context.Background(), "/export", nil)
	if err != nil || string(data) != "a,b\n1,2\n" || ct != "text/csv" {
		t.Fatalf("data=%q ct=%q err=%v", data, ct, err)
	}
}

func TestDebugNeverShowsTheToken(t *testing.T) {
	var debug bytes.Buffer
	c := newTestClient(t, func(w http.ResponseWriter, _ *http.Request) { w.Write([]byte(`{}`)) })
	c.Debug = &debug
	c.Get(context.Background(), "/x", nil, nil)
	if debug.Len() == 0 || strings.Contains(debug.String(), "jat_segredo") {
		t.Fatalf("debug = %q", debug.String())
	}
}

func TestRedirectToPlainHTTPIsRefusedAndTheTokenNeverFollows(t *testing.T) {
	var plainHits atomic.Int32
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		plainHits.Add(1)
		w.Write([]byte(`{}`))
	}))
	t.Cleanup(plain.Close)
	secure := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+r.URL.Path, http.StatusFound)
	}))
	t.Cleanup(secure.Close)
	c, err := New(secure.URL, "jat_segredo", "ua")
	if err != nil {
		t.Fatal(err)
	}
	c.HTTP, c.RetryDelay = secure.Client(), 0

	err = c.Get(context.Background(), "/api/v1/account", nil, nil)

	if !errors.Is(err, ErrUnsafeRedirect) || IsNetwork(err) {
		t.Fatalf("err = %v", err)
	}
	if plainHits.Load() != 0 {
		t.Fatalf("followed the redirect %d times", plainHits.Load())
	}
}

func TestRedirectToAnotherHostIsRefused(t *testing.T) {
	var otherHits atomic.Int32
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		otherHits.Add(1)
	}))
	t.Cleanup(other.Close)
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/roubo", http.StatusTemporaryRedirect)
	})

	err := c.Post(context.Background(), "/api/v1/apps", map[string]any{"a": 1}, nil)

	if !errors.Is(err, ErrUnsafeRedirect) || otherHits.Load() != 0 {
		t.Fatalf("err = %v, hits = %d", err, otherHits.Load())
	}
}

func TestRedirectWithinTheSameHostIsFollowed(t *testing.T) {
	c := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/velho" {
			http.Redirect(w, r, "/novo", http.StatusMovedPermanently)
			return
		}
		if r.Header.Get("Authorization") != "Bearer jat_segredo" {
			t.Errorf("Authorization = %q", r.Header.Get("Authorization"))
		}
		w.Write([]byte(`{"slug":"loja"}`))
	})
	var out struct{ Slug string }
	if err := c.Get(context.Background(), "/velho", nil, &out); err != nil || out.Slug != "loja" {
		t.Fatalf("out = %+v, err = %v", out, err)
	}
}
