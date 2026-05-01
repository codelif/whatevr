package app

type State int32

const (
	StateStarting State = iota + 1
	StateNeedLogin
	StateConnecting
	StateOnline
	StateReconnecting
	StateOffline
)

func (s State) String() string {
	switch s {
	case StateStarting:
		return "STARTING"
	case StateNeedLogin:
		return "NEED_LOGIN"
	case StateConnecting:
		return "CONNECTING"
	case StateOnline:
		return "ONLINE"
	case StateReconnecting:
		return "RECONNECTING"
	case StateOffline:
		return "OFFLINE"
	default:
		return "UNSPECIFIED"
	}
}
