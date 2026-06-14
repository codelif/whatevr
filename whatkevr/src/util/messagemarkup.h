#pragma once

#include <QList>
#include <QString>
#include <QStringList>

namespace whatevr::util {

struct MessageMarkup {
    int emojiOnlyCount = 0;
    bool hasRichText = false;
    QString richText;
    QString layoutText;
};

// One @-mention to weave into the rich text: the JID's user-part is matched
// against the `@<userpart>` token in the message body and rendered as the
// display name, linked via the `wamention:<jid>` scheme.
struct MessageMention {
    QString jid;
    QString displayName;
};

// Parse WhatsApp's small markdown subset into Qt rich text. Plain messages stay
// on the QML PlainText path; richText is populated only when formatting, emoji
// enlargement, or an @-mention is actually present. `mentions` linkifies the
// `@<userpart>` tokens to `wamention:<jid>`; in a group the literal `@all` /
// `@everyone` tokens linkify to `wamention-all:` regardless of `mentions`.
MessageMarkup parseWhatsAppMessageMarkup(const QString &text, const QList<MessageMention> &mentions = {}, bool isGroup = false);

// Collect the unique clickable links the rich-text path would linkify, in
// order of first appearance. Bare domains get the same https:// scheme the
// anchors use; code spans are skipped exactly like the renderer skips them.
QStringList extractMessageLinks(const QString &text);

// Convert WhatsApp's markup subset to CommonMark: *b*→**b**, _i_→*i*,
// ~s~→~~s~~, inline and fenced code, quotes and lists. Plain segments have
// CommonMark metacharacters escaped so the text round-trips verbatim.
QString whatsAppToCommonMark(const QString &text);

}
