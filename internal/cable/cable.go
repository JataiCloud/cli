// Package cable fala o protocolo do ActionCable (JSON sobre WebSocket) para
// acompanhar provisionamento, deploy e run ao vivo.
package cable

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"
)

// ErrConnection: o WebSocket não serve — quem chama cai para o polling.
var ErrConnection = errors.New("WebSocket indisponível")

// connectionError descreve cada falha; errors.Is(err, ErrConnection) vale
// para todas, e a mensagem não repete "WebSocket indisponível".
type connectionError struct{ msg string }

func (e *connectionError) Error() string        { return e.msg }
func (e *connectionError) Is(target error) bool { return target == ErrConnection }

func connErr(format string, args ...any) error {
	return &connectionError{msg: fmt.Sprintf(format, args...)}
}

type Options struct {
	APIURL          string
	Token           string
	UserAgent       string
	WelcomeTimeout  time.Duration
	ReconnectDelays []time.Duration
	Debug           io.Writer // JATAI_DEBUG=1; o token só vai no cabeçalho, nunca num frame
}

type Message struct {
	Status string `json:"status"`
	Line   string `json:"line"`
}

type frame struct {
	Type       string          `json:"type"`
	Identifier string          `json:"identifier"`
	Message    json.RawMessage `json:"message"`
}

type event struct {
	identifier   string
	message      Message
	disconnected bool
	rejected     bool
}

// Conn é uma conexão ActionCable. Toda goroutine que ela inicia (uma readLoop
// por WebSocket aberto) termina em Close() ou quando o contexto do Connect é
// cancelado: o contexto da conexão vive em c.ctx, independente do contexto de
// cada WaitFor, para que cancelar uma espera não derrube a conexão.
type Conn struct {
	opts    Options
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	debugMu sync.Mutex
	mu      sync.Mutex // protege ws, subs e closed
	ws      *websocket.Conn
	subs    []string
	closed  bool
	events  chan event
	done    chan struct{}
}

func Connect(ctx context.Context, opts Options) (*Conn, error) {
	if opts.WelcomeTimeout == 0 {
		opts.WelcomeTimeout = 10 * time.Second
	}
	if opts.ReconnectDelays == nil {
		opts.ReconnectDelays = []time.Duration{time.Second, 3 * time.Second, 10 * time.Second}
	}
	c := &Conn{opts: opts, events: make(chan event, 64), done: make(chan struct{})}
	c.ctx, c.cancel = context.WithCancel(ctx)
	if err := c.dial(ctx); err != nil {
		c.cancel()
		return nil, err
	}
	return c, nil
}

// Identifier monta o identificador do canal, como o cliente JS do Rails.
func Identifier(channel string, params map[string]any) string {
	m := map[string]any{"channel": channel}
	for k, v := range params {
		m[k] = v
	}
	data, _ := json.Marshal(m)
	return string(data)
}

func endpoints(apiURL string) (string, string, error) {
	u, err := url.Parse(apiURL)
	if err != nil || u.Host == "" {
		return "", "", connErr("endereço inválido %q", apiURL)
	}
	scheme := "wss"
	if u.Scheme == "http" {
		scheme = "ws"
	}
	return scheme + "://" + u.Host + strings.TrimRight(u.Path, "/") + "/cable", u.Scheme + "://" + u.Host, nil
}

func (c *Conn) dial(ctx context.Context) error {
	wsURL, origin, err := endpoints(c.opts.APIURL)
	if err != nil {
		return err
	}
	dctx, cancel := context.WithTimeout(ctx, c.opts.WelcomeTimeout)
	defer cancel()
	defer context.AfterFunc(c.ctx, cancel)() // Close() interrompe um handshake em andamento
	ws, _, err := websocket.Dial(dctx, wsURL, &websocket.DialOptions{
		HTTPHeader: http.Header{
			"Authorization": {"Bearer " + c.opts.Token}, "Origin": {origin}, "User-Agent": {c.opts.UserAgent},
		},
		Subprotocols: []string{"actioncable-v1-json"},
	})
	if err != nil {
		return connErr("não conectou em %s (%v)", wsURL, err)
	}
	ws.SetReadLimit(1 << 20)
	if err := c.awaitWelcome(dctx, ws); err != nil {
		_ = ws.CloseNow()
		return err
	}
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		_ = ws.CloseNow()
		return c.closedErr()
	}
	c.ws = ws
	c.wg.Add(1)
	c.mu.Unlock()
	go c.readLoop(ws)
	return nil
}

func (c *Conn) closedErr() error { return connErr("conexão encerrada") }

func (c *Conn) awaitWelcome(ctx context.Context, ws *websocket.Conn) error {
	for {
		_, data, err := ws.Read(ctx)
		if err != nil {
			return connErr("sem boas-vindas do servidor (%v)", err)
		}
		c.debugf("← %s", data)
		var f frame
		if json.Unmarshal(data, &f) == nil {
			switch f.Type {
			case "welcome":
				return nil
			case "disconnect":
				return connErr("servidor recusou a conexão")
			}
		}
	}
}

