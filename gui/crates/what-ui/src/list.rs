//! anchored virtual list. the position is a row plus how far its bottom sits
//! above the viewport bottom, and a row is only measured by laying it out, so
//! nothing on screen ever moves because an estimate got corrected.

use std::collections::hash_map::DefaultHasher;
use std::collections::{BTreeMap, HashMap, HashSet};
use std::hash::{Hash, Hasher};
use std::time::{Duration, Instant};

use what_core::{PhysicalRect, Scale};
use what_render::{Clip, GlyphFormat, GlyphImage, Gpu, Rect, Renderer, Scene};
use what_text::{GlyphKey, GlyphKind, Raster, TextSystem};

use crate::row::Row;
use crate::worker::{self, Done, Job, Worker};

/// longest a frame waits on the worker for rows it has to show; past it
/// they're laid out on the ui thread after all
const URGENT_WAIT: Duration = Duration::from_millis(8);
/// longest a frame waits for a jump's screen; past it the jump shows on a
/// later frame, so this one stays in budget
const JUMP_WAIT: Duration = Duration::from_micros(1500);

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Dir {
    Older,
    Newer,
}

/// which edge the list opens on and short lists sit against: Bottom for a
/// chat (newest at the bottom), Top for the chat list
#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Gravity {
    Top,
    Bottom,
}

#[derive(Clone, Copy, Debug, PartialEq, Eq)]
pub enum Align {
    Top,
    Center,
    Bottom,
}

/// the app's data. cheap calls, ui thread only.
pub trait ListSource {
    type Key: Copy + Eq + Hash + Send + 'static;
    /// what the builder needs to lay a row out, owned so it can cross threads
    type Content: Send + 'static;
    fn newest(&self) -> Option<Self::Key>;
    fn neighbour(&self, key: Self::Key, dir: Dir) -> Option<Self::Key>;
    /// bumps when the row's content changes
    fn revision(&self, key: Self::Key) -> u64;
    fn content(&self, key: Self::Key) -> Self::Content;
    /// rough position of a key, 0 = oldest .. 1 = newest
    fn fraction(&self, key: Self::Key) -> f32;
    fn at_fraction(&self, f: f32) -> Option<Self::Key>;
    /// every row known and few enough to measure them all
    fn total(&self) -> Option<usize>;
}

/// lays a row out; runs on the worker and, on a miss, on the ui thread
pub trait RowBuilder<C>: Send + 'static {
    fn build(&mut self, content: &C, width: u32, scale: Scale, text: &mut TextSystem) -> Row;
}

/// where glyph uploads go: the renderer, or a recorder in tests. an empty
/// raster (0x0) means the glyph has no ink.
pub trait GlyphSink {
    fn has(&self, key: GlyphKey) -> bool;
    fn add(&mut self, key: GlyphKey, raster: &Raster);
}

/// the real sink: uploads into the renderer's glyph atlas
pub struct RendererSink<'a> {
    pub renderer: &'a mut Renderer,
    pub gpu: &'a Gpu,
}

impl GlyphSink for RendererSink<'_> {
    fn has(&self, key: GlyphKey) -> bool {
        self.renderer.has_glyph(key.0)
    }

    fn add(&mut self, key: GlyphKey, r: &Raster) {
        let image = GlyphImage {
            left: r.left,
            top: r.top,
            width: r.width,
            height: r.height,
            format: match r.kind {
                GlyphKind::Mask => GlyphFormat::Mask,
                GlyphKind::Color => GlyphFormat::Rgba,
            },
            data: &r.data,
        };
        self.renderer.add_glyph(self.gpu, key.0, image);
    }
}

#[derive(Debug, Default)]
pub struct FrameReport {
    /// physical px the content went up (towards newer) this frame
    pub moved: i32,
    pub hit_edge: bool,
    /// a deferred jump or the first anchoring landed: no continuity this frame
    pub jumped: bool,
    /// (key hash, top y) of every drawn row, for the jump checker
    pub rows: Vec<(u64, i32)>,
    /// rows the frame had to lay out on the spot because they weren't ready
    pub sync_shaped: u32,
}

type Slot<K> = (K, u64, u32, Scale);

struct Entry {
    row: Row,
    bytes: usize,
    /// its key in the lru
    used: u64,
    /// frame it was last drawn in; never evicted from the current one
    frame: u64,
}

#[derive(Clone, Copy)]
enum Target<K> {
    Key(K, Align),
    Fraction(f32),
}

/// every row's height, oldest first, when the source says it's small
struct Measured<K> {
    width: u32,
    scale: Scale,
    tops: Vec<u64>,
    index: HashMap<K, usize>,
    keys: Vec<K>,
    total: u64,
}

/// rows covering the viewport, oldest first, tops in physical px
struct Span<K> {
    rows: Vec<(K, i32, u32)>,
    oldest: bool,
    newest: bool,
}

impl<K> Span<K> {
    fn top(&self) -> i32 {
        self.rows.first().map_or(0, |r| r.1)
    }

