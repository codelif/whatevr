package whatsapp

import (
	"context"
	"errors"
	"strings"
	"sync"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/appstate"
	"go.mau.fi/whatsmeow/proto/waCommon"
	"go.mau.fi/whatsmeow/proto/waE2E"
	"go.mau.fi/whatsmeow/proto/waSyncAction"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"
	"google.golang.org/protobuf/proto"

	"whatevrd/internal/core"
	"whatevrd/internal/live"
	"whatevrd/internal/model"
)

const (
	// whatsapp's own limit on pins in one chat
	maxPins = 3
	// a pin that does not say lasts a week
	pinDefault = 7 * 24 * time.Hour
	// older history is asked for this many at a time
	olderCount = 50
	// a request for older history the phone never answers is dropped after
	olderTimeout = 90 * time.Second
)

// acted is a message an action points at, and what a key to it needs.
type acted struct {
	cli *whatsmeow.Client
	m   model.Message
	w   *model.World
	// chat is the address the message came under, sender who sent it,
	// empty for ours
	chat, sender types.JID
}

func (c *Client) acted(ctx context.Context, ref Ref) (acted, error) {
	cli, err := c.connected()
	if err != nil {
		return acted{}, err
	}
	m, w, err := c.message(ctx, ref)
	if err != nil {
		return acted{}, err
	}
	chat, err := types.ParseJID(m.Chat)
	if err != nil {
		return acted{}, Errorf(ErrInvalid, "chat %q", m.Chat)
	}
	a := acted{cli: cli, m: m, w: w, chat: chat}
	if !m.FromMe && m.Sender != model.Me {
		s := m.Sender
		if s == "" && !model.IsGroup(m.Chat) {
			s = m.Chat
		}
		a.sender, _ = types.ParseJID(s)
	}
	return a, nil
}

func (a acted) key() *waCommon.MessageKey {
	return a.cli.BuildMessageKey(a.chat, a.sender, types.MessageID(a.m.ID))
}

// sendNow sends body into a's chat right away, past the queue: what it says
// is about a message already there, and it is worth nothing late.
func (c *Client) sendNow(ctx context.Context, a acted, body *waE2E.Message) error {
	if err := c.guard(ctx, a.chat); err != nil {
		return err
	}
	if _, err := a.cli.SendMessage(ctx, a.chat, body); err != nil {
		if errors.Is(err, whatsmeow.ErrNotConnected) || errors.Is(err, whatsmeow.ErrNotLoggedIn) {
			return Errorf(ErrNotConnected, "%v", err)
		}
		return Errorf(ErrRejected, "%v", err)
	}
	c.waitLogged(ctx)
	return nil
}

// React puts emoji on a message, or takes ours off with "".
func (c *Client) React(ctx context.Context, ref Ref, emoji string) error {
	a, err := c.acted(ctx, ref)
	if err != nil {
		return err
	}
	if a.m.Facts.Revoked {
		return Errorf(ErrRejected, "the message was deleted")
	}
	return c.sendNow(ctx, a, a.cli.BuildReaction(a.chat, a.sender, types.MessageID(a.m.ID), emoji))
}

// Edit changes the text of a message of ours, or a photo's caption.
func (c *Client) Edit(ctx context.Context, ref Ref, text string) error {
	a, err := c.acted(ctx, ref)
	if err != nil {
		return err
	}
	if !a.m.FromMe {
		return Errorf(ErrRejected, "only your own messages can be edited")
	}
	if a.m.Facts.Revoked {
		return Errorf(ErrRejected, "the message was deleted")
	}
	if time.Since(time.UnixMilli(a.m.T)) > whatsmeow.EditWindow {
		return Errorf(ErrExpired, "the edit window for this message has passed")
	}
	raw, _ := a.m.Content()
	cur := model.Unwrap(raw).Msg
	if e := a.m.Facts.Edit; e != nil {
		cur = model.Unwrap(e).Msg
	}
	var content *waE2E.Message
	switch {
	case cur.GetImageMessage() != nil:
		img := proto.Clone(cur.GetImageMessage()).(*waE2E.ImageMessage)
		img.Caption = proto.String(text)
		content = &waE2E.Message{ImageMessage: img}
	case cur.GetExtendedTextMessage() != nil:
		// the reply and mentions stay; the link preview went with the old text
		ci := cur.GetExtendedTextMessage().GetContextInfo()
		content = &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: proto.String(text), ContextInfo: ci}}
	case cur.GetConversation() != "":
		content = &waE2E.Message{Conversation: proto.String(text)}
	default:
		return Errorf(ErrRejected, "this message can't be edited")
	}
	return c.sendNow(ctx, a, a.cli.BuildEdit(a.chat, types.MessageID(a.m.ID), content))
}

