//! the scroll lab: a sidebar of 5k chats next to a 100k-row group chat,
//! both on what-ui's list, with the overlay scrollbars and the corner
//! button. no wayland or gpu in here, so the scripted test drives the same
//! code the window does.

pub mod bubble;
pub mod check;
pub mod fake;
pub mod sidebar;

use std::collections::HashSet;
use std::time::{Duration, Instant};

use what_core::{
    Button, LogicalPoint, LogicalRect, LogicalSize, PhysicalPoint, PhysicalRect, PhysicalSize,
    PointerEvent, Scale, Srgba8, Timestamp,
};
use what_render::{Clip, Corners, Fill, Glyph, Quad, Rect, Scene};
use what_text::{Family, GlyphKey, Icon, Paragraph, Raster, Style, TextSystem};
use what_ui::{
    Align, FrameReport, GlyphSink, Gravity, List, Part, Row, Scrollbar, ScrollbarAction, Scroller,
};

use crate::bubble::Bubbles;
use crate::check::Checker;
use crate::fake::{BOTTOM, Chat, Chats};
use crate::sidebar::SideRows;

#[derive(Clone, Copy, Debug)]
pub struct Theme {
    pub bg: Srgba8,
    pub chrome: Srgba8,
    pub inc: Srgba8,
    pub inc2: Srgba8,
    pub out: Srgba8,
    pub text: Srgba8,
    pub muted: Srgba8,
    pub faint: Srgba8,
    pub acc: Srgba8,
    pub acc_ink: Srgba8,
    /// link preview card over a bubble
    pub link_bg: Srgba8,
    /// the corner button's outline
    pub ring: Srgba8,
    pub bar_idle: Srgba8,
    pub bar_hot: Srgba8,
    /// sender name colours
    pub ids: [Srgba8; 5],
}

pub const DARK: Theme = Theme {
    bg: Srgba8::hex(0x1f1f1d),
    chrome: Srgba8::hex(0x161615),
    inc: Srgba8::hex(0x2d2d2a),
    inc2: Srgba8::hex(0x363633),
    out: Srgba8::hex(0x22384f),
    text: Srgba8::hex(0xecece8),
    muted: Srgba8::hex(0x9a9a94),
    faint: Srgba8::hex(0x4a4a44),
    acc: Srgba8::hex(0x7fa3c9),
    acc_ink: Srgba8::hex(0x161615),
    link_bg: Srgba8::new(0, 0, 0, 46),
    ring: Srgba8::hex(0x3a3a35),
    bar_idle: Srgba8::hex(0x6a6a63),
    bar_hot: Srgba8::hex(0x9a9a94),
    ids: [
        Srgba8::hex(0xd39a78),
        Srgba8::hex(0x7fb8bd),
        Srgba8::hex(0xaa9fd6),
        Srgba8::hex(0xc9b97c),
        Srgba8::hex(0x92bd98),
    ],
};

/// straight srgb lerp, like the sheet's hexMix
pub fn mix(a: Srgba8, b: Srgba8, t: f32) -> Srgba8 {
    let l = |x: u8, y: u8| (x as f32 + (y as f32 - x as f32) * t).round() as u8;
    Srgba8::new(l(a.r, b.r), l(a.g, b.g), l(a.b, b.b), l(a.a, b.a))
}

/// css hsl(), hue in degrees
pub fn hsl(h: f32, s: f32, l: f32) -> Srgba8 {
    let h = h.rem_euclid(360.0) / 30.0;
    let a = s * l.min(1.0 - l);
    let f = |n: f32| {
        let k = (n + h).rem_euclid(12.0);
        let v = l - a * (k - 3.0).min(9.0 - k).clamp(-1.0, 1.0);
        (v * 255.0).round() as u8
    };
    Srgba8::rgb(f(0.0), f(8.0), f(4.0))
}

/// a css linear-gradient angle as start and end in the box's own 0..1 space
pub fn css_linear(deg: f32, w: f32, h: f32) -> ([f32; 2], [f32; 2]) {
    let (s, c) = deg.to_radians().sin_cos();
    let len = (w * s).abs() + (h * c).abs();
    let (dx, dy) = (s * len / 2.0 / w, -c * len / 2.0 / h);
    ([0.5 - dx, 0.5 - dy], [0.5 + dx, 0.5 + dy])
}

