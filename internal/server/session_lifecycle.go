package server

type sessionState string

const (
	sessionStateStarting  sessionState = "starting"
	sessionStateConnected sessionState = "connected"
	sessionStateDetached  sessionState = "detached"
	sessionStateEnding    sessionState = "ending"
	sessionStateEnded     sessionState = "ended"
	sessionStateFailed    sessionState = "failed"
)

type sessionLifecycle struct {
	state sessionState
}

func newSessionLifecycle() sessionLifecycle {
	return sessionLifecycle{state: sessionStateStarting}
}

func (l *sessionLifecycle) started() bool {
	if l.state != sessionStateStarting {
		return false
	}
	l.state = sessionStateDetached
	return true
}

func (l *sessionLifecycle) attach() bool {
	if l.state != sessionStateDetached {
		return false
	}
	l.state = sessionStateConnected
	return true
}

func (l *sessionLifecycle) detach() bool {
	if l.state != sessionStateConnected {
		return false
	}
	l.state = sessionStateDetached
	return true
}

func (l *sessionLifecycle) beginEnding() bool {
	switch l.state {
	case sessionStateStarting, sessionStateConnected, sessionStateDetached:
		l.state = sessionStateEnding
		return true
	default:
		return false
	}
}

func (l *sessionLifecycle) finish(runErr error) bool {
	switch l.state {
	case sessionStateEnded, sessionStateFailed:
		return false
	case sessionStateEnding:
		l.state = sessionStateEnded
	default:
		if runErr != nil {
			l.state = sessionStateFailed
		} else {
			l.state = sessionStateEnded
		}
	}
	return true
}

func (l sessionLifecycle) done() bool {
	return l.state == sessionStateEnded || l.state == sessionStateFailed
}

func (l sessionLifecycle) acceptsOutput() bool {
	return l.state == sessionStateConnected || l.state == sessionStateDetached
}

func (l sessionLifecycle) publicState() string {
	switch l.state {
	case sessionStateConnected:
		return "connected"
	case sessionStateStarting, sessionStateDetached:
		return "detached"
	default:
		// The current browser contract has one terminal state. Protocol V1 can
		// expose ending and failed after capability negotiation is introduced.
		return "ended"
	}
}