// Revoke deletes a message of ours for everyone.
func (c *Client) Revoke(ctx context.Context, ref Ref) error {
	a, err := c.acted(ctx, ref)
	if err != nil {
		return err
	}
	if !a.m.FromMe {
		return Errorf(ErrRejected, "only your own messages can be deleted for everyone")
	}
	if a.m.Facts.Revoked {
		return nil
	}
	if err := c.sendNow(ctx, a, a.cli.BuildRevoke(a.chat, types.EmptyJID, types.MessageID(a.m.ID))); err != nil {
		if errors.Is(err, ErrRejected) && time.Since(time.UnixMilli(a.m.T)) > revokeWindow {
			return Errorf(ErrExpired, "the window to delete this for everyone has passed")
		}
		return err
	}
	return nil
}

// revokeWindow is roughly how long whatsapp takes a delete for everyone;
// only used to name a refusal
const revokeWindow = 60 * time.Hour

// DeleteForMe deletes a message on every device of ours, not for the chat.
func (c *Client) DeleteForMe(ctx context.Context, ref Ref) error {
	a, err := c.acted(ctx, ref)
	if err != nil {
		return err
	}
	fromMe, sender := "1", "0"
	if !a.m.FromMe {
		fromMe = "0"
		if model.IsGroup(a.m.Chat) && !a.sender.IsEmpty() {
			sender = a.sender.ToNonAD().String()
		}
	}
	return c.sendAppState(ctx, a.cli, appstate.PatchInfo{
		Type: appstate.WAPatchRegularHigh,
		Mutations: []appstate.MutationInfo{{
			Index:   []string{appstate.IndexDeleteMessageForMe, a.chat.String(), a.m.ID, fromMe, sender},
			Version: 3,
			Value: &waSyncAction.SyncActionValue{DeleteMessageForMeAction: &waSyncAction.DeleteMessageForMeAction{
				DeleteMedia: proto.Bool(true), MessageTimestamp: proto.Int64(a.m.T / 1000)}},
		}},
	})
}

// Star stars a message on every device of ours.
func (c *Client) Star(ctx context.Context, ref Ref, on bool) error {
	a, err := c.acted(ctx, ref)
	if err != nil {
		return err
	}
	sender := a.sender
	if a.m.FromMe {
		sender = a.cli.Store.GetJID().ToNonAD()
	}
	return c.sendAppState(ctx, a.cli, appstate.BuildStar(a.chat, sender, types.MessageID(a.m.ID), a.m.FromMe, on))
}

// Pin pins a message in its chat for everyone, for d or a week.
func (c *Client) Pin(ctx context.Context, ref Ref, on bool, d time.Duration) error {
	a, err := c.acted(ctx, ref)
	if err != nil {
		return err
	}
	if a.m.Facts.Revoked {
		return Errorf(ErrRejected, "the message was deleted")
	}
	now := time.Now()
	if on && !(a.m.Facts.Pinned && a.m.Facts.PinEnd > now.UnixMilli()) {
		pins, err := c.r.Pinned(ctx, a.w.Addrs(a.w.Now(model.Norm(a.m.Chat))))
		if err != nil {
			return err
		}
		n := 0
		for _, p := range pins {
			if p.Facts.PinEnd > now.UnixMilli() {
				n++
			}
		}
		if n >= maxPins {
			return Errorf(ErrRejected, "a chat holds %d pins at most", maxPins)
		}
	}
	if d <= 0 {
		d = pinDefault
	}
	typ := waE2E.PinInChatMessage_PIN_FOR_ALL
	if !on {
		typ = waE2E.PinInChatMessage_UNPIN_FOR_ALL
	}
	body := &waE2E.Message{PinInChatMessage: &waE2E.PinInChatMessage{
		Key: a.key(), Type: typ.Enum(), SenderTimestampMS: proto.Int64(now.UnixMilli())}}
	if on {
		body.MessageContextInfo = &waE2E.MessageContextInfo{
			MessageAddOnDurationInSecs: proto.Uint32(uint32(d / time.Second)),
			MessageAddOnExpiryType:     waE2E.MessageContextInfo_STATIC.Enum(),
		}
	}
	return c.sendNow(ctx, a, body)
}