pub fn font() -> Family {
    Family::Named(what_assets::fonts::FAMILY.into())
}

/// `line` is css px
pub fn style(size: f32, line: f32, weight: f32, color: Srgba8) -> Style {
    Style {
        family: font(),
        size,
        weight,
        italic: false,
        color: color.premultiply(),
        line_height: line / size,
    }
}

/// delivery ticks as the sheet drew them (state h): read is the heavier
/// stroke. returns the icon and its width, 10 px tall.
pub fn ticks(state: u8) -> (Icon, f32) {
    let w = if state == 3 { 1.9 } else { 1.25 };
    let n = state.clamp(1, 2);
    let mut b = Icon::builder();
    b.stroke(&[[1.0, 5.2], [3.6, 7.8], [8.6, 1.8]], w);
    if n == 2 {
        b.stroke(&[[4.4 + 4.2, 7.6], [4.4 + 9.6, 1.8]], w);
    }
    (b.build(), 11.0 + (n - 1) as f32 * 4.4)
}

const CHEVRON: u16 = 10;

fn chevron() -> Icon {
    Icon::builder()
        .stroke(&[[3.0, 5.5], [7.0, 9.5], [11.0, 5.5]], 1.6)
        .build()
}

/// builds a row in logical px, snapping every edge to the device grid
pub(crate) struct Pen<'a> {
    pub s: f32,
    scale: Scale,
    pub text: &'a mut TextSystem,
    shipped: &'a mut HashSet<GlyphKey>,
    parts: Vec<Part>,
    rasters: Vec<(GlyphKey, Raster)>,
}

impl<'a> Pen<'a> {
    pub fn new(scale: Scale, text: &'a mut TextSystem, shipped: &'a mut HashSet<GlyphKey>) -> Self {
        Self {
            s: scale.as_f32(),
            scale,
            text,
            shipped,
            parts: Vec::new(),
            rasters: Vec::new(),
        }
    }

    pub fn px(&self, v: f32) -> f32 {
        (v * self.s).round()
    }

    fn rect(&self, x: f32, y: f32, w: f32, h: f32) -> Rect {
        let (x0, y0) = (self.px(x), self.px(y));
        Rect::new(x0, y0, self.px(x + w) - x0, self.px(y + h) - y0)
    }

    fn corners(&self, r: [f32; 4]) -> Corners {
        Corners {
            top_left: r[0] * self.s,
            top_right: r[1] * self.s,
            bottom_right: r[2] * self.s,
            bottom_left: r[3] * self.s,
        }
    }

    pub fn quad(&mut self, x: f32, y: f32, w: f32, h: f32, r: [f32; 4], c: Srgba8) {
        self.parts.push(Part::Quad(Quad {
            rect: self.rect(x, y, w, h),
            radii: self.corners(r),
            fill: Fill::Solid(c.premultiply()),
            ..Quad::default()
        }));
    }

    /// css linear-gradient(deg, a, b)
    #[allow(clippy::too_many_arguments)]
    pub fn gradient(
        &mut self,
        x: f32,
        y: f32,
        w: f32,
        h: f32,
        r: [f32; 4],
        deg: f32,
        a: Srgba8,
        b: Srgba8,
    ) {
        let (from, to) = css_linear(deg, w, h);
        self.parts.push(Part::Quad(Quad {
            rect: self.rect(x, y, w, h),
            radii: self.corners(r),
            fill: Fill::Linear {
                from,
                to,
                start: a.premultiply(),
                end: b.premultiply(),
            },
            ..Quad::default()
        }));
    }

    pub fn para(&mut self, s: &str, st: &Style, spans: &[what_text::Span]) -> Paragraph {
        self.text.paragraph(s, st, spans, self.scale)
    }

    /// the paragraph's top left at (x, y)
    pub fn put(&mut self, x: f32, y: f32, p: Paragraph) {
        self.parts.push(Part::Text {
            x: self.px(x) as i32,
            y: self.px(y) as i32,
            paragraph: p,
        });
    }

