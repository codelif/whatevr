package wa

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"go.mau.fi/whatsmeow"
	"go.mau.fi/whatsmeow/types"
	"go.mau.fi/whatsmeow/types/events"

	"whatevrd/internal/app"
	"whatevrd/internal/notify"
	appstore "whatevrd/internal/store"
)

const maxAvatarBytes = 5 * 1024 * 1024

const (
	// Available avatars are re-verified with a cheap ExistingID round trip;
	// real changes arrive as events.Picture, so verification can be lazy.
	availableAvatarTTL    = 7 * 24 * time.Hour
	notSetAvatarTTL       = 24 * time.Hour
	unauthorizedAvatarTTL = 7 * 24 * time.Hour
	unresolvedAvatarTTL   = 24 * time.Hour
	// Transient errors back off exponentially per retry_count instead of a
	// flat day: min(1min * 2^retries, 1h).
	avatarTransientErrorBase = time.Minute
	avatarTransientErrorMax  = time.Hour

	// Concurrent avatar fetches. Small on purpose: visible avatars should
	// load promptly without crowding the socket that live traffic uses.
	avatarWorkerCount = 2
	// Background fetches pass through a token bucket so a fresh pair with a
	// huge chat list can't hammer the server; visible fetches are unthrottled.
	avatarBackgroundRatePerSec = 2.0
	avatarBackgroundBurst      = 5.0
	// Cap per queue; anything beyond this is dropped (callers re-request).
	avatarQueueLimit = 2048
)

type avatarPriority int

const (
	// avatarPriorityVisible is for subjects that are on screen or about to
	// be (open chat senders, info pages, RequestAvatars from the frontend).
	avatarPriorityVisible avatarPriority = iota
	// avatarPriorityBackground is opportunistic refresh (live messages in
	// unopened chats, the periodic refresher); rate limited.
	avatarPriorityBackground
)

type avatarJob struct {
	subject appstore.AvatarSubject
	force   bool
}

func (c *Client) startAvatarWorker(ctx context.Context) {
	c.avatarMu.Lock()
	if c.avatarQueued == nil {
		c.avatarQueued = make(map[appstore.AvatarSubject]avatarPriority)
	}
	if c.avatarWake == nil {
		c.avatarWake = make(chan struct{}, 1)
	}
	if c.avatarRefreshKick == nil {
		c.avatarRefreshKick = make(chan struct{}, 1)
	}
	c.avatarMu.Unlock()
	for range avatarWorkerCount {
		c.startRunGoroutine(func() { c.runAvatarWorker(ctx) })
	}
	c.startRunGoroutine(func() { c.runAvatarBackgroundRefresher(ctx) })
}

func (c *Client) runAvatarWorker(ctx context.Context) {
	for {
		if ctx.Err() != nil {
			return
		}
		job, priority, ok := c.popAvatarJob()
		if !ok {
			select {
			case <-ctx.Done():
				return
			case <-c.avatarWake:
			}
			continue
		}
		if priority == avatarPriorityBackground {
			if delay := c.takeAvatarBackgroundToken(); delay > 0 {
				// Bucket is empty: put the job back and wait for the refill,
				// yielding immediately if new (possibly visible) work lands.
				c.requeueAvatarJob(job, priority)
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
					return
				case <-timer.C:
				case <-c.avatarWake:
					timer.Stop()
				}
				continue
			}
		}
		c.fetchAvatarJob(ctx, job)
	}
}

// popAvatarJob returns the next queued job, visible-priority first.
func (c *Client) popAvatarJob() (avatarJob, avatarPriority, bool) {
	c.avatarMu.Lock()
	defer c.avatarMu.Unlock()
	if len(c.avatarHigh) > 0 {
		job := c.avatarHigh[0]
		c.avatarHigh = c.avatarHigh[1:]
		delete(c.avatarQueued, job.subject)
		return job, avatarPriorityVisible, true
	}
	if len(c.avatarLow) > 0 {
		job := c.avatarLow[0]
		c.avatarLow = c.avatarLow[1:]
		delete(c.avatarQueued, job.subject)
		return job, avatarPriorityBackground, true
	}
	return avatarJob{}, 0, false
}