    fn bottom(&self) -> i32 {
        self.rows.last().map_or(0, |r| r.1 + r.2 as i32)
    }
}

pub struct List<S: ListSource> {
    /// key and viewport bottom minus that row's bottom
    anchor: Option<(S::Key, i32)>,
    /// sub-px rest of the deltas
    frac: f32,
    stuck: bool,
    jump: Option<Target<S::Key>>,
    /// a jump that ran out of time, kept in case its rows arrive: a drag
    /// shows the last target that finished while a newer one builds
    tried: Option<Target<S::Key>>,
    builder: Box<dyn RowBuilder<S::Content>>,
    worker: Worker<S::Key, S::Content>,
    cache: HashMap<Slot<S::Key>, Entry>,
    lru: BTreeMap<u64, Slot<S::Key>>,
    bytes: usize,
    budget: usize,
    stamp: u64,
    tick: u64,
    /// running row height for walking past rows not laid out yet
    est: f32,
    inflight: HashSet<Slot<S::Key>>,
    /// bitmaps from worker rows, uploaded at the next frame
    rasters: Vec<(GlyphKey, Raster)>,
    measured: Option<Measured<S::Key>>,
    /// last frame's viewport and top row, for position()
    last: Option<(PhysicalRect, S::Key, i32, u32)>,
    last_bottom: Option<(S::Key, i32, u32)>,
    /// rows the last frame put on screen, for draw() and the app
    visible: Vec<(S::Key, i32, u32)>,
    slots: Vec<Slot<S::Key>>,
    /// the viewport left and clip draw() uses
    view: (i32, Clip),
    /// running cost of one ui-thread build, for the idle budget
    build_cost: Duration,
    gravity: Gravity,
    /// last frame's viewport height, so a resize keeps the gravity edge
    vh: Option<i32>,
}

/// what one frame lays out against
struct Ctx<'a> {
    width: u32,
    scale: Scale,
    sync: u32,
    text: &'a mut TextSystem,
}

impl<S: ListSource> List<S> {
    pub fn new(
        builder: impl RowBuilder<S::Content> + Clone,
        text: &TextSystem,
        waker: Box<dyn Fn() + Send>,
        cache_bytes: usize,
        gravity: Gravity,
    ) -> Self {
        let worker = Worker::spawn(&builder, text, waker);
        Self {
            anchor: None,
            frac: 0.0,
            stuck: true,
            jump: None,
            tried: None,
            builder: Box::new(builder),
            worker,
            cache: HashMap::new(),
            lru: BTreeMap::new(),
            bytes: 0,
            budget: cache_bytes,
            stamp: 0,
            tick: 0,
            est: 0.0,
            inflight: HashSet::new(),
            rasters: Vec::new(),
            measured: None,
            last: None,
            last_bottom: None,
            build_cost: Duration::from_micros(500),
            gravity,
            vh: None,
            visible: Vec::new(),
            slots: Vec::new(),
            view: (0, Clip::NONE),
        }
    }