    /// the icon's (0, 0) at (x, y); its bitmap ships with the first row
    /// this builder makes that uses it
    pub fn icon(&mut self, id: u16, icon: &Icon, x: f32, y: f32, c: Srgba8) {
        let key = GlyphKey::icon(id, self.s);
        if self.shipped.insert(key)
            && let Some(r) = self.text.rasterize_icon(icon, self.s)
        {
            self.rasters.push((key, r));
        }
        self.parts.push(Part::Icon {
            x: self.px(x) as i32,
            y: self.px(y) as i32,
            key,
            color: c.premultiply(),
        });
    }

    pub fn finish(self, h: f32) -> Row {
        Row {
            height: (h * self.s).round() as u32,
            parts: self.parts,
            rasters: self.rasters,
        }
    }
}

pub const SIDEBAR_W: f32 = 330.0;
/// ui-thread layout time per frame, both lists together
const IDLE_BUDGET: Duration = Duration::from_micros(1200);
const CACHE: usize = 16 << 20;
/// rows of history loaded per prepend, and how close to the oldest loaded
/// row the view gets before the next batch is asked for
const PREPEND: usize = 500;
const PREPEND_AT: u32 = 200;
const CORNER_MS: u64 = 180;

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
enum Pane {
    Side,
    Chat,
}

/// fixed text, shaped once per scale
struct Chrome {
    scale: Scale,
    side_title: Paragraph,
    side_count: Paragraph,
    chat_title: Paragraph,
    chat_people: Paragraph,
    footer: Paragraph,
    /// logical px
    side_head: f32,
    chat_head: f32,
    foot: f32,
}

/// what one frame did, for the window's log and the scripted test
pub struct LabFrame {
    pub chat: FrameReport,
    pub side: FrameReport,
    pub chat_jumps: usize,
    pub side_jumps: usize,
    pub cpu: Duration,
}

pub struct Lab {
    pub theme: Theme,
    text: TextSystem,
    pub chat: Chat,
    pub chats: Chats,
    chat_list: List<Chat>,
    side_list: List<Chats>,
    chat_scroll: Scroller,
    side_scroll: Scroller,
    chat_bar: Scrollbar,
    side_bar: Scrollbar,
    pub chat_check: Checker,
    pub side_check: Checker,
    size: (LogicalSize, Scale),
    chrome: Option<Chrome>,
    pointer: Option<LogicalPoint>,
    /// the pane a scrollbar drag started in
    dragging: Option<Pane>,
    selected: u32,
    hover: Option<u32>,
    /// came in while scrolled up
    unread: u32,
    unread_text: Option<(u32, Paragraph)>,
    /// corner button: shown or not, since when, and the opacity it left from
    corner: (bool, Timestamp, f32),
    /// the chat followed an append since the last frame
    followed: bool,
    pub auto_prepend: bool,
    shipped: HashSet<GlyphKey>,
}

impl Lab {
    pub fn new(mut text: TextSystem, waker: impl Fn() + Clone + Send + 'static) -> Self {
        text.add_font(what_assets::fonts::INSTRUMENT_SANS);
        text.add_font(what_assets::fonts::INSTRUMENT_SANS_ITALIC);
        let theme = DARK;
        let w = waker.clone();
        let chat_list = List::new(
            Bubbles::new(theme),
            &text,
            Box::new(w),
            CACHE,
            Gravity::Bottom,
        );
        let side_list = List::new(
            SideRows::new(theme),
            &text,
            Box::new(waker),
            CACHE / 4,
            Gravity::Top,
        );
        Self {
            theme,
            text,
            chat: Chat::new(0x5eed, 105_000, 5_000),
            chats: Chats::new(0xc4a7, 5_000),
            chat_list,
            side_list,
            chat_scroll: Scroller::new(),
            side_scroll: Scroller::new(),
            chat_bar: Scrollbar::new(),
            side_bar: Scrollbar::new(),
            chat_check: Checker::new(Gravity::Bottom),
            side_check: Checker::new(Gravity::Top),
            size: (LogicalSize::new(960.0, 640.0), Scale::ONE),
            chrome: None,
            pointer: None,
            dragging: None,
            selected: 1,
            hover: None,
            unread: 0,
            unread_text: None,
            corner: (false, Timestamp::from_nanos(0), 0.0),
            followed: false,
            auto_prepend: true,
            shipped: HashSet::new(),
        }
    }

