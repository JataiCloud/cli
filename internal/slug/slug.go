// internal/slug/slug.go

// Package slug converte texto livre no identificador que o servidor usa na URL.
//
// O servidor gera o slug com `name.parameterize` e valida com
// /\A[a-z0-9]([a-z0-9\-]*[a-z0-9])?\z/. A transliteração cobre o que os
// clientes usam de fato (português); o resto vira hífen. É só o PALPITE da
// primeira requisição: quem manda no slug é o servidor, e o `jatai up` grava no
// .jatai.yml o slug que ele devolve. Idempotente sobre um slug válido.
package slug

import (
	"regexp"
	"strings"
)

var transliterations = map[rune]string{
	'á': "a", 'à': "a", 'â': "a", 'ã': "a", 'ä': "a", 'å': "a",
	'é': "e", 'è': "e", 'ê': "e", 'ë': "e",
	'í': "i", 'ì': "i", 'î': "i", 'ï': "i",
	'ó': "o", 'ò': "o", 'ô': "o", 'õ': "o", 'ö': "o",
	'ú': "u", 'ù': "u", 'û': "u", 'ü': "u",
	'ç': "c", 'ñ': "n", 'ý': "y",
}

var repeatedHyphens = regexp.MustCompile(`-+`)

// From devolve o slug e false quando não sobra nada utilizável — quem chama
// decide o que dizer. Devolver "" mandaria uma URL vazia para a API.
func From(text string) (string, bool) {
	var b strings.Builder
	for _, r := range strings.ToLower(text) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			b.WriteRune(r)
		default:
			if t, ok := transliterations[r]; ok {
				b.WriteString(t)
			} else {
				b.WriteByte('-')
			}
		}
	}
	s := strings.Trim(repeatedHyphens.ReplaceAllString(b.String(), "-"), "-")
	return s, s != ""
}
