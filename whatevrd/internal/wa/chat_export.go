package wa

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"time"

	"whatevrd/internal/app"
	appstore "whatevrd/internal/store"
)

// exportTimestampLayout matches the official WhatsApp .txt chat export:
// non-padded month/day/year with 12-hour clock, e.g. "5/7/24, 9:50 AM".
const exportTimestampLayout = "1/2/06, 3:04 PM"

// ExportChat writes the chat transcript to destPath in the official WhatsApp
// .txt export format ("M/D/YY, H:MM AM - Sender: body", media as
// "<Media omitted>"). Revoked rows are omitted — deleted stays deleted. The
// write is atomic (temp file + rename) so a failed export never leaves a
// half-written transcript.
func (c *Client) ExportChat(ctx context.Context, chatID, destPath string) (string, error) {
	chatID = strings.TrimSpace(chatID)
	if chatID == "" {
		return "", app.NewCommandError(app.CommandErrorInvalidArgument, "chat_id is required")
	}
	destPath = strings.TrimSpace(destPath)
	if destPath == "" {
		return "", app.NewCommandError(app.CommandErrorInvalidArgument, "path is required")
	}
	if !filepath.IsAbs(destPath) {
		return "", app.NewCommandError(app.CommandErrorInvalidArgument, "destination path must be absolute")
	}
	if _, err := c.store.GetChat(ctx, chatID); err != nil {
		return "", err
	}
	messages, err := c.store.ListMessagesForExport(ctx, chatID)
	if err != nil {
		return "", err
	}

	var out strings.Builder
	out.WriteString("Messages and calls are end-to-end encrypted. Only people in this chat can read, listen to, or share them.\n")
	for _, message := range messages {
		if message.IsRevoked {
			continue
		}
		line := exportMessageLine(message)
		if line == "" {
			continue
		}
		out.WriteString(line)
		out.WriteString("\n")
	}

	parent := filepath.Dir(destPath)
	if info, err := os.Stat(parent); err != nil || !info.IsDir() {
		return "", app.NewCommandError(app.CommandErrorInvalidArgument, "destination directory does not exist")
	}
	if info, err := os.Lstat(destPath); err == nil {
		if info.Mode()&os.ModeSymlink != 0 {
			return "", app.NewCommandError(app.CommandErrorInvalidArgument, "destination must not be a symlink")
		}
		if !info.Mode().IsRegular() {
			return "", app.NewCommandError(app.CommandErrorInvalidArgument, "destination must be a regular file")
		}
	} else if !os.IsNotExist(err) {
		return "", app.NewCommandError(app.CommandErrorInvalidArgument, "destination is not accessible")
	}
	if err := writeFileAtomic(destPath, []byte(out.String()), 0o600); err != nil {
		return "", app.NewCommandError(app.CommandErrorInternal, "write export file: %v", err)
	}
	return destPath, nil
}

// exportMessageLine renders one transcript line. The body keeps its raw text
// (official exports preserve *markup* verbatim); media of any kind becomes
// "<Media omitted>". Empty results (rows with neither text nor media, e.g.
// pure reaction tombstones) return "" and are skipped.
func exportMessageLine(message appstore.Message) string {
	stamp := time.Unix(message.TimestampUnix, 0).Local().Format(exportTimestampLayout)
	sender := exportSenderName(message)
	body := strings.TrimSpace(message.Text)
	if body == "" {
		if message.MediaKind == "" {
			return ""
		}
		body = "<Media omitted>"
	}
	return stamp + " - " + sender + ": " + body
}

// exportSenderName matches the export convention in the official files:
// "You" for outgoing, display name otherwise, phone user part as fallback.
func exportSenderName(message appstore.Message) string {
	if message.Direction == appstore.DirectionOutgoing {
		return "You"
	}
	if name := strings.TrimSpace(message.SenderName); name != "" {
		return name
	}
	if at := strings.Index(message.SenderID, "@"); at > 0 {
		return message.SenderID[:at]
	}
	return strings.TrimSpace(message.SenderID)
}
