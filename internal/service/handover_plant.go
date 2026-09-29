package service

import (
	"fmt"
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
)

// OpenHandover assembles the current-batch brief (phase, feed source, running
// equipment and open matters) and opens a draft handover for the outgoing
// operator.
func (p *Plant) OpenHandover(outgoing string) (Handover, error) {
	handover, err := p.Handovers.Create(p.handoverBrief(), outgoing)
	if err != nil {
		return Handover{}, err
	}
	return handover, nil
}

// SignHandover freezes the handover point when the successor accepts it.
func (p *Plant) SignHandover(id uuid.UUID, successor string) (Handover, error) {
	return p.Handovers.Sign(id, successor)
}

func (p *Plant) LatestHandover() (Handover, bool) { return p.Handovers.Latest() }

func (p *Plant) GetHandover(id uuid.UUID) (Handover, bool) { return p.Handovers.Get(id) }

func (p *Plant) ListHandovers() []Handover { return p.Handovers.List() }

// AppendHandoverNote records a successor follow-up note without touching the
// signed content.
func (p *Plant) AppendHandoverNote(id uuid.UUID, kind, message string) (Handover, error) {
	return p.Handovers.AppendEvent(id, kind, message)
}

func (p *Plant) handoverBrief() HandoverBrief {
	current := p.Campaign.Snapshot()
	snapshot := p.Snapshot()
	p.mu.RLock()
	started := p.started
	p.mu.RUnlock()

	equipment := []HandoverEquipment{
		{ID: "feed-line", Running: p.Feed.Current.Open, Detail: fmt.Sprintf("source=%s flow=%.2f", p.Feed.Current.Source, p.Feed.Current.Flow)},
		{ID: snapshot.Column.ID, Running: started, Detail: fmt.Sprintf("pressure=%.2f temperature=%.2f level=%.2f", snapshot.Column.Pressure, snapshot.Column.Temperature, snapshot.Column.Level)},
		{ID: "reboiler", Running: !snapshot.Reboiler.Closed, Detail: fmt.Sprintf("steam=%.2f trip=%d", snapshot.Reboiler.Steam, snapshot.Reboiler.Trip)},
		{ID: "reflux-pump", Running: snapshot.Reflux.Ratio > 0, Detail: fmt.Sprintf("ratio=%.2f lease=%s", snapshot.Reflux.Ratio, snapshot.Reflux.Lease.Owner)},
	}
	if target := p.Product.Route.Target(); p.Product.Route.Opened() {
		equipment = append(equipment, HandoverEquipment{ID: "product-route", Running: true, Detail: "target=" + target})
	}
	equipment = append(equipment, HandoverEquipment{ID: "relief-" + snapshot.Relief.Valve, Running: snapshot.Relief.Released, Detail: reliefDetail(snapshot.Relief)})

	return HandoverBrief{
		Campaign:     current,
		FeedSource:   p.Feed.Current.Source,
		FeedRevision: p.Feed.Current.Revision,
		Equipment:    equipment,
		OpenItems:    p.openHandoverItems(current),
	}
}

func (p *Plant) openHandoverItems(current model.Campaign) []OpenItem {
	items := make([]OpenItem, 0)
	for _, incident := range p.Incidents.List(true) {
		items = append(items, OpenItem{Kind: "incident", Ref: incident.ID.String(), Message: incident.Message, Revision: incident.Revision})
	}
	for _, pending := range p.ReboilerCommands.Pending() {
		items = append(items, OpenItem{
			Kind:     "pending-command",
			Ref:      pending.Equipment,
			Message:  fmt.Sprintf("%s command %s awaiting acknowledgement", pending.Action, pending.Command.ID),
			Revision: pending.Command.Revision.String(),
		})
	}
	_ = current
	return items
}

func reliefDetail(relief model.Relief) string {
	switch {
	case relief.TimedOut:
		return "timed-out"
	case relief.Released:
		return "released"
	default:
		return "armed"
	}
}

// noteHandoverFollowup appends an event that happened after the handover point;
// it is a no-op until a handover exists.
func (p *Plant) noteHandoverFollowup(kind, message string) {
	p.Handovers.AppendLatest(kind, message)
}

// openIncident records an operational incident and, when a handover is open,
// attaches it as a follow-up event rather than altering the handover content.
func (p *Plant) openIncident(kind, message, revision string) Incident {
	item := p.Incidents.Open(kind, message, revision)
	p.noteHandoverFollowup("incident:"+kind, message)
	return item
}
