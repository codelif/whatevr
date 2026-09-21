package store

import (
	"fmt"
	"strings"
)

// KindDescriptor is how one message kind names itself wherever a message has to
// collapse to a single line: the chat-list preview, the wire `fallback`, the
// body of a quote we reconstruct for WhatsApp, and a desktop notification.
//
// Those four renderings used to be four independent switch statements in three
// packages, which is why voice notes and documents read correctly in two of them
// and fell through to "[Media]" or "New message" in the other two. A kind is one
// entry in this table now.
//
// The vocabulary is the emoji style PROTOCOL.md documents and shows in its own
// example session ("📷 Photo", "🎤 Voice message (0:12)", "📊 Poll: dinner?").
// The daemon used to emit a second, bracketed vocabulary ("[Image]") into the
// chat row's preview, which the document never described.
type KindDescriptor struct {
	Kind string
	// Emoji leads the line. Empty for kinds whose whole line comes from the
	// message itself, like a system event.
	Emoji string
	// Label is the human name for the kind, without the glyph.
	Label string
	// CaptionWins lets the message's own caption stand in place of the label.
	// A sticker deliberately does not: "🎨 Sticker" is the whole message, there
	// is nothing else to say about it.
	CaptionWins bool
	// FilenameWins lets the attachment's filename beat both label and caption.
	// It is what someone scanning a chat list full of attachments is looking
	// for, and it is what WhatsApp shows.
	FilenameWins bool
	// ShowDuration appends " (0:12)" for timed media.
	ShowDuration bool
	// MediaBearing marks the kinds that carry a downloadable blob, which is
	// what the `media` object on the wire and the auto-download machinery key
	// off. A poll row has a kind but nothing to fetch.
	MediaBearing bool
}

// kindDescriptors is keyed by the same strings the wire uses, because the store
// constants are deliberately spelled identically to the protocol kinds.
var kindDescriptors = map[string]KindDescriptor{
	MediaKindImage:     {Kind: MediaKindImage, Emoji: "📷", Label: "Photo", CaptionWins: true, MediaBearing: true},
	MediaKindSticker:   {Kind: MediaKindSticker, Emoji: "🎨", Label: "Sticker", MediaBearing: true},
	MediaKindVideo:     {Kind: MediaKindVideo, Emoji: "🎥", Label: "Video", CaptionWins: true, ShowDuration: true, MediaBearing: true},
	MediaKindGIF:       {Kind: MediaKindGIF, Emoji: "🎞️", Label: "GIF", CaptionWins: true, MediaBearing: true},
	MediaKindVideoNote: {Kind: MediaKindVideoNote, Emoji: "📹", Label: "Video message", CaptionWins: true, ShowDuration: true, MediaBearing: true},
	MediaKindVoice:     {Kind: MediaKindVoice, Emoji: "🎤", Label: "Voice message", CaptionWins: true, ShowDuration: true, MediaBearing: true},
	MediaKindAudio:     {Kind: MediaKindAudio, Emoji: "🎵", Label: "Audio", CaptionWins: true, ShowDuration: true, MediaBearing: true},
	MediaKindDocument:  {Kind: MediaKindDocument, Emoji: "📄", Label: "Document", CaptionWins: true, FilenameWins: true, MediaBearing: true},

	// A location's map is fetched and stitched by the daemon, so the row goes
	// through the ordinary download lifecycle and counts as media-bearing.
	MediaKindLocation:     {Kind: MediaKindLocation, Emoji: "📍", Label: "Location", CaptionWins: true, MediaBearing: true},
	MediaKindLiveLocation: {Kind: MediaKindLiveLocation, Emoji: "📍", Label: "Live location", CaptionWins: true, MediaBearing: true},

	MediaKindContact:     {Kind: MediaKindContact, Emoji: "👤", Label: "Contact", CaptionWins: true},
	MediaKindContacts:    {Kind: MediaKindContacts, Emoji: "👥", Label: "Contacts", CaptionWins: true},
	MediaKindPoll:        {Kind: MediaKindPoll, Emoji: "📊", Label: "Poll", CaptionWins: true},
	MediaKindGroupInvite: {Kind: MediaKindGroupInvite, Emoji: "👥", Label: "Group invite", CaptionWins: true},
	// Not marked media-bearing: only an event with a venue has a map to draw,
	// and the kind alone cannot tell those from an event that is a call link or
	// a bare time. MessageCarriesMedia answers that per row.
	MediaKindEvent: {Kind: MediaKindEvent, Emoji: "📅", Label: "Event", CaptionWins: true},
	MediaKindAlbum: {Kind: MediaKindAlbum, Emoji: "🖼️", Label: "Album", CaptionWins: true},
	// A business message and a call log carry no label of their own: what they
	// say is the summary the ingest composed, and prefixing it with the kind
	// would give "💬 Message: your order is on its way" and "📞 Call: missed
	// video call", both of which say less than the summary alone. The glyph
	// still leads the line.
	MediaKindInteractive: {Kind: MediaKindInteractive, Emoji: "💬", CaptionWins: true},
	MediaKindCallLog:     {Kind: MediaKindCallLog, Emoji: "📞"},

	MediaKindProduct:     {Kind: MediaKindProduct, Emoji: "🛍️", Label: "Product", CaptionWins: true},
	MediaKindOrder:       {Kind: MediaKindOrder, Emoji: "🧾", Label: "Order", CaptionWins: true},
	MediaKindPayment:     {Kind: MediaKindPayment, Emoji: "💳", Label: "Payment", CaptionWins: true},
	MediaKindStickerPack: {Kind: MediaKindStickerPack, Emoji: "🎨", Label: "Sticker pack", CaptionWins: true},

	// A system row's whole line is the summary the daemon composed when it
	// coalesced the event ("Ana, Bo and 12 others joined"), so it carries
	// neither glyph nor label here; the pill's icon is a rendering choice.
	MediaKindSystem: {Kind: MediaKindSystem},

	// A message we could not decrypt and have asked the phone to resend. The
	// caption must not win: there is no caption, only a placeholder.
	MediaKindWaiting: {Kind: MediaKindWaiting, Emoji: "⏳", Label: "Waiting for this message"},

	MediaKindUnsupported: {Kind: MediaKindUnsupported, Label: "Unsupported message", CaptionWins: true},
}

