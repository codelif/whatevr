//! the shaping threads: rows go out as jobs, laid out with their new glyphs
//! rasterized, and come back through another ring. jobs on the urgent rings
//! go first; the ui blocks on those. wakes the ui only while it waits on
//! something (a deferred jump).

use std::collections::HashSet;
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::Duration;

use rtrb::{Consumer, Producer, RingBuffer};
use what_core::Scale;
use what_text::{GlyphKey, GlyphKind, Raster, TextSystem};

use crate::list::RowBuilder;
use crate::row::Row;

/// jobs in flight per list, split across its threads
pub(crate) const RING: usize = 256;
/// a screen's worth of rows, with room, per thread
const URGENT: usize = 64;

pub(crate) struct Job<K, C> {
    pub key: K,
    pub rev: u64,
    pub width: u32,
    pub scale: Scale,
    pub content: C,
}

pub(crate) struct Done<K> {
    pub key: K,
    pub rev: u64,
    pub width: u32,
    pub scale: Scale,
    pub row: Row,
}

struct Shared {
    stop: AtomicBool,
    /// the ui waits on results: wake it after each one
    waiting: AtomicBool,
}

/// one shaping thread and its rings
struct Lane<K, C> {
    jobs: Producer<Job<K, C>>,
    urgent: Producer<Job<K, C>>,
    done: Consumer<Done<K>>,
    thread: Option<JoinHandle<()>>,
}

impl<K, C> Lane<K, C> {
    fn unpark(&self) {
        if let Some(t) = &self.thread {
            t.thread().unpark();
        }
    }
}

pub(crate) struct Worker<K, C> {
    lanes: Vec<Lane<K, C>>,
    /// lane the next job tries first
    next: usize,
    shared: Arc<Shared>,
}

/// shaping threads per list. a screen the ui waits on gets split across
/// them; past 4 the split stops paying for the extra cold caches
fn threads() -> usize {
    thread::available_parallelism()
        .map_or(1, |n| n.get() / 3)
        .clamp(1, 4)
}

impl<K: Send + 'static, C: Send + 'static> Worker<K, C> {
    pub fn spawn<B: RowBuilder<C> + Clone>(
        builder: &B,
        text: &TextSystem,
        waker: Box<dyn Fn() + Send>,
    ) -> Self {
        let shared = Arc::new(Shared {
            stop: AtomicBool::new(false),
            waiting: AtomicBool::new(false),
        });
        let waker = Arc::new(Mutex::new(waker));
        let n = threads();
        let lanes = (0..n)
            .map(|i| {
                let (jobs, mut rx) = RingBuffer::<Job<K, C>>::new(RING / n);
                let (urgent, mut urx) = RingBuffer::<Job<K, C>>::new(URGENT);
                let (mut tx, done) = RingBuffer::<Done<K>>::new(RING / n);
                let (sh, waker) = (shared.clone(), waker.clone());
                let wake = move || {
                    if let Ok(w) = waker.lock() {
                        w();
                    }
                };
                let mut builder = builder.clone();
                let mut text = text.sibling();
                let thread = thread::Builder::new()
                    .name(format!("what-ui rows {i}"))
                    .spawn(move || {
                        let mut seen: HashSet<GlyphKey> = HashSet::new();
                        while !sh.stop.load(Ordering::Acquire) {
                            let Ok(job) = urx.pop().or_else(|_| rx.pop()) else {
                                thread::park();
                                continue;
                            };
                            let mut row =
                                builder.build(&job.content, job.width, job.scale, &mut text);
                            row.rasters.retain(|(k, _)| seen.insert(*k));
                            let fresh: Vec<GlyphKey> =
                                row.keys().filter(|k| seen.insert(*k)).collect();
                            for k in fresh {
                                // no ink still ships, so the ui marks the key known
                                let r = text.rasterize(k).unwrap_or_else(empty);
                                row.rasters.push((k, r));
                            }
                            let mut out = Done {
                                key: job.key,
                                rev: job.rev,
                                width: job.width,
                                scale: job.scale,
                                row,
                            };
                            // the ui drains every frame; a full ring only waits that long
                            loop {
                                match tx.push(out) {
                                    Ok(()) => break,
                                    Err(rtrb::PushError::Full(back)) => {
                                        if sh.stop.load(Ordering::Acquire) {
                                            return;
                                        }
                                        out = back;
                                        wake();
                                        thread::park_timeout(Duration::from_millis(2));
                                    }
                                }
                            }
                            if sh.waiting.load(Ordering::Acquire) {
                                wake();
                            }
                        }
                    })
                    .expect("spawning a row worker");
                Lane {
                    jobs,
                    urgent,
                    done,
                    thread: Some(thread),
                }
            })
            .collect();
        Self {
            lanes,
            next: 0,
            shared,
        }
    }

    /// false when every ring is full; the job comes back in the error
    pub fn push(&mut self, job: Job<K, C>) -> Result<(), Job<K, C>> {
        self.spread(job, |l| &mut l.jobs)
    }

    /// ahead of every queued job, round robin so a screen builds in
    /// parallel; false (job back) when those rings are full
    pub fn push_urgent(&mut self, job: Job<K, C>) -> Result<(), Job<K, C>> {
        self.spread(job, |l| &mut l.urgent)
    }

    fn spread(
        &mut self,
        mut job: Job<K, C>,
        ring: impl Fn(&mut Lane<K, C>) -> &mut Producer<Job<K, C>>,
    ) -> Result<(), Job<K, C>> {
        let n = self.lanes.len();
        for i in 0..n {
            let l = &mut self.lanes[(self.next + i) % n];
            match ring(l).push(job) {
                Ok(()) => {
                    l.unpark();
                    self.next = (self.next + i + 1) % n;
                    return Ok(());
                }
                Err(rtrb::PushError::Full(j)) => job = j,
            }
        }
        Err(job)
    }

    /// jobs that fit before the rings are full
    pub fn room(&self) -> usize {
        self.lanes.iter().map(|l| l.jobs.slots()).sum()
    }

    pub fn pop(&mut self) -> Option<Done<K>> {
        for l in &mut self.lanes {
            if let Ok(d) = l.done.pop() {
                // it may be parked on a full ring
                l.unpark();
                return Some(d);
            }
        }
        None
    }

    pub fn set_waiting(&self, on: bool) {
        self.shared.waiting.store(on, Ordering::Release);
    }
}

impl<K, C> Drop for Worker<K, C> {
    fn drop(&mut self) {
        self.shared.stop.store(true, Ordering::Release);
        for l in &mut self.lanes {
            if let Some(t) = l.thread.take() {
                t.thread().unpark();
                let _ = t.join();
            }
        }
    }
}

/// a glyph with no ink, like a space
pub(crate) fn empty() -> Raster {
    Raster {
        left: 0,
        top: 0,
        width: 0,
        height: 0,
        kind: GlyphKind::Mask,
        data: Vec::new(),
    }
}
