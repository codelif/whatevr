package wa

import (
	"context"
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
	appstore "whatevrd/internal/store"
)

const maxAvatarBytes = 5 * 1024 * 1024

const (
	availableAvatarTTL            = 24 * time.Hour
	notSetAvatarTTL               = 24 * time.Hour
	errorAvatarTTL                = 24 * time.Hour
	avatarQueueSize               = 256
	historySyncAvatarDeferralIdle = 45 * time.Second
)

type avatarJob struct {
	subject appstore.AvatarSubject
	force   bool
}

func (c *Client) startAvatarWorker(ctx context.Context) {
	if c.avatarQueue == nil {
		c.avatarQueue = make(chan avatarJob, avatarQueueSize)
		c.avatarQueued = make(map[appstore.AvatarSubject]bool)
		c.avatarDeferred = make(map[appstore.AvatarSubject]avatarJob)
	}
	c.startRunGoroutine(func() { c.runAvatarWorker(ctx) })
}

func (c *Client) runAvatarWorker(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case job := <-c.avatarQueue:
			c.avatarMu.Lock()
			delete(c.avatarQueued, job.subject)
			c.avatarMu.Unlock()
			if c.deferAvatarJobIfHistorySyncActive(job) {
				continue
			}
			c.fetchAvatarJob(ctx, job)
			if !c.historySyncBlocksAvatarFetch() {
				c.flushDeferredAvatarJobs()
			}
		}
	}
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

func (c *Client) refreshAvatarIfDue(ctx context.Context, subject appstore.AvatarSubject) {
	if c.historySyncBlocksAvatarFetch() {
		return
	}
	c.ensureAvatarQueuedIfDue(ctx, subject)
}

func (c *Client) ensureAvatarQueuedIfDue(ctx context.Context, subject appstore.AvatarSubject) bool {
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
	return c.enqueueAvatar(subject, false)
}

func (c *Client) ensureAvatar(ctx context.Context, subject appstore.AvatarSubject) (appstore.Avatar, error) {
	fetchJID := c.fetchJIDForAvatarSubject(ctx, subject)
	avatar, err := c.store.EnsureAvatar(ctx, subject, fetchJID)
	if err != nil {
		return appstore.Avatar{}, err
	}
	if fetchJID == "" && avatar.Status == "" {
		return c.store.UpdateAvatarStatus(ctx, subject, appstore.AvatarStatusUnresolved, "", time.Now().Add(errorAvatarTTL))
	}
	return avatar, nil
}

func (c *Client) RefreshLoadedChatAvatars(ctx context.Context, chatID string, messages []appstore.Message) {
	if c.store == nil || strings.TrimSpace(chatID) == "" || c.historySyncBlocksAvatarFetch() {
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
		c.ensureAvatarQueuedIfDue(ctx, subject)
	}
}

func (c *Client) refreshOpenedChatAvatars(ctx context.Context, chatID string) {
	if c.store == nil || strings.TrimSpace(chatID) == "" || c.historySyncBlocksAvatarFetch() {
		return
	}
	messages, err := c.store.ListMessages(ctx, chatID, 200, "")
	if err != nil {
		c.refreshAvatarIfDue(ctx, appstore.AvatarSubject{Kind: appstore.AvatarSubjectChat, ID: chatID})
		return
	}
	c.RefreshLoadedChatAvatars(ctx, chatID, messages)
}

func (c *Client) startProfilePictureSync(ctx context.Context) {
	if c.store == nil {
		return
	}
	c.historySyncMu.Lock()
	if c.profilePictureSyncRunning {
		c.historySyncMu.Unlock()
		return
	}
	c.profilePictureSyncRunning = true
	c.profilePictureSyncActive = true
	c.historySyncMu.Unlock()

	go c.runProfilePictureSync(ctx)
}

func (c *Client) runProfilePictureSync(ctx context.Context) {
	defer func() {
		c.historySyncMu.Lock()
		c.profilePictureSyncActive = false
		c.profilePictureSyncRunning = false
		c.historySyncMu.Unlock()
		c.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
			SyncType:        app.HistorySyncTypeProfilePicture,
			ProgressPercent: 100,
			IsComplete:      true,
		})
		c.flushDeferredAvatarJobs()
	}()

	jobs, err := c.profilePictureSyncJobs(ctx)
	if err != nil {
		c.log.Warnf("Failed to list profile picture sync subjects: %v", err)
		return
	}
	total := len(jobs)
	c.publishProfilePictureSyncProgress(0, total)
	if total == 0 {
		return
	}

	for i, job := range jobs {
		if ctx.Err() != nil {
			return
		}
		avatar, err := c.ensureAvatar(ctx, job.subject)
		if err != nil {
			c.log.Warnf("Failed to ensure profile picture %s/%s: %v", job.subject.Kind, job.subject.ID, err)
			c.publishProfilePictureSyncProgress(i+1, total)
			continue
		}
		if c.avatarNeedsFetch(avatar, job.force) {
			c.fetchAvatarJob(ctx, job)
		}
		c.publishProfilePictureSyncProgress(i+1, total)
	}
}

func (c *Client) profilePictureSyncJobs(ctx context.Context) ([]avatarJob, error) {
	subjects, err := c.store.ListKnownAvatarSubjects(ctx)
	if err != nil {
		return nil, err
	}
	seen := make(map[appstore.AvatarSubject]bool, len(subjects))
	out := make([]avatarJob, 0, len(subjects))
	appendJob := func(job avatarJob) {
		subject := job.subject
		subject = normalizeAvatarSubject(subject)
		if subject.Kind == "" || subject.ID == "" || seen[subject] {
			return
		}
		seen[subject] = true
		job.subject = subject
		out = append(out, job)
	}
	for _, subject := range subjects {
		appendJob(avatarJob{subject: subject})
	}

	c.avatarMu.Lock()
	for subject, job := range c.avatarDeferred {
		appendJob(job)
		delete(c.avatarDeferred, subject)
	}
	c.avatarMu.Unlock()
	return out, nil
}

