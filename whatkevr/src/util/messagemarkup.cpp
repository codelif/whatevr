#include "messagemarkup.h"

#include <QFont>
#include <QGuiApplication>
#include <QHash>
#include <QScreen>
#include <QTextBoundaryFinder>
#include <QVector>

#include "tlds.h"

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
    // Mention linkification. checkMentions gates the whole `@` code path so
    // plain/non-group messages pay nothing. mentionsByUserpart maps a JID's
    // user-part to its resolved mention; isGroup additionally enables the
    // literal @all / @everyone tokens.
    bool checkMentions = false;
    bool isGroup = false;
    const QHash<QString, const MessageMention *> *mentionsByUserpart = nullptr;
};

struct UrlMatch {
    int end = -1;
    QString href;
};

bool isAsciiLetter(QChar ch)
{
    const ushort code = ch.unicode();
    return (code >= 'a' && code <= 'z') || (code >= 'A' && code <= 'Z');
}

bool isAsciiDigit(QChar ch)
{
    const ushort code = ch.unicode();
    return code >= '0' && code <= '9';
}

bool isAsciiAlphaNumeric(QChar ch)
{
    return isAsciiLetter(ch) || isAsciiDigit(ch);
}

bool isDomainLabelChar(QChar ch)
{
    return isAsciiAlphaNumeric(ch) || ch == QLatin1Char('-');
}

bool startsWithAtCaseInsensitive(const QString &text, int index, QStringView marker)
{
    return index >= 0
        && index + marker.size() <= text.size()
        && QStringView{text}.mid(index, marker.size()).compare(marker, Qt::CaseInsensitive) == 0;
}

bool hasUrlBoundaryBefore(const QString &text, int index)
{
    if (index <= 0) {
        return true;
    }

    const QChar prev = text.at(index - 1);
    return !prev.isLetterOrNumber()
        && prev != QLatin1Char('-')
        && prev != QLatin1Char('_')
        && prev != QLatin1Char('.')
        && prev != QLatin1Char('@');
}

bool isUrlStopChar(QChar ch)
{
    return ch.isSpace()
        || ch == QLatin1Char('<')
        || ch == QLatin1Char('>')
        || ch == QLatin1Char('"')
        || ch == QLatin1Char('\'');
}

bool isUrlTailStarter(QChar ch)
{
    return ch == QLatin1Char('/') || ch == QLatin1Char('?') || ch == QLatin1Char('#');
}

bool isTrailingUrlPunctuation(QChar ch)
{
    switch (ch.unicode()) {
    case '.':
    case ',':
    case '!':
    case '?':
    case ';':
    case ':':
    case ')':
    case ']':
    case '}':
        return true;
    default:
        return false;
    }
}

int trimmedUrlEnd(const QString &text, int start, int rawEnd)
{
    int end = rawEnd;
    while (end > start && isTrailingUrlPunctuation(text.at(end - 1))) {
        --end;
    }
    return end;
}

bool isValidTopLevelDomain(const QString &text, int start, int end)
{
    const int length = end - start;
    if (length < 2 || length > 63) {
        return false;
    }
    return isKnownIanaTld(QStringView{text}.mid(start, length));
}

int parseDomainEnd(const QString &text, int start, int end)
{
    int pos = start;
    int labelCount = 0;
    int lastLabelStart = -1;
    int lastLabelEnd = -1;

    while (pos < end) {
        const int labelStart = pos;
        if (!isAsciiAlphaNumeric(text.at(pos))) {
            return -1;
        }

        while (pos < end && isDomainLabelChar(text.at(pos))) {
            ++pos;
        }
        if (text.at(pos - 1) == QLatin1Char('-')) {
            return -1;
        }

        ++labelCount;
        lastLabelStart = labelStart;
        lastLabelEnd = pos;

        if (pos < end && text.at(pos) == QLatin1Char('.') && pos + 1 < end && isAsciiAlphaNumeric(text.at(pos + 1))) {
            ++pos;
            continue;
        }
        break;
    }

    if (labelCount < 2 || !isValidTopLevelDomain(text, lastLabelStart, lastLabelEnd)) {
        return -1;
    }
    return lastLabelEnd;
}

