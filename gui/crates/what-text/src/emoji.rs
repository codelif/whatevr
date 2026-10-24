//! finds emoji presentation sequences (uts #51) so they can try the emoji
//! fonts before the paragraph's own fallback list, like firefox does.

use std::iter::Peekable;
use std::ops::Range;
use std::str::CharIndices;

use icu_properties::props::{
    Emoji, EmojiModifier, EmojiPresentation, ExtendedPictographic, RegionalIndicator,
};
use icu_properties::{CodePointSetData, CodePointSetDataBorrowed};

const EMOJI: CodePointSetDataBorrowed<'static> = CodePointSetData::new::<Emoji>();
const PRESENTATION: CodePointSetDataBorrowed<'static> =
    CodePointSetData::new::<EmojiPresentation>();
const PICTOGRAPHIC: CodePointSetDataBorrowed<'static> =
    CodePointSetData::new::<ExtendedPictographic>();
const MODIFIER: CodePointSetDataBorrowed<'static> = CodePointSetData::new::<EmojiModifier>();
const REGIONAL: CodePointSetDataBorrowed<'static> = CodePointSetData::new::<RegionalIndicator>();

const VS15: char = '\u{fe0e}';
const VS16: char = '\u{fe0f}';
const ZWJ: char = '\u{200d}';
const KEYCAP: char = '\u{20e3}';

/// byte ranges that want emoji presentation, adjacent ones merged
pub(crate) fn ranges(text: &str) -> Vec<Range<usize>> {
    let mut out: Vec<Range<usize>> = Vec::new();
    let mut it = text.char_indices().peekable();
    while let Some((start, c)) = it.next() {
        let emoji = if REGIONAL.contains(c) {
            eat(&mut it, |n| REGIONAL.contains(n))
        } else if matches!(c, '0'..='9' | '#' | '*') {
            // a bare digit is text, only the keycap form is emoji
            let vs = eat(&mut it, |n| n == VS16);
            eat(&mut it, |n| n == KEYCAP) || vs
        } else if EMOJI.contains(c) || PICTOGRAPHIC.contains(c) {
            let mut emoji = element(&mut it, c);
            while it.peek().is_some_and(|&(_, n)| n == ZWJ) {
                let mut ahead = it.clone();
                ahead.next();
                match ahead.next() {
                    Some((_, n)) if EMOJI.contains(n) || PICTOGRAPHIC.contains(n) => {
                        it = ahead;
                        element(&mut it, n);
                        // a zwj sequence only exists as an emoji
                        emoji = true;
                    }
                    _ => break,
                }
            }
            emoji
        } else {
            false
        };
        if !emoji {
            continue;
        }
        let end = it.peek().map_or(text.len(), |&(i, _)| i);
        match out.last_mut() {
            Some(last) if last.end == start => last.end = end,
            _ => out.push(start..end),
        }
    }
    out
}

/// one emoji with its modifier, selector and tags; true if it renders as emoji
fn element(it: &mut Peekable<CharIndices>, c: char) -> bool {
    let mut emoji = PRESENTATION.contains(c);
    if eat(it, |n| n == VS15) {
        emoji = false;
    } else if eat(it, |n| n == VS16 || MODIFIER.contains(n)) {
        emoji = true;
        eat(it, |n| n == VS16);
    }
    // subdivision flags: black flag + tag letters + cancel tag
    let mut tags = false;
    while eat(it, |n| ('\u{e0020}'..='\u{e007f}').contains(&n)) {
        tags = true;
    }
    emoji || tags
}

fn eat(it: &mut Peekable<CharIndices>, f: impl Fn(char) -> bool) -> bool {
    it.next_if(|&(_, n)| f(n)).is_some()
}

#[cfg(test)]
mod tests {
    use super::ranges;

    fn found(s: &str) -> Vec<&str> {
        ranges(s).into_iter().map(|r| &s[r]).collect()
    }

    #[test]
    fn presentation_rules() {
        assert_eq!(found("a😀b"), ["😀"]);
        // text default stays text unless asked
        assert_eq!(found("☺ ☺\u{fe0f}"), ["☺\u{fe0f}"]);
        assert_eq!(found("😀\u{fe0e}"), Vec::<&str>::new());
        assert_eq!(found("1 #\u{fe0f}\u{20e3}"), ["#\u{fe0f}\u{20e3}"]);
        assert_eq!(found("x🇮🇳y"), ["🇮🇳"]);
    }

    #[test]
    fn sequences_stay_whole() {
        assert_eq!(found("👍🏽."), ["👍🏽"]);
        assert_eq!(found("👨\u{200d}👩\u{200d}👧!"), ["👨\u{200d}👩\u{200d}👧"]);
        assert_eq!(found("❤\u{200d}🔥"), ["❤\u{200d}🔥"]);
        let scotland = "🏴\u{e0067}\u{e0062}\u{e0073}\u{e0063}\u{e0074}\u{e007f}";
        assert_eq!(found(scotland), [scotland]);
        assert_eq!(found("😀😀 😀"), ["😀😀", "😀"]);
    }
}
