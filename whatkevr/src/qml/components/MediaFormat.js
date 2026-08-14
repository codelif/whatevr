.pragma library
// Shared formatting for media chrome. Both of these had grown a copy per file
// (DocumentBubble's humanSize, and formatTime in VideoBubble and MediaViewer),
// which is how the same clip could be labelled two different ways in the bubble
// and in the full-screen viewer.

/// "3.4 MB", or "" for an unknown or absent size.
function humanSize(bytes) {
    if (!bytes || bytes <= 0)
        return "";
    const units = ["B", "kB", "MB", "GB"];
    let value = bytes;
    let unit = 0;
    while (value >= 1024 && unit < units.length - 1) {
        value /= 1024;
        unit++;
    }
    return (unit === 0 ? value.toFixed(0) : value.toFixed(1)) + " " + units[unit];
}

/// m:ss, or h:mm:ss once a clip is over an hour. Clamped at zero, because a
/// countdown against a duration the sender rounded down goes negative.
function clockTime(seconds) {
    const whole = Math.max(0, Math.floor(seconds));
    const hours = Math.floor(whole / 3600);
    const minutes = Math.floor((whole % 3600) / 60);
    const rest = whole % 60;
    const pad = v => (v < 10 ? "0" + v : "" + v);
    if (hours > 0)
        return hours + ":" + pad(minutes) + ":" + pad(rest);
    return minutes + ":" + pad(rest);
}