    /// per frame: apply the scroller's delta (logical px, + = content up),
    /// re-anchor, prefetch out to `ahead` (Scroller::remaining), fill gaps
    /// until `budget` from now runs out. draws nothing: paint under the
    /// rows using visible(), then call draw()
    #[allow(clippy::too_many_arguments)]
    pub fn frame(
        &mut self,
        src: &S,
        viewport: PhysicalRect,
        scale: Scale,
        delta: f32,
        ahead: f32,
        budget: Duration,
        text: &mut TextSystem,
    ) -> FrameReport {
        let start = Instant::now();
        self.stamp += 1;
        self.poll();
        let mut report = FrameReport::default();
        let mut cx = Ctx {
            width: viewport.size.width,
            scale,
            sync: 0,
            text,
        };
        let vt = viewport.origin.y;
        let vh = viewport.size.height as i32;
        let vb = vt + vh;
        if self
            .measured
            .as_ref()
            .is_some_and(|m| m.width != cx.width || m.scale != scale)
        {
            self.measured = None;
        }

        // the anchor is bottom based; a top list keeps its top still instead
        if self.gravity == Gravity::Top
            && let (Some(old), Some(a)) = (self.vh, self.anchor.as_mut())
        {
            a.1 += vh - old;
        }
        self.vh = Some(vh);
        if self.anchor.is_none() {
            self.anchor = match self.gravity {
                Gravity::Bottom => src.newest().map(|k| (k, 0)),
                Gravity::Top => oldest(src).map(|k| {
                    let h = self.height(src, k, &mut cx, true).expect("sync") as i32;
                    (k, vh - h)
                }),
            };
            report.jumped = true;
        }
        // a jump lands only once its whole screen is laid out. the worker gets
        // it urgently and the frame waits, so a thumb drag lands every frame;
        // past the wait it lands on a later one
        let until = Instant::now() + JUMP_WAIT;
        let mut rounds = 0;
        while let Some(t) = self.jump {
            let Some((k, off, exact)) = self.target(t, src, vh, &mut cx) else {
                self.jump = None;
                break;
            };
            if exact && self.walk(src, (k, off), vt, vb, &mut cx, false).is_some() {
                self.anchor = Some((k, off));
                self.jump = None;
                self.tried = None;
                self.frac = 0.0;
                report.jumped = true;
                break;
            }
            // a guessed offset is off by up to a row: fetch that much more
            let slack = if exact { 0 } else { self.guess(&cx) as i32 };
            rounds += 1;
            if rounds < 4 && self.fetch(src, (k, off), vt, vb, slack, until, &cx) {
                continue;
            }
            if let Some(p) = self.tried.replace(t)
                && let Some((pk, po, true)) = self.target(p, src, vh, &mut cx)
                && self.walk(src, (pk, po), vt, vb, &mut cx, false).is_some()
            {
                self.anchor = Some((pk, po));
                self.frac = 0.0;
                report.jumped = true;
            }
            for m in self.screen_misses(src, k, off, vt, vb, &cx) {
                let slot = self.slot(src, m, &cx);
                if !self.inflight.contains(&slot) && self.worker.room() > 0 {
                    self.push_job(src, m, slot);
                }
            }
            break;
        }
        self.worker.set_waiting(self.jump.is_some());
        self.visible.clear();
        let Some((key, mut offset)) = self.anchor else {
            self.last = None;
            return report;
        };

        self.frac += delta * scale.as_f32();
        let d = self.frac as i32;
        self.frac -= d as f32;
        let before = offset;
        offset += d;
        // the worker's text caches are warm and the ui's aren't, so even rows
        // needed right now are laid out there; the next frame's too
        let until = Instant::now() + URGENT_WAIT;
        self.fetch(src, (key, offset), vt, vb, d.abs(), until, &cx);

        let mut span = self
            .walk(src, (key, offset), vt, vb, &mut cx, true)
            .expect("sync walks finish");
        for _ in 0..3 {
            let shift = self.clamp(&span, vt, vb);
            if shift == 0 {
                break;
            }
            offset -= shift;
            self.frac = 0.0;
            // the edge pushed back against this frame's movement
            if d != 0 && shift.signum() == d.signum() {
                report.hit_edge = true;
            }
            span = self
                .walk(src, (key, offset), vt, vb, &mut cx, true)
                .expect("sync walks finish");
        }
        report.moved = if report.jumped { 0 } else { offset - before };
        self.stuck = match self.gravity {
            Gravity::Bottom => span.newest && span.bottom() == vb,
            Gravity::Top => span.oldest && span.top() == vt,
        };

        // re-anchor on the row at the viewport bottom; the rows are exact so
        // the conversion is too
        let bottom = span
            .rows
            .iter()
            .rev()
            .find(|r| r.1 < vb)
            .or(span.rows.first())
            .copied();
        if let Some((k, top, h)) = bottom {
            self.anchor = Some((k, vb - (top + h as i32)));
            self.last_bottom = Some((k, top, h));
        }
        self.last = span
            .rows
            .iter()
            .find(|r| r.1 + r.2 as i32 > vt)
            .or(span.rows.first())
            .map(|&(k, top, h)| (viewport, k, top, h));

        self.view = (
            viewport.origin.x,
            Clip::rect(Rect::new(
                viewport.origin.x as f32,
                vt as f32,
                viewport.size.width as f32,
                vh as f32,
            )),
        );
        self.slots.clear();
        for &(k, top, h) in &span.rows {
            if top >= vb || top + h as i32 <= vt {
                continue;
            }
            let slot = self.slot(src, k, &cx);
            self.touch(&slot);
            self.slots.push(slot);
            self.visible.push((k, top, h));
            report.rows.push((hash(&k), top));
        }

        // while jumps land (a thumb drag) the rows around this spot are stale
        // by the next frame: the workers stay free for the next screen
        let order = if report.jumped || self.jump.is_some() {
            Vec::new()
        } else {
            self.prefetch_order(src, &span, ahead * scale.as_f32(), vh, &cx)
        };
        for &k in &order {
            if self.worker.room() == 0 {
                break;
            }
            let slot = self.slot(src, k, &cx);
            if !self.cache.contains_key(&slot) && !self.inflight.contains(&slot) {
                self.push_job(src, k, slot);
            }
        }
        // what the workers didn't take, with the frame's leftover time
        for &k in &order {
            if start.elapsed() + self.build_cost > budget {
                break;
            }
            let slot = self.slot(src, k, &cx);
            if self.cache.contains_key(&slot) || self.inflight.contains(&slot) {
                continue;
            }
            self.build_here(src, k, slot, &mut cx);
        }
        if self.measured.is_none()
            && let Some(n) = src.total()
        {
            self.measure(src, n, &cx);
        }
        self.evict();
        report.sync_shaped = cx.sync;
        report
    }

    /// the rows the last frame() put on screen, top to bottom: key, top and
    /// height in physical px
    pub fn visible(&self) -> &[(S::Key, i32, u32)] {
        &self.visible
    }