// Forward queues a copy of a message to each chat, marked forwarded, and
// says the new messages in chats' order. a media copy goes with the
// original's keys, no upload. a repeat under o's key queues only the chats
// the first did not get to.
func (c *Client) Forward(ctx context.Context, ref Ref, chats []string, o Once) ([]Ref, error) {
	var out []Ref
	if o.Key != "" {
		done, err := c.turns.take(ctx, o.Key)
		if err != nil {
			return nil, err
		}
		defer done()
		if out, err = c.earlier(ctx, o); err != nil || len(out) >= len(chats) {
			return out, err
		}
	}
	cli, err := c.loggedIn()
	if err != nil {
		return nil, err
	}
	m, _, err := c.message(ctx, ref)
	if err != nil {
		return nil, err
	}
	if m.Facts.Revoked {
		return nil, Errorf(ErrRejected, "deleted messages can't be forwarded")
	}
	raw, _ := m.Content()
	if e := m.Facts.Edit; e != nil {
		raw = e
	}
	body := forwardable(model.Unwrap(raw).Msg)
	if body == nil {
		return nil, Errorf(ErrRejected, "this message can't be forwarded")
	}
	tos := make([]types.JID, len(chats))
	for i, ch := range chats {
		if tos[i], err = c.sendJID(ch); err != nil {
			return nil, err
		}
		if err := c.guard(ctx, tos[i]); err != nil {
			return nil, err
		}
	}
	for _, to := range tos[len(out):] {
		r, err := c.queue(ctx, cli, to, proto.Clone(body).(*waE2E.Message), "", o)
		if err != nil {
			return out, err
		}
		out = append(out, r)
	}
	return out, nil
}

// forwardable is m as a forward of it goes out, nil for what does not go.
func forwardable(m *waE2E.Message) *waE2E.Message {
	if m == nil {
		return nil
	}
	m = proto.Clone(m).(*waE2E.Message)
	m.MessageContextInfo = nil
	score := uint32(1)
	mark := func(ci **waE2E.ContextInfo) {
		old := *ci
		if old != nil && old.GetForwardingScore() > 0 {
			score = old.GetForwardingScore() + 1
		}
		*ci = &waE2E.ContextInfo{IsForwarded: proto.Bool(true), ForwardingScore: proto.Uint32(score)}
	}
	switch {
	case m.Conversation != nil:
		return &waE2E.Message{ExtendedTextMessage: &waE2E.ExtendedTextMessage{Text: m.Conversation,
			ContextInfo: &waE2E.ContextInfo{IsForwarded: proto.Bool(true), ForwardingScore: proto.Uint32(1)}}}
	case m.ExtendedTextMessage != nil:
		mark(&m.ExtendedTextMessage.ContextInfo)
		return &waE2E.Message{ExtendedTextMessage: m.ExtendedTextMessage}
	case m.ImageMessage != nil:
		mark(&m.ImageMessage.ContextInfo)
		m.ImageMessage.ViewOnce = nil
		return &waE2E.Message{ImageMessage: m.ImageMessage}
	case m.VideoMessage != nil:
		mark(&m.VideoMessage.ContextInfo)
		m.VideoMessage.ViewOnce = nil
		return &waE2E.Message{VideoMessage: m.VideoMessage}
	case m.AudioMessage != nil:
		mark(&m.AudioMessage.ContextInfo)
		return &waE2E.Message{AudioMessage: m.AudioMessage}
	case m.DocumentMessage != nil:
		mark(&m.DocumentMessage.ContextInfo)
		return &waE2E.Message{DocumentMessage: m.DocumentMessage}
	case m.StickerMessage != nil:
		mark(&m.StickerMessage.ContextInfo)
		return &waE2E.Message{StickerMessage: m.StickerMessage}
	case m.LocationMessage != nil:
		mark(&m.LocationMessage.ContextInfo)
		return &waE2E.Message{LocationMessage: m.LocationMessage}
	case m.ContactMessage != nil:
		mark(&m.ContactMessage.ContextInfo)
		return &waE2E.Message{ContactMessage: m.ContactMessage}
	case m.ContactsArrayMessage != nil:
		mark(&m.ContactsArrayMessage.ContextInfo)
		return &waE2E.Message{ContactsArrayMessage: m.ContactsArrayMessage}
	}
	return nil
}