// requeueAvatarJob puts a popped job back at the front of its queue.
func (c *Client) requeueAvatarJob(job avatarJob, priority avatarPriority) {
	c.avatarMu.Lock()
	if _, queued := c.avatarQueued[job.subject]; !queued {
		c.avatarQueued[job.subject] = priority
		if priority == avatarPriorityVisible {
			c.avatarHigh = append([]avatarJob{job}, c.avatarHigh...)
		} else {
			c.avatarLow = append([]avatarJob{job}, c.avatarLow...)
		}
	}
	c.avatarMu.Unlock()
}

func (c *Client) wakeAvatarWorkers() {
	select {
	case c.avatarWake <- struct{}{}:
	default:
	}
}

// takeAvatarBackgroundToken consumes one background-fetch token, or returns
// how long until the bucket refills enough for one.
func (c *Client) takeAvatarBackgroundToken() time.Duration {
	c.avatarTokenMu.Lock()
	defer c.avatarTokenMu.Unlock()
	now := time.Now()
	if c.avatarTokensAt.IsZero() {
		c.avatarTokens = avatarBackgroundBurst
	} else {
		c.avatarTokens = min(avatarBackgroundBurst, c.avatarTokens+now.Sub(c.avatarTokensAt).Seconds()*avatarBackgroundRatePerSec)
	}
	c.avatarTokensAt = now
	if c.avatarTokens >= 1 {
		c.avatarTokens--
		return 0
	}
	return time.Duration((1 - c.avatarTokens) / avatarBackgroundRatePerSec * float64(time.Second))
}

func normalizeAvatarSubject(subject appstore.AvatarSubject) appstore.AvatarSubject {
	subject.Kind = strings.TrimSpace(subject.Kind)
	subject.ID = strings.TrimSpace(subject.ID)
	switch subject.Kind {
	case appstore.AvatarSubjectChat, appstore.AvatarSubjectSender:
	default:
		subject.Kind = ""
	}
	if subject.ID == "me" {
		subject.ID = ""
	}
	return subject
}

func (c *Client) refreshAvatarIfDue(ctx context.Context, subject appstore.AvatarSubject, priority avatarPriority) {
	c.ensureAvatarQueuedIfDue(ctx, subject, priority)
}

func (c *Client) ensureAvatarQueuedIfDue(ctx context.Context, subject appstore.AvatarSubject, priority avatarPriority) bool {
	if c.store == nil {
		return false
	}
	subject = normalizeAvatarSubject(subject)
	if subject.Kind == "" || subject.ID == "" || ctx.Err() != nil {
		return false
	}

	avatar, err := c.ensureAvatar(ctx, subject)
	if err != nil {
		c.log.Warnf("Failed to ensure avatar %s/%s: %v", subject.Kind, subject.ID, err)
		return false
	}
	if !c.avatarNeedsFetch(avatar, false) {
		return false
	}
	return c.enqueueAvatar(subject, false, priority)
}

func (c *Client) ensureAvatar(ctx context.Context, subject appstore.AvatarSubject) (appstore.Avatar, error) {
	fetchJID := c.fetchJIDForAvatarSubject(ctx, subject)
	avatar, err := c.store.EnsureAvatar(ctx, subject, fetchJID)
	if err != nil {
		return appstore.Avatar{}, err
	}
	if fetchJID == "" && avatar.Status == "" {
		return c.store.UpdateAvatarStatus(ctx, subject, appstore.AvatarStatusUnresolved, "", time.Now().Add(unresolvedAvatarTTL))
	}
	return avatar, nil
}