int scanDomainUrlEnd(const QString &text, int domainEnd, int end)
{
    int urlEnd = domainEnd;

    if (urlEnd < end && text.at(urlEnd) == QLatin1Char(':') && urlEnd + 1 < end && isAsciiDigit(text.at(urlEnd + 1))) {
        urlEnd += 2;
        while (urlEnd < end && isAsciiDigit(text.at(urlEnd))) {
            ++urlEnd;
        }
    }

    if (urlEnd < end && isUrlTailStarter(text.at(urlEnd))) {
        ++urlEnd;
        while (urlEnd < end && !isUrlStopChar(text.at(urlEnd))) {
            ++urlEnd;
        }
    }

    return urlEnd;
}

UrlMatch findUrlAt(const QString &text, int index, int end)
{
    UrlMatch match;
    if (index < 0 || index >= end || !hasUrlBoundaryBefore(text, index)) {
        return match;
    }

    int schemeLength = 0;
    if (startsWithAtCaseInsensitive(text, index, QStringLiteral("https://"))) {
        schemeLength = 8;
    } else if (startsWithAtCaseInsensitive(text, index, QStringLiteral("http://"))) {
        schemeLength = 7;
    }

    if (schemeLength > 0) {
        int rawEnd = index + schemeLength;
        while (rawEnd < end && !isUrlStopChar(text.at(rawEnd))) {
            ++rawEnd;
        }

        const int urlEnd = trimmedUrlEnd(text, index + schemeLength, rawEnd);
        if (urlEnd > index + schemeLength) {
            match.end = urlEnd;
            match.href = text.mid(index, urlEnd - index);
        }
        return match;
    }

    const int domainEnd = parseDomainEnd(text, index, end);
    if (domainEnd < 0) {
        return match;
    }

    const int rawEnd = scanDomainUrlEnd(text, domainEnd, end);
    const int urlEnd = trimmedUrlEnd(text, index, rawEnd);
    match.end = urlEnd;
    match.href = QStringLiteral("https://") + text.mid(index, urlEnd - index);
    return match;
}

bool textMayContainUrl(const QString &text)
{
    for (int i = 0; i < text.size(); ++i) {
        if (findUrlAt(text, i, text.size()).end > i) {
            return true;
        }
    }
    return false;
}

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

