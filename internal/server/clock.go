package server

import "time"

type timer interface {
	Stop() bool
	Reset(time.Duration) bool
}

type clock interface {
	Now() time.Time
	After(time.Duration) <-chan time.Time
	AfterFunc(time.Duration, func()) timer
}

type systemClock struct{}

func (systemClock) Now() time.Time {
	return time.Now()
}

func (systemClock) After(duration time.Duration) <-chan time.Time {
	return time.After(duration)
}

func (systemClock) AfterFunc(duration time.Duration, callback func()) timer {
	return time.AfterFunc(duration, callback)
}