func (c *Client) RefreshLoadedChatAvatars(ctx context.Context, chatID string, messages []appstore.Message) {
	if c.store == nil || strings.TrimSpace(chatID) == "" {
		return
	}
	seen := map[appstore.AvatarSubject]bool{}
	subjects := []appstore.AvatarSubject{{Kind: appstore.AvatarSubjectChat, ID: chatID}}
	for _, message := range messages {
		if message.SenderID == "" || message.SenderID == "me" || message.SenderID == chatID {
			continue
		}
		subjects = append(subjects, appstore.AvatarSubject{Kind: appstore.AvatarSubjectSender, ID: message.SenderID})
	}
	for _, subject := range subjects {
		subject = normalizeAvatarSubject(subject)
		if subject.Kind == "" || subject.ID == "" || seen[subject] {
			continue
		}
		seen[subject] = true
		// The user is looking at (or about to look at) this chat.
		c.ensureAvatarQueuedIfDue(ctx, subject, avatarPriorityVisible)
	}
}

func (c *Client) refreshOpenedChatAvatars(ctx context.Context, chatID string) {
	if c.store == nil || strings.TrimSpace(chatID) == "" {
		return
	}
	messages, err := c.store.ListMessages(ctx, chatID, 200, "")
	if err != nil {
		c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: chatID}, avatarPriorityVisible)
		return
	}
	c.RefreshLoadedChatAvatars(ctx, chatID, messages)
}

// enqueueAvatar queues a fetch for the subject, deduping against jobs already
// queued and promoting a queued background job when a visible request for the
// same subject arrives. Returns false when the queue is saturated.
func (c *Client) enqueueAvatar(subject appstore.AvatarSubject, force bool, priority avatarPriority) bool {
	c.avatarMu.Lock()
	if c.avatarQueued == nil {
		c.avatarQueued = make(map[appstore.AvatarSubject]avatarPriority)
	}
	if c.avatarWake == nil {
		c.avatarWake = make(chan struct{}, 1)
	}
	if existing, queued := c.avatarQueued[subject]; queued {
		if priority == avatarPriorityVisible && existing == avatarPriorityBackground {
			// Promote: the subject is now on screen.
			for i, job := range c.avatarLow {
				if job.subject == subject {
					job.force = job.force || force
					c.avatarLow = append(c.avatarLow[:i], c.avatarLow[i+1:]...)
					c.avatarHigh = append(c.avatarHigh, job)
					c.avatarQueued[subject] = avatarPriorityVisible
					break
				}
			}
		} else if force {
			queue := c.avatarHigh
			if existing == avatarPriorityBackground {
				queue = c.avatarLow
			}
			for i, job := range queue {
				if job.subject == subject {
					queue[i].force = true
					break
				}
			}
		}
		c.avatarMu.Unlock()
		c.wakeAvatarWorkers()
		return true
	}
	if priority == avatarPriorityVisible {
		if len(c.avatarHigh) >= avatarQueueLimit {
			c.avatarMu.Unlock()
			return false
		}
		c.avatarHigh = append(c.avatarHigh, avatarJob{subject: subject, force: force})
	} else {
		if len(c.avatarLow) >= avatarQueueLimit {
			c.avatarMu.Unlock()
			return false
		}
		c.avatarLow = append(c.avatarLow, avatarJob{subject: subject, force: force})
	}
	c.avatarQueued[subject] = priority
	c.avatarMu.Unlock()
	c.wakeAvatarWorkers()
	return true
}

