package deployflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// deploy_log.json é a resposta real de GET /api/v1/apps/:slug/deploys/:id/log,
// gravada pelos request specs da plataforma (ver README). O fluxo só lê log;
// deploy_id, status e line_count são conferidos pela chave, porque um log
// vazio é uma resposta legítima.
func TestDeployLogResponseCarriesWhatTheFlowReads(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "cli", "testdata", "api", "deploy_log.json"))
	if err != nil {
		t.Fatalf("fixture deploy_log ausente — regenere com CLI_FIXTURES_DIR (README): %v", err)
	}
	var body buildLogResponse
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("fixture deploy_log não cabe em buildLogResponse: %v", err)
	}
	if body.Log == "" {
		t.Error("resposta real de /log sem o texto do log")
	}
	var keys map[string]any
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatalf("fixture deploy_log não é um objeto JSON: %v", err)
	}
	for _, key := range []string{"deploy_id", "status", "line_count", "log"} {
		if _, ok := keys[key]; !ok {
			t.Errorf("resposta real de /log sem a chave %q", key)
		}
	}
}
