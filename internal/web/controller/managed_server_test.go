package controller

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/op/go-logging"

	"github.com/mhsanaei/3x-ui/v3/internal/database"
	"github.com/mhsanaei/3x-ui/v3/internal/database/model"
	xuilogger "github.com/mhsanaei/3x-ui/v3/internal/logger"
	"github.com/mhsanaei/3x-ui/v3/internal/util/crypto"
	"github.com/mhsanaei/3x-ui/v3/internal/web/service"
)

// TestAddBatchBindsJSONCredentials guards the bug where addBatch bound the
// request straight onto model.ManagedServer, whose credential fields are
// json:"-" — so every pasted password was silently dropped and the row failed
// with "ssh password is required". The batch add must read the JSON password.
func TestAddBatchBindsJSONCredentials(t *testing.T) {
	t.Setenv("XUI_SECRET_KEY", "test-key")
	xuilogger.InitLogger(logging.ERROR)
	gin.SetMode(gin.TestMode)
	dbDir := t.TempDir()
	t.Setenv("XUI_DB_FOLDER", dbDir)
	if err := database.InitDB(filepath.Join(dbDir, "x-ui.db")); err != nil {
		t.Fatalf("InitDB: %v", err)
	}
	t.Cleanup(func() { _ = database.CloseDB() })

	engine := gin.New()
	NewManagedServerController(engine.Group("/managedServers"))

	body := map[string]any{
		// verify=false: no reachable host in a unit test; this asserts the JSON
		// credential binding + storage, not connectivity.
		"verify": false,
		"servers": []map[string]any{
			{"name": "", "address": "203.0.113.5", "sshPort": 22, "sshUser": "root", "sshAuthType": "password", "sshPassword": "secret", "sshHostKeyMode": "trust"},
		},
	}
	b, _ := json.Marshal(body)
	req := httptest.NewRequest(http.MethodPost, "/managedServers/addBatch", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	engine.ServeHTTP(w, req)
	if w.Code != http.StatusOK {
		t.Fatalf("status %d, body=%s", w.Code, w.Body.String())
	}

	var env struct {
		Success bool `json:"success"`
		Obj     struct {
			Results []service.BulkAddResult `json:"results"`
		} `json:"obj"`
	}
	if err := json.Unmarshal(w.Body.Bytes(), &env); err != nil {
		t.Fatalf("decode: %v body=%s", err, w.Body.String())
	}
	if len(env.Obj.Results) != 1 || !env.Obj.Results[0].Success {
		t.Fatalf("results = %+v, want one success (password bound from JSON)", env.Obj.Results)
	}

	// The stored password must be the encrypted form of what we sent, proving
	// the JSON password reached the model and was not dropped.
	var stored model.ManagedServer
	if err := database.GetDB().Where("address = ?", "203.0.113.5").First(&stored).Error; err != nil {
		t.Fatalf("load stored server: %v", err)
	}
	if stored.Name != "203.0.113.5" {
		t.Fatalf("empty name did not default to address: %q", stored.Name)
	}
	pw, err := crypto.DecryptSecret(stored.SshPassword)
	if err != nil || pw != "secret" {
		t.Fatalf("decrypted password = (%q, %v), want secret", pw, err)
	}
}