QString escapedHtmlAttribute(const QString &text)
{
    QString html;
    html.reserve(text.size());
    for (QChar ch : text) {
        appendEscapedChar(ch, html, false);
    }
    return html;
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

void appendInline(const QString &text, int start, int end, QString &html, QString &layoutText, HtmlBuildContext &context);

void appendLayoutText(const QString &text, int start, int end, QString &layoutText)
{
    if (start < end) {
        layoutText += QStringView{text}.mid(start, end - start);
    }
}

void appendDelimited(const QString &text, int open, int close, QChar marker, QString &html, QString &layoutText, HtmlBuildContext &context)
{
    context.hasFormatting = true;
    switch (marker.unicode()) {
    case '*':
        html += QStringLiteral("<b>");
        appendInline(text, open + 1, close, html, layoutText, context);
        html += QStringLiteral("</b>");
        break;
    case '_':
        html += QStringLiteral("<i>");
        appendInline(text, open + 1, close, html, layoutText, context);
        html += QStringLiteral("</i>");
        break;
    case '~':
        html += QStringLiteral("<s>");
        appendInline(text, open + 1, close, html, layoutText, context);
        html += QStringLiteral("</s>");
        break;
    default:
        appendEscapedWithEmoji(text, open, close + 1, html, context);
        appendLayoutText(text, open, close + 1, layoutText);
        break;
    }
}

// Try to linkify an @-mention starting at text[i] ('@'). Returns the index just
// past the mention on a hit, or -1 when text[i] is not a recognised mention.
// The token after '@' is the JID user-part (digits for phone/LID) or, in a
// group, the literal "all"/"everyone".
int tryAppendMention(const QString &text, int i, int end, QString &html, QString &layoutText, HtmlBuildContext &context)
{
    if (!context.checkMentions || text.at(i) != QLatin1Char('@')) {
        return -1;
    }

    const int tokenStart = i + 1;
    int tokenEnd = tokenStart;
    while (tokenEnd < end && text.at(tokenEnd).isLetterOrNumber()) {
        ++tokenEnd;
    }
    if (tokenEnd == tokenStart) {
        return -1;
    }
    const QString token = text.mid(tokenStart, tokenEnd - tokenStart);

    QString href;
    QString display;
    if (context.isGroup) {
        const QString lower = token.toLower();
        if (lower == QLatin1String("all") || lower == QLatin1String("everyone")) {
            href = QStringLiteral("wamention-all:");
            display = QLatin1Char('@') + token;
        }
    }
    if (href.isEmpty() && context.mentionsByUserpart != nullptr) {
        const auto it = context.mentionsByUserpart->constFind(token);
        if (it != context.mentionsByUserpart->constEnd()) {
            const MessageMention *mention = it.value();
            href = QStringLiteral("wamention:") + mention->jid;
            display = QLatin1Char('@') + (mention->displayName.isEmpty() ? token : mention->displayName);
        }
    }
    if (href.isEmpty()) {
        return -1;
    }

    context.hasFormatting = true;
    html += QStringLiteral("<a href=\"");
    html += escapedHtmlAttribute(href);
    html += QStringLiteral("\" style=\"text-decoration:none;\">");
    appendEscapedWithEmoji(display, 0, display.size(), html, context);
    html += QStringLiteral("</a>");
    layoutText += display;
    return tokenEnd;
}

void appendUrlAnchor(const QString &text, int start, const UrlMatch &url, QString &html, QString &layoutText, HtmlBuildContext &context)
{
    context.hasFormatting = true;
    html += QStringLiteral("<a href=\"");
    html += escapedHtmlAttribute(url.href);
    html += QStringLiteral("\">");
    appendEscapedWithEmoji(text, start, url.end, html, context);
    html += QStringLiteral("</a>");
    appendLayoutText(text, start, url.end, layoutText);
}

void appendInline(const QString &text, int start, int end, QString &html, QString &layoutText, HtmlBuildContext &context)
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
                appendLayoutText(text, i + 3, close, layoutText);
                i = close + 3;
                continue;
            }
            appendEscapedWithEmoji(text, i, i + 3, html, context);
            appendLayoutText(text, i, i + 3, layoutText);
            i += 3;
            continue;
        }

        const QChar ch = text.at(i);
        if (ch == QLatin1Char('`')) {
            const int close = findClosingBacktick(text, i, end);
            if (close >= 0) {
                context.hasFormatting = true;
                html += QStringLiteral("<code>");
                appendEscapedWithEmoji(text, i + 1, close, html, context, true);
                html += QStringLiteral("</code>");
                appendLayoutText(text, i + 1, close, layoutText);
                i = close + 1;
                continue;
            }
        }

        const UrlMatch url = findUrlAt(text, i, end);
        if (url.end > i) {
            appendUrlAnchor(text, i, url, html, layoutText, context);
            i = url.end;
            continue;
        }

        if (ch == QLatin1Char('@')) {
            const int mentionEnd = tryAppendMention(text, i, end, html, layoutText, context);
            if (mentionEnd > i) {
                i = mentionEnd;
                continue;
            }
        }

        if (ch == QLatin1Char('*') || ch == QLatin1Char('_') || ch == QLatin1Char('~')) {
            const int close = findClosingDelimiter(text, i, end, ch);
            if (close >= 0) {
                appendDelimited(text, i, close, ch, html, layoutText, context);
                i = close + 1;
                continue;
            }
        }

        int runEnd = i + 1;
        while (runEnd < end) {
            if (findUrlAt(text, runEnd, end).end > runEnd) {
                break;
            }
            const QChar next = text.at(runEnd);
            if (next == QLatin1Char('`') || next == QLatin1Char('*') || next == QLatin1Char('_') || next == QLatin1Char('~')) {
                break;
            }
            if (context.checkMentions && next == QLatin1Char('@')) {
                break;
            }
            ++runEnd;
        }
        appendEscapedWithEmoji(text, i, runEnd, html, context);
        appendLayoutText(text, i, runEnd, layoutText);
        i = runEnd;
    }
}

void appendLine(const QString &text, int start, int end, QString &html, QString &layoutText, HtmlBuildContext &context)
{
    if (startsWithAt(text, start, QStringLiteral("> "))) {
        context.hasFormatting = true;
        html += QStringLiteral("&#9474;&nbsp;");
        layoutText += QStringLiteral("| ");
        appendInline(text, start + 2, end, html, layoutText, context);
        return;
    }
    if (startsWithAt(text, start, QStringLiteral("* ")) || startsWithAt(text, start, QStringLiteral("- "))) {
        context.hasFormatting = true;
        html += QStringLiteral("&#8226;&nbsp;");
        layoutText += QChar(0x2022);
        layoutText += QLatin1Char(' ');
        appendInline(text, start + 2, end, html, layoutText, context);
        return;
    }

    int numberedContentStart = start;
    if (lineStartsNumberedList(text, start, end, &numberedContentStart)) {
        context.hasFormatting = true;
        appendEscapedWithEmoji(text, start, numberedContentStart, html, context);
        appendLayoutText(text, start, numberedContentStart, layoutText);
        appendInline(text, numberedContentStart, end, html, layoutText, context);
        return;
    }

    appendInline(text, start, end, html, layoutText, context);
}