// MarkPlayed tells a voice note's sender we listened. again is nothing.
func (c *Client) MarkPlayed(ctx context.Context, ref Ref) error {
	m, _, err := c.message(ctx, ref)
	if err != nil {
		return err
	}
	if m.Kind != "audioMessage" {
		return Errorf(ErrRejected, "only voice messages can be marked played")
	}
	if m.FromMe || m.Facts.Local.Played {
		return nil
	}
	c.logLocal(ctx, core.LocalHead{Chat: m.Chat, ID: m.ID, Op: core.MediaPlayed})
	c.waitLogged(ctx)
	cli, err := c.connected()
	if err != nil {
		// the flag stands; the receipt is best effort, like a read one
		return nil
	}
	chat, err := types.ParseJID(m.Chat)
	if err != nil {
		return nil
	}
	var sender types.JID
	if model.IsGroup(m.Chat) {
		sender, _ = types.ParseJID(m.Sender)
	}
	if err := cli.MarkRead(ctx, []types.MessageID{types.MessageID(m.ID)}, time.Now(), chat, sender, types.ReceiptTypePlayed); err != nil {
		c.log.Warn().Err(err).Str("id", m.ID).Msg("whatsapp: played receipt")
	}
	return nil
}

// RequestFromPhone asks our own phone to resend a message that did not
// decrypt here.
func (c *Client) RequestFromPhone(ctx context.Context, ref Ref) error {
	a, err := c.acted(ctx, ref)
	if err != nil {
		return err
	}
	if !a.m.Waiting || a.m.Wait != "" {
		return Errorf(ErrRejected, "this message is not waiting for anything")
	}
	sender := a.sender
	if sender.IsEmpty() {
		sender = a.chat
	}
	if _, err := a.cli.SendPeerMessage(ctx, a.cli.BuildUnavailableMessageRequest(a.chat, sender, a.m.ID)); err != nil {
		return Errorf(ErrRejected, "could not ask your phone: %v", err)
	}
	c.logLocal(ctx, core.LocalHead{Chat: a.m.Chat, ID: a.m.ID, Op: core.LocalAsked, N: a.m.Facts.Local.Asked + 1})
	c.waitLogged(ctx)
	return nil
}

// own is our address in the namespace other is in, for what is sealed
// between the two.
func own(cli *whatsmeow.Client, other string) string {
	if strings.HasSuffix(other, "@"+types.DefaultUserServer) {
		return cli.Store.GetJID().ToNonAD().String()
	}
	return cli.Store.GetLID().ToNonAD().String()
}

// sealed is what a vote or an answer is sealed with: the target's secret,
// its sender's address and ours.
func (c *Client) sealed(ctx context.Context, a acted) (secret []byte, orig, mod string, err error) {
	secret, sender, err := c.r.Secret(ctx, []string{a.m.Chat}, a.m.ID)
	if err != nil {
		return nil, "", "", err
	}
	if len(secret) == 0 {
		return nil, "", "", Errorf(ErrRejected, "the message's secret is not here")
	}
	if a.m.FromMe || sender == model.Me || sender == "" {
		// ours, in the namespace the chat speaks
		if strings.HasSuffix(a.m.Chat, "@"+types.DefaultUserServer) {
			orig = a.cli.Store.GetJID().ToNonAD().String()
		} else {
			orig = a.cli.Store.GetLID().ToNonAD().String()
		}
	} else {
		orig = sender
	}
	return secret, orig, own(a.cli, orig), nil
}

