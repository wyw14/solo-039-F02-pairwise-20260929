package service

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
	"strings"
	"sync"
	"time"
)

const (
	HandoverDraft  = "draft"
	HandoverSigned = "signed"
)

// ErrHandoverMissing is returned when no handover exists for the given id.
var ErrHandoverMissing = errors.New("handover not found")

// HandoverEquipment is the running-state of one piece of equipment captured at
// the handover point.
type HandoverEquipment struct {
	ID      string `json:"id"`
	Running bool   `json:"running"`
	Detail  string `json:"detail"`
}

// OpenItem is a matter still awaiting action when the shift changes: an open
// incident or a command issued but not yet acknowledged by the equipment.
type OpenItem struct {
	Kind     string `json:"kind"`
	Ref      string `json:"ref"`
	Message  string `json:"message"`
	Revision string `json:"revision"`
}

// HandoverEvent is a follow-up recorded AFTER the handover point. These always
// live in their own append-only list and never rewrite the signed content.
type HandoverEvent struct {
	Kind    string    `json:"kind"`
	Message string    `json:"message"`
	At      time.Time `json:"at"`
}

// HandoverBrief is the current-batch picture the plant assembles when a
// handover is opened. The book copies it once and never re-reads live state.
type HandoverBrief struct {
	Campaign     model.Campaign
	FeedSource   string
	FeedRevision uuid.UUID
	Equipment    []HandoverEquipment
	OpenItems    []OpenItem
}

type Handover struct {
	ID           uuid.UUID           `json:"id"`
	CampaignID   uuid.UUID           `json:"campaign_id"`
	Revision     uuid.UUID           `json:"revision"`
	Epoch        uint64              `json:"epoch"`
	Grade        string              `json:"grade"`
	Phase        model.Phase         `json:"phase"`
	FeedSource   string              `json:"feed_source"`
	FeedRevision uuid.UUID           `json:"feed_revision"`
	Equipment    []HandoverEquipment `json:"equipment"`
	OpenItems    []OpenItem          `json:"open_items"`
	Outgoing     string              `json:"outgoing_operator"`
	Successor    string              `json:"successor,omitempty"`
	Status       string              `json:"status"`
	CreatedAt    time.Time           `json:"created_at"`
	SignedAt     *time.Time          `json:"signed_at,omitempty"`
	Digest       string              `json:"digest,omitempty"`
	Events       []HandoverEvent     `json:"events"`
}

type HandoverBook struct {
	mu        sync.Mutex
	handovers []Handover
}

func NewHandoverBook() *HandoverBook { return &HandoverBook{handovers: make([]Handover, 0, 8)} }

// Create opens a new draft. Only one unsigned handover may be open at a time;
// the successor must sign it off before the next shift can prepare one.
func (b *HandoverBook) Create(brief HandoverBrief, outgoing string) (Handover, error) {
	if outgoing == "" {
		return Handover{}, errors.New("outgoing operator is required")
	}
	if brief.FeedSource == "" || len(brief.Equipment) == 0 {
		return Handover{}, errors.New("handover brief is incomplete")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.handovers) > 0 && b.handovers[len(b.handovers)-1].Status == HandoverDraft {
		return Handover{}, errors.New("an unsigned handover already exists")
	}
	h := Handover{
		ID:           uuid.New(),
		CampaignID:   brief.Campaign.ID,
		Revision:     brief.Campaign.Revision,
		Epoch:        brief.Campaign.Epoch,
		Grade:        brief.Campaign.Grade,
		Phase:        brief.Campaign.Phase,
		FeedSource:   brief.FeedSource,
		FeedRevision: brief.FeedRevision,
		Equipment:    append([]HandoverEquipment(nil), brief.Equipment...),
		OpenItems:    append([]OpenItem(nil), brief.OpenItems...),
		Outgoing:     outgoing,
		Status:       HandoverDraft,
		CreatedAt:    time.Now().UTC(),
		Events:       make([]HandoverEvent, 0, 8),
	}
	b.handovers = append(b.handovers, h)
	return cloneHandover(h), nil
}