    /// draw what the last frame() placed, uploading the glyphs it needs
    pub fn draw(&mut self, text: &mut TextSystem, scene: &mut Scene, glyphs: &mut impl GlyphSink) {
        self.flush_rasters(glyphs);
        let (x, clip) = self.view;
        for (slot, &(_, top, _)) in self.slots.iter().zip(&self.visible) {
            let row = &self.cache[slot].row;
            for g in row.keys() {
                if !glyphs.has(g) {
                    let r = text.rasterize(g).unwrap_or_else(worker::empty);
                    glyphs.add(g, &r);
                }
            }
            row.draw(scene, x, top, clip);
        }
    }

    /// sitting on the gravity edge (the bottom for a chat, the top for a
    /// top list)
    pub fn stick_to_bottom(&self) -> bool {
        self.stuck
    }

    /// a new newest row arrived; follows it when stuck to the bottom
    pub fn appended(&mut self, src: &S) -> bool {
        self.measured = None;
        if !self.stuck || self.jump.is_some() || self.gravity == Gravity::Top {
            return false;
        }
        if let Some(k) = src.newest() {
            self.anchor = Some((k, 0));
            self.frac = 0.0;
        }
        true
    }

    /// older rows arrived above; the anchor doesn't move, except that a top
    /// list stuck to its top shows the new first row
    pub fn prepended(&mut self) {
        self.measured = None;
        if self.gravity == Gravity::Top && self.stuck && self.jump.is_none() {
            self.anchor = None;
        }
    }

    /// lands once the target screen is laid out; the old position keeps
    /// drawing until then
    pub fn jump_to(&mut self, key: S::Key, align: Align) {
        self.jump = Some(Target::Key(key, align));
        self.worker.set_waiting(true);
    }

    /// 0 = oldest row's top at the top .. 1 = newest row's bottom at the bottom
    pub fn jump_to_fraction(&mut self, f: f32) {
        self.jump = Some(Target::Fraction(f.clamp(0.0, 1.0)));
        self.worker.set_waiting(true);
    }

    /// drain worker results; frame() does it too
    pub fn poll(&mut self) {
        while let Some(Done {
            key,
            rev,
            width,
            scale,
            mut row,
        }) = self.worker.pop()
        {
            let slot = (key, rev, width, scale);
            self.inflight.remove(&slot);
            self.rasters.append(&mut row.rasters);
            self.insert(slot, row);
        }
    }

    /// a jump waits on the worker; the app should wake on the waker
    pub fn waiting(&self) -> bool {
        self.jump.is_some()
    }

    /// scrollbar: where the view is (0 = oldest row's top at the top .. 1 =
    /// newest row's bottom at the bottom) and, when every row is measured,
    /// viewport / total height
    pub fn position(&self, src: &S) -> (f32, Option<f32>) {
        let Some((vp, top_key, top, h)) = self.last else {
            return (1.0, None);
        };
        let vh = vp.size.height as u64;
        if let Some(m) = &self.measured
            && let Some(&i) = m.index.get(&top_key)
        {
            if m.total <= vh {
                return (1.0, Some(1.0));
            }
            let above = m.tops[i] as i64 + (vp.origin.y - top) as i64;
            let f = above.max(0) as f32 / (m.total - vh) as f32;
            return (f.clamp(0.0, 1.0), Some(vh as f32 / m.total as f32));
        }
        let at = |k: S::Key, top: i32, h: u32, y: i32| {
            let f0 = src.fraction(k);
            let f1 = src
                .neighbour(k, Dir::Newer)
                .map_or(1.0, |n| src.fraction(n));
            f0 + (f1 - f0) * ((y - top) as f32 / h.max(1) as f32).clamp(0.0, 1.0)
        };
        let ft = at(top_key, top, h, vp.origin.y);
        let fb = self.last_bottom.map_or(ft, |(k, t, h)| {
            at(k, t, h, vp.origin.y + vp.size.height as i32)
        });
        // what's on screen is a slice of the range, so both ends reach 0 and 1
        let seen = (fb - ft).clamp(0.0, 0.999);
        ((ft / (1.0 - seen)).clamp(0.0, 1.0), None)
    }

    /// how far to move the span (+ = down) to settle it on the edges; the
    /// gravity edge wins when the content is shorter than the viewport
    fn clamp(&self, span: &Span<S::Key>, vt: i32, vb: i32) -> i32 {
        let (top, bottom) = (span.top(), span.bottom());
        match self.gravity {
            Gravity::Bottom => {
                if span.newest && bottom < vb {
                    vb - bottom
                } else if span.oldest && top > vt {
                    let room = if span.newest { bottom - vb } else { i32::MAX };
                    -(top - vt).min(room)
                } else {
                    0
                }
            }
            Gravity::Top => {
                if span.oldest && top > vt {
                    vt - top
                } else if span.newest && bottom < vb {
                    let room = if span.oldest { vt - top } else { i32::MAX };
                    (vb - bottom).min(room)
                } else {
                    0
                }
            }
        }
    }

    fn slot(&self, src: &S, k: S::Key, cx: &Ctx<'_>) -> Slot<S::Key> {
        (k, src.revision(k), cx.width, cx.scale)
    }

