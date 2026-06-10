#pragma once

#include <QString>
#include <QStringList>

namespace whatevr::util {

struct MessageMarkup {
    int emojiOnlyCount = 0;
    bool hasRichText = false;
    QString richText;
    QString layoutText;
};

// Parse WhatsApp's small markdown subset into Qt rich text. Plain messages stay
// on the QML PlainText path; richText is populated only when formatting or emoji
// enlargement is actually needed.
MessageMarkup parseWhatsAppMessageMarkup(const QString &text);

// Collect the unique clickable links the rich-text path would linkify, in
// order of first appearance. Bare domains get the same https:// scheme the
// anchors use; code spans are skipped exactly like the renderer skips them.
QStringList extractMessageLinks(const QString &text);

// Convert WhatsApp's markup subset to CommonMark: *b*→**b**, _i_→*i*,
// ~s~→~~s~~, inline and fenced code, quotes and lists. Plain segments have
// CommonMark metacharacters escaped so the text round-trips verbatim.
QString whatsAppToCommonMark(const QString &text);

}