void appendOutputLineBreak(QString &html, QString &layoutText, bool &firstLine)
{
    if (!firstLine) {
        html += QStringLiteral("<br/>");
        layoutText += QLatin1Char('\n');
    }
    firstLine = false;
}

void appendRichTextSegment(const QString &text, int start, int end, QString &html, QString &layoutText, HtmlBuildContext &context, bool &firstLine)
{
    int lineStart = start;
    bool lineBreakAlreadyEmitted = false;
    if (lineStart < end && !firstLine && text.at(lineStart) == QLatin1Char('\n')) {
        html += QStringLiteral("<br/>");
        layoutText += QLatin1Char('\n');
        ++lineStart;
        lineBreakAlreadyEmitted = true;
    }

    while (lineStart < end) {
        const int newline = text.indexOf(QLatin1Char('\n'), lineStart);
        const int lineEnd = (newline < 0 || newline >= end) ? end : newline;
        if (lineBreakAlreadyEmitted) {
            firstLine = false;
            lineBreakAlreadyEmitted = false;
        } else {
            appendOutputLineBreak(html, layoutText, firstLine);
        }
        appendLine(text, lineStart, lineEnd, html, layoutText, context);
        if (lineEnd >= end) {
            break;
        }
        lineStart = lineEnd + 1;
    }
}

int firstCodeContentChar(const QString &text, int start, int end)
{
    if (start < end && text.at(start) == QLatin1Char('\r')) {
        if (start + 1 < end && text.at(start + 1) == QLatin1Char('\n')) {
            return start + 2;
        }
        return start + 1;
    }
    if (start < end && text.at(start) == QLatin1Char('\n')) {
        return start + 1;
    }
    return start;
}

int lastCodeContentChar(const QString &text, int start, int end)
{
    if (end > start && text.at(end - 1) == QLatin1Char('\n')) {
        --end;
        if (end > start && text.at(end - 1) == QLatin1Char('\r')) {
            --end;
        }
        return end;
    }
    if (end > start && text.at(end - 1) == QLatin1Char('\r')) {
        return end - 1;
    }
    return end;
}

void appendCodeText(const QString &text, int start, int end, bool forceLineBreakBefore, QString &html, QString &layoutText, HtmlBuildContext &context, bool &firstLine)
{
    context.hasFormatting = true;

    start = firstCodeContentChar(text, start, end);
    end = lastCodeContentChar(text, start, end);

    if (firstLine) {
        firstLine = false;
    } else if (forceLineBreakBefore) {
        html += QStringLiteral("<br/>");
        layoutText += QLatin1Char('\n');
    }
    html += QStringLiteral("<code>");
    int lineStart = start;
    while (lineStart <= end) {
        const int newline = text.indexOf(QLatin1Char('\n'), lineStart);
        const int lineEnd = (newline < 0 || newline >= end) ? end : newline;
        if (lineStart > start) {
            html += QStringLiteral("<br/>");
            layoutText += QLatin1Char('\n');
        }
        appendEscapedWithEmoji(text, lineStart, lineEnd, html, context, true);
        appendLayoutText(text, lineStart, lineEnd, layoutText);
        if (lineEnd >= end) {
            break;
        }
        lineStart = lineEnd + 1;
    }
    html += QStringLiteral("</code>");
}

int findNextClosedTripleBacktick(const QString &text, int start, int end, int *close)
{
    for (int i = start; i + 2 < end; ++i) {
        if (!startsWithAt(text, i, QStringLiteral("```"))) {
            continue;
        }
        const int candidateClose = findClosingTripleBacktick(text, i, end);
        if (candidateClose >= 0) {
            *close = candidateClose;
            return i;
        }
        i += 2;
    }
    return -1;
}