    fn guess(&self, cx: &Ctx<'_>) -> u32 {
        if self.est > 0.0 {
            self.est as u32
        } else {
            (64.0 * cx.scale.as_f32()) as u32
        }
    }

    /// row height in physical px; lays it out right here on a miss when `sync`
    fn height(&mut self, src: &S, k: S::Key, cx: &mut Ctx<'_>, sync: bool) -> Option<u32> {
        let slot = self.slot(src, k, cx);
        if let Some(e) = self.cache.get(&slot) {
            return Some(e.row.height);
        }
        if !sync {
            return None;
        }
        cx.sync += 1;
        Some(self.build_here(src, k, slot, cx))
    }

    fn build_here(&mut self, src: &S, k: S::Key, slot: Slot<S::Key>, cx: &mut Ctx<'_>) -> u32 {
        let t = Instant::now();
        let mut row = self
            .builder
            .build(&src.content(k), cx.width, cx.scale, cx.text);
        self.build_cost = (self.build_cost * 7 + t.elapsed()) / 8;
        let h = row.height;
        self.rasters.append(&mut row.rasters);
        self.insert(slot, row);
        h
    }

    /// rows covering [vt, vb) with `anchor`'s bottom at vb - offset. None
    /// when a row isn't laid out and `sync` is off.
    fn walk(
        &mut self,
        src: &S,
        (key, offset): (S::Key, i32),
        vt: i32,
        vb: i32,
        cx: &mut Ctx<'_>,
        sync: bool,
    ) -> Option<Span<S::Key>> {
        let bottom = vb - offset;
        let h = self.height(src, key, cx, sync)?;
        let mut up = vec![(key, bottom - h as i32, h)];
        let mut oldest = false;
        let (mut k, mut top) = (key, bottom - h as i32);
        while top > vt {
            let Some(p) = src.neighbour(k, Dir::Older) else {
                oldest = true;
                break;
            };
            let h = self.height(src, p, cx, sync)?;
            top -= h as i32;
            up.push((p, top, h));
            k = p;
        }
        oldest = oldest || src.neighbour(k, Dir::Older).is_none();
        up.reverse();
        let mut newest = false;
        let (mut k, mut b) = (key, bottom);
        while b < vb {
            let Some(n) = src.neighbour(k, Dir::Newer) else {
                newest = true;
                break;
            };
            let h = self.height(src, n, cx, sync)?;
            up.push((n, b, h));
            b += h as i32;
            k = n;
        }
        newest = newest || src.neighbour(k, Dir::Newer).is_none();
        Some(Span {
            rows: up,
            oldest,
            newest,
        })
    }

    /// where a jump wants the anchor: its key, its offset, and whether that
    /// offset is exact (false while the row's height is still a guess)
    fn target(
        &mut self,
        t: Target<S::Key>,
        src: &S,
        vh: i32,
        cx: &mut Ctx<'_>,
    ) -> Option<(S::Key, i32, bool)> {
        let guess = self.guess(cx);
        match t {
            Target::Key(k, a) => {
                let known = self.height(src, k, cx, false);
                let h = known.unwrap_or(guess) as i32;
                let off = match a {
                    Align::Top => vh - h,
                    Align::Center => (vh - h) / 2,
                    Align::Bottom => 0,
                };
                Some((k, off, known.is_some()))
            }
            Target::Fraction(f) => {
                if let Some(m) = &self.measured {
                    let last = *m.keys.last()?;
                    if m.total <= vh as u64 {
                        return Some((last, 0, true));
                    }
                    // exact: the pixel the fraction names
                    let above = (f as f64 * (m.total - vh as u64) as f64).round() as u64;
                    let i = m.tops.partition_point(|&t| t <= above).max(1) - 1;
                    let h = m.tops.get(i + 1).copied().unwrap_or(m.total) - m.tops[i];
                    let off = vh as i64 + (above - m.tops[i]) as i64 - h as i64;
                    return Some((m.keys[i], off as i32, true));
                }
                let k = src.at_fraction(f)?;
                // slide the row through the viewport as f goes 0 to 1
                let known = self.height(src, k, cx, false);
                let h = known.unwrap_or(guess) as i32;
                let off = ((1.0 - f) * (vh - h) as f32).round() as i32;
                Some((k, off, known.is_some()))
            }
        }
    }

    /// rows a screen still needs, guessing heights past the gaps
    fn screen_misses(
        &mut self,
        src: &S,
        key: S::Key,
        offset: i32,
        vt: i32,
        vb: i32,
        cx: &Ctx<'_>,
    ) -> Vec<S::Key> {
        let mut miss = Vec::new();
        let guess = self.guess(cx);
        let mut h_of = |this: &Self, k: S::Key| {
            let slot = this.slot(src, k, cx);
            match this.cache.get(&slot) {
                Some(e) => e.row.height,
                None => {
                    miss.push(k);
                    guess
                }
            }
        };
        let bottom = vb - offset;
        let mut top = bottom - h_of(self, key) as i32;
        let mut k = key;
        while top > vt {
            let Some(p) = src.neighbour(k, Dir::Older) else {
                break;
            };
            top -= h_of(self, p) as i32;
            k = p;
        }
        let (mut k, mut b) = (key, bottom);
        while b < vb {
            let Some(n) = src.neighbour(k, Dir::Newer) else {
                break;
            };
            b += h_of(self, n) as i32;
            k = n;
        }
        miss
    }

