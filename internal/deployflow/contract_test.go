package deployflow

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// deploy_log.json é a resposta real de GET /api/v1/apps/:slug/deploys/:id/log,
// gravada pelos request specs da plataforma (ver README).
func TestDeployLogResponseCarriesWhatTheFlowReads(t *testing.T) {
	data, err := os.ReadFile(filepath.Join("..", "cli", "testdata", "api", "deploy_log.json"))
	if err != nil {
		t.Fatalf("fixture deploy_log ausente — regenere com CLI_FIXTURES_DIR (README): %v", err)
	}
	var body struct {
		DeployID  int    `json:"deploy_id"`
		Status    string `json:"status"`
		LineCount int    `json:"line_count"`
		Log       string `json:"log"`
	}
	if err := json.Unmarshal(data, &body); err != nil {
		t.Fatalf("fixture deploy_log não cabe nos tipos do fluxo: %v", err)
	}
	var keys map[string]any
	if err := json.Unmarshal(data, &keys); err != nil {
		t.Fatal(err)
	}

	checks := map[string]bool{
		"deploy_id":  body.DeployID != 0,
		"status":     body.Status != "",
		"line_count": body.LineCount > 0,
		"log":        body.Log != "",
	}
	for field, ok := range checks {
		if _, present := keys[field]; !ok || !present {
			t.Errorf("resposta real de /log sem o que o fluxo lê: %s", field)
		}
	}
}
