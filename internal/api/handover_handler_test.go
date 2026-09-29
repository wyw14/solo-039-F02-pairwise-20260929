package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
)

type handoverResponse struct {
	ID         string `json:"id"`
	Status     string `json:"status"`
	Phase      string `json:"phase"`
	FeedSource string `json:"feed_source"`
	Digest     string `json:"digest"`
	Events     []struct {
		Kind string `json:"kind"`
	} `json:"events"`
}

func doHandoverRequest(t *testing.T, s *Server, method, path, body string) (int, handoverResponse) {
	t.Helper()
	req := httptest.NewRequest(method, path, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	s.Handler().ServeHTTP(rec, req)
	var resp handoverResponse
	if rec.Code < 400 && rec.Body.Len() > 0 {
		if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
			t.Fatalf("decode response: %v: %s", err, rec.Body.String())
		}
	}
	return rec.Code, resp
}

func TestHandoverAPI(t *testing.T) {
	defer os.RemoveAll("data")
	s := NewServer()

	start := httptest.NewRequest(http.MethodPost, "/api/campaign/start?grade=baseline&source=primary", nil)
	startRec := httptest.NewRecorder()
	s.Handler().ServeHTTP(startRec, start)
	if startRec.Code != http.StatusAccepted {
		t.Fatalf("start status = %d body=%s", startRec.Code, startRec.Body.String())
	}

	// Missing outgoing operator -> 400.
	if code, _ := doHandoverRequest(t, s, http.MethodPost, "/api/handovers", `{}`); code != http.StatusBadRequest {
		t.Fatalf("create without operator status = %d, want 400", code)
	}

	code, draft := doHandoverRequest(t, s, http.MethodPost, "/api/handovers", `{"outgoing_operator":"alice"}`)
	if code != http.StatusCreated {
		t.Fatalf("create status = %d, want 201", code)
	}
	if draft.Status != "draft" || draft.FeedSource != "primary" {
		t.Fatalf("draft not assembled: %+v", draft)
	}
	if code, _ := doHandoverRequest(t, s, http.MethodPost, "/api/handovers", `{"outgoing_operator":"carol"}`); code != http.StatusConflict {
		t.Fatalf("second draft status = %d, want 409", code)
	}

	code, latest := doHandoverRequest(t, s, http.MethodGet, "/api/handovers/latest", "")
	if code != http.StatusOK || latest.ID != draft.ID {
		t.Fatalf("latest mismatch code=%d latest=%+v draft=%+v", code, latest, draft)
	}

	if code, _ := doHandoverRequest(t, s, http.MethodPost, "/api/handovers/"+draft.ID+"/sign", `{}`); code != http.StatusBadRequest {
		t.Fatalf("sign without successor status = %d, want 400", code)
	}
	code, signed := doHandoverRequest(t, s, http.MethodPost, "/api/handovers/"+draft.ID+"/sign", `{"successor":"bob"}`)
	if code != http.StatusOK || signed.Status != "signed" || signed.Digest == "" {
		t.Fatalf("sign failed code=%d handover=%+v", code, signed)
	}
	if code, _ := doHandoverRequest(t, s, http.MethodPost, "/api/handovers/"+draft.ID+"/sign", `{"successor":"carol"}`); code != http.StatusConflict {
		t.Fatalf("re-sign status = %d, want 409", code)
	}

	// Unknown handover id -> 404.
	if code, _ := doHandoverRequest(t, s, http.MethodPost, "/api/handovers/00000000-0000-0000-0000-000000000000/events", `{"kind":"k","message":"m"}`); code != http.StatusNotFound {
		t.Fatalf("event on unknown handover status = %d, want 404", code)
	}

	// Plant moves on after the handover point.
	code, _ = doHandoverRequest(t, s, http.MethodPost, "/api/campaign/transition", `{"grade":"lighter","source":"secondary"}`)
	if code != http.StatusAccepted {
		t.Fatalf("transition status = %d, want 202", code)
	}

	code, frozen := doHandoverRequest(t, s, http.MethodGet, "/api/handovers/"+draft.ID, "")
	if code != http.StatusOK {
		t.Fatalf("get handover status = %d", code)
	}
	if frozen.Digest != signed.Digest {
		t.Fatal("digest changed after the plant moved on")
	}
	if frozen.FeedSource != "primary" {
		t.Fatalf("signed feed source rewritten to %q", frozen.FeedSource)
	}
	if len(frozen.Events) == 0 {
		t.Fatal("subsequent plant events were not appended to the handover")
	}

	code, noted := doHandoverRequest(t, s, http.MethodPost, "/api/handovers/"+draft.ID+"/events", `{"kind":"note","message":"bob on watch"}`)
	if code != http.StatusCreated || len(noted.Events) != len(frozen.Events)+1 {
		t.Fatalf("append follow-up failed code=%d events=%d want %d", code, len(noted.Events), len(frozen.Events)+1)
	}
	if noted.Digest != signed.Digest {
		t.Fatal("digest changed after appending a follow-up note")
	}

	// Next shift can open a fresh handover once the previous one is signed.
	if code, _ := doHandoverRequest(t, s, http.MethodPost, "/api/handovers", `{"outgoing_operator":"carol"}`); code != http.StatusCreated {
		t.Fatalf("next-shift create status = %d, want 201", code)
	}
}