    /// rows to lay out ahead of time, nearest first: out to the scroll's
    /// landing point plus a screen in its direction, a screen the other way
    fn prefetch_order(
        &self,
        src: &S,
        span: &Span<S::Key>,
        ahead: f32,
        vh: i32,
        cx: &Ctx<'_>,
    ) -> Vec<S::Key> {
        const MAX: usize = 512;
        let far = ahead.abs() as i64 + vh as i64;
        let (newer, older) = if ahead >= 0.0 {
            (far, vh as i64)
        } else {
            (vh as i64, far)
        };
        let guess = self.guess(cx) as i64;
        // past half the cache the far rows would evict the near ones before
        // they ever show
        let avg = self.bytes / self.cache.len().max(1);
        let mut room = self.budget / 2;
        let mut side = |from: Option<S::Key>, dir: Dir, reach: i64, out: &mut Vec<S::Key>| {
            let (mut k, mut gone) = (from, 0);
            while let Some(c) = k.and_then(|c| src.neighbour(c, dir)) {
                if gone >= reach || out.len() >= MAX {
                    break;
                }
                let slot = self.slot(src, c, cx);
                let e = self.cache.get(&slot);
                let bytes = e.map_or(avg, |e| e.bytes);
                if bytes > room {
                    break;
                }
                room -= bytes;
                out.push(c);
                gone += e.map_or(guess, |e| e.row.height as i64);
                k = Some(c);
            }
        };
        let (first, last) = (
            span.rows.first().map(|r| r.0),
            span.rows.last().map(|r| r.0),
        );
        let mut out = Vec::new();
        if ahead >= 0.0 {
            side(last, Dir::Newer, newer, &mut out);
            side(first, Dir::Older, older, &mut out);
        } else {
            side(first, Dir::Older, older, &mut out);
            side(last, Dir::Newer, newer, &mut out);
        }
        out
    }

    /// sum every row once the source says there are few enough; misses go
    /// to the worker and it tries again next frame
    fn measure(&mut self, src: &S, n: usize, cx: &Ctx<'_>) {
        let mut keys = Vec::with_capacity(n);
        let mut heights = Vec::with_capacity(n);
        let mut whole = true;
        let mut k = src.newest();
        while let Some(c) = k {
            let slot = self.slot(src, c, cx);
            match self.cache.get(&slot) {
                Some(e) => heights.push(e.row.height),
                None => {
                    whole = false;
                    if !self.inflight.contains(&slot) && self.worker.room() > 0 {
                        self.push_job(src, c, slot);
                    }
                }
            }
            keys.push(c);
            k = src.neighbour(c, Dir::Older);
        }
        if !whole {
            return;
        }
        keys.reverse();
        heights.reverse();
        let mut tops = Vec::with_capacity(keys.len());
        let mut total = 0u64;
        for h in heights {
            tops.push(total);
            total += h as u64;
        }
        let index = keys.iter().enumerate().map(|(i, &k)| (k, i)).collect();
        self.measured = Some(Measured {
            width: cx.width,
            scale: cx.scale,
            tops,
            index,
            keys,
            total,
        });
    }

    /// blocks until the rows [vt, vb) needs are laid out or `until` passes,
    /// sending them and `margin` px more each way to the worker's urgent
    /// lane. true when they all made it
    #[allow(clippy::too_many_arguments)]
    fn fetch(
        &mut self,
        src: &S,
        (key, offset): (S::Key, i32),
        vt: i32,
        vb: i32,
        margin: i32,
        until: Instant,
        cx: &Ctx<'_>,
    ) -> bool {
        let mut sent = HashSet::new();
        loop {
            if self.screen_misses(src, key, offset, vt, vb, cx).is_empty() {
                return true;
            }
            for k in self.screen_misses(src, key, offset, vt - margin, vb + margin, cx) {
                let slot = self.slot(src, k, cx);
                if sent.insert(slot) {
                    let job = Job {
                        key: k,
                        rev: slot.1,
                        width: slot.2,
                        scale: slot.3,
                        content: src.content(k),
                    };
                    if self.worker.push_urgent(job).is_ok() {
                        self.inflight.insert(slot);
                    }
                }
            }
            if Instant::now() >= until {
                return false;
            }
            std::thread::yield_now();
            self.poll();
        }
    }

    fn push_job(&mut self, src: &S, k: S::Key, slot: Slot<S::Key>) {
        let job = Job {
            key: k,
            rev: slot.1,
            width: slot.2,
            scale: slot.3,
            content: src.content(k),
        };
        if self.worker.push(job).is_ok() {
            self.inflight.insert(slot);
        }
    }