// Vote casts our whole selection on a poll; none takes every vote back.
func (c *Client) Vote(ctx context.Context, ref Ref, picks []uint32) error {
	a, err := c.acted(ctx, ref)
	if err != nil {
		return err
	}
	raw, _ := a.m.Content()
	opts := pollOptions(model.Unwrap(raw).Msg)
	if opts == nil {
		return Errorf(ErrInvalid, "the message is not a poll")
	}
	names := make([]string, 0, len(picks))
	for _, i := range picks {
		if int(i) >= len(opts) {
			return Errorf(ErrInvalid, "the poll has no option %d", i)
		}
		names = append(names, opts[i])
	}
	secret, orig, mod, err := c.sealed(ctx, a)
	if err != nil {
		return err
	}
	plain, err := proto.Marshal(&waE2E.PollVoteMessage{SelectedOptions: whatsmeow.HashPollOptions(names)})
	if err != nil {
		return err
	}
	iv, payload, err := model.Seal(model.SealVote, a.m.ID, orig, mod, secret, plain)
	if err != nil {
		return err
	}
	return c.sendNow(ctx, a, &waE2E.Message{PollUpdateMessage: &waE2E.PollUpdateMessage{
		PollCreationMessageKey: a.key(),
		Vote:                   &waE2E.PollEncValue{EncPayload: payload, EncIV: iv},
		SenderTimestampMS:      proto.Int64(time.Now().UnixMilli()),
	}})
}

func pollOptions(m *waE2E.Message) []string {
	p := m.GetPollCreationMessage()
	for _, x := range []*waE2E.PollCreationMessage{m.GetPollCreationMessageV2(), m.GetPollCreationMessageV3(),
		m.GetPollCreationMessageV5(), m.GetPollCreationMessageV6()} {
		if p == nil {
			p = x
		}
	}
	if p == nil {
		return nil
	}
	out := make([]string, 0, len(p.GetOptions()))
	for _, o := range p.GetOptions() {
		out = append(out, o.GetOptionName())
	}
	return out
}

// Rsvp answers an event, with guests when the event takes them.
func (c *Client) Rsvp(ctx context.Context, ref Ref, answer waE2E.EventResponseMessage_EventResponseType, guests uint32) error {
	a, err := c.acted(ctx, ref)
	if err != nil {
		return err
	}
	raw, _ := a.m.Content()
	ev := model.Unwrap(raw).Msg.GetEventMessage()
	if ev == nil {
		return Errorf(ErrInvalid, "the message is not an event")
	}
	if ev.GetIsCanceled() {
		return Errorf(ErrRejected, "the event was cancelled")
	}
	if !ev.GetExtraGuestsAllowed() {
		guests = 0
	}
	secret, orig, mod, err := c.sealed(ctx, a)
	if err != nil {
		return err
	}
	plain, err := proto.Marshal(&waE2E.EventResponseMessage{Response: answer.Enum(),
		TimestampMS: proto.Int64(time.Now().UnixMilli()), ExtraGuestCount: proto.Int32(int32(guests))})
	if err != nil {
		return err
	}
	iv, payload, err := model.Seal(model.SealEvent, a.m.ID, orig, mod, secret, plain)
	if err != nil {
		return err
	}
	return c.sendNow(ctx, a, &waE2E.Message{EncEventResponseMessage: &waE2E.EncEventResponseMessage{
		EventCreationMessageKey: a.key(), EncPayload: payload, EncIV: iv}})
}

