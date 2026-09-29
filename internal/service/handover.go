package service

import (
	"errors"
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
	"sort"
	"sync"
	"time"
)

// FeedHandover 是交接时点锁定的进料源状态。
type FeedHandover struct {
	Source         string    `json:"source"`
	Revision       uuid.UUID `json:"revision"`
	Open           bool      `json:"open"`
	Flow           float64   `json:"flow"`
	PreviousSource string    `json:"previous_source"`
}

// EquipmentHandover 是交接时点一台运行设备的读数。
type EquipmentHandover struct {
	Name    string         `json:"name"`
	Running bool           `json:"running"`
	Detail  map[string]any `json:"detail,omitempty"`
}

// PendingAction 是需要接班人继续跟进的未结事项。
type PendingAction struct {
	Kind     string    `json:"kind"`
	Ref      string    `json:"ref"`
	Message  string    `json:"message"`
	Revision string    `json:"revision"`
	At       time.Time `json:"at,omitempty"`
}

// FollowupEvent 是交接单生成之后发生的事件，只单独追加，不回写已冻结内容。
type FollowupEvent struct {
	Kind     string    `json:"kind"`
	Payload  string    `json:"payload"`
	Revision uuid.UUID `json:"revision"`
	Epoch    uint64    `json:"epoch"`
	At       time.Time `json:"at"`
}

// Handover 是一张班次交接单。创建时按当前批次拍下快照，接班人签收后冻结；
// 签收之后只允许 Followups 追加，快照字段没有任何修改入口。
type Handover struct {
	ID          uuid.UUID           `json:"id"`
	CampaignID  uuid.UUID           `json:"campaign_id"`
	Revision    uuid.UUID           `json:"revision"`
	Grade       string              `json:"grade"`
	Phase       model.Phase         `json:"phase"`
	Epoch       uint64              `json:"epoch"`
	CreatedAt   time.Time           `json:"created_at"`
	CreatedBy   string              `json:"created_by"`
	Feed        FeedHandover        `json:"feed"`
	Equipment   []EquipmentHandover `json:"equipment"`
	Pending     []PendingAction     `json:"pending"`
	EventCursor int                 `json:"event_cursor"`
	Followups   []FollowupEvent     `json:"followups"`
	Frozen      bool                `json:"frozen"`
	SignedBy    string              `json:"signed_by,omitempty"`
	SignedAt    *time.Time          `json:"signed_at,omitempty"`
}

// HandoverBook 保存全部交接单。
type HandoverBook struct {
	mu    sync.Mutex
	items map[uuid.UUID]Handover
}

func NewHandoverBook() *HandoverBook {
	return &HandoverBook{items: make(map[uuid.UUID]Handover)}
}

// Begin 收录一张已按当前批次拍好快照的交接单。
func (b *HandoverBook) Begin(h Handover) Handover {
	b.mu.Lock()
	b.items[h.ID] = h
	b.mu.Unlock()
	return h
}

// Sign 由接班人签收：签收后交接单冻结，拒绝重复签收。
func (b *HandoverBook) Sign(id uuid.UUID, receiver string) (Handover, error) {
	if receiver == "" {
		return Handover{}, errors.New("receiver is required")
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	h, ok := b.items[id]
	if !ok {
		return Handover{}, errors.New("handover not found")
	}
	if h.Frozen {
		return Handover{}, errors.New("handover already signed and frozen")
	}
	signedAt := time.Now()
	h.Frozen = true
	h.SignedBy = receiver
	h.SignedAt = &signedAt
	b.items[id] = h
	return h, nil
}

// NoteEvent 把交接单生成之后发生的事件单独追加到同批次的交接单。
// seq 是事件在日志中的序号，交接单在创建时记录了光标 EventCursor，
// 只有创建后（seq >= EventCursor）的事件才入附录，快照字段始终不被改动。
func (b *HandoverBook) NoteEvent(seq int, event model.Event) {
	b.mu.Lock()
	defer b.mu.Unlock()
	for id, h := range b.items {
		if h.CampaignID != event.Campaign || seq < h.EventCursor {
			continue
		}
		h.Followups = append(h.Followups, FollowupEvent{
			Kind:     event.Kind,
			Payload:  event.Payload,
			Revision: event.Revision,
			Epoch:    event.Epoch,
			At:       time.Unix(0, event.At),
		})
		b.items[id] = h
	}
}

func (b *HandoverBook) Get(id uuid.UUID) (Handover, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	h, ok := b.items[id]
	return h, ok
}

func (b *HandoverBook) List() []Handover {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]Handover, 0, len(b.items))
	for _, h := range b.items {
		out = append(out, h)
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].CreatedAt.Before(out[j].CreatedAt) })
	return out
}

func defaultOperator(value, fallback string) string {
	if value == "" {
		return fallback
	}
	return value
}

