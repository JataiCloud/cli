package api

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/textproto"
	"os"
	"path/filepath"
	"slices"
	"strings"
)

// Upload envia o pacote como multipart (campo `source`, application/gzip) com
// Content-Length conhecido — sem carregar os até 500 MB na memória.
func (c *Client) Upload(ctx context.Context, path, filePath string, fields map[string]string, out any) error {
	file, err := os.Open(filePath)
	if err != nil {
		return err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return err
	}

	var head bytes.Buffer
	mw := multipart.NewWriter(&head)
	keys := make([]string, 0, len(fields))
	for k := range fields {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	for _, k := range keys {
		if err := mw.WriteField(k, fields[k]); err != nil {
			return err
		}
	}
	part := textproto.MIMEHeader{}
	part.Set("Content-Disposition", fmt.Sprintf(`form-data; name="source"; filename=%q`, filepath.Base(filePath)))
	part.Set("Content-Type", "application/gzip")
	if _, err := mw.CreatePart(part); err != nil {
		return err
	}
	tail := fmt.Sprintf("\r\n--%s--\r\n", mw.Boundary())

	data, _, err := c.do(ctx, request{
		method: http.MethodPost, path: path,
		body:          io.MultiReader(&head, file, strings.NewReader(tail)),
		contentType:   mw.FormDataContentType(),
		contentLength: int64(head.Len()) + info.Size() + int64(len(tail)),
	})
	if err != nil {
		return err
	}
	return decode(data, out)
}