// MarkRead sends read receipts for what came into chat up to and with ref,
// and logs them as ours: whatsmeow does not hand back what it sends.
func (c *Client) MarkRead(ctx context.Context, chat string, upTo Ref) error {
	w, err := c.world()
	if err != nil {
		return err
	}
	key := w.Now(model.Norm(chat))
	addrs := w.Addrs(key)
	last, _, err := c.message(ctx, upTo)
	if err != nil {
		return err
	}
	ms, err := c.r.Unseen(ctx, addrs, model.Cursor{T: last.T, Ord: last.Ord, ID: last.ID}, 1000)
	if err != nil {
		return err
	}
	c.notes.read(key)
	if len(ms) == 0 {
		return nil
	}
	cli, cerr := c.connected()
	// per address and sender, as a receipt names one of each
	type batch struct {
		chat, sender string
		ids          []types.MessageID
	}
	var order []string
	batches := map[string]*batch{}
	for _, m := range ms {
		s := ""
		if model.IsGroup(m.Chat) {
			s = m.Sender
		}
		k := m.Chat + " " + s
		b := batches[k]
		if b == nil {
			b = &batch{chat: m.Chat, sender: s}
			batches[k] = b
			order = append(order, k)
		}
		b.ids = append(b.ids, types.MessageID(m.ID))
	}
	now := time.Now()
	for _, k := range order {
		b := batches[k]
		ch, err := types.ParseJID(b.chat)
		if err != nil {
			continue
		}
		var sender types.JID
		if b.sender != "" {
			sender, _ = types.ParseJID(b.sender)
		}
		if cerr == nil {
			if err := cli.MarkRead(ctx, b.ids, now, ch, sender); err != nil {
				c.log.Warn().Err(err).Str("chat", b.chat).Msg("whatsapp: read receipt")
			}
		}
		// logged even offline: read here is read, the receipt is best effort
		evt := &events.Receipt{
			MessageSource: types.MessageSource{Chat: ch, Sender: ownJID(cli), IsFromMe: true, IsGroup: model.IsGroup(b.chat)},
			MessageIDs:    b.ids, Timestamp: now, Type: types.ReceiptTypeReadSelf, MessageSender: sender,
		}
		if err := c.ingest.Event(ctx, evt); err != nil {
			return err
		}
	}
	c.waitLogged(ctx)
	return nil
}

func ownJID(cli *whatsmeow.Client) types.JID {
	if cli == nil {
		return types.EmptyJID
	}
	return cli.Store.GetJID().ToNonAD()
}

// chatTarget is a chat to change app state on, by the address it is known
// to whatsapp under, and its newest message for the ones that want it.
func (c *Client) chatTarget(ctx context.Context, chat string) (*whatsmeow.Client, types.JID, *model.Message, error) {
	cli, err := c.loggedIn()
	if err != nil {
		return nil, types.JID{}, nil, err
	}
	to, err := c.sendJID(chat)
	if err != nil {
		return nil, types.JID{}, nil, err
	}
	w, err := c.world()
	if err != nil {
		return nil, types.JID{}, nil, err
	}
	ms, err := c.r.Messages(ctx, w.Addrs(w.Now(model.Norm(chat))), model.Cursor{}, 1, false)
	if err != nil {
		return nil, types.JID{}, nil, err
	}
	if len(ms) == 0 {
		return cli, to, nil, nil
	}
	return cli, to, &ms[0], nil
}

func (c *Client) lastKey(cli *whatsmeow.Client, last *model.Message) (time.Time, *waCommon.MessageKey) {
	if last == nil {
		return time.Time{}, nil
	}
	chat, err := types.ParseJID(last.Chat)
	if err != nil {
		return time.Time{}, nil
	}
	var sender types.JID
	if !last.FromMe {
		sender, _ = types.ParseJID(last.Sender)
	}
	return time.UnixMilli(last.T), cli.BuildMessageKey(chat, sender, types.MessageID(last.ID))
}

// PinChat pins a chat to the top of the list on every device.
func (c *Client) PinChat(ctx context.Context, chat string, on bool) error {
	cli, to, _, err := c.chatTarget(ctx, chat)
	if err != nil {
		return err
	}
	return c.sendAppState(ctx, cli, appstate.BuildPin(to, on))
}

// ArchiveChat archives a chat on every device.
func (c *Client) ArchiveChat(ctx context.Context, chat string, on bool) error {
	cli, to, last, err := c.chatTarget(ctx, chat)
	if err != nil {
		return err
	}
	t, k := c.lastKey(cli, last)
	return c.sendAppState(ctx, cli, appstate.BuildArchive(to, on, t, k))
}

