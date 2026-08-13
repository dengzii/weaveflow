package trigger

import (
	"context"
	"fmt"
	"time"

	"github.com/robfig/cron/v3"
)

type scheduleEntry struct {
	cron *cron.Cron
	id   cron.EntryID
}

func (s *Service) buildSchedule(definition Trigger) (*scheduleEntry, error) {
	if !definition.Enabled || definition.Type != TypeSchedule {
		return nil, nil
	}
	location := time.UTC
	if definition.Schedule.Timezone != "" {
		loaded, err := time.LoadLocation(definition.Schedule.Timezone)
		if err != nil {
			return nil, err
		}
		location = loaded
	}
	scheduler := cron.New(cron.WithLocation(location))
	entryID, err := scheduler.AddFunc(definition.Schedule.Cron, func() {
		ctx := s.scheduleContext()
		if ctx == nil {
			return
		}
		_, _ = s.InvokeSchedule(ctx, definition.ID)
	})
	if err != nil {
		return nil, fmt.Errorf("%w: parse schedule %q: %v", ErrInvalidTrigger, definition.Schedule.Cron, err)
	}
	return &scheduleEntry{cron: scheduler, id: entryID}, nil
}

func (s *Service) replaceSchedule(id string, schedule *scheduleEntry) {
	s.mu.Lock()
	previous := s.schedules[id]
	if schedule != nil && s.cancel != nil {
		s.schedules[id] = schedule
		schedule.cron.Start()
	} else {
		delete(s.schedules, id)
	}
	s.mu.Unlock()
	if previous != nil {
		previous.cron.Remove(previous.id)
		previous.cron.Stop()
	}
}

func (s *Service) scheduleContext() context.Context {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.ctx
}

func validateScheduleExpression(definition Trigger) error {
	if definition.Type != TypeSchedule || definition.Schedule == nil {
		return nil
	}
	parser := cron.NewParser(cron.Minute | cron.Hour | cron.Dom | cron.Month | cron.Dow)
	if _, err := parser.Parse(definition.Schedule.Cron); err != nil {
		return fmt.Errorf("%w: parse schedule %q: %v", ErrInvalidTrigger, definition.Schedule.Cron, err)
	}
	return nil
}
