package multiuser

import (
	"context"
	"sync"
)

// editionDraftGate serializes draft preparation for one profile. References
// include both the active operation and callers waiting for its permit.
type editionDraftGate struct {
	permit     chan struct{}
	references int
}

// acquireEditionDraftPermit reserves the profile's single draft permit or
// returns when the operation context ends. The returned release function is
// safe to call once and keeps the gate registered until all waiters leave.
func (s *MultiUserService) acquireEditionDraftPermit(ctx context.Context, profileID string) (func(), error) {
	s.editionDraftMutex.Lock()
	gate := s.editionDraftGates[profileID]
	if gate == nil {
		gate = &editionDraftGate{permit: make(chan struct{}, 1)}
		s.editionDraftGates[profileID] = gate
	}
	gate.references++
	s.editionDraftMutex.Unlock()

	select {
	case gate.permit <- struct{}{}:
		var once sync.Once
		return func() {
			once.Do(func() {
				<-gate.permit
				s.releaseEditionDraftReference(profileID, gate)
			})
		}, nil
	case <-ctx.Done():
		s.releaseEditionDraftReference(profileID, gate)
		return nil, ctx.Err()
	}
}

func (s *MultiUserService) releaseEditionDraftReference(profileID string, gate *editionDraftGate) {
	s.editionDraftMutex.Lock()
	defer s.editionDraftMutex.Unlock()
	gate.references--
	if gate.references == 0 && s.editionDraftGates[profileID] == gate {
		delete(s.editionDraftGates, profileID)
	}
}