// MuteChat mutes a chat for d, or for good with 0.
func (c *Client) MuteChat(ctx context.Context, chat string, on bool, d time.Duration) error {
	cli, to, _, err := c.chatTarget(ctx, chat)
	if err != nil {
		return err
	}
	return c.sendAppState(ctx, cli, appstate.BuildMute(to, on, d))
}

// sendAppState sends one patch. a conflict means another device moved
// first: a full fetch of the collection, then one more try. what the patch
// did comes back through the fetch whatsmeow does after, into the log.
func (c *Client) sendAppState(ctx context.Context, cli *whatsmeow.Client, patch appstate.PatchInfo) error {
	c.appState.Lock()
	defer c.appState.Unlock()
	err := cli.SendAppState(ctx, patch)
	if err != nil && conflict(err) {
		c.log.Warn().Err(err).Str("collection", string(patch.Type)).Msg("whatsapp: app state conflict, fetching it whole")
		if ferr := cli.FetchAppState(ctx, patch.Type, true, false); ferr != nil {
			return Errorf(ErrRejected, "WhatsApp sync conflict; try again in a moment")
		}
		err = cli.SendAppState(ctx, patch)
	}
	if err != nil {
		if errors.Is(err, whatsmeow.ErrNotConnected) {
			return Errorf(ErrNotConnected, "%v", err)
		}
		return Errorf(ErrRejected, "%v", err)
	}
	c.waitLogged(ctx)
	return nil
}

func conflict(err error) bool {
	if errors.Is(err, appstate.ErrMismatchingLTHash) {
		return true
	}
	s := err.Error()
	return strings.Contains(s, `code="409"`) || strings.Contains(s, "(409)") || strings.Contains(s, "conflict")
}

// older is the chats with a request for older history out.
type older struct {
	mu  sync.Mutex
	out map[string]*time.Timer
}

// RequestOlder asks the phone for older history of a chat. false is nothing
// to ask: the phone said there is no more, the chat is empty, or a request
// is out already.
func (c *Client) RequestOlder(ctx context.Context, chat string) (bool, error) {
	cli, err := c.connected()
	if err != nil {
		return false, err
	}
	w, err := c.world()
	if err != nil {
		return false, err
	}
	ch, ok, err := c.r.ChatIn(ctx, w, chat)
	if err != nil {
		return false, err
	}
	if !ok || ch.Exhausted {
		return false, nil
	}
	ms, err := c.r.Messages(ctx, ch.Addrs, model.Cursor{}, 1, true)
	if err != nil || len(ms) == 0 {
		return false, err
	}
	first := ms[0]
	c.older.mu.Lock()
	if c.older.out == nil {
		c.older.out = map[string]*time.Timer{}
	}
	if c.older.out[ch.Key] != nil {
		c.older.mu.Unlock()
		return false, nil
	}
	key := ch.Key
	c.older.out[key] = time.AfterFunc(olderTimeout, func() { c.olderDone(key) })
	c.older.mu.Unlock()
	c.live.SetLoadingOlder(key, true)
	jid, err := types.ParseJID(first.Chat)
	if err != nil {
		c.olderDone(key)
		return false, Errorf(ErrInvalid, "chat %q", first.Chat)
	}
	info := &types.MessageInfo{
		MessageSource: types.MessageSource{Chat: jid, IsFromMe: first.FromMe, IsGroup: ch.Group},
		ID:            types.MessageID(first.ID), Timestamp: time.UnixMilli(first.T),
	}
	if _, err := cli.SendPeerMessage(ctx, cli.BuildHistorySyncRequest(info, olderCount)); err != nil {
		c.olderDone(key)
		return false, Errorf(ErrRejected, "%v", err)
	}
	return true, nil
}

func (c *Client) olderDone(chat string) {
	c.older.mu.Lock()
	t := c.older.out[chat]
	if t != nil {
		t.Stop()
		delete(c.older.out, chat)
	}
	c.older.mu.Unlock()
	if t != nil {
		c.live.SetLoadingOlder(chat, false)
	}
}

