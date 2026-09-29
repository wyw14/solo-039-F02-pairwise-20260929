package reboiler

import (
	"context"
	"errors"
	"github.com/wyw14/solo-039/internal/model"
	"sync"
)

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

// Pending 返回仍待现场确认的命令：没有签收，或签收已属于更早的命令代次。
func (b *CommandBook) Pending() []model.Command {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]model.Command, 0)
	for _, command := range b.active {
		ack, ok := b.acknowledgements[command.Equipment]
		if ok && ack.Matches(command) {
			continue
		}
		out = append(out, command)
	}
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