func (c *Conn) readLoop(ws *websocket.Conn) {
	defer c.wg.Done()
	defer ws.CloseNow() //nolint:errcheck // já pode estar fechado
	for {
		_, data, err := ws.Read(c.ctx)
		if err != nil {
			c.emit(event{disconnected: true})
			return
		}
		var f frame
		if json.Unmarshal(data, &f) != nil {
			continue
		}
		if f.Type != "ping" {
			c.debugf("← %s", data)
		}
		switch f.Type {
		case "ping", "welcome", "confirm_subscription":
			continue
		case "reject_subscription":
			c.emit(event{rejected: true, identifier: f.Identifier})
			continue
		case "disconnect":
			c.emit(event{disconnected: true})
			return
		}
		var m Message
		if f.Identifier == "" || len(f.Message) == 0 || json.Unmarshal(f.Message, &m) != nil {
			continue
		}
		c.emit(event{identifier: f.Identifier, message: m})
	}
}

func (c *Conn) emit(e event) {
	select {
	case c.events <- e:
	case <-c.ctx.Done():
	}
}

func (c *Conn) debugf(format string, args ...any) {
	if c.opts.Debug != nil {
		c.debugMu.Lock()
		defer c.debugMu.Unlock()
		fmt.Fprintf(c.opts.Debug, "[debug] ws "+format+"\n", args...)
	}
}

func (c *Conn) send(ctx context.Context, command, identifier string) error {
	data, _ := json.Marshal(map[string]string{"command": command, "identifier": identifier})
	c.debugf("→ %s", data)
	c.mu.Lock()
	ws, closed := c.ws, c.closed
	c.mu.Unlock()
	if closed {
		return c.closedErr()
	}
	return ws.Write(ctx, websocket.MessageText, data)
}

func (c *Conn) Subscribe(ctx context.Context, channel string, params map[string]any) (string, error) {
	id := Identifier(channel, params)
	c.mu.Lock()
	if !slices.Contains(c.subs, id) {
		c.subs = append(c.subs, id)
	}
	c.mu.Unlock()
	if err := c.send(ctx, "subscribe", id); err != nil {
		return "", connErr("falha ao assinar %s (%v)", channel, err)
	}
	return id, nil
}

// WaitFor assina o canal e espera um dos status de `until`. Linhas com `line`
// de qualquer assinatura vão para onLine (os logs de build, por exemplo).
func (c *Conn) WaitFor(ctx context.Context, channel string, params map[string]any, until []string, onLine func(string)) (string, error) {
	id, err := c.Subscribe(ctx, channel, params)
	if err != nil {
		return "", err
	}
	for {
		select {
		case <-ctx.Done():
			return "", ctx.Err()
		case <-c.ctx.Done():
			return "", c.stoppedErr()
		case e := <-c.events:
			switch {
			case e.disconnected:
				if err := c.reconnect(ctx); err != nil {
					return "", err
				}
			case e.rejected:
				return "", connErr("inscrição recusada em %s", channel)
			default:
				if e.message.Line != "" && onLine != nil {
					onLine(e.message.Line)
				}
				if e.identifier == id && slices.Contains(until, e.message.Status) {
					return e.message.Status, nil
				}
			}
		}
	}
}

func (c *Conn) reconnect(ctx context.Context) error {
	for _, delay := range c.opts.ReconnectDelays {
		t := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-c.ctx.Done():
			t.Stop()
			return c.stoppedErr()
		case <-t.C:
		}
		if err := c.dial(ctx); err != nil {
			if c.ctx.Err() != nil {
				return c.stoppedErr()
			}
			continue
		}
		c.mu.Lock()
		subs := append([]string{}, c.subs...)
		c.mu.Unlock()
		for _, id := range subs {
			if err := c.send(ctx, "subscribe", id); err != nil {
				break
			}
		}
		return nil
	}
	return connErr("falha na reconexão após %d tentativas", len(c.opts.ReconnectDelays))
}

// stoppedErr explica por que a conexão parou: Close() é um erro de conexão
// (quem chama cai no polling); cancelar o contexto do Connect devolve o erro
// do próprio contexto.
func (c *Conn) stoppedErr() error {
	select {
	case <-c.done:
		return c.closedErr()
	default:
		return c.ctx.Err()
	}
}

// Close encerra a conexão e espera as goroutines terminarem. É idempotente e
// pode ser chamado de qualquer goroutine, inclusive durante uma reconexão.
func (c *Conn) Close() {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	close(c.done)
	ws := c.ws
	c.mu.Unlock()
	if ws != nil {
		_ = ws.Close(websocket.StatusNormalClosure, "")
	}
	c.cancel()
	c.wg.Wait()
}