// olderAnswered clears the requests a logged on-demand blob answers.
func (c *Client) olderAnswered(jids []string) {
	for _, s := range jids {
		if j, err := types.ParseJID(s); err == nil {
			c.olderDone(c.chatKey(j))
		}
	}
}

// PhoneCheck is what whatsapp says of a phone number.
type PhoneCheck struct {
	Registered bool
	// Phone is the number as +digits
	Phone    string
	JID      string
	Name     string
	Business bool
}

// CheckPhone asks whatsapp whether a number has an account.
func (c *Client) CheckPhone(ctx context.Context, phone string) (PhoneCheck, error) {
	digits := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, phone)
	if digits == "" {
		return PhoneCheck{}, Errorf(ErrInvalid, "no digits in %q", phone)
	}
	cli, err := c.connected()
	if err != nil {
		return PhoneCheck{}, err
	}
	res, err := cli.IsOnWhatsApp(ctx, []string{"+" + digits})
	if err != nil {
		return PhoneCheck{}, Errorf(ErrRejected, "%v", err)
	}
	out := PhoneCheck{Phone: "+" + digits}
	for _, r := range res {
		if !r.IsIn {
			continue
		}
		out.Registered, out.JID = true, r.JID.ToNonAD().String()
		if v := r.VerifiedName; v != nil && v.Details != nil {
			if n := strings.TrimSpace(v.Details.GetVerifiedName()); n != "" {
				out.Name, out.Business = n, true
			}
		}
		break
	}
	return out, nil
}

// SetPrivacy changes one privacy setting.
func (c *Client) SetPrivacy(ctx context.Context, name types.PrivacySettingType, value types.PrivacySetting) error {
	cli, err := c.connected()
	if err != nil {
		return err
	}
	s, err := cli.SetPrivacySetting(ctx, name, value)
	if err != nil {
		return Errorf(ErrRejected, "%v", err)
	}
	evt := &events.PrivacySettings{NewSettings: s}
	switch name {
	case types.PrivacySettingTypeGroupAdd:
		evt.GroupAddChanged = true
	case types.PrivacySettingTypeLastSeen:
		evt.LastSeenChanged = true
	case types.PrivacySettingTypeStatus:
		evt.StatusChanged = true
	case types.PrivacySettingTypeProfile:
		evt.ProfileChanged = true
	case types.PrivacySettingTypeReadReceipts:
		evt.ReadReceiptsChanged = true
	case types.PrivacySettingTypeOnline:
		evt.OnlineChanged = true
	case types.PrivacySettingTypeCallAdd:
		evt.CallAddChanged = true
	}
	if err := c.ingest.Event(ctx, evt); err != nil {
		return err
	}
	c.waitLogged(ctx)
	return nil
}

// SetAbout sets our own about text.
func (c *Client) SetAbout(ctx context.Context, text string) error {
	cli, err := c.connected()
	if err != nil {
		return err
	}
	if err := cli.SetStatusMessage(ctx, types.SetStatusInput{Text: &text}); err != nil {
		return Errorf(ErrRejected, "%v", err)
	}
	if self := cli.Store.GetJID(); !self.IsEmpty() {
		c.live.SetAbout(c.personKey(self), live.About{Text: text, At: time.Now()})
	}
	return nil
}

// Block blocks a person, or unblocks them.
func (c *Client) Block(ctx context.Context, person string, on bool) error {
	cli, err := c.connected()
	if err != nil {
		return err
	}
	j, err := c.sendJID(person)
	if err != nil {
		return err
	}
	action := events.BlocklistChangeActionUnblock
	if on {
		action = events.BlocklistChangeActionBlock
	}
	if _, err := cli.UpdateBlocklist(ctx, j.ToNonAD(), action, ""); err != nil {
		return Errorf(ErrRejected, "%v", err)
	}
	evt := &events.Blocklist{Changes: []events.BlocklistChange{{JID: j.ToNonAD(), Action: action}}}
	if err := c.ingest.Event(ctx, evt); err != nil {
		return err
	}
	c.waitLogged(ctx)
	return nil
}
