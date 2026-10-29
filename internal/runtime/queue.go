// Package runtime holds runtime decision logic for serve commands.
//
// Horizon-start semantics (fix review H1: --queue=false was a silent no-op in
// the predecessor). The flag is authoritative when set; when unset, the
// decision falls back to discovered evidence (plan.Horizon auto-detect):
//
//   - --queue=true  -> Horizon starts, regardless of evidence
//   - --queue=false -> Horizon NEVER starts, regardless of evidence
//   - unset         -> plan.Horizon (discovered evidence) decides
package runtime

// Options is the user-supplied intent for the serve command.
type Options struct {
	// Queue is the --queue flag intent. nil = unset (auto-detect).
	Queue *bool
	// Scheduler is the --scheduler flag intent. nil = unset (auto-detect).
	Scheduler *bool
}

// Plan is discovered evidence about the project.
// ponytail: extend when more evidence is used.
type Plan struct {
	// Horizon is discovered evidence that the project uses Laravel Horizon.
	Horizon bool
	// Scheduler is discovered evidence that the project schedules tasks.
	Scheduler bool
	// Vite is discovered evidence that the project has a Vite dev workflow.
	Vite bool
}

// QueueOn returns intent with --queue=true.
func QueueOn(o Options) Options {
	o.Queue = new(bool)
	*o.Queue = true
	return o
}

// QueueOff returns intent with --queue=false.
func QueueOff(o Options) Options {
	o.Queue = new(bool)
	*o.Queue = false
	return o
}

// QueueAuto returns intent with the flag unset (auto-detect).
func QueueAuto(o Options) Options {
	o.Queue = nil
	return o
}

// ShouldStartHorizon decides whether Horizon starts. One authoritative
// intent: explicit flag wins; unset defers to plan.Horizon.
func ShouldStartHorizon(o Options, p Plan) bool {
	if o.Queue != nil {
		return *o.Queue
	}
	return p.Horizon
}

// ShouldStartScheduler mirrors ShouldStartHorizon for --scheduler.
func ShouldStartScheduler(o Options, p Plan) bool {
	if o.Scheduler != nil {
		return *o.Scheduler
	}
	return p.Scheduler
}
