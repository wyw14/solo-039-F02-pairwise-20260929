package reboiler

import (
	"context"
	"errors"
	"github.com/wyw14/solo-039/internal/model"
	"sort"
	"sync"
)

type PendingCommand struct {
	Equipment string
	Action    string
	Command   model.Command
}

type CommandBook struct {
	mu               sync.Mutex
	active           map[string]model.Command
	acknowledgements map[string]model.Acknowledgement
}

func NewCommandBook() *CommandBook {
	return &CommandBook{active: make(map[string]model.Command), acknowledgements: make(map[string]model.Acknowledgement)}
}

func (b *CommandBook) Issue(campaign model.Campaign, equipment, action string, setpoint float64) (model.Command, error) {
	command := model.NewCommand(campaign, equipment, action, setpoint)
	if err := command.Validate(); err != nil {
		return model.Command{}, err
	}
	b.mu.Lock()
	b.active[equipment] = command
	b.mu.Unlock()
	return command, nil
}

func (b *CommandBook) Acknowledge(value model.Acknowledgement) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	command, ok := b.active[value.Equipment]
	if !ok {
		return errors.New("equipment has no active command")
	}
	if err := value.Validate(command); err != nil {
		return err
	}
	b.acknowledgements[value.Equipment] = value
	return nil
}

func (b *CommandBook) Confirmed(equipment, state string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	command, ok := b.active[equipment]
	if !ok {
		return false
	}
	ack, ok := b.acknowledgements[equipment]
	return ok && ack.State == state && ack.Matches(command)
}

// Pending reports commands that have been issued but not yet acknowledged by
// the equipment — operations awaiting confirmation at handover time.
func (b *CommandBook) Pending() []PendingCommand {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]PendingCommand, 0)
	for equipment, command := range b.active {
		ack, acknowledged := b.acknowledgements[equipment]
		if acknowledged && ack.Matches(command) {
			continue
		}
		out = append(out, PendingCommand{Equipment: equipment, Action: command.Action, Command: command})
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Equipment < out[j].Equipment })
	return out
}

func (b *CommandBook) Close(ctx context.Context, state *State, campaign model.Campaign) error {
	command, err := b.Issue(campaign, "steam-valve", "close", 0)
	if err != nil {
		return err
	}
	if err := state.Close(ctx, command.Epoch); err != nil {
		return err
	}
	acknowledgement := model.Acknowledgement{Command: command.ID, Campaign: command.Campaign, Revision: command.Revision, Epoch: command.Epoch, Equipment: command.Equipment, State: "closed", Value: 0}
	if err := b.Acknowledge(acknowledgement); err != nil {
		return err
	}
	if !state.AcknowledgeClosed(command.Epoch) {
		return errors.New("steam closure generation rejected")
	}
	return nil
}