    pub fn resize(&mut self, logical: LogicalSize, scale: Scale) {
        self.size = (logical, scale);
        if self.chrome.as_ref().is_some_and(|c| c.scale != scale) {
            self.chrome = None;
            self.unread_text = None;
        }
    }

    /// a fake message arrives
    pub fn incoming(&mut self) {
        self.chat.incoming();
        if self.chat_list.appended(&self.chat) {
            self.followed = true;
        } else {
            self.unread += 1;
        }
    }

    /// keep frames coming
    pub fn animating(&self, now: Timestamp) -> bool {
        self.chat_scroll.active()
            || self.side_scroll.active()
            || self.chat_bar.active(now)
            || self.side_bar.active(now)
            || self.chat_list.waiting()
            || self.side_list.waiting()
            || now.saturating_since(self.corner.1) < Duration::from_millis(CORNER_MS)
    }

    fn chrome(&mut self) -> &Chrome {
        let scale = self.size.1;
        if self.chrome.is_none() {
            let t = self.theme;
            let n = self.chats.chats.len();
            let count = if n >= 1000 {
                format!("{},{:03}", n / 1000, n % 1000)
            } else {
                n.to_string()
            };
            let mut p = |s: &str, st: Style| self.text.paragraph(s, &st, &[], scale);
            let side_title = p("Chats", style(15.0, 15.0 * 1.45, 600.0, t.text));
            let side_count = p(&count, style(12.5, 12.5 * 1.45, 400.0, t.muted));
            let chat_title = p("Weekend plans", style(15.0, 15.0 * 1.45, 600.0, t.text));
            let chat_people = p("6 people", style(12.5, 12.5 * 1.45, 400.0, t.muted));
            let footer = p("Message", style(14.5, 14.5 * 1.45, 400.0, t.muted));
            let side_head = 12.0 + side_title.height() + 10.0;
            let chat_head = 10.0 + chat_title.height() + 9.0;
            let foot = 11.0 + footer.height() + 11.0;
            self.chrome = Some(Chrome {
                scale,
                side_title,
                side_count,
                chat_title,
                chat_people,
                footer,
                side_head,
                chat_head,
                foot,
            });
        }
        self.chrome.as_ref().unwrap()
    }

    /// logical rects: sidebar list, chat list
    fn panes(&mut self) -> (LogicalRect, LogicalRect) {
        let (size, _) = self.size;
        let (sh, ch, f) = {
            let c = self.chrome();
            (c.side_head, c.chat_head, c.foot)
        };
        let side_w = SIDEBAR_W.min(size.width);
        let side = LogicalRect::new(
            LogicalPoint::new(0.0, sh),
            LogicalSize::new(side_w, (size.height - sh).max(0.0)),
        );
        let chat = LogicalRect::new(
            LogicalPoint::new(side_w, ch),
            LogicalSize::new(
                (size.width - side_w).max(0.0),
                (size.height - ch - f).max(0.0),
            ),
        );
        (side, chat)
    }

    fn physical(&self, r: LogicalRect) -> PhysicalRect {
        let s = self.size.1.as_f32();
        let x0 = (r.origin.x * s).round() as i32;
        let y0 = (r.origin.y * s).round() as i32;
        let x1 = ((r.origin.x + r.size.width) * s).round() as i32;
        let y1 = ((r.origin.y + r.size.height) * s).round() as i32;
        PhysicalRect::new(
            PhysicalPoint::new(x0, y0),
            PhysicalSize::new((x1 - x0).max(0) as u32, (y1 - y0).max(0) as u32),
        )
    }

    fn track(r: LogicalRect) -> LogicalRect {
        LogicalRect::new(
            LogicalPoint::new(r.origin.x + r.size.width - 16.0, r.origin.y),
            LogicalSize::new(16.0, r.size.height),
        )
    }

    /// corner button centre and whether the pointer is on it, logical
    fn corner_at(&mut self) -> LogicalPoint {
        let (size, _) = self.size;
        LogicalPoint::new(size.width - 22.0 - 18.0, size.height - 54.0 - 18.0)
    }

