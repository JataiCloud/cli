// internal/slug/slug_test.go
package slug

import "testing"

func TestFrom(t *testing.T) {
	cases := []struct {
		in   string
		want string
		ok   bool
	}{
		{"Alves Barbearia", "alves-barbearia", true},
		{"São João do Açaí", "sao-joao-do-acai", true},
		{"  Loja   Nova!! ", "loja-nova", true},
		{"alves-barbearia", "alves-barbearia", true},
		{"API_v2", "api-v2", true},
		{"🙂🙂", "", false},
		{"", "", false},
	}
	for _, c := range cases {
		got, ok := From(c.in)
		if got != c.want || ok != c.ok {
			t.Errorf("From(%q) = (%q, %v), want (%q, %v)", c.in, got, ok, c.want, c.ok)
		}
	}
}