// RequestAvatars serves the demand-driven avatar RPC: it returns the current
// cached row for every subject immediately and enqueues fetches for anything
// stale or missing; those land later as AvatarUpdated events.
func (c *Client) RequestAvatars(ctx context.Context, subjects []appstore.AvatarSubject, background bool) []app.Avatar {
	priority := avatarPriorityVisible
	if background {
		priority = avatarPriorityBackground
	}
	out := make([]app.Avatar, 0, len(subjects))
	seen := make(map[appstore.AvatarSubject]bool, len(subjects))
	for _, subject := range subjects {
		subject = normalizeAvatarSubject(subject)
		if subject.Kind == "" || subject.ID == "" || seen[subject] || ctx.Err() != nil {
			continue
		}
		seen[subject] = true
		avatar, err := c.ensureAvatar(ctx, subject)
		if err != nil {
			c.log.Warnf("Failed to ensure requested avatar %s/%s: %v", subject.Kind, subject.ID, err)
			continue
		}
		fetching := false
		if c.avatarNeedsFetch(avatar, false) {
			fetching = c.enqueueAvatar(subject, false, priority)
		}
		daemonAvatar := toDaemonAvatar(avatar)
		daemonAvatar.Fetching = fetching
		out = append(out, daemonAvatar)
	}
	return out
}

// kickAvatarBackgroundRefresh asks the refresher for an immediate pass; used
// when the initial history sync settles so the chat list fills in soon after
// pairing without waiting for the periodic tick.
func (c *Client) kickAvatarBackgroundRefresh() {
	c.avatarMu.Lock()
	kick := c.avatarRefreshKick
	c.avatarMu.Unlock()
	if kick == nil {
		return
	}
	select {
	case kick <- struct{}{}:
	default:
	}
}

// runAvatarBackgroundRefresher periodically re-enqueues due avatars for the
// most recent chats at background priority. This is the tamed replacement for
// the old post-history-sync bulk prefetch: rate-limited, repeatable, and
// never in the way of visible fetches.
func (c *Client) runAvatarBackgroundRefresher(ctx context.Context) {
	const refreshInterval = 6 * time.Hour
	const refreshChatLimit = 200
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		case <-c.avatarRefreshKick:
		}
		client := c.currentClient()
		if client == nil || !client.IsLoggedIn() {
			continue
		}
		c.sweepOrphanAvatarFiles(ctx)
		subjects, err := c.store.ListChatAvatarSubjects(ctx, refreshChatLimit)
		if err != nil {
			c.log.Warnf("Failed to list chats for avatar refresh: %v", err)
			continue
		}
		queued := 0
		for _, subject := range subjects {
			if ctx.Err() != nil {
				return
			}
			if c.ensureAvatarQueuedIfDue(ctx, subject, avatarPriorityBackground) {
				queued++
			}
		}
		if queued > 0 {
			c.log.Debugf("Avatar background refresh queued %d/%d subjects", queued, len(subjects))
		}
	}
}

func (c *Client) avatarNeedsFetch(avatar appstore.Avatar, force bool) bool {
	if force {
		return true
	}
	if avatar.FetchJID == "" {
		return false
	}
	now := time.Now().Unix()
	if avatar.LocalPath != "" && avatarLocalFileExists(avatar.LocalPath) && avatar.NextCheckAt > now {
		return false
	}
	if avatar.Status != "" && avatar.NextCheckAt > now {
		return false
	}
	return true
}

func (c *Client) fetchAvatarJob(ctx context.Context, job avatarJob) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() || ctx.Err() != nil {
		return
	}
	avatar, err := c.store.GetAvatar(ctx, job.subject)
	if err != nil || !c.avatarNeedsFetch(avatar, job.force) {
		return
	}
	jid, err := types.ParseJID(avatar.FetchJID)
	if err != nil || shouldSkipAvatarJID(jid) {
		updated, updateErr := c.store.UpdateAvatarStatus(ctx, job.subject, appstore.AvatarStatusUnresolved, "", time.Now().Add(unresolvedAvatarTTL))
		if updateErr == nil {
			c.publishAvatarUpdated(updated)
		}
		return
	}

	existingPicID := avatar.PictureID
	if !avatarLocalFileExists(avatar.LocalPath) || job.force {
		existingPicID = ""
	}
	picID, localPath, err := c.fetchAndCacheAvatar(ctx, jid, existingPicID)
	if err != nil {
		if ctx.Err() != nil {
			return
		}
		updated, updateErr := c.updateAvatarError(ctx, job.subject, err, avatar.RetryCount)
		if updateErr == nil {
			c.publishAvatarUpdated(updated)
		}
		return
	}
	var updated appstore.Avatar
	if picID == "" {
		updated, err = c.store.MarkAvatarUnchanged(ctx, job.subject, time.Now().Add(availableAvatarTTL))
	} else {
		updated, err = c.store.UpdateAvatarAvailable(ctx, job.subject, avatar.FetchJID, picID, localPath, time.Now().Add(availableAvatarTTL))
	}
	if err == nil {
		c.publishAvatarUpdated(updated)
	}
}