    /// true when the event changed what's on screen and a frame is due
    pub fn pointer(&mut self, ev: &PointerEvent, now: Timestamp) -> bool {
        let was = (self.hover, self.selected);
        match *ev {
            PointerEvent::Enter { pos }
            | PointerEvent::Motion { pos, .. }
            | PointerEvent::Button { pos, .. } => self.pointer = Some(pos),
            PointerEvent::Leave => self.pointer = None,
            _ => {}
        }
        let (side, chat) = self.panes();
        let over = self.pointer.map(|p| {
            if p.x < side.origin.x + side.size.width {
                Pane::Side
            } else {
                Pane::Chat
            }
        });
        match *ev {
            PointerEvent::Scroll(f) => match over {
                Some(Pane::Side) => self.side_scroll.scroll(&f),
                Some(Pane::Chat) => self.chat_scroll.scroll(&f),
                None => {}
            },
            PointerEvent::Hold { begin, time_ms, .. } => match over {
                Some(Pane::Side) => self.side_scroll.hold(begin, time_ms),
                Some(Pane::Chat) => self.chat_scroll.hold(begin, time_ms),
                None => {}
            },
            _ => {
                let mut took = false;
                for pane in [Pane::Side, Pane::Chat] {
                    if self.dragging.is_some_and(|d| d != pane) {
                        continue;
                    }
                    let (bar, r) = match pane {
                        Pane::Side => (&mut self.side_bar, side),
                        Pane::Chat => (&mut self.chat_bar, chat),
                    };
                    if let Some(ScrollbarAction::DragTo(f)) = bar.pointer(ev, Self::track(r), now) {
                        took = true;
                        self.dragging = Some(pane);
                        match pane {
                            Pane::Side => {
                                self.side_scroll.stop();
                                self.side_list.jump_to_fraction(f);
                            }
                            Pane::Chat => {
                                self.chat_scroll.stop();
                                self.chat_list.jump_to_fraction(f);
                            }
                        }
                    }
                }
                if matches!(
                    ev,
                    PointerEvent::Button { pressed: false, .. } | PointerEvent::Leave
                ) {
                    self.dragging = None;
                }
                if !took {
                    self.click_or_hover(ev, side, now);
                }
            }
        }
        // scrolls, drags, the corner button and bar hovers all start
        // something moving
        (self.hover, self.selected) != was || self.animating(now)
    }

    fn click_or_hover(&mut self, ev: &PointerEvent, side: LogicalRect, now: Timestamp) {
        let s = self.size.1.as_f32();
        let row_at = |list: &List<Chats>, p: LogicalPoint| {
            let inside = p.x >= side.origin.x
                && p.x < side.origin.x + side.size.width
                && p.y >= side.origin.y;
            let y = (p.y * s) as i32;
            inside
                .then(|| {
                    list.visible()
                        .iter()
                        .find(|r| y >= r.1 && y < r.1 + r.2 as i32)
                        .map(|r| r.0)
                })
                .flatten()
        };
        self.hover = self.pointer.and_then(|p| row_at(&self.side_list, p));
        if let PointerEvent::Button {
            button: Button::Left,
            pressed: true,
            pos,
            ..
        } = *ev
        {
            if let Some(k) = self.hover {
                self.selected = k;
            }
            let c = self.corner_at();
            let (dx, dy) = (pos.x - c.x, pos.y - c.y);
            if self.corner.0 && dx * dx + dy * dy <= 18.0 * 18.0 {
                self.chat_scroll.stop();
                self.chat_list.jump_to(BOTTOM, Align::Bottom);
                self.unread = 0;
                self.set_corner(false, now);
            }
        }
    }

    fn set_corner(&mut self, on: bool, now: Timestamp) {
        if self.corner.0 != on {
            let from = self.corner_alpha(now);
            self.corner = (on, now, from);
        }
    }

    fn corner_alpha(&self, now: Timestamp) -> f32 {
        let (on, since, from) = self.corner;
        let t = (now.saturating_since(since).as_secs_f32() * 1000.0 / CORNER_MS as f32).min(1.0);
        let to = if on { 1.0 } else { 0.0 };
        from + (to - from) * ease(t)
    }

