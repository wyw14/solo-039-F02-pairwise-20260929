package service

import (
	"errors"
	"github.com/wyw14/solo-039/internal/model"
	"testing"
)

func testBrief() HandoverBrief {
	campaign := model.NewCampaign("baseline")
	return HandoverBrief{
		Campaign:     campaign,
		FeedSource:   "primary",
		FeedRevision: campaign.Revision,
		Equipment: []HandoverEquipment{
			{ID: "T-101", Running: true, Detail: "pressure=1.10"},
			{ID: "reboiler", Running: true, Detail: "steam=0.50"},
		},
		OpenItems: []OpenItem{
			{Kind: "incident", Ref: "inc-1", Message: "pressure wobble", Revision: campaign.Revision.String()},
		},
	}
}

func TestCreateHandoverCapturesCurrentBatch(t *testing.T) {
	book := NewHandoverBook()
	brief := testBrief()
	handover, err := book.Create(brief, "alice")
	if err != nil {
		t.Fatal(err)
	}
	if handover.Status != HandoverDraft {
		t.Fatalf("new handover status = %q, want draft", handover.Status)
	}
	if handover.Phase != model.Preparing || handover.FeedSource != "primary" {
		t.Fatalf("handover did not capture batch state: %+v", handover)
	}
	if len(handover.Equipment) != 2 || handover.Equipment[0].ID != "T-101" {
		t.Fatalf("equipment not captured: %+v", handover.Equipment)
	}
	if len(handover.OpenItems) != 1 || handover.OpenItems[0].Kind != "incident" {
		t.Fatalf("open items not captured: %+v", handover.OpenItems)
	}
	if handover.Digest != "" {
		t.Fatal("draft handover must not carry a freeze digest yet")
	}
}

func TestCreateRequiresOutgoingOperatorAndBrief(t *testing.T) {
	book := NewHandoverBook()
	if _, err := book.Create(testBrief(), ""); err == nil {
		t.Fatal("expected error for empty outgoing operator")
	}
	brief := testBrief()
	brief.FeedSource = ""
	if _, err := book.Create(brief, "alice"); err == nil {
		t.Fatal("expected error for incomplete brief")
	}
}

func TestSignFreezesHandoverPoint(t *testing.T) {
	book := NewHandoverBook()
	handover, _ := book.Create(testBrief(), "alice")
	signed, err := book.Sign(handover.ID, "bob")
	if err != nil {
		t.Fatal(err)
	}
	if signed.Status != HandoverSigned || signed.Successor != "bob" || signed.SignedAt == nil {
		t.Fatalf("handover not signed: %+v", signed)
	}
	if signed.Digest == "" {
		t.Fatal("signed handover must carry a freeze digest")
	}
	if _, err := book.Sign(handover.ID, "carol"); err == nil {
		t.Fatal("re-signing a signed handover must be rejected")
	}
}

func TestFollowupEventsNeverRewriteSignedContent(t *testing.T) {
	book := NewHandoverBook()
	brief := testBrief()
	handover, _ := book.Create(brief, "alice")
	signed, _ := book.Sign(handover.ID, "bob")

	// Live state keeps moving after the handover point; the caller mutating
	// its own brief must not bleed into the frozen handover.
	brief.FeedSource = "secondary"
	brief.Equipment[0].Running = false
	brief.OpenItems[0].Message = "rewritten"

	updated, err := book.AppendEvent(handover.ID, "incident:runtime", "flare opened after sign-off")
	if err != nil {
		t.Fatal(err)
	}
	if updated.Digest != signed.Digest {
		t.Fatal("digest changed after a follow-up event")
	}
	if updated.FeedSource != "primary" {
		t.Fatalf("feed source was rewritten to %q", updated.FeedSource)
	}
	if !updated.Equipment[0].Running {
		t.Fatal("equipment running state was rewritten after sign-off")
	}
	if updated.OpenItems[0].Message != "pressure wobble" {
		t.Fatalf("open item was rewritten after sign-off: %q", updated.OpenItems[0].Message)
	}
	if len(updated.Events) != 1 || updated.Events[0].Kind != "incident:runtime" {
		t.Fatalf("follow-up event not appended: %+v", updated.Events)
	}

	again, err := book.AppendEvent(handover.ID, "campaign", "grade transition completed")
	if err != nil {
		t.Fatal(err)
	}
	if len(again.Events) != 2 || again.Digest != signed.Digest {
		t.Fatal("second follow-up broke append-only invariants")
	}
}

func TestOnlyOneDraftOpenAtATime(t *testing.T) {
	book := NewHandoverBook()
	first, err := book.Create(testBrief(), "alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := book.Create(testBrief(), "carol"); err == nil {
		t.Fatal("second draft while one is unsigned must be rejected")
	}
	if _, err := book.Sign(first.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := book.Create(testBrief(), "carol"); err != nil {
		t.Fatalf("next shift must be able to open a handover after sign-off: %v", err)
	}
}

func TestHandoverLookupErrors(t *testing.T) {
	book := NewHandoverBook()
	if _, err := book.Sign(model.NewCampaign("x").ID, "bob"); !errors.Is(err, ErrHandoverMissing) {
		t.Fatalf("expected ErrHandoverMissing, got %v", err)
	}
	if _, err := book.AppendEvent(model.NewCampaign("x").ID, "k", "m"); !errors.Is(err, ErrHandoverMissing) {
		t.Fatalf("expected ErrHandoverMissing, got %v", err)
	}
	if _, ok := book.Latest(); ok {
		t.Fatal("empty book must report no latest handover")
	}
}