QString buildRichText(const QString &text, QString &layoutText, HtmlBuildContext &context)
{
    QString html;
    html.reserve(text.size() + text.size() / 2);
    layoutText.reserve(text.size());

    int segmentStart = 0;
    bool firstLine = true;
    while (segmentStart < text.size()) {
        int close = -1;
        const int open = findNextClosedTripleBacktick(text, segmentStart, text.size(), &close);
        if (open < 0) {
            break;
        }
        appendRichTextSegment(text, segmentStart, open, html, layoutText, context, firstLine);
        appendCodeText(text, open + 3, close, open > 0 && text.at(open - 1) == QLatin1Char('\n'), html, layoutText, context, firstLine);
        segmentStart = close + 3;
    }
    appendRichTextSegment(text, segmentStart, text.size(), html, layoutText, context, firstLine);

    return html;
}

}

namespace {

void appendCommonMarkEscaped(const QString &text, int start, int end, QString &out)
{
    for (int i = start; i < end; ++i) {
        const QChar ch = text.at(i);
        switch (ch.unicode()) {
        case '\\':
        case '`':
        case '*':
        case '_':
        case '~':
        case '[':
        case ']':
        case '<':
        case '>':
            out += QLatin1Char('\\');
            out += ch;
            break;
        default:
            out += ch;
            break;
        }
    }
}

void appendCommonMarkInline(const QString &text, int start, int end, QString &out)
{
    int i = start;
    while (i < end) {
        const QChar ch = text.at(i);
        if (ch == QLatin1Char('`')) {
            const int close = findClosingBacktick(text, i, end);
            if (close >= 0) {
                out += QLatin1Char('`');
                out += QStringView{text}.mid(i + 1, close - i - 1);
                out += QLatin1Char('`');
                i = close + 1;
                continue;
            }
        }

        const UrlMatch url = findUrlAt(text, i, end);
        if (url.end > i) {
            const QString visible = text.mid(i, url.end - i);
            if (visible == url.href) {
                out += QLatin1Char('<');
                out += url.href;
                out += QLatin1Char('>');
            } else {
                out += QLatin1Char('[');
                appendCommonMarkEscaped(text, i, url.end, out);
                out += QStringLiteral("](");
                out += url.href;
                out += QLatin1Char(')');
            }
            i = url.end;
            continue;
        }

        if (ch == QLatin1Char('*') || ch == QLatin1Char('_') || ch == QLatin1Char('~')) {
            const int close = findClosingDelimiter(text, i, end, ch);
            if (close >= 0) {
                const QString marker = ch == QLatin1Char('*') ? QStringLiteral("**")
                    : ch == QLatin1Char('_')                  ? QStringLiteral("*")
                                                              : QStringLiteral("~~");
                out += marker;
                appendCommonMarkInline(text, i + 1, close, out);
                out += marker;
                i = close + 1;
                continue;
            }
        }

        int runEnd = i + 1;
        while (runEnd < end) {
            if (findUrlAt(text, runEnd, end).end > runEnd) {
                break;
            }
            const QChar next = text.at(runEnd);
            if (next == QLatin1Char('`') || next == QLatin1Char('*') || next == QLatin1Char('_') || next == QLatin1Char('~')) {
                break;
            }
            ++runEnd;
        }
        appendCommonMarkEscaped(text, i, runEnd, out);
        i = runEnd;
    }
}

void appendCommonMarkLine(const QString &text, int start, int end, QString &out)
{
    if (startsWithAt(text, start, QStringLiteral("> "))) {
        out += QStringLiteral("> ");
        appendCommonMarkInline(text, start + 2, end, out);
        return;
    }
    if (startsWithAt(text, start, QStringLiteral("* ")) || startsWithAt(text, start, QStringLiteral("- "))) {
        out += QStringLiteral("- ");
        appendCommonMarkInline(text, start + 2, end, out);
        return;
    }

    int numberedContentStart = start;
    if (lineStartsNumberedList(text, start, end, &numberedContentStart)) {
        out += QStringView{text}.mid(start, numberedContentStart - start);
        appendCommonMarkInline(text, numberedContentStart, end, out);
        return;
    }

    appendCommonMarkInline(text, start, end, out);
}

void appendCommonMarkSegment(const QString &text, int start, int end, QString &out)
{
    int lineStart = start;
    while (lineStart < end) {
        const int newline = text.indexOf(QLatin1Char('\n'), lineStart);
        const int lineEnd = (newline < 0 || newline >= end) ? end : newline;
        appendCommonMarkLine(text, lineStart, lineEnd, out);
        if (lineEnd >= end) {
            break;
        }
        out += QLatin1Char('\n');
        lineStart = lineEnd + 1;
    }
}

}

