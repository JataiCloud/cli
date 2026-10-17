package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/jataicloud/cli/internal/auth"
)

// Os arquivos em testdata/api são respostas reais da API, gravadas pelos
// request specs da plataforma (spec/support/cli_fixtures.rb; ver README).
func fixture(t *testing.T, name string, out any) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("testdata", "api", name+".json"))
	if err != nil {
		t.Fatalf("fixture %s ausente — regenere com CLI_FIXTURES_DIR (README): %v", name, err)
	}
	if err := json.Unmarshal(data, out); err != nil {
		t.Fatalf("fixture %s não cabe nos tipos da CLI: %v", name, err)
	}
}

func TestServerResponsesCarryWhatTheCLIReads(t *testing.T) {
	var app, created appInfo
	fixture(t, "app", &app)
	fixture(t, "app_created", &created)
	var apps []appInfo
	fixture(t, "apps", &apps)
	var deploy, deployCreated deployInfo
	fixture(t, "deploy", &deploy)
	fixture(t, "deploy_created", &deployCreated)
	var env map[string]string
	fixture(t, "env", &env)
	var logs, found []logEntry
	fixture(t, "logs", &logs)
	fixture(t, "logs_search", &found)
	var start auth.Start
	fixture(t, "device_authorization", &start)
	var grant auth.Grant
	fixture(t, "device_token", &grant)

	checks := map[string]bool{
		"app: id, slug, status, url":         app.ID != 0 && app.Slug != "" && app.Status != "" && app.URL != "",
		"app_created: id, slug, status":      created.ID != 0 && created.Slug != "" && created.Status != "",
		"apps: id, slug, status":             len(apps) > 0 && apps[0].ID != 0 && apps[0].Slug != "" && apps[0].Status != "",
		"deploy: id, status, commit_sha":     deploy.ID != 0 && deploy.Status != "" && deploy.CommitSHA != "",
		"deploy: process_failures":           len(deploy.ProcessFailures) > 0 && deploy.ProcessFailures[0].Name != "" && deploy.ProcessFailures[0].Reason != "",
		"deploy_created: id, status":         deployCreated.ID != 0 && deployCreated.Status != "",
		"env: chave e valor":                 len(env) > 0,
		"logs: timestamp, source, message":   len(logs) > 0 && logs[0].Timestamp != "" && logs[0].Source != "" && logs[0].Message != "",
		"logs_search: timestamp, level, msg": len(found) > 0 && found[0].Timestamp != "" && found[0].Level != "" && found[0].Message != "",
		"device_authorization: códigos":      start.DeviceCode != "" && start.UserCode != "" && start.VerificationURI != "" && start.VerificationURIComplete != "" && start.ExpiresIn > 0 && start.Interval > 0,
		"device_token: token, user":          grant.Token != "" && grant.User.Name != "" && grant.User.Email != "",
	}
	for field, ok := range checks {
		if !ok {
			t.Errorf("resposta real sem o que a CLI lê: %s", field)
		}
	}
}

// Campos que o servidor pode mandar nulos (last_deploy, process) ou que a CLI
// lê como lista: o contrato confere que a chave existe na resposta real.
func TestServerResponsesKeepTheKeysTheCLIReads(t *testing.T) {
	var app map[string]any
	fixture(t, "app", &app)
	var deploy map[string]any
	fixture(t, "deploy", &deploy)
	var logs []map[string]any
	fixture(t, "logs", &logs)

	required := map[string][]string{
		"app":    {"last_deploy", "database_plan", "subdomain", "stack", "region", "preset", "name"},
		"deploy": {"created_at", "process_failures"},
	}
	for name, keys := range required {
		obj := map[string]map[string]any{"app": app, "deploy": deploy}[name]
		for _, key := range keys {
			if _, ok := obj[key]; !ok {
				t.Errorf("%s: a resposta real não traz a chave %q", name, key)
			}
		}
	}
	if len(logs) == 0 {
		t.Fatal("logs: fixture sem entradas")
	}
	if _, ok := logs[0]["level"]; !ok {
		t.Error("logs: a resposta real não traz a chave \"level\"")
	}
}