    fn flush_rasters(&mut self, glyphs: &mut impl GlyphSink) {
        for (k, r) in self.rasters.drain(..) {
            if !glyphs.has(k) {
                glyphs.add(k, &r);
            }
        }
    }

    fn insert(&mut self, slot: Slot<S::Key>, row: Row) {
        self.est = if self.est == 0.0 {
            row.height as f32
        } else {
            self.est * 0.95 + row.height as f32 * 0.05
        };
        self.tick += 1;
        let bytes = row.bytes();
        self.bytes += bytes;
        self.lru.insert(self.tick, slot);
        let old = self.cache.insert(
            slot,
            Entry {
                row,
                bytes,
                used: self.tick,
                frame: 0,
            },
        );
        if let Some(o) = old {
            self.bytes -= o.bytes;
            self.lru.remove(&o.used);
        }
    }

    fn touch(&mut self, slot: &Slot<S::Key>) {
        let Some(e) = self.cache.get_mut(slot) else {
            return;
        };
        self.tick += 1;
        self.lru.remove(&e.used);
        e.used = self.tick;
        e.frame = self.stamp;
        self.lru.insert(self.tick, *slot);
    }

    fn evict(&mut self) {
        while self.bytes > self.budget {
            let Some((&t, &slot)) = self.lru.iter().next() else {
                break;
            };
            if self.cache.get(&slot).is_some_and(|e| e.frame == self.stamp) {
                break;
            }
            self.lru.remove(&t);
            if let Some(e) = self.cache.remove(&slot) {
                self.bytes -= e.bytes;
            }
        }
    }
}

/// at_fraction(0) is only rough; walk the rest of the way
fn oldest<S: ListSource>(src: &S) -> Option<S::Key> {
    let mut k = src.at_fraction(0.0).or_else(|| src.newest())?;
    while let Some(p) = src.neighbour(k, Dir::Older) {
        k = p;
    }
    Some(k)
}

fn hash<K: Hash>(k: &K) -> u64 {
    let mut h = DefaultHasher::new();
    k.hash(&mut h);
    h.finish()
}

#[cfg(test)]
mod tests {
    use std::collections::HashSet;

    use what_core::{PhysicalPoint, PhysicalSize};

    use super::*;

    /// rows 0 (oldest) ..n, heights 20..83 px
    struct Src(u32);

    impl ListSource for Src {
        type Key = u32;
        type Content = u32;
        fn newest(&self) -> Option<u32> {
            self.0.checked_sub(1)
        }
        fn neighbour(&self, k: u32, dir: Dir) -> Option<u32> {
            match dir {
                Dir::Older => k.checked_sub(1),
                Dir::Newer => (k + 1 < self.0).then_some(k + 1),
            }
        }
        fn revision(&self, _: u32) -> u64 {
            0
        }
        fn content(&self, k: u32) -> u32 {
            k
        }
        fn fraction(&self, k: u32) -> f32 {
            k as f32 / (self.0 - 1).max(1) as f32
        }
        fn at_fraction(&self, f: f32) -> Option<u32> {
            self.newest().map(|n| (f * n as f32).round() as u32)
        }
        fn total(&self) -> Option<usize> {
            (self.0 <= 200).then_some(self.0 as usize)
        }
    }

    #[derive(Clone)]
    struct Rows;

    impl RowBuilder<u32> for Rows {
        fn build(&mut self, k: &u32, _: u32, _: Scale, _: &mut TextSystem) -> Row {
            Row {
                height: 20 + (k * 37 % 64),
                parts: Vec::new(),
                rasters: Vec::new(),
            }
        }
    }

    #[derive(Default)]
    struct Sink(HashSet<GlyphKey>);

    impl GlyphSink for Sink {
        fn has(&self, k: GlyphKey) -> bool {
            self.0.contains(&k)
        }
        fn add(&mut self, k: GlyphKey, _: &Raster) {
            self.0.insert(k);
        }
    }

    struct Harness {
        list: List<Src>,
        src: Src,
        text: TextSystem,
        sink: Sink,
        vp: PhysicalRect,
    }

    impl Harness {
        fn new(n: u32) -> Self {
            Self::with(n, Gravity::Bottom)
        }

        fn with(n: u32, gravity: Gravity) -> Self {
            let text = TextSystem::with_fonts(&[]).unwrap();
            Self {
                list: List::new(Rows, &text, Box::new(|| {}), 1 << 20, gravity),
                src: Src(n),
                text,
                sink: Sink::default(),
                vp: PhysicalRect::new(PhysicalPoint::new(0, 10), PhysicalSize::new(300, 400)),
            }
        }

        fn frame(&mut self, delta: f32, ahead: f32) -> FrameReport {
            let r = self.list.frame(
                &self.src,
                self.vp,
                Scale::ONE,
                delta,
                ahead,
                Duration::ZERO,
                &mut self.text,
            );
            let mut scene = Scene::new();
            self.list.draw(&mut self.text, &mut scene, &mut self.sink);
            r
        }

