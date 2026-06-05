#pragma once

#include <QString>

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

}
