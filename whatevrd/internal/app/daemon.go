package app

import "sync/atomic"

type Status struct {
	State State
	Paths Paths
}

type Daemon struct {
	paths Paths
	state atomic.Int32
}

func NewDaemon(paths Paths) *Daemon {
	d := &Daemon{paths: paths}
	d.SetState(StateStarting)
	return d
}

func (d *Daemon) SetState(state State) {
	d.state.Store(int32(state))
}

func (d *Daemon) Status() Status {
	return Status{
		State: State(d.state.Load()),
		Paths: d.paths,
	}
}
