package service

import (
	"context"
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
	"testing"
	"time"
)

func mustUUID(t *testing.T) uuid.UUID {
	t.Helper()
	value, err := uuid.NewRandom()
	if err != nil {
		t.Fatalf("uuid: %v", err)
	}
	return value
}

func newTestPlant(t *testing.T) *Plant {
	t.Helper()
	return NewPlant(t.TempDir())
}

func startedPlant(t *testing.T) *Plant {
	t.Helper()
	p := newTestPlant(t)
	if _, err := p.Start(context.Background(), "baseline", "primary"); err != nil {
		t.Fatalf("start: %v", err)
	}
	return p
}

func TestBeginHandoverSnapshotsCurrentCampaign(t *testing.T) {
	p := startedPlant(t)

	h := p.BeginHandover("alice")
	current := p.Campaign.Snapshot()

	if h.CampaignID != current.ID || h.Revision != current.Revision {
		t.Fatalf("handover not tied to current campaign: %+v vs %+v", h.CampaignID, current.ID)
	}
	if h.Phase != model.Stabilizing {
		t.Fatalf("phase = %s, want stabilizing", h.Phase)
	}
	if h.CreatedBy != "alice" || h.Frozen {
		t.Fatalf("handover should start unsigned: %+v", h)
	}
	if h.Feed.Source != "primary" || !h.Feed.Open || h.Feed.Flow != 1 {
		t.Fatalf("feed snapshot wrong: %+v", h.Feed)
	}
	equipment := map[string]EquipmentHandover{}
	for _, e := range h.Equipment {
		equipment[e.Name] = e
	}
	for _, name := range []string{"column", "reboiler", "reflux", "analyzer", "relief", "product-route"} {
		if _, ok := equipment[name]; !ok {
			t.Fatalf("equipment %s missing from handover", name)
		}
	}
	if !equipment["column"].Running {
		t.Fatalf("column should be running in stabilizing phase")
	}
}

func TestHandoverListsPendingActions(t *testing.T) {
	p := startedPlant(t)
	p.Incidents.Open("pump-vibration", "reflux pump vibration high", p.Campaign.Snapshot().Revision.String())

	h := p.BeginHandover("")
	kinds := map[string]bool{}
	for _, item := range h.Pending {
		kinds[item.Kind] = true
	}
	if !kinds["incident"] {
		t.Fatalf("open incident missing from pending: %+v", h.Pending)
	}
	// 启动后尚未采满三个样本，成分证据应当是未结事项。
	if !kinds["evidence"] {
		t.Fatalf("incomplete evidence missing from pending: %+v", h.Pending)
	}
	if h.CreatedBy != "outgoing-operator" {
		t.Fatalf("default outgoing operator not applied: %q", h.CreatedBy)
	}
}

func TestSignHandoverFreezesSnapshot(t *testing.T) {
	p := startedPlant(t)
	h := p.BeginHandover("alice")

	signed, err := p.SignHandover(h.ID, "bob")
	if err != nil {
		t.Fatalf("sign: %v", err)
	}
	if !signed.Frozen || signed.SignedBy != "bob" || signed.SignedAt == nil {
		t.Fatalf("handover not frozen by receiver: %+v", signed)
	}
	if _, err := p.SignHandover(h.ID, "carol"); err == nil {
		t.Fatal("second signing must be rejected")
	}
	if _, ok := p.Handovers.Get(h.ID); !ok {
		t.Fatal("signed handover disappeared from book")
	}
}