func (c *Client) updateAvatarError(ctx context.Context, subject appstore.AvatarSubject, err error, retryCount int32) (appstore.Avatar, error) {
	if status := avatarStatusForError(err); status != "" {
		ttl := unauthorizedAvatarTTL
		if status == appstore.AvatarStatusNotSet {
			ttl = notSetAvatarTTL
		}
		return c.store.UpdateAvatarStatus(ctx, subject, status, err.Error(), time.Now().Add(ttl))
	}
	if !isTransientAvatarError(err) {
		c.log.Warnf("Avatar refresh: failed for %s/%s: %v", subject.Kind, subject.ID, err)
	}
	return c.store.UpdateAvatarTransientError(ctx, subject, err.Error(), time.Now().Add(avatarTransientErrorBackoff(retryCount)))
}

// avatarTransientErrorBackoff returns min(base * 2^retries, max) so flaky
// fetches retry quickly at first instead of going dark for a day.
func avatarTransientErrorBackoff(retryCount int32) time.Duration {
	if retryCount < 0 {
		retryCount = 0
	}
	backoff := avatarTransientErrorBase
	for range retryCount {
		backoff *= 2
		if backoff >= avatarTransientErrorMax {
			return avatarTransientErrorMax
		}
	}
	return backoff
}

func (c *Client) publishAvatarUpdated(avatar appstore.Avatar) {
	if c.daemon == nil {
		return
	}
	c.daemon.PublishAvatarUpdated(toDaemonAvatar(avatar))
}

func toDaemonAvatar(avatar appstore.Avatar) app.Avatar {
	kind := app.AvatarSubjectKindUnspecified
	switch avatar.SubjectKind {
	case appstore.AvatarSubjectChat:
		kind = app.AvatarSubjectKindChat
	case appstore.AvatarSubjectSender:
		kind = app.AvatarSubjectKindSender
	}
	return app.Avatar{Kind: kind, ID: avatar.SubjectID, LocalPath: avatar.LocalPath, Status: avatar.Status, UpdatedAtUnix: avatar.UpdatedAt, Fetching: avatar.Fetching}
}

func (c *Client) fetchJIDForAvatarSubject(ctx context.Context, subject appstore.AvatarSubject) string {
	jid, err := types.ParseJID(subject.ID)
	if err != nil || shouldSkipAvatarJID(jid) {
		return ""
	}
	jid = bareAvatarJID(jid)
	if jid.Server == types.HiddenUserServer {
		pn := c.normalizeJIDForChat(ctx, jid)
		if pn.IsEmpty() || pn.Server == types.HiddenUserServer {
			return ""
		}
		jid = bareAvatarJID(pn)
	}
	if shouldSkipAvatarJID(jid) {
		return ""
	}
	return jid.String()
}

func bareAvatarJID(jid types.JID) types.JID {
	return jid.ToNonAD()
}

func avatarLocalFileExists(path string) bool {
	path = strings.TrimSpace(path)
	if path == "" {
		return false
	}
	info, err := os.Stat(path)
	return err == nil && !info.IsDir()
}

func shouldSkipAvatarJID(jid types.JID) bool {
	return jid.IsEmpty() || jid.User == "0" || jid.Server == types.BroadcastServer || jid.Server == types.NewsletterServer
}