// DescribeKind looks up a kind. The second result is false for the empty kind
// (a plain text message) and for anything this build does not know about.
func DescribeKind(kind string) (KindDescriptor, bool) {
	d, ok := kindDescriptors[kind]
	return d, ok
}

// IsMediaBearingKind reports whether a kind carries a downloadable blob. The
// wire's `media` object, the gallery and every auto-download decision key off
// this, so a poll never looks like something waiting to be fetched.
func IsMediaBearingKind(kind string) bool {
	d, ok := kindDescriptors[kind]
	return ok && d.MediaBearing
}

// MessageCarriesMedia is the same question asked of one row rather than of a
// kind, and it is the one every caller actually wants.
//
// The kind table answers for kinds whose every row has something to fetch. An
// event is the exception: it has a map only when its author attached a venue,
// and an event that is a call link or a bare time has nothing. Answering from
// the kind alone would hang a download button on the ones that do not.
func MessageCarriesMedia(m Message) bool {
	if IsMediaBearingKind(m.MediaKind) {
		return true
	}
	return m.MediaKind == MediaKindEvent && DecodePayload(m.PayloadJSON).Event.HasVenue()
}

// HasVenue reports whether an event names a place, which is what decides
// whether it has a map at all.
func (p *EventPayload) HasVenue() bool {
	return p != nil && p.Location != nil && (p.Location.Latitude != 0 || p.Location.Longitude != 0)
}

// PreviewFacts is everything the one-line renderings need from a message. It
// exists so the four call sites cannot drift apart by passing different subsets.
type PreviewFacts struct {
	Text string
	// PayloadSummary is the kind-specific detail the ingest already computed:
	// a poll's question, a location's place name, a system event's sentence.
	PayloadSummary string
	MediaKind      string
	MediaMimeType  string
	MediaFileName  string
	DurationSecs   int32
	Revoked        bool
	// ViewOnce marks our own view-once sends, rendered with a one-eye marker
	// so they are distinguishable from ordinary media in every frontend,
	// including ones that never heard of view-once.
	ViewOnce bool
}

