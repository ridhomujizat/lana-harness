package knowledge

import "context"

// StartUpkeepJoined starts the background upkeep loop like StartUpkeep, but
// registers the loop in the service's worker WaitGroup so drain waits for an
// in-flight pass to finish. The daemon runtime uses it to join the loop on
// shutdown, before the process exits (REQ-KNU-011, design §9).
//
// Like StartUpkeep it starts the loop once; a later call, or a StartUpkeep that
// already ran, is a no-op.
func (s *Service) StartUpkeepJoined(ctx context.Context) {
	if ctx == nil {
		ctx = s.base
	}
	s.upkeep.once.Do(func() {
		s.seedFirstStart()
		s.workers.Add(1)
		go func() {
			defer s.workers.Done()
			s.upkeepLoop(ctx)
		}()
	})
}