        fn wait(&mut self) {
            std::thread::sleep(Duration::from_millis(5));
        }
    }

    /// rows seen in both frames moved by exactly what the list says
    fn continuous(a: &FrameReport, b: &FrameReport) {
        let tops: HashMap<u64, i32> = a.rows.iter().copied().collect();
        for (k, t) in &b.rows {
            if let Some(t0) = tops.get(k) {
                assert_eq!(t0 - t, b.moved, "row {k} jumped");
            }
        }
    }

    #[test]
    fn starts_at_the_bottom_and_scrolls_without_jumps() {
        let mut h = Harness::new(10_000);
        let first = h.frame(0.0, 0.0);
        assert!(first.jumped && h.list.stick_to_bottom());
        assert_eq!(first.rows.last().unwrap().0, hash(&9_999u32));
        let mut prev = first;
        h.wait();
        let mut sync = 0;
        for i in 0..300 {
            // towards older, uneven steps with sub-px parts
            let r = h.frame(-(37.3 + (i % 7) as f32 * 11.9), -4000.0);
            continuous(&prev, &r);
            assert!(!r.hit_edge);
            sync += r.sync_shaped;
            prev = r;
            h.wait();
        }
        assert!(!h.list.stick_to_bottom());
        assert_eq!(sync, 0, "the worker should stay ahead");
    }

    #[test]
    fn stops_at_both_ends() {
        let mut h = Harness::new(30);
        h.frame(0.0, 0.0);
        let r = h.frame(-100_000.0, 0.0);
        assert!(r.hit_edge);
        assert_eq!(r.rows[0], (hash(&0u32), h.vp.origin.y));
        let r = h.frame(100_000.0, 0.0);
        assert!(r.hit_edge && h.list.stick_to_bottom());
        let r = h.frame(50.0, 0.0);
        assert!(r.hit_edge && r.moved == 0);
    }

    #[test]
    fn short_lists_sit_at_the_bottom() {
        let mut h = Harness::new(3);
        h.frame(0.0, 0.0);
        let r = h.frame(-500.0, 0.0);
        let bottom = r.rows.last().unwrap().1 + 20 + (2 * 37 % 64);
        assert_eq!(bottom, h.vp.origin.y + 400);
        assert_eq!(r.moved, 0);
    }

    #[test]
    fn deferred_jump_lands_on_its_row() {
        let mut h = Harness::new(100_000);
        h.frame(0.0, 0.0);
        h.list.jump_to(50_000, Align::Top);
        assert!(h.list.waiting());
        let mut landed = None;
        for _ in 0..100 {
            let r = h.frame(0.0, 0.0);
            assert_eq!(r.sync_shaped, 0);
            if r.jumped {
                landed = Some(r);
                break;
            }
            h.wait();
        }
        let r = landed.expect("jump never landed");
        assert!(r.rows.contains(&(hash(&50_000u32), h.vp.origin.y)));
        assert!(!h.list.waiting());
    }

    #[test]
    fn measured_lists_report_exact_position() {
        let mut h = Harness::new(120);
        for _ in 0..50 {
            h.frame(0.0, 0.0);
            h.wait();
        }
        let (pos, len) = h.list.position(&h.src);
        assert_eq!(pos, 1.0);
        let total: u32 = (0..120).map(|k| 20 + k * 37 % 64).sum();
        assert!((len.unwrap() - 400.0 / total as f32).abs() < 1e-6);
        h.list.jump_to_fraction(0.5);
        let mut r = h.frame(0.0, 0.0);
        while !r.jumped {
            h.wait();
            r = h.frame(0.0, 0.0);
        }
        h.frame(0.0, 0.0);
        let (pos, _) = h.list.position(&h.src);
        assert!((pos - 0.5).abs() < 0.002, "{pos}");
    }

    #[test]
    fn top_lists_open_at_the_top_and_keep_it() {
        let mut h = Harness::with(5_000, Gravity::Top);
        let first = h.frame(0.0, 0.0);
        assert!(first.jumped && h.list.stick_to_bottom());
        assert_eq!(first.rows[0], (hash(&0u32), h.vp.origin.y));
        let r = h.frame(-300.0, 0.0);
        assert!(r.hit_edge && r.moved == 0);
        let mut prev = h.frame(250.0, 0.0);
        assert_eq!(prev.moved, 250);
        // a taller window shows more below; nothing on screen moves
        h.vp.size.height += 120;
        let r = h.frame(0.0, 0.0);
        continuous(&prev, &r);
        assert_eq!(r.moved, 0);
        prev = r;
        h.vp.size.height -= 200;
        let r = h.frame(0.0, 0.0);
        continuous(&prev, &r);
    }

    #[test]
    fn short_top_lists_sit_at_the_top() {
        let mut h = Harness::with(3, Gravity::Top);
        h.frame(0.0, 0.0);
        let r = h.frame(500.0, 0.0);
        assert_eq!(r.rows[0], (hash(&0u32), h.vp.origin.y));
        assert_eq!(r.moved, 0);
        assert!(h.list.stick_to_bottom());
    }
}