var errAvatarClientNotReady = errors.New("whatsapp client is not connected")

func isTransientAvatarError(err error) bool {
	if errors.Is(err, errAvatarClientNotReady) || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	message := err.Error()
	return strings.Contains(message, "websocket not connected") ||
		strings.Contains(message, "websocket disconnected before info query returned response")
}

func avatarStatusForError(err error) string {
	if errors.Is(err, whatsmeow.ErrProfilePictureUnauthorized) {
		return appstore.AvatarStatusUnauthorized
	}
	if errors.Is(err, whatsmeow.ErrProfilePictureNotSet) {
		return appstore.AvatarStatusNotSet
	}
	return ""
}

func (c *Client) fetchAndCacheAvatar(ctx context.Context, jid types.JID, existingPicID string) (picID, localPath string, err error) {
	client := c.currentClient()
	if client == nil || !client.IsLoggedIn() {
		// Must be an error: a (nil, nil)-style success here used to be cached
		// as "unchanged" for a full TTL, leaving avatars blank for a day.
		return "", "", errAvatarClientNotReady
	}
	if ctx.Err() != nil {
		return "", "", ctx.Err()
	}

	info, err := client.GetProfilePictureInfo(ctx, jid, &whatsmeow.GetProfilePictureParams{
		Preview:    false,
		ExistingID: existingPicID,
	})
	if err != nil {
		return "", "", err
	}
	if info == nil {
		// nil info means "unchanged relative to ExistingID". Without an
		// ExistingID there is nothing to be unchanged relative to; record it
		// as "no picture set" rather than a bogus available state.
		if existingPicID == "" {
			return "", "", whatsmeow.ErrProfilePictureNotSet
		}
		return "", "", nil
	}

	avatarDir := filepath.Join(c.paths.MediaCacheDir, "avatars")
	if err := os.MkdirAll(avatarDir, 0o700); err != nil {
		return "", "", err
	}

	safeID := strings.NewReplacer("/", "_", ":", "_", "@", "_").Replace(jid.String())
	tmpPath := filepath.Join(avatarDir, safeID+".tmp")
	ext, err := downloadAvatarFile(ctx, info.URL, tmpPath)
	if err != nil {
		_ = os.Remove(tmpPath)
		return "", "", err
	}
	// Content-addressed destination: a changed picture gets a new path, so
	// file:// consumers (Qt keys its pixmap cache on the URL) can never show
	// a stale image for a reused path. Old files are removed by the orphan
	// sweep once no avatar row references them.
	picIDSum := sha256.Sum256([]byte(info.ID))
	destPath := filepath.Join(avatarDir, safeID+"-"+hex.EncodeToString(picIDSum[:8])+ext)
	if err := os.Rename(tmpPath, destPath); err != nil {
		_ = os.Remove(tmpPath)
		return "", "", err
	}

	return info.ID, destPath, nil
}

func downloadAvatarFile(ctx context.Context, url, destPath string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}

	client := &http.Client{Timeout: 15 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", errors.New("avatar download returned non-success status")
	}
	if resp.ContentLength > maxAvatarBytes {
		return "", errors.New("avatar image is too large")
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxAvatarBytes+1))
	if err != nil {
		return "", err
	}
	if len(data) == 0 || len(data) > maxAvatarBytes {
		return "", errors.New("avatar image size is outside allowed bounds")
	}
	ext, ok := outboundImageExtension(http.DetectContentType(data))
	if !ok {
		return "", errors.New("avatar image has unsupported content type")
	}
	if err := writeFileAtomic(destPath, data, 0o600); err != nil {
		return "", err
	}
	return ext, nil
}