QStringList extractMessageLinks(const QString &text)
{
    QStringList links;
    const int end = text.size();
    int i = 0;
    while (i < end) {
        // Skip code spans exactly like the rich-text renderer: their content
        // is never linkified.
        if (startsWithAt(text, i, QStringLiteral("```"))) {
            const int close = findClosingTripleBacktick(text, i, end);
            i = close >= 0 ? close + 3 : i + 3;
            continue;
        }
        if (text.at(i) == QLatin1Char('`')) {
            const int close = findClosingBacktick(text, i, end);
            if (close >= 0) {
                i = close + 1;
                continue;
            }
        }
        const UrlMatch url = findUrlAt(text, i, end);
        if (url.end > i) {
            if (!links.contains(url.href)) {
                links.append(url.href);
            }
            i = url.end;
            continue;
        }
        ++i;
    }
    return links;
}

QString whatsAppToCommonMark(const QString &text)
{
    QString out;
    out.reserve(text.size() + text.size() / 4);

    int segmentStart = 0;
    bool firstBlock = true;
    while (segmentStart < text.size()) {
        int close = -1;
        const int open = findNextClosedTripleBacktick(text, segmentStart, text.size(), &close);
        if (open < 0) {
            break;
        }
        if (open > segmentStart) {
            if (!firstBlock) {
                out += QLatin1Char('\n');
            }
            // Trim the newline that visually separates text from the block so
            // the fenced block doesn't pick up an extra blank line.
            int textEnd = open;
            if (textEnd > segmentStart && text.at(textEnd - 1) == QLatin1Char('\n')) {
                --textEnd;
            }
            appendCommonMarkSegment(text, segmentStart, textEnd, out);
            firstBlock = false;
        }

        const int codeStart = firstCodeContentChar(text, open + 3, close);
        const int codeEnd = lastCodeContentChar(text, codeStart, close);
        if (!firstBlock) {
            out += QLatin1Char('\n');
        }
        out += QStringLiteral("```\n");
        out += QStringView{text}.mid(codeStart, codeEnd - codeStart);
        out += QStringLiteral("\n```");
        firstBlock = false;

        segmentStart = close + 3;
        if (segmentStart < text.size() && text.at(segmentStart) == QLatin1Char('\n')) {
            ++segmentStart;
        }
    }
    if (segmentStart < text.size()) {
        if (!firstBlock) {
            out += QLatin1Char('\n');
        }
        appendCommonMarkSegment(text, segmentStart, text.size(), out);
    }
    return out;
}

MessageMarkup parseWhatsAppMessageMarkup(const QString &text, const QList<MessageMention> &mentions, bool isGroup)
{
    MessageMarkup result;
    result.emojiOnlyCount = emojiOnlyClusterCount(text);

    // Mentions are linkified only when actually present; @all/@everyone work in
    // any group. Both gate on the cheap `@`-contains check so plain messages
    // skip the whole mention path.
    const bool checkMentions = (!mentions.isEmpty() || isGroup) && text.contains(QLatin1Char('@'));

    const bool hasEmoji = textContainsEmojiCluster(text);
    if (!hasEmoji && !textMayContainWhatsAppMarkup(text) && !textMayContainUrl(text) && !checkMentions) {
        return result;
    }

    // Index mentions by JID user-part (the token that appears in the text).
    QHash<QString, const MessageMention *> mentionsByUserpart;
    if (checkMentions && !mentions.isEmpty()) {
        mentionsByUserpart.reserve(mentions.size());
        for (const MessageMention &mention : mentions) {
            const int at = mention.jid.indexOf(QLatin1Char('@'));
            const QString userpart = at > 0 ? mention.jid.left(at) : mention.jid;
            if (!userpart.isEmpty()) {
                mentionsByUserpart.insert(userpart, &mention);
            }
        }
    }

    HtmlBuildContext context;
    context.emojiSpanOpen = emojiSpanOpenTag();
    context.checkMentions = checkMentions;
    context.isGroup = isGroup;
    context.mentionsByUserpart = mentionsByUserpart.isEmpty() ? nullptr : &mentionsByUserpart;
    result.richText = buildRichText(text, result.layoutText, context);
    result.hasRichText = context.hasFormatting || context.hasEmoji;
    if (!result.hasRichText) {
        result.richText.clear();
        result.layoutText.clear();
    } else if (!context.hasFormatting || result.layoutText == text) {
        result.layoutText.clear();
    }
    return result;
}

}