    pub fn frame(
        &mut self,
        now: Timestamp,
        scene: &mut Scene,
        glyphs: &mut impl GlyphSink,
    ) -> LabFrame {
        let start = Instant::now();
        let (side, chat) = self.panes();
        let (side_vp, chat_vp) = (self.physical(side), self.physical(chat));
        let scale = self.size.1;

        let dy = self.chat_scroll.tick(now);
        let cr = self.chat_list.frame(
            &self.chat,
            chat_vp,
            scale,
            dy,
            self.chat_scroll.remaining(),
            IDLE_BUDGET.saturating_sub(start.elapsed()),
            &mut self.text,
        );
        if cr.hit_edge {
            self.chat_scroll.hit_edge();
        }
        if cr.moved != 0 {
            self.chat_bar.scrolled(now);
        }
        let dy = self.side_scroll.tick(now);
        let sr = self.side_list.frame(
            &self.chats,
            side_vp,
            scale,
            dy,
            self.side_scroll.remaining(),
            IDLE_BUDGET.saturating_sub(start.elapsed()),
            &mut self.text,
        );
        if sr.hit_edge {
            self.side_scroll.hit_edge();
        }
        if sr.moved != 0 {
            self.side_bar.scrolled(now);
        }

        let chat_jumps = self
            .chat_check
            .frame(self.chat_list.visible(), chat_vp, &cr, self.followed)
            .len();
        let side_jumps = self
            .side_check
            .frame(self.side_list.visible(), side_vp, &sr, false)
            .len();
        self.followed = false;

        if self.chat_list.stick_to_bottom() {
            self.unread = 0;
        }
        let newest = self
            .chat
            .oldest_loaded()
            .map(|lo| lo + self.chat.len() as u32 - 1);
        let at_newest = self
            .chat_list
            .visible()
            .iter()
            .any(|r| r.0 == BOTTOM || Some(r.0) == newest);
        self.set_corner(!at_newest || self.unread > 0, now);

        if self.auto_prepend
            && self.chat.has_more()
            && let (Some(first), Some(lo)) = (
                self.chat_list.visible().first().map(|r| r.0),
                self.chat.oldest_loaded(),
            )
            && first != BOTTOM
            && first.saturating_sub(lo) < PREPEND_AT
        {
            self.chat.prepend(PREPEND);
            self.chat_list.prepended();
        }

        self.draw(now, side, chat, side_vp, chat_vp, scene, glyphs);
        LabFrame {
            chat: cr,
            side: sr,
            chat_jumps,
            side_jumps,
            cpu: start.elapsed(),
        }
    }

    /// test hook: what the chat list shows
    pub fn chat_rows(&self) -> &[(u32, i32, u32)] {
        self.chat_list.visible()
    }

    pub fn side_rows(&self) -> &[(u32, i32, u32)] {
        self.side_list.visible()
    }

    pub fn prepend(&mut self, n: usize) -> usize {
        let got = self.chat.prepend(n);
        if got > 0 {
            self.chat_list.prepended();
        }
        got
    }