func (c *Client) publishProfilePictureSyncProgress(done, total int) {
	progress := uint32(100)
	if total > 0 {
		progress = uint32(done * 100 / total)
	}
	c.daemon.PublishHistorySyncProgress(app.HistorySyncEvent{
		SyncType:             app.HistorySyncTypeProfilePicture,
		ProgressPercent:      progress,
		ConversationsInChunk: uint32(done),
		MessagesInChunk:      uint32(total),
	})
}

func (c *Client) enqueueAvatar(subject appstore.AvatarSubject, force bool) bool {
	c.avatarMu.Lock()
	if c.avatarQueue == nil {
		c.avatarQueue = make(chan avatarJob, avatarQueueSize)
		c.avatarQueued = make(map[appstore.AvatarSubject]bool)
		c.avatarDeferred = make(map[appstore.AvatarSubject]avatarJob)
	}
	if c.avatarQueued[subject] {
		c.avatarMu.Unlock()
		return true
	}
	if existing, ok := c.avatarDeferred[subject]; ok {
		if force && !existing.force {
			existing.force = true
			c.avatarDeferred[subject] = existing
		}
		c.avatarMu.Unlock()
		return true
	}
	if c.historySyncBlocksAvatarFetch() {
		c.avatarDeferred[subject] = avatarJob{subject: subject, force: force}
		c.avatarMu.Unlock()
		return true
	}
	c.avatarQueued[subject] = true
	c.avatarMu.Unlock()

	select {
	case c.avatarQueue <- avatarJob{subject: subject, force: force}:
		return true
	default:
		c.avatarMu.Lock()
		delete(c.avatarQueued, subject)
		c.avatarMu.Unlock()
		return false
	}
}

func (c *Client) deferAvatarJobIfHistorySyncActive(job avatarJob) bool {
	if !c.historySyncBlocksAvatarFetch() {
		return false
	}
	c.avatarMu.Lock()
	if c.avatarDeferred == nil {
		c.avatarDeferred = make(map[appstore.AvatarSubject]avatarJob)
	}
	if existing, ok := c.avatarDeferred[job.subject]; ok {
		job.force = job.force || existing.force
	}
	c.avatarDeferred[job.subject] = job
	c.avatarMu.Unlock()
	return true
}

func (c *Client) flushDeferredAvatarJobs() {
	c.avatarMu.Lock()
	if len(c.avatarDeferred) == 0 {
		c.avatarMu.Unlock()
		return
	}
	deferred := len(c.avatarDeferred)
	flushed := 0
	if c.avatarQueue == nil {
		c.avatarQueue = make(chan avatarJob, avatarQueueSize)
	}
	if c.avatarQueued == nil {
		c.avatarQueued = make(map[appstore.AvatarSubject]bool)
	}
	for subject, job := range c.avatarDeferred {
		if c.avatarQueued[subject] {
			delete(c.avatarDeferred, subject)
			continue
		}
		select {
		case c.avatarQueue <- job:
			c.avatarQueued[subject] = true
			delete(c.avatarDeferred, subject)
			flushed++
		default:
			remaining := len(c.avatarDeferred)
			c.avatarMu.Unlock()
			c.log.Debugf("Avatar deferral flush queued %d/%d jobs; %d remain deferred", flushed, deferred, remaining)
			return
		}
	}
	c.avatarMu.Unlock()
	c.log.Debugf("Avatar deferral flush queued %d/%d jobs", flushed, deferred)
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
		updated, updateErr := c.store.UpdateAvatarStatus(ctx, job.subject, appstore.AvatarStatusUnresolved, "", time.Now().Add(errorAvatarTTL))
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
		updated, updateErr := c.updateAvatarError(ctx, job.subject, err)
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

func (c *Client) updateAvatarError(ctx context.Context, subject appstore.AvatarSubject, err error) (appstore.Avatar, error) {
	if status := avatarStatusForError(err); status != "" {
		ttl := errorAvatarTTL
		if status == appstore.AvatarStatusNotSet {
			ttl = notSetAvatarTTL
		}
		return c.store.UpdateAvatarStatus(ctx, subject, status, err.Error(), time.Now().Add(ttl))
	}
	if isTransientAvatarError(err) {
		return c.store.UpdateAvatarTransientError(ctx, subject, err.Error(), time.Now().Add(errorAvatarTTL))
	}
	c.log.Warnf("Avatar refresh: failed for %s/%s: %v", subject.Kind, subject.ID, err)
	return c.store.UpdateAvatarTransientError(ctx, subject, err.Error(), time.Now().Add(errorAvatarTTL))
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

func isTransientAvatarError(err error) bool {
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
	if client == nil || !client.IsLoggedIn() || ctx.Err() != nil {
		return "", "", nil
	}

	info, err := client.GetProfilePictureInfo(ctx, jid, &whatsmeow.GetProfilePictureParams{
		Preview:    false,
		ExistingID: existingPicID,
	})
	if err != nil {
		return "", "", err
	}
	if info == nil {
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
	destPath := filepath.Join(avatarDir, safeID+ext)
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
		c.ensureAvatarQueuedIfDue(ctx, subject)
	}
}