// PreviewLine renders a message as one human-readable line. It is the wire
// `fallback`, the chat row's preview and the body of a reconstructed quote, all
// at once, so those three can never disagree again.
func PreviewLine(f PreviewFacts) string {
	if f.Revoked {
		return RevokedPreview
	}

	caption := oneLine(f.Text)
	if f.ViewOnce {
		if caption != "" {
			return caption
		}
		switch f.MediaKind {
		case MediaKindImage:
			return "👁 View-once photo"
		case MediaKindVideo:
			return "👁 View-once video" + durationSuffix(f.DurationSecs)
		case MediaKindVoice:
			return "👁 View-once voice message" + durationSuffix(f.DurationSecs)
		case MediaKindAudio:
			return "👁 View-once audio" + durationSuffix(f.DurationSecs)
		}
	}
	descriptor, known := DescribeKind(f.MediaKind)
	if !known {
		if caption != "" {
			return caption
		}
		// Text messages land here with nothing to say, and so do rows written
		// by an older build whose kind this one has never heard of. Sniffing
		// the mime type is the only thing left.
		return legacyMimePreview(f.MediaKind, f.MediaMimeType)
	}

	if descriptor.FilenameWins {
		if name := oneLine(f.MediaFileName); name != "" {
			return descriptor.prefix() + name
		}
	}
	if descriptor.CaptionWins && caption != "" {
		return caption
	}

	label := descriptor.Label
	if descriptor.ShowDuration {
		label += durationSuffix(f.DurationSecs)
	}
	detail := oneLine(f.PayloadSummary)
	if label == "" {
		// Kinds whose whole line is the summary the ingest composed: a system
		// event, a call log, a business message. The glyph, where there is one,
		// still leads it. With no summary there is nothing to lead, and a bare
		// glyph on its own line says less than an empty preview.
		if detail == "" {
			return ""
		}
		return strings.TrimSpace(descriptor.prefix() + detail)
	}
	line := strings.TrimSpace(descriptor.prefix() + label)
	if detail != "" {
		return line + ": " + detail
	}
	return line
}

// MessagePreviewLine renders a stored message as one line.
func MessagePreviewLine(m Message) string {
	return PreviewLine(PreviewFacts{
		Text:           m.Text,
		PayloadSummary: m.PayloadSummary,
		MediaKind:      m.MediaKind,
		MediaMimeType:  m.MediaMimeType,
		MediaFileName:  m.MediaFileName,
		DurationSecs:   m.MediaDurationSecs,
		Revoked:        m.IsRevoked,
		ViewOnce:       m.IsViewOnce,
	})
}

// ReplyPreviewLine renders the quoted message behind a reply. The stored quote
// keeps only kind and mime, so this is deliberately thinner than the full line.
func ReplyPreviewLine(reply MessageReply) string {
	return PreviewLine(PreviewFacts{
		Text:          reply.Text,
		MediaKind:     reply.MediaKind,
		MediaMimeType: reply.MediaMimeType,
	})
}

// RevokedPreview is the one-line rendering of a deleted message, shared by the
// wire fallback and the chat-list recompute.
const RevokedPreview = "This message was deleted"

func (d KindDescriptor) prefix() string {
	if d.Emoji == "" {
		return ""
	}
	return d.Emoji + " "
}

// legacyMimePreview handles rows written before media_kind carried anything but
// image and sticker, where the mime type is all there is to go on.
func legacyMimePreview(mediaKind, mediaMimeType string) string {
	switch {
	case strings.HasPrefix(mediaMimeType, "image/"):
		return "📷 Photo"
	case strings.HasPrefix(mediaMimeType, "video/"):
		return "🎥 Video"
	case strings.HasPrefix(mediaMimeType, "audio/"):
		return "🎵 Audio"
	case mediaKind != "" || mediaMimeType != "":
		return "📎 Media"
	default:
		return ""
	}
}

// durationSuffix renders " (0:12)" for a known duration and nothing for an
// unknown one, so a line reads "🎤 Voice message (0:12)".
func durationSuffix(seconds int32) string {
	if seconds <= 0 {
		return ""
	}
	return fmt.Sprintf(" (%d:%02d)", seconds/60, seconds%60)
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