    #[allow(clippy::too_many_arguments)]
    fn draw(
        &mut self,
        now: Timestamp,
        side: LogicalRect,
        chat: LogicalRect,
        side_vp: PhysicalRect,
        chat_vp: PhysicalRect,
        scene: &mut Scene,
        glyphs: &mut impl GlyphSink,
    ) {
        let t = self.theme;
        let s = self.size.1.as_f32();
        let (size, _) = self.size;
        let px = |v: f32| (v * s).round();
        let rect = |x: f32, y: f32, w: f32, h: f32| {
            Rect::new(px(x), px(y), px(x + w) - px(x), px(y + h) - px(y))
        };
        let solid = |r: Rect, c: Srgba8, clip: Clip| Quad {
            rect: r,
            fill: Fill::Solid(c.premultiply()),
            clip,
            ..Quad::default()
        };

        // sidebar on the chrome colour; chat header and footer strips
        scene.quad(solid(
            rect(0.0, 0.0, side.size.width, size.height),
            t.chrome,
            Clip::NONE,
        ));
        let (ch, f) = {
            let c = self.chrome();
            (c.chat_head, c.foot)
        };
        scene.quad(solid(
            rect(chat.origin.x, 0.0, chat.size.width, ch),
            t.chrome,
            Clip::NONE,
        ));
        scene.quad(solid(
            rect(chat.origin.x, size.height - f, chat.size.width, f),
            t.chrome,
            Clip::NONE,
        ));

        // selection and hover under the sidebar rows
        let clip = Clip::rect(Rect::new(
            side_vp.origin.x as f32,
            side_vp.origin.y as f32,
            side_vp.size.width as f32,
            side_vp.size.height as f32,
        ));
        for &(k, top, h) in self.side_list.visible() {
            let r = Rect::new(0.0, top as f32, side_vp.size.width as f32, h as f32);
            if k == self.selected {
                scene.quad(solid(r, t.inc, clip));
                let bar = Rect::new(0.0, top as f32, px(3.0), h as f32);
                scene.quad(solid(bar, t.acc, clip));
            } else if Some(k) == self.hover {
                scene.quad(solid(r, mix(t.chrome, t.inc, 0.5), clip));
            }
        }
        self.side_list.draw(&mut self.text, scene, glyphs);
        self.chat_list.draw(&mut self.text, scene, glyphs);

        // headers and footer text
        let chrome = self.chrome.take().expect("chrome");
        let mut text_at = |p: &Paragraph, x: f32, y: f32, text: &mut TextSystem| {
            draw_paragraph(scene, text, glyphs, p, px(x) as i32, px(y) as i32);
        };
        let base = |p: &Paragraph| p.lines().next().map_or(0.0, |l| l.baseline);
        text_at(&chrome.side_title, 14.0, 12.0, &mut self.text);
        let y = 12.0 + base(&chrome.side_title) - base(&chrome.side_count);
        text_at(
            &chrome.side_count,
            14.0 + chrome.side_title.width() + 8.0,
            y,
            &mut self.text,
        );
        let x = chat.origin.x + 16.0;
        text_at(&chrome.chat_title, x, 10.0, &mut self.text);
        let y = 10.0 + base(&chrome.chat_title) - base(&chrome.chat_people);
        text_at(
            &chrome.chat_people,
            x + chrome.chat_title.width() + 8.0,
            y,
            &mut self.text,
        );
        text_at(&chrome.footer, x, size.height - f + 11.0, &mut self.text);
        self.chrome = Some(chrome);

        // overlays
        scene.layer();
        let (pos, len) = self.chat_list.position(&self.chat);
        let track = self.physical(Self::track(chat));
        self.chat_bar.draw(
            scene,
            track,
            pos,
            len,
            self.size.1,
            now,
            t.bar_idle,
            t.bar_hot,
        );
        let (pos, len) = self.side_list.position(&self.chats);
        let track = self.physical(Self::track(side));
        self.side_bar.draw(
            scene,
            track,
            pos,
            len,
            self.size.1,
            now,
            t.bar_idle,
            t.bar_hot,
        );
        self.draw_corner(now, scene, glyphs, chat_vp);
    }