// sweepOrphanAvatarFiles deletes files in the avatar cache directory that no
// avatars row references: leftovers from the legacy stable-name scheme,
// content-addressed files replaced by a newer picture, and stray .tmp files.
// Files modified within the last hour are kept — they may belong to a fetch
// whose DB row hasn't committed yet.
func (c *Client) sweepOrphanAvatarFiles(ctx context.Context) {
	avatarDir := filepath.Join(c.paths.MediaCacheDir, "avatars")
	entries, err := os.ReadDir(avatarDir)
	if err != nil || len(entries) == 0 {
		return
	}
	referenced, err := c.store.ListAvatarLocalPaths(ctx)
	if err != nil {
		c.log.Warnf("Failed to list avatar paths for sweep: %v", err)
		return
	}
	refSet := make(map[string]bool, len(referenced))
	for _, path := range referenced {
		refSet[path] = true
	}
	cutoff := time.Now().Add(-time.Hour)
	removed := 0
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		path := filepath.Join(avatarDir, entry.Name())
		if refSet[path] {
			continue
		}
		info, err := entry.Info()
		if err != nil || info.ModTime().After(cutoff) {
			continue
		}
		if err := os.Remove(path); err == nil {
			removed++
		}
	}
	if removed > 0 {
		c.log.Debugf("Swept %d orphaned avatar files", removed)
	}
}

// notifyWithAvatar delivers a message notification, first giving a missing
// chat avatar a brief bounded chance to arrive so the toast shows a face
// instead of the generic app icon. The wait runs detached: the whatsmeow
// event handler must never block, and the notify worker queues asynchronously
// anyway.
func (c *Client) notifyWithAvatar(ctx context.Context, message app.Message, chat app.Chat, opts notify.Options) {
	if c.notifier == nil {
		return
	}
	if strings.TrimSpace(chat.AvatarLocalPath) != "" || c.store == nil {
		c.notifier.NotifyMessage(ctx, message, chat, opts)
		return
	}
	subject := normalizeAvatarSubject(appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: chat.ID})
	if !c.ensureAvatarQueuedIfDue(ctx, subject, avatarPriorityVisible) {
		// Nothing in flight to wait for (no picture, unauthorized, ...).
		c.notifier.NotifyMessage(ctx, message, chat, opts)
		return
	}
	go func() {
		const pollInterval = 150 * time.Millisecond
		deadline := time.Now().Add(1500 * time.Millisecond)
		for time.Now().Before(deadline) && ctx.Err() == nil {
			time.Sleep(pollInterval)
			avatar, err := c.store.GetAvatar(ctx, subject)
			if err != nil {
				break
			}
			if avatar.LocalPath != "" && avatarLocalFileExists(avatar.LocalPath) {
				chat.AvatarLocalPath = avatar.LocalPath
				break
			}
			if avatar.Status != "" {
				// Terminal for this attempt: not set, unauthorized, error.
				break
			}
		}
		c.notifier.NotifyMessage(ctx, message, chat, opts)
	}()
}

func (c *Client) handlePictureEvent(ctx context.Context, evt *events.Picture) {
	if evt == nil || ctx.Err() != nil {
		return
	}
	jid := bareAvatarJID(c.normalizeJIDForChat(ctx, evt.JID))
	if jid.IsEmpty() || shouldSkipAvatarJID(jid) {
		return
	}
	for _, subject := range []appstore.AvatarSubject{{Kind: appstore.AvatarSubjectChat, ID: jid.String()}, {Kind: appstore.AvatarSubjectSender, ID: jid.String()}} {
		if _, err := c.store.EnsureAvatar(ctx, subject, c.fetchJIDForAvatarSubject(ctx, subject)); err != nil {
			continue
		}
		if evt.Remove {
			if avatar, err := c.store.ClearAvatar(ctx, subject, appstore.AvatarStatusNotSet, time.Now().Add(notSetAvatarTTL)); err == nil {
				c.publishAvatarUpdated(avatar)
			}
			continue
		}
		// The picture definitely changed: skip TTL checks and re-fetch now.
		c.enqueueAvatar(subject, true, avatarPriorityVisible)
	}
}
