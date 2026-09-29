package service

import (
	"context"
	"errors"
	"testing"
)

func newTestPlant(t *testing.T) *Plant {
	t.Helper()
	return NewPlant(t.TempDir())
}

func TestPlantHandoverCapturesOperatingPicture(t *testing.T) {
	plant := newTestPlant(t)
	ctx := context.Background()
	if _, err := plant.Start(ctx, "baseline", "primary"); err != nil {
		t.Fatal(err)
	}
	// An unresolved failure must show up as an open matter at handover.
	plant.fail(errors.New("pressure wobble"))
	// An issued-but-unacknowledged command must also appear.
	if _, err := plant.ReboilerCommands.Issue(plant.Campaign.Snapshot(), "steam-valve", "close", 0); err != nil {
		t.Fatal(err)
	}

	handover, err := plant.OpenHandover("alice")
	if err != nil {
		t.Fatal(err)
	}
	if handover.FeedSource != "primary" {
		t.Fatalf("feed source = %q, want primary", handover.FeedSource)
	}
	if handover.Phase == "" {
		t.Fatal("phase not captured")
	}
	var sawIncident, sawPending bool
	for _, item := range handover.OpenItems {
		switch item.Kind {
		case "incident":
			sawIncident = true
			if item.Message != "pressure wobble" {
				t.Fatalf("incident message = %q", item.Message)
			}
		case "pending-command":
			sawPending = true
		}
	}
	if !sawIncident || !sawPending {
		t.Fatalf("open items missing incident=%v pending=%v: %+v", sawIncident, sawPending, handover.OpenItems)
	}
	if len(handover.Equipment) == 0 {
		t.Fatal("no running equipment captured")
	}
}

func TestPlantEventsAfterSignoffAppendOnly(t *testing.T) {
	plant := newTestPlant(t)
	ctx := context.Background()
	if _, err := plant.Start(ctx, "baseline", "primary"); err != nil {
		t.Fatal(err)
	}
	handover, err := plant.OpenHandover("alice")
	if err != nil {
		t.Fatal(err)
	}
	signed, err := plant.SignHandover(handover.ID, "bob")
	if err != nil {
		t.Fatal(err)
	}

	// The campaign moves on after the handover point: new feed source, new
	// journal events, a fresh incident.
	if _, err := plant.Transition(ctx, "lighter", "secondary"); err != nil {
		t.Fatal(err)
	}
	plant.fail(errors.New("post-signoff trip"))

	after, ok := plant.GetHandover(handover.ID)
	if !ok {
		t.Fatal("signed handover vanished")
	}
	if after.Digest != signed.Digest {
		t.Fatal("signed digest changed after subsequent events")
	}
	if after.FeedSource != "primary" {
		t.Fatalf("signed feed source rewritten to %q", after.FeedSource)
	}
	if after.Phase != signed.Phase {
		t.Fatal("signed phase rewritten")
	}
	if len(after.Events) < 2 {
		t.Fatalf("subsequent events not appended: %+v", after.Events)
	}
	var sawTransition, sawIncident bool
	for _, event := range after.Events {
		switch {
		case event.Kind == "campaign.transition":
			sawTransition = true
		case event.Kind == "incident:runtime" && event.Message == "post-signoff trip":
			sawIncident = true
		}
	}
	if !sawTransition || !sawIncident {
		t.Fatalf("follow-ups missing transition=%v incident=%v: %+v", sawTransition, sawIncident, after.Events)
	}
}

func TestPlantSecondDraftBlockedUntilSignoff(t *testing.T) {
	plant := newTestPlant(t)
	ctx := context.Background()
	if _, err := plant.Start(ctx, "baseline", "primary"); err != nil {
		t.Fatal(err)
	}
	first, err := plant.OpenHandover("alice")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := plant.OpenHandover("carol"); err == nil {
		t.Fatal("expected second draft to be rejected")
	}
	if _, err := plant.SignHandover(first.ID, "bob"); err != nil {
		t.Fatal(err)
	}
	if _, err := plant.OpenHandover("carol"); err != nil {
		t.Fatalf("handover after sign-off should succeed: %v", err)
	}
	if len(plant.ListHandovers()) != 2 {
		t.Fatal("both handovers should be retained in the book")
	}
}
