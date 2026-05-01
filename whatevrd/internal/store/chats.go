package store

type Chat struct {
	ID              string
	Name            string
	LastMessage     string
	LastMessageTime int64
	UnreadCount     int32
	IsGroup         bool
}
