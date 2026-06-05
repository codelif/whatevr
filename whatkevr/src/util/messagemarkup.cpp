#include "messagemarkup.h"

#include <QFont>
#include <QGuiApplication>
#include <QScreen>
#include <QTextBoundaryFinder>
#include <QVector>

namespace whatevr::util {
namespace {

constexpr double kInlineEmojiScale = 1.3;
constexpr double kFallbackBodyPointSize = 10.0;

bool isEmojiCodepoint(char32_t cp)
{
    return (cp >= 0x1F000 && cp <= 0x1FAFF)   // pictographs, symbols, faces, etc.
        || (cp >= 0x2600 && cp <= 0x27BF)     // misc symbols + dingbats
        || (cp >= 0x2B00 && cp <= 0x2BFF)     // stars, arrows
        || (cp >= 0x1F1E6 && cp <= 0x1F1FF)   // regional indicators (flags)
        || (cp >= 0x2300 && cp <= 0x23FF)     // watches, hourglasses, media controls
        || cp == 0x303D || cp == 0x3030       // part alternation mark, wavy dash
        || cp == 0x2122 || cp == 0x2139       // trade mark, information
        || (cp >= 0x2194 && cp <= 0x21AA);    // arrows with emoji presentation
}

bool isEmojiModifierCodepoint(char32_t cp)
{
    return cp == 0x200D                       // zero-width joiner
        || cp == 0xFE0F || cp == 0xFE0E       // variation selectors
        || (cp >= 0x1F3FB && cp <= 0x1F3FF)   // skin tone modifiers
        || cp == 0x20E3;                      // combining enclosing keycap
}

bool isEmojiCluster(const QString &cluster)
{
    const QVector<uint> cps = cluster.toUcs4();
    if (cps.isEmpty()) {
        return false;
    }

    bool hasKeycap = false;
    for (uint cp : cps) {
        if (cp == 0x20E3) {
            hasKeycap = true;
            break;
        }
    }

    bool hasStandaloneEmoji = false;
    for (uint rawCp : cps) {
        const auto cp = static_cast<char32_t>(rawCp);
        if (isEmojiCodepoint(cp) || isEmojiModifierCodepoint(cp)) {
            hasStandaloneEmoji = hasStandaloneEmoji || isEmojiCodepoint(cp);
            continue;
        }
        if (hasKeycap && (QChar(cp).isDigit() || cp == '#' || cp == '*')) {
            hasStandaloneEmoji = true;
            continue;
        }
        return false;
    }
    return hasStandaloneEmoji;
}

int emojiOnlyClusterCount(const QString &text)
{
    const QString trimmed = text.trimmed();
    if (trimmed.isEmpty()) {
        return 0;
    }

    QTextBoundaryFinder finder(QTextBoundaryFinder::Grapheme, trimmed);
    int count = 0;
    int start = 0;
    finder.toStart();
    while (true) {
        const int end = finder.toNextBoundary();
        if (end < 0) {
            break;
        }
        const QString cluster = trimmed.mid(start, end - start);
        start = end;
        if (cluster.trimmed().isEmpty()) {
            continue;
        }
        if (!isEmojiCluster(cluster)) {
            return 0;
        }
        ++count;
    }
    return count;
}

bool textContainsEmojiCluster(const QString &text)
{
    bool mayContainEmoji = false;
    for (uint rawCp : text.toUcs4()) {
        const auto cp = static_cast<char32_t>(rawCp);
        if (isEmojiCodepoint(cp) || cp == 0x20E3) {
            mayContainEmoji = true;
            break;
        }
    }
    if (!mayContainEmoji) {
        return false;
    }

    QTextBoundaryFinder finder(QTextBoundaryFinder::Grapheme, text);
    int start = 0;
    finder.toStart();
    while (true) {
        const int end = finder.toNextBoundary();
        if (end < 0) {
            break;
        }
        if (isEmojiCluster(text.mid(start, end - start))) {
            return true;
        }
        start = end;
    }
    return false;
}

bool startsWithAt(const QString &text, int index, QStringView marker)
{
    return index >= 0
        && index + marker.size() <= text.size()
        && QStringView{text}.mid(index, marker.size()) == marker;
}

bool lineStartsNumberedList(const QString &text, int start, int end, int *contentStart)
{
    int i = start;
    while (i < end && text.at(i).isDigit()) {
        ++i;
    }
    if (i == start || i + 1 >= end || text.at(i) != QLatin1Char('.') || text.at(i + 1) != QLatin1Char(' ')) {
        return false;
    }
    *contentStart = i + 2;
    return true;
}

bool textMayContainWhatsAppMarkup(const QString &text)
{
    bool atLineStart = true;
    for (int i = 0; i < text.size(); ++i) {
        const QChar ch = text.at(i);
        if (ch == QLatin1Char('\n')) {
            atLineStart = true;
            continue;
        }
        if (ch == QLatin1Char('_') || ch == QLatin1Char('*') || ch == QLatin1Char('~') || ch == QLatin1Char('`')) {
            return true;
        }
        if (atLineStart) {
            if ((ch == QLatin1Char('>') || ch == QLatin1Char('-')) && i + 1 < text.size() && text.at(i + 1) == QLatin1Char(' ')) {
                return true;
            }
            if (ch.isDigit()) {
                int j = i + 1;
                while (j < text.size() && text.at(j).isDigit()) {
                    ++j;
                }
                if (j + 1 < text.size() && text.at(j) == QLatin1Char('.') && text.at(j + 1) == QLatin1Char(' ')) {
                    return true;
                }
            }
        }
        atLineStart = false;
    }
    return false;
}

double bodyBasePointSize()
{
    if (qGuiApp) {
        const QFont font = qGuiApp->font();
        if (font.pointSizeF() > 0) {
            return font.pointSizeF();
        }
        if (font.pixelSize() > 0) {
            if (QScreen *screen = qGuiApp->primaryScreen()) {
                const qreal dpi = screen->logicalDotsPerInchY();
                if (dpi > 0) {
                    return font.pixelSize() * 72.0 / dpi;
                }
            }
        }
    }
    return kFallbackBodyPointSize;
}

QString emojiSpanOpenTag()
{
    return QStringLiteral("<span style=\"font-size:%1pt\">")
        .arg(QString::number(bodyBasePointSize() * kInlineEmojiScale, 'f', 2));
}

struct HtmlBuildContext {
    bool hasFormatting = false;
    bool hasEmoji = false;
    QString emojiSpanOpen;
};

void appendEscapedChar(QChar ch, QString &html, bool preserveSpaces)
{
    switch (ch.unicode()) {
    case '&':
        html += QStringLiteral("&amp;");
        break;
    case '<':
        html += QStringLiteral("&lt;");
        break;
    case '>':
        html += QStringLiteral("&gt;");
        break;
    case '"':
        html += QStringLiteral("&quot;");
        break;
    case '\'':
        html += QStringLiteral("&#39;");
        break;
    case ' ':
        html += preserveSpaces ? QStringLiteral("&nbsp;") : QStringLiteral(" ");
        break;
    case '\t':
        html += preserveSpaces ? QStringLiteral("&nbsp;&nbsp;&nbsp;&nbsp;") : QStringLiteral(" ");
        break;
    default:
        html += ch;
        break;
    }
}

void appendEscapedWithEmoji(const QString &text, int start, int end, QString &html, HtmlBuildContext &context, bool preserveSpaces = false)
{
    if (start >= end) {
        return;
    }

    const QString segment = text.mid(start, end - start);
    QTextBoundaryFinder finder(QTextBoundaryFinder::Grapheme, segment);
    int clusterStart = 0;
    finder.toStart();
    while (true) {
        const int clusterEnd = finder.toNextBoundary();
        if (clusterEnd < 0) {
            break;
        }
        const QString cluster = segment.mid(clusterStart, clusterEnd - clusterStart);
        clusterStart = clusterEnd;
        if (isEmojiCluster(cluster)) {
            context.hasEmoji = true;
            html += context.emojiSpanOpen;
            for (QChar ch : cluster) {
                appendEscapedChar(ch, html, preserveSpaces);
            }
            html += QStringLiteral("</span>");
            continue;
        }
        for (QChar ch : cluster) {
            appendEscapedChar(ch, html, preserveSpaces);
        }
    }
}

int findClosingBacktick(const QString &text, int open, int end)
{
    for (int i = open + 1; i < end; ++i) {
        if (text.at(i) == QLatin1Char('`')) {
            return i;
        }
    }
    return -1;
}

int findClosingTripleBacktick(const QString &text, int open, int end)
{
    for (int i = open + 3; i + 2 < end; ++i) {
        if (startsWithAt(text, i, QStringLiteral("```"))) {
            return i;
        }
    }
    return -1;
}

int findClosingDelimiter(const QString &text, int open, int end, QChar marker)
{
    if (open + 1 >= end || text.at(open + 1).isSpace()) {
        return -1;
    }
    for (int i = open + 1; i < end; ++i) {
        if (text.at(i) == marker && !text.at(i - 1).isSpace()) {
            return i;
        }
    }
    return -1;
}

void appendInline(const QString &text, int start, int end, QString &html, HtmlBuildContext &context);

void appendDelimited(const QString &text, int open, int close, QChar marker, QString &html, HtmlBuildContext &context)
{
    context.hasFormatting = true;
    switch (marker.unicode()) {
    case '*':
        html += QStringLiteral("<b>");
        appendInline(text, open + 1, close, html, context);
        html += QStringLiteral("</b>");
        break;
    case '_':
        html += QStringLiteral("<i>");
        appendInline(text, open + 1, close, html, context);
        html += QStringLiteral("</i>");
        break;
    case '~':
        html += QStringLiteral("<s>");
        appendInline(text, open + 1, close, html, context);
        html += QStringLiteral("</s>");
        break;
    default:
        appendEscapedWithEmoji(text, open, close + 1, html, context);
        break;
    }
}

void appendInline(const QString &text, int start, int end, QString &html, HtmlBuildContext &context)
{
    int i = start;
    while (i < end) {
        if (startsWithAt(text, i, QStringLiteral("```"))) {
            const int close = findClosingTripleBacktick(text, i, end);
            if (close >= 0) {
                context.hasFormatting = true;
                html += QStringLiteral("<code>");
                appendEscapedWithEmoji(text, i + 3, close, html, context, true);
                html += QStringLiteral("</code>");
                i = close + 3;
                continue;
            }
        }

        const QChar ch = text.at(i);
        if (ch == QLatin1Char('`')) {
            const int close = findClosingBacktick(text, i, end);
            if (close >= 0) {
                context.hasFormatting = true;
                html += QStringLiteral("<code>");
                appendEscapedWithEmoji(text, i + 1, close, html, context, true);
                html += QStringLiteral("</code>");
                i = close + 1;
                continue;
            }
        }
        if (ch == QLatin1Char('*') || ch == QLatin1Char('_') || ch == QLatin1Char('~')) {
            const int close = findClosingDelimiter(text, i, end, ch);
            if (close >= 0) {
                appendDelimited(text, i, close, ch, html, context);
                i = close + 1;
                continue;
            }
        }

        int runEnd = i + 1;
        while (runEnd < end) {
            const QChar next = text.at(runEnd);
            if (next == QLatin1Char('`') || next == QLatin1Char('*') || next == QLatin1Char('_') || next == QLatin1Char('~')) {
                break;
            }
            ++runEnd;
        }
        appendEscapedWithEmoji(text, i, runEnd, html, context);
        i = runEnd;
    }
}

void appendLine(const QString &text, int start, int end, QString &html, HtmlBuildContext &context)
{
    if (startsWithAt(text, start, QStringLiteral("> "))) {
        context.hasFormatting = true;
        html += QStringLiteral("&#9474;&nbsp;");
        appendInline(text, start + 2, end, html, context);
        return;
    }
    if (startsWithAt(text, start, QStringLiteral("* ")) || startsWithAt(text, start, QStringLiteral("- "))) {
        context.hasFormatting = true;
        html += QStringLiteral("&#8226;&nbsp;");
        appendInline(text, start + 2, end, html, context);
        return;
    }

    int numberedContentStart = start;
    if (lineStartsNumberedList(text, start, end, &numberedContentStart)) {
        context.hasFormatting = true;
        appendEscapedWithEmoji(text, start, numberedContentStart, html, context);
        appendInline(text, numberedContentStart, end, html, context);
        return;
    }

    appendInline(text, start, end, html, context);
}

QString buildRichText(const QString &text, HtmlBuildContext &context)
{
    QString html;
    html.reserve(text.size() + text.size() / 2);

    int lineStart = 0;
    bool firstLine = true;
    while (lineStart <= text.size()) {
        const int lineEnd = text.indexOf(QLatin1Char('\n'), lineStart);
        const int end = lineEnd < 0 ? text.size() : lineEnd;
        if (!firstLine) {
            html += QStringLiteral("<br/>");
        }
        appendLine(text, lineStart, end, html, context);
        if (lineEnd < 0) {
            break;
        }
        lineStart = lineEnd + 1;
        firstLine = false;
    }

    return html;
}

}

MessageMarkup parseWhatsAppMessageMarkup(const QString &text)
{
    MessageMarkup result;
    result.emojiOnlyCount = emojiOnlyClusterCount(text);

    const bool hasEmoji = textContainsEmojiCluster(text);
    if (!hasEmoji && !textMayContainWhatsAppMarkup(text)) {
        return result;
    }

    HtmlBuildContext context;
    context.emojiSpanOpen = emojiSpanOpenTag();
    result.richText = buildRichText(text, context);
    result.hasRichText = context.hasFormatting || context.hasEmoji;
    if (!result.hasRichText) {
        result.richText.clear();
    }
    return result;
}

}
