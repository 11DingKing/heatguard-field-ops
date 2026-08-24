package domain

import "fmt"

var waveTransitions = map[WaveState]map[WaveState]bool{
	WaveDraft:       {WaveReady: true, WaveCancelled: true},
	WaveReady:       {WaveActive: true, WaveCancelled: true},
	WaveActive:      {WaveWithdrawing: true, WaveSplit: true, WaveClosed: true},
	WaveWithdrawing: {WaveSplit: true, WaveClosed: true},
	WaveSplit:       {WaveWithdrawing: true, WaveClosed: true},
}

func ValidateWaveTransition(from, to WaveState) error {
	if from == to {
		return nil
	}
	if waveTransitions[from][to] {
		return nil
	}
	return fmt.Errorf("wave %s to %s: %w", from, to, ErrInvalidState)
}

var participantTransitions = map[ParticipantState]map[ParticipantState]bool{
	ParticipantEnrolled: {
		ParticipantDeparted:  true,
		ParticipantWithdrawn: true,
	},
	ParticipantDeparted: {
		ParticipantCheckedIn: true,
		ParticipantResting:   true,
		ParticipantMissing:   true,
		ParticipantWithdrawn: true,
		ParticipantCompleted: true,
	},
	ParticipantCheckedIn: {
		ParticipantCheckedIn: true,
		ParticipantResting:   true,
		ParticipantMissing:   true,
		ParticipantWithdrawn: true,
		ParticipantCompleted: true,
	},
	ParticipantResting: {
		ParticipantCheckedIn: true,
		ParticipantMissing:   true,
		ParticipantWithdrawn: true,
		ParticipantCompleted: true,
	},
	ParticipantMissing: {
		ParticipantCheckedIn: true,
		ParticipantWithdrawn: true,
	},
}

func ValidateParticipantTransition(from, to ParticipantState) error {
	if participantTransitions[from][to] {
		return nil
	}
	return fmt.Errorf("participant %s to %s: %w", from, to, ErrInvalidState)
}

func StateForEvent(current ParticipantState, event FieldEventType) (ParticipantState, error) {
	var next ParticipantState
	switch event {
	case EventCheckpoint, EventFound:
		next = ParticipantCheckedIn
	case EventHydrationStart:
		next = ParticipantResting
	case EventHydrationEnd:
		next = ParticipantCheckedIn
	case EventMissing:
		next = ParticipantMissing
	case EventWithdraw:
		next = ParticipantWithdrawn
	case EventComplete:
		next = ParticipantCompleted
	default:
		return "", Validation("event_type", "does not change participant state")
	}
	if err := ValidateParticipantTransition(current, next); err != nil {
		return "", err
	}
	return next, nil
}
