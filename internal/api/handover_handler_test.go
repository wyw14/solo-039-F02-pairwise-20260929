package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func createHandoverRequest(t *testing.T, server *Server) map[string]any {
	t.Helper()
	body := bytes.NewBufferString(`{"operator":"alice"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/handovers", body)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusCreated {
		t.Fatalf("create handover status = %d body=%s", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode created: %v", err)
	}
	return created
}

func TestHandoverAPI(t *testing.T) {
	server := NewServer()

	created := createHandoverRequest(t, server)
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("created handover missing id")
	}

	// 列表能查到新建的未签收交接单。
	req := httptest.NewRequest(http.MethodGet, "/api/handovers", nil)
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), id) {
		t.Fatalf("list handovers failed: %d %s", rec.Code, rec.Body.String())
	}

	// 接班人签收，交接单冻结。
	sign := bytes.NewBufferString(`{"receiver":"bob"}`)
	req = httptest.NewRequest(http.MethodPost, "/api/handovers/"+id+"/sign", sign)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusAccepted {
		t.Fatalf("sign status = %d body=%s", rec.Code, rec.Body.String())
	}
	var signed map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &signed); err != nil {
		t.Fatalf("decode signed: %v", err)
	}
	if frozen, _ := signed["frozen"].(bool); !frozen {
		t.Fatalf("signed handover not frozen: %s", rec.Body.String())
	}

	// 重复签收必须冲突。
	req = httptest.NewRequest(http.MethodPost, "/api/handovers/"+id+"/sign", bytes.NewBufferString(`{"receiver":"carol"}`))
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusConflict {
		t.Fatalf("second sign status = %d, want 409", rec.Code)
	}

	// 单个查询能取到已冻结交接单。
	req = httptest.NewRequest(http.MethodGet, "/api/handovers/"+id, nil)
	rec = httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"frozen":true`) {
		t.Fatalf("get handover failed: %d %s", rec.Code, rec.Body.String())
	}
}

func TestHandoverAPIRejectsBadID(t *testing.T) {
	server := NewServer()
	req := httptest.NewRequest(http.MethodPost, "/api/handovers/not-a-uuid/sign", bytes.NewBufferString(`{}`))
	rec := httptest.NewRecorder()
	server.Handler().ServeHTTP(rec, req)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rec.Code)
	}
}