func TestSignedHandoverRecordsFollowupsWithoutMutation(t *testing.T) {
	p := startedPlant(t)
	h := p.BeginHandover("alice")
	if _, err := p.SignHandover(h.ID, "bob"); err != nil {
		t.Fatalf("sign: %v", err)
	}

	before := p.Handovers.items[h.ID]

	// 签收之后批次继续推进：阶段与进料源都发生变化。
	if _, err := p.Transition(context.Background(), "premium", "secondary"); err != nil {
		t.Fatalf("transition: %v", err)
	}

	after, ok := p.Handovers.Get(h.ID)
	if !ok {
		t.Fatal("handover lost after transition")
	}
	if !after.Frozen || after.SignedBy != "bob" {
		t.Fatalf("sign-off metadata changed: %+v", after)
	}
	// 已签收的快照内容必须停留在交接时点。
	if after.Phase != model.Stabilizing || after.Phase == model.Transitioning {
		t.Fatalf("frozen phase mutated: %s", after.Phase)
	}
	if after.Feed.Source != "primary" || after.Feed.PreviousSource != "" {
		t.Fatalf("frozen feed snapshot mutated: %+v", after.Feed)
	}
	if after.Epoch != before.Epoch || after.Revision != before.Revision {
		t.Fatalf("frozen campaign coordinates mutated: %+v", after)
	}
	if len(after.Pending) != len(before.Pending) {
		t.Fatalf("frozen pending list mutated: %d -> %d", len(before.Pending), len(after.Pending))
	}

	// 后续事件只能出现在独立的附录里。
	var sawTransition bool
	for _, event := range after.Followups {
		if event.Kind == "campaign.transition" {
			sawTransition = true
			if event.Revision == before.Revision {
				t.Fatal("followup carries the pre-handover revision")
			}
		}
		if event.At.IsZero() {
			t.Fatal("followup event missing timestamp")
		}
	}
	if !sawTransition {
		t.Fatalf("post-handover event not appended to followups: %+v", after.Followups)
	}
}

func TestFollowupsRespectCreationCursor(t *testing.T) {
	p := startedPlant(t)
	// Start 已写入 plant.started，交接单的光标在该事件之后，历史事件不得回填附录。
	h := p.BeginHandover("alice")
	stored, ok := p.Handovers.Get(h.ID)
	if !ok {
		t.Fatal("handover missing")
	}
	if len(stored.Followups) != 0 {
		t.Fatalf("events before handover creation leaked into followups: %+v", stored.Followups)
	}
	if stored.EventCursor != len(p.Journal.Events()) {
		t.Fatalf("cursor = %d, want %d", stored.EventCursor, len(p.Journal.Events()))
	}

	if err := p.record("shift.note", "watch reflux drum"); err != nil {
		t.Fatalf("record: %v", err)
	}
	stored, _ = p.Handovers.Get(h.ID)
	if len(stored.Followups) != 1 || stored.Followups[0].Kind != "shift.note" {
		t.Fatalf("new event not appended to unsigned handover: %+v", stored.Followups)
	}
}

func TestFollowupsStayInOrder(t *testing.T) {
	p := startedPlant(t)
	h := p.BeginHandover("alice")
	if _, err := p.SignHandover(h.ID, "bob"); err != nil {
		t.Fatalf("sign: %v", err)
	}
	for _, kind := range []string{"note.one", "note.two", "note.three"} {
		if err := p.record(kind, ""); err != nil {
			t.Fatalf("record %s: %v", kind, err)
		}
	}
	stored, _ := p.Handovers.Get(h.ID)
	if len(stored.Followups) != 3 {
		t.Fatalf("followups = %d, want 3", len(stored.Followups))
	}
	for i, kind := range []string{"note.one", "note.two", "note.three"} {
		if stored.Followups[i].Kind != kind {
			t.Fatalf("followup %d = %s, want %s", i, stored.Followups[i].Kind, kind)
		}
	}
}

func TestSignRejectsUnknownHandover(t *testing.T) {
	p := newTestPlant(t)
	if _, err := p.SignHandover(mustUUID(t), "bob"); err == nil {
		t.Fatal("signing unknown handover must fail")
	}
}

func TestHandoverListOrdered(t *testing.T) {
	p := startedPlant(t)
	first := p.BeginHandover("a")
	time.Sleep(time.Millisecond)
	second := p.BeginHandover("b")

	items := p.Handovers.List()
	if len(items) != 2 || items[0].ID != first.ID || items[1].ID != second.ID {
		t.Fatalf("list order wrong: %+v", items)
	}
}
