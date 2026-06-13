// Shared search-term highlighting, used by both the global search-result
// snippets and the in-conversation bubble highlighting so matches look
// identical in both places. Pure functions over (text, query, colors) — colors
// are passed in from QML (opaque "#rrggbb" strings) so theming stays in QML.
.pragma library

// Qt rich text only accepts "#rrggbb"; a QML color stringifies to "#aarrggbb",
// which it silently drops. Normalize either form to an opaque hex string.
function cssColor(c) {
    if (typeof c === "string")
        return c;
    function h(v) {
        var s = Math.round(v * 255).toString(16);
        return s.length < 2 ? "0" + s : s;
    }
    return "#" + h(c.r) + h(c.g) + h(c.b);
}

function escapeHtml(s) {
    return String(s)
        .replace(/&/g, "&amp;")
        .replace(/</g, "&lt;")
        .replace(/>/g, "&gt;");
}

function tokenize(query) {
    var trimmed = String(query).trim();
    if (trimmed.length === 0)
        return [];
    return trimmed.toLowerCase().split(/\s+/);
}

// Merged, sorted [start, end) match ranges for every token (case-insensitive
// substring), so overlapping/adjacent hits highlight as one span.
function matchRanges(text, tokens) {
    var lower = text.toLowerCase();
    var ranges = [];
    for (var t = 0; t < tokens.length; ++t) {
        var tok = tokens[t];
        if (tok.length === 0)
            continue;
        var from = 0;
        while (true) {
            var idx = lower.indexOf(tok, from);
            if (idx < 0)
                break;
            ranges.push([idx, idx + tok.length]);
            from = idx + tok.length;
        }
    }
    if (ranges.length === 0)
        return ranges;
    ranges.sort(function (a, b) { return a[0] - b[0]; });
    var merged = [ranges[0].slice()];
    for (var i = 1; i < ranges.length; ++i) {
        var last = merged[merged.length - 1];
        if (ranges[i][0] <= last[1]) {
            if (ranges[i][1] > last[1])
                last[1] = ranges[i][1];
        } else {
            merged.push(ranges[i].slice());
        }
    }
    return merged;
}

function wrapSpan(content, bg, fg) {
    return "<span style=\"background-color:" + bg + "; color:" + fg + ";\">" + content + "</span>";
}

// Escaped HTML for text[from, to) with the given absolute-offset ranges
// highlighted.
function render(text, ranges, from, to, bg, fg) {
    var html = "";
    var cursor = from;
    for (var i = 0; i < ranges.length; ++i) {
        var s = ranges[i][0];
        var e = ranges[i][1];
        if (e <= from || s >= to)
            continue;
        if (s > cursor)
            html += escapeHtml(text.substring(cursor, s));
        var ms = Math.max(s, from);
        var me = Math.min(e, to);
        html += wrapSpan(escapeHtml(text.substring(ms, me)), bg, fg);
        cursor = me;
    }
    if (cursor < to)
        html += escapeHtml(text.substring(cursor, to));
    return html;
}

// Full text (rich HTML) with each query token highlighted. Falls back to the
// escaped plain text when there is no query or no match.
function highlight(text, query, bg, fg) {
    text = String(text);
    var tokens = tokenize(query);
    if (tokens.length === 0)
        return escapeHtml(text);
    var ranges = matchRanges(text, tokens);
    if (ranges.length === 0)
        return escapeHtml(text);
    return render(text, ranges, 0, text.length, cssColor(bg), cssColor(fg));
}

// A windowed snippet (~radius chars of context around the first match) with all
// matches highlighted, plus leading/trailing ellipses when truncated.
function snippet(text, query, bg, fg, radius) {
    text = String(text);
    if (radius === undefined)
        radius = 40;
    var tokens = tokenize(query);
    var ranges = tokens.length > 0 ? matchRanges(text, tokens) : [];
    if (ranges.length === 0) {
        // The hit was outside the visible body (e.g. a media placeholder); just
        // clip the head so the row still shows something sensible.
        var head = text.length > radius * 2 ? text.substring(0, radius * 2) + "…" : text;
        return escapeHtml(head);
    }
    var first = ranges[0][0];
    var start = Math.max(0, first - radius);
    var end = Math.min(text.length, ranges[ranges.length - 1][1] + radius);
    var prefix = start > 0 ? "…" : "";
    var suffix = end < text.length ? "…" : "";
    return prefix + render(text, ranges, start, end, cssColor(bg), cssColor(fg)) + suffix;
}
