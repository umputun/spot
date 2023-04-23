package syncs

import "context"

type options struct {
	ctx           context.Context
	cancel        context.CancelFunc
	preLock       bool
	termOnError   bool
	discardIfFull bool
}

// GroupOption functional option type
type GroupOption func(o *options)

// Context passes ctx and makes it cancelable
func Context(ctx context.Context) GroupOption {
	return func(o *options) {
		o.ctx, o.cancel = context.WithCancel(ctx)
	}
}

// Preemptive sets locking mode preventing spawning waiting goroutine. May cause Go call to block!
func Preemptive(o *options) {
	o.preLock = true
}

// TermOnErr prevents new goroutines to start after first error
func TermOnErr(o *options) {
	o.termOnError = true
}

// Discard will discard new goroutines if semaphore is full, i.e. no more goroutines allowed
func Discard(o *options) {
	o.discardIfFull = true
	o.preLock = true // discard implies preemptive
}
