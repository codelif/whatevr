package app

// MediaSendOptions steers media sends. Kind is "", "auto", "image", "video",
// "audio", "voice" or "document"; empty/auto classifies from the file's MIME
// type. ViewOnce sends photo/video/audio media view-once. Filename overrides
// the document display name. Quality is "", "standard" or "hd": standard
// downscales photos to 1600px (like official clients), hd sends the original
// bytes; empty means standard. It lives here (rather than in wa) so the
// protocol layer can name it without importing the WhatsApp client.
type MediaSendOptions struct {
	Kind     string
	ViewOnce bool
	Filename string
	Quality  string
}

// MediaBatchFile is one file in a send.media_batch call. Caption applies per
// file (the frontend puts the shared caption on the first one only).
type MediaBatchFile struct {
	Path    string
	Caption string
}

// MediaBatchError records one failed file in a batch, by index.
type MediaBatchError struct {
	Index   int
	Message string
}

// CommunityGroup is one sub-group linked under a community: its JID plus the
// best-effort display name. Shared between wa and protocol for the same
// import-cycle reason as MediaSendOptions.
type CommunityGroup struct {
	ID   string
	Name string
}

// StatusViewer is one recorded view of our status, with a resolved display
// name for rendering. Shared between wa and protocol for the same
// import-cycle reason as MediaSendOptions.
type StatusViewer struct {
	ViewerJID   string
	DisplayName string
	ViewedAt    int64
}
type ChannelMessage struct {
	ServerID  int64
	Timestamp int64
	Kind      string
	Text      string
	Fallback  string
	Views     int
}
