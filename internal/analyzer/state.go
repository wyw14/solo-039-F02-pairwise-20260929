package analyzer

import (
	"github.com/google/uuid"
	"github.com/wyw14/solo-039/internal/model"
	"sync"
)

type State struct {
	mu          sync.RWMutex
	calibration uuid.UUID
	window      *Window
	last        model.Proof
	online      bool
}

func New() *State {
	calibration := uuid.New()
	w := NewWindow()
	w.Reset(calibration, uuid.Nil)
	return &State{calibration: calibration, window: w, online: true}
}

func (s *State) SetOnline(value bool) { s.mu.Lock(); s.online = value; s.mu.Unlock() }

func (s *State) Online() bool { s.mu.RLock(); defer s.mu.RUnlock(); return s.online }

// ProofReady 表示当前窗口样本已凑齐，可以出具合格的成分证据。
func (s *State) ProofReady() bool {
	s.mu.RLock()
	online := s.online
	window := s.window
	s.mu.RUnlock()
	return online && window.Complete()
}

func (s *State) Calibrate() uuid.UUID {
	s.mu.Lock()
	s.calibration = uuid.New()
	s.window.Reset(s.calibration, uuid.Nil)
	s.mu.Unlock()
	return s.calibration
}

func (s *State) Calibration() uuid.UUID {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.calibration
}

func (s *State) BindFeed(revision uuid.UUID) {
	s.mu.Lock()
	s.window.Reset(s.calibration, revision)
	s.mu.Unlock()
}

func (s *State) Add(sample model.Sample) bool {
	s.mu.RLock()
	online := s.online
	w := s.window
	s.mu.RUnlock()
	if !online {
		return false
	}
	return w.Add(sample)
}

func (s *State) Proof(revision uuid.UUID) (model.Proof, bool) {
	s.mu.RLock()
	online := s.online
	complete := s.window.Complete()
	calibration := s.calibration
	s.mu.RUnlock()
	if !online || !complete {
		return model.Proof{}, false
	}
	proof := model.Proof{ID: uuid.New(), Revision: revision, Source: calibration.String(), At: 1, Independent: true}
	proof.Digest = model.ProofDigest(proof)
	s.mu.Lock()
	s.last = proof
	s.mu.Unlock()
	return proof, true
}

func (s *State) LastProof() model.Proof { s.mu.RLock(); defer s.mu.RUnlock(); return s.last }

func (s *State) ConfigureWindow(needed int, maxGap int64) {
	s.mu.RLock()
	window := s.window
	s.mu.RUnlock()
	window.Configure(needed, maxGap)
}

func (s *State) SampleCount() int {
	s.mu.RLock()
	window := s.window
	s.mu.RUnlock()
	return len(window.Samples())
}
