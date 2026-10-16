package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Error é uma resposta não-2xx da API, já com a mensagem para a pessoa.
type Error struct {
	Status  int
	Message string
	Body    map[string]any
}

func (e *Error) Error() string { return e.Message }

// Code é o campo `error` do corpo (ex.: "authorization_pending").
func (e *Error) Code() string {
	s, _ := e.Body["error"].(string)
	return s
}

func IsStatus(err error, status int) bool {
	var e *Error
	return errors.As(err, &e) && e.Status == status
}

func newError(status int, data []byte) *Error {
	var body map[string]any
	_ = json.Unmarshal(data, &body)
	e := &Error{Status: status, Body: body}
	fromBody := func(fallback string) string {
		if s, ok := body["error"].(string); ok && s != "" {
			return s
		}
		return fallback
	}
	switch status {
	case 401:
		e.Message = "Sua sessão expirou. Rode `jatai login`."
	case 403:
		e.Message = fromBody("Sem permissão para esta ação")
	case 404:
		e.Message = "Recurso não encontrado."
	case 409:
		e.Message = fromBody("Conflito — recurso já existe ou operação em andamento")
	case 422:
		e.Message = validationMessage(body)
	case 426:
		e.Message = fromBody("Sua CLI está desatualizada.")
	default:
		e.Message = fromBody(fmt.Sprintf("Erro na API (%d)", status))
	}
	return e
}

func validationMessage(body map[string]any) string {
	if list, ok := body["errors"].([]any); ok && len(list) > 0 {
		parts := make([]string, 0, len(list))
		for _, item := range list {
			parts = append(parts, fmt.Sprint(item))
		}
		return strings.Join(parts, ", ")
	}
	if s, ok := body["error"].(string); ok && s != "" {
		return s
	}
	return "Dados inválidos"
}
