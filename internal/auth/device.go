// Package auth faz o login pelo navegador (RFC 8628): pede um código, a
// pessoa aprova no painel, a CLI recebe um token pessoal.
package auth

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/jataicloud/cli/internal/api"
)

var (
	ErrDenied  = errors.New("Pedido cancelado no navegador. Rode `jatai login` de novo se quiser tentar outra vez.")
	ErrExpired = errors.New("O código expirou antes da aprovação. Rode `jatai login` de novo.")
)

type ClientInfo struct{ Hostname, OS, Arch, Version string }

type Start struct {
	DeviceCode              string `json:"device_code"`
	UserCode                string `json:"user_code"`
	VerificationURI         string `json:"verification_uri"`
	VerificationURIComplete string `json:"verification_uri_complete"`
	ExpiresIn               int    `json:"expires_in"`
	Interval                int    `json:"interval"`
}

type Grant struct {
	Token string `json:"token"`
	User  struct {
		Name  string `json:"name"`
		Email string `json:"email"`
	} `json:"user"`
}

type Poster interface {
	Post(ctx context.Context, path string, body, out any) error
}

func Begin(ctx context.Context, c Poster, info ClientInfo) (Start, error) {
	var s Start
	err := c.Post(ctx, "/api/v1/cli/device_authorizations", map[string]any{"client": map[string]string{
		"hostname": info.Hostname, "os": info.OS, "arch": info.Arch, "version": info.Version,
	}}, &s)
	return s, err
}

// Wait consulta o token no intervalo pedido pelo servidor, somando 5 s a cada
// `slow_down`, até vir o token, a negação ou a expiração.
func Wait(ctx context.Context, c Poster, s Start, sleep func(context.Context, time.Duration) error) (Grant, error) {
	interval := time.Duration(max(s.Interval, 1)) * time.Second
	for {
		if err := sleep(ctx, interval); err != nil {
			return Grant{}, err
		}
		var g Grant
		err := c.Post(ctx, "/api/v1/cli/device_authorizations/token", map[string]string{"device_code": s.DeviceCode}, &g)
		if err == nil {
			return g, nil
		}
		var apiErr *api.Error
		if !errors.As(err, &apiErr) || apiErr.Status != http.StatusBadRequest {
			return Grant{}, err
		}
		switch apiErr.Code() {
		case "authorization_pending":
		case "slow_down":
			interval += 5 * time.Second
		case "access_denied":
			return Grant{}, ErrDenied
		case "expired_token":
			return Grant{}, ErrExpired
		default:
			return Grant{}, err
		}
	}
}