// BeginHandover 按当前批次生成交接单：阶段、进料源、运行设备、未结事项一并固化。
func (p *Plant) BeginHandover(createdBy string) Handover {
	current := p.Campaign.Snapshot()
	snapshot := p.Snapshot()
	feedCurrent := p.Feed.Current
	feedPrevious := p.Feed.Previous

	h := Handover{
		ID:         uuid.New(),
		CampaignID: current.ID,
		Revision:   current.Revision,
		Grade:      current.Grade,
		Phase:      current.Phase,
		Epoch:      current.Epoch,
		CreatedAt:  time.Now(),
		CreatedBy:  defaultOperator(createdBy, "outgoing-operator"),
		// 当前日志长度即下一条事件的序号，签收后只从这里开始追加。
		EventCursor: len(p.Journal.Events()),
		Feed: FeedHandover{
			Source:         feedCurrent.Source,
			Revision:       feedCurrent.Revision,
			Open:           feedCurrent.Open,
			Flow:           feedCurrent.Flow,
			PreviousSource: feedPrevious.Source,
		},
		Equipment: p.handoverEquipment(snapshot),
		Pending:   p.pendingActions(current, snapshot),
	}
	return p.Handovers.Begin(h)
}

func (p *Plant) SignHandover(id uuid.UUID, receiver string) (Handover, error) {
	return p.Handovers.Sign(id, defaultOperator(receiver, "incoming-operator"))
}

func (p *Plant) handoverEquipment(snapshot model.Snapshot) []EquipmentHandover {
	refluxLease := ""
	if snapshot.Reflux.Lease.Active {
		refluxLease = snapshot.Reflux.Lease.Owner
	}
	products := append([]string(nil), snapshot.Products...)
	return []EquipmentHandover{
		{
			Name:    "column",
			Running: snapshot.Column.Phase != model.Stopped,
			Detail: map[string]any{
				"id": snapshot.Column.ID, "phase": snapshot.Column.Phase,
				"pressure": snapshot.Column.Pressure, "level": snapshot.Column.Level,
				"temperature": snapshot.Column.Temperature, "safe": p.Column.Safe(),
			},
		},
		{
			Name:    "reboiler",
			Running: !snapshot.Reboiler.Closed,
			Detail: map[string]any{
				"steam": snapshot.Reboiler.Steam, "closed": snapshot.Reboiler.Closed,
				"command_epoch": snapshot.Reboiler.CommandEpoch, "trip": snapshot.Reboiler.Trip,
			},
		},
		{
			Name:    "reflux",
			Running: snapshot.Reflux.Ratio > 0,
			Detail: map[string]any{
				"ratio": snapshot.Reflux.Ratio, "pump_capacity": snapshot.Reflux.PumpCapacity,
				"lease_owner": refluxLease, "capacity_used": p.Reflux.Used(),
			},
		},
		{
			Name:    "analyzer",
			Running: p.Analyzer.Online(),
			Detail: map[string]any{
				"samples": p.Analyzer.SampleCount(), "proof_ready": p.Analyzer.ProofReady(),
				"calibration": p.Analyzer.Calibration(),
			},
		},
		{
			Name:    "relief",
			Running: snapshot.Relief.Released,
			Detail: map[string]any{
				"valve": snapshot.Relief.Valve, "released": snapshot.Relief.Released,
				"timed_out": snapshot.Relief.TimedOut, "pressure": snapshot.Relief.Pressure,
			},
		},
		{
			Name:    "product-route",
			Running: len(products) > 0,
			Detail:  map[string]any{"targets": products},
		},
	}
}

// pendingActions 汇总接班人需要确认的未结事项：未恢复事故、未签收命令、
// 排队中的回流申请、未齐的成分证据、未投用的产品路线、泄压异常和未完成的停车。
func (p *Plant) pendingActions(current model.Campaign, snapshot model.Snapshot) []PendingAction {
	revision := current.Revision.String()
	pending := make([]PendingAction, 0)

	for _, incident := range p.Incidents.List(true) {
		pending = append(pending, PendingAction{
			Kind: "incident", Ref: incident.ID.String(),
			Message:  incident.Kind + ": " + incident.Message,
			Revision: incident.Revision, At: incident.At,
		})
	}
	for _, command := range p.ReboilerCommands.Pending() {
		pending = append(pending, PendingAction{
			Kind: "command", Ref: command.Equipment,
			Message:  command.Action + " awaiting field acknowledgement",
			Revision: command.Revision.String(),
		})
	}
	for _, request := range p.RefluxAllocator.Pending() {
		pending = append(pending, PendingAction{
			Kind: "reflux-request", Ref: request.Owner,
			Message:  "queued capacity request awaiting allocation",
			Revision: revision,
		})
	}
	if !p.Analyzer.ProofReady() {
		pending = append(pending, PendingAction{
			Kind: "evidence", Ref: "analyzer",
			Message:  "composition evidence incomplete, quality proof unavailable",
			Revision: revision,
		})
	}
	if current.Phase == model.OnSpec && !p.Product.Route.Opened() {
		pending = append(pending, PendingAction{
			Kind: "product-route", Ref: "route",
			Message:  "campaign on-spec but product route is not open",
			Revision: revision,
		})
	}
	if snapshot.Relief.TimedOut {
		pending = append(pending, PendingAction{
			Kind: "relief", Ref: snapshot.Relief.Valve,
			Message:  "relief release timed out, isolation must be confirmed",
			Revision: revision,
		})
	}
	if current.Phase == model.Shutting {
		pending = append(pending, PendingAction{
			Kind: "shutdown", Ref: current.ID.String(),
			Message:  "shutdown in progress, safe stop not yet confirmed",
			Revision: revision,
		})
	}

	sort.SliceStable(pending, func(i, j int) bool {
		if pending[i].At.Equal(pending[j].At) {
			return pending[i].Kind < pending[j].Kind
		}
		return pending[i].At.Before(pending[j].At)
	})
	return pending
}