    fn draw_corner(
        &mut self,
        now: Timestamp,
        scene: &mut Scene,
        glyphs: &mut impl GlyphSink,
        chat_vp: PhysicalRect,
    ) {
        let a = self.corner_alpha(now);
        if a <= 0.0 {
            return;
        }
        let t = self.theme;
        let s = self.size.1.as_f32();
        let c = self.corner_at();
        // slides 6 px down as it fades out
        let (cx, cy) = (c.x, c.y + 6.0 * (1.0 - a));
        let px = |v: f32| (v * s).round();
        let fade = |c: Srgba8| Srgba8::new(c.r, c.g, c.b, (c.a as f32 * a).round() as u8);
        let circle = |r: f32, col: Srgba8| Quad {
            rect: Rect::new(
                px(cx - r),
                px(cy - r),
                px(cx + r) - px(cx - r),
                px(cy + r) - px(cy - r),
            ),
            radii: Corners::all(r * s),
            fill: Fill::Solid(fade(col).premultiply()),
            clip: Clip::rect(Rect::new(
                chat_vp.origin.x as f32,
                0.0,
                chat_vp.size.width as f32,
                (self.size.0.height * s).round(),
            )),
            ..Quad::default()
        };
        let acc = self.unread > 0;
        if acc {
            scene.quad(circle(18.0, t.acc));
            let n = self.unread;
            if self.unread_text.as_ref().is_none_or(|(m, _)| *m != n) {
                let label = if n > 99 {
                    "99+".to_string()
                } else {
                    n.to_string()
                };
                let size = if label.len() > 2 { 11.0 } else { 13.0 };
                let p = self.text.paragraph(
                    &label,
                    &style(size, size * 1.45, 600.0, t.acc_ink),
                    &[],
                    self.size.1,
                );
                self.unread_text = Some((n, p));
            }
            let (_, p) = self.unread_text.as_ref().unwrap();
            let (w, h) = (p.width(), p.height());
            let (x, y) = (px(cx - w / 2.0) as i32, px(cy - h / 2.0) as i32);
            draw_tinted(scene, &mut self.text, glyphs, p, x, y, a);
        } else {
            scene.quad(circle(19.0, t.ring));
            scene.quad(circle(18.0, t.inc));
            let key = GlyphKey::icon(CHEVRON, s);
            if self.shipped.insert(key) || !glyphs.has(key) {
                let r = self
                    .text
                    .rasterize_icon(&chevron(), s)
                    .unwrap_or_else(empty_raster);
                glyphs.add(key, &r);
            }
            let mut col = t.text;
            col.a = (255.0 * a).round() as u8;
            scene.glyph(Glyph {
                x: px(cx - 7.0) as i32,
                y: px(cy - 7.0) as i32,
                key: key.0,
                color: col.premultiply(),
                clip: Clip::NONE,
            });
        }
    }
}

/// css `ease`
fn ease(t: f32) -> f32 {
    // close enough to cubic-bezier(.25, .1, .25, 1) for a 180 ms fade
    let t = t.clamp(0.0, 1.0);
    let x = t * t * (3.0 - 2.0 * t);
    x + (t - x) * 0.25 * (1.0 - t)
}

fn empty_raster() -> Raster {
    Raster {
        left: 0,
        top: 0,
        width: 0,
        height: 0,
        kind: what_text::GlyphKind::Mask,
        data: Vec::new(),
    }
}

fn ensure(text: &mut TextSystem, glyphs: &mut impl GlyphSink, p: &Paragraph) {
    for k in p.keys() {
        if !glyphs.has(k) {
            let r = text.rasterize(k).unwrap_or_else(empty_raster);
            glyphs.add(k, &r);
        }
    }
}

fn draw_paragraph(
    scene: &mut Scene,
    text: &mut TextSystem,
    glyphs: &mut impl GlyphSink,
    p: &Paragraph,
    x: i32,
    y: i32,
) {
    draw_tinted(scene, text, glyphs, p, x, y, 1.0);
}

fn draw_tinted(
    scene: &mut Scene,
    text: &mut TextSystem,
    glyphs: &mut impl GlyphSink,
    p: &Paragraph,
    x: i32,
    y: i32,
    alpha: f32,
) {
    ensure(text, glyphs, p);
    for g in p.glyphs() {
        let c = g.color;
        scene.glyph(Glyph {
            x: x + g.x,
            y: y + g.y,
            key: g.key.0,
            color: what_core::PremulRgba {
                r: c.r * alpha,
                g: c.g * alpha,
                b: c.b * alpha,
                a: c.a * alpha,
            },
            clip: Clip::NONE,
        });
    }
}

#[cfg(test)]
mod tests {
    use super::*;

    #[test]
    fn css_hsl_and_gradients() {
        assert_eq!(hsl(0.0, 1.0, 0.5), Srgba8::rgb(255, 0, 0));
        assert_eq!(hsl(120.0, 1.0, 0.25), Srgba8::rgb(0, 128, 0));
        // 180deg runs straight down
        let (a, b) = css_linear(180.0, 100.0, 50.0);
        assert!((a[0] - 0.5).abs() < 1e-6 && a[1].abs() < 1e-6);
        assert!((b[1] - 1.0).abs() < 1e-6);
    }
}
