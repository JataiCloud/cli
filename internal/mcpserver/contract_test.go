package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// account.json é a resposta real de GET /api/v1/account, gravada pelos request
// specs da plataforma (ver README). courtesy.app, courtesy.available e
// payment_method podem valer null/false: o contrato confere a chave, não o valor.
func TestAccountResponseCarriesWhatTheMCPReads(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "cli", "testdata", "api", "account.json"))
	if err != nil {
		t.Fatalf("fixture account ausente — regenere com CLI_FIXTURES_DIR (README): %v", err)
	}
	var info accountInfo
	if err := json.Unmarshal(data, &info); err != nil {
		t.Fatalf("fixture account não cabe em accountInfo: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		t.Fatalf("fixture account não é um objeto JSON: %v", err)
	}

	checks := map[string]bool{
		"user.name":          info.User.Name != "",
		"user.email":         info.User.Email != "",
		"team.name":          info.Team.Name != "",
		"team.slug":          info.Team.Slug != "",
		"apps.limit":         info.Apps.Limit > 0,
		"apps.count":         hasKey(raw, "apps", "count"),
		"courtesy.available": hasKey(raw, "courtesy", "available"),
		"courtesy.app":       hasKey(raw, "courtesy", "app"),
		"payment_method":     hasKey(raw, "payment_method"),
	}
	for field, ok := range checks {
		if !ok {
			t.Errorf("resposta real de /account sem o que o MCP lê: %s", field)
		}
	}
}

// hasKey percorre o caminho de chaves e diz se a última existe, mesmo com
// valor nulo ou falso.
func hasKey(m map[string]any, path ...string) bool {
	for i, key := range path {
		v, ok := m[key]
		if !ok {
			return false
		}
		if i == len(path)-1 {
			return true
		}
		if m, ok = v.(map[string]any); !ok {
			return false
		}
	}
	return false
}
