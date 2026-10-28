package runtime

import "testing"

func TestCommandsProductionQueueFlag(t *testing.T) {
	tests := []struct {
		name string
		opts Options
		plan Plan
		want bool
	}{
		{name: "flag true starts", opts: QueueOn(Options{}), plan: Plan{Horizon: false}, want: true},
		{name: "flag false never starts", opts: QueueOff(Options{}), plan: Plan{Horizon: true}, want: false},
		{name: "unset auto-detects horizon", opts: QueueAuto(Options{}), plan: Plan{Horizon: true}, want: true},
		{name: "unset without horizon", opts: QueueAuto(Options{}), plan: Plan{Horizon: false}, want: false},
		{name: "zero value unset auto-detects", opts: Options{}, plan: Plan{Horizon: true}, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ShouldStartHorizon(tt.opts, tt.plan); got != tt.want {
				t.Errorf("ShouldStartHorizon(%+v, %+v) = %v, want %v", tt.opts, tt.plan, got, tt.want)
			}
		})
	}
}

func TestQueueHelpersRoundTrip(t *testing.T) {
	if QueueOff(QueueOn(Options{})).Queue == nil || *QueueOff(QueueOn(Options{})).Queue {
		t.Error("QueueOff(QueueOn()) should be non-nil false")
	}
	if QueueAuto(QueueOn(Options{})).Queue != nil {
		t.Error("QueueAuto(QueueOn()) should be nil")
	}
}