// Sign freezes the handover at the moment the successor accepts it. The digest
// is computed over the captured content only; later follow-ups never touch it.
func (b *HandoverBook) Sign(id uuid.UUID, successor string) (Handover, error) {
	if successor == "" {
		return Handover{}, errors.New("successor operator is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	idx := b.indexLocked(id)
	if idx < 0 {
		return Handover{}, ErrHandoverMissing
	}
	h := &b.handovers[idx]
	if h.Status == HandoverSigned {
		return Handover{}, errors.New("handover already signed")
	}
	h.Successor = successor
	signedAt := time.Now().UTC()
	h.SignedAt = &signedAt
	h.Status = HandoverSigned
	h.Digest = handoverDigest(*h)
	return cloneHandover(*h), nil
}

// AppendEvent records a follow-up after the handover point. It is allowed in
// both draft and signed states and only ever appends — the captured content
// cannot be modified through it.
func (b *HandoverBook) AppendEvent(id uuid.UUID, kind, message string) (Handover, error) {
	if kind == "" || message == "" {
		return Handover{}, errors.New("event kind and message are required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	idx := b.indexLocked(id)
	if idx < 0 {
		return Handover{}, ErrHandoverMissing
	}
	b.handovers[idx].Events = append(b.handovers[idx].Events, HandoverEvent{Kind: kind, Message: message, At: time.Now().UTC()})
	return cloneHandover(b.handovers[idx]), nil
}

// AppendLatest attaches a follow-up to the most recent handover. It is a no-op
// when no handover exists yet, so plant operations can call it unconditionally.
func (b *HandoverBook) AppendLatest(kind, message string) {
	if kind == "" || message == "" {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.handovers) == 0 {
		return
	}
	last := len(b.handovers) - 1
	b.handovers[last].Events = append(b.handovers[last].Events, HandoverEvent{Kind: kind, Message: message, At: time.Now().UTC()})
}

func (b *HandoverBook) Get(id uuid.UUID) (Handover, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	idx := b.indexLocked(id)
	if idx < 0 {
		return Handover{}, false
	}
	return cloneHandover(b.handovers[idx]), true
}

func (b *HandoverBook) Latest() (Handover, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.handovers) == 0 {
		return Handover{}, false
	}
	return cloneHandover(b.handovers[len(b.handovers)-1]), true
}

func (b *HandoverBook) List() []Handover {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Handover, len(b.handovers))
	for i, h := range b.handovers {
		out[i] = cloneHandover(h)
	}
	return out
}

func (b *HandoverBook) indexLocked(id uuid.UUID) int {
	for i := range b.handovers {
		if b.handovers[i].ID == id {
			return i
		}
	}
	return -1
}

func cloneHandover(h Handover) Handover {
	h.Equipment = append([]HandoverEquipment(nil), h.Equipment...)
	h.OpenItems = append([]OpenItem(nil), h.OpenItems...)
	h.Events = append([]HandoverEvent(nil), h.Events...)
	if h.SignedAt != nil {
		signedAt := *h.SignedAt
		h.SignedAt = &signedAt
	}
	return h
}

// handoverDigest hashes exactly the frozen handover content (identity, captured
// operating picture, outgoing operator and creation point). Follow-up events,
// successor name and signature timestamp are deliberately excluded so the
// digest stays stable after sign-off.
func handoverDigest(h Handover) string {
	var b strings.Builder
	fmt.Fprintf(&b, "%s|%s|%s|%d|%s|%s|%s|%s|%d",
		h.ID, h.CampaignID, h.Revision, h.Epoch, h.Grade, h.Phase,
		h.FeedSource, h.FeedRevision, h.CreatedAt.UnixNano())
	for _, equipment := range h.Equipment {
		fmt.Fprintf(&b, "|E:%s:%t:%s", equipment.ID, equipment.Running, equipment.Detail)
	}
	for _, item := range h.OpenItems {
		fmt.Fprintf(&b, "|O:%s:%s:%s:%s", item.Kind, item.Ref, item.Revision, item.Message)
	}
	fmt.Fprintf(&b, "|out:%s", h.Outgoing)
	sum := sha256.Sum256([]byte(b.String()))
	return hex.EncodeToString(sum[:])
}
