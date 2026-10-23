//! frame bookkeeping: what the app gets per frame, presentation feedback, the
//! per-burst stderr summary and the optional per-frame TSV.

use std::collections::VecDeque;
use std::fs::File;
use std::io::{BufWriter, Write};
use std::path::Path;

use log::{info, warn};
use wayland_client::protocol::wl_surface::WlSurface;
use wayland_client::{Connection, Dispatch, QueueHandle, WEnum};
use wayland_protocols::wp::presentation_time::client::{wp_presentation, wp_presentation_feedback};
use what_core::{Timestamp, WindowId};

use crate::{Event, State};

#[derive(Clone, Copy, Debug)]
pub struct FrameInfo {
    /// increments per frame across all windows
    pub seq: u64,
    /// CLOCK_MONOTONIC when the frame started; advance animations to this
    pub time: Timestamp,
    /// the wl_callback timestamp that released this frame, if one did (ms, compositor clock)
    pub callback_ms: Option<u32>,
}

#[derive(Clone, Copy, Debug)]
pub struct PresentInfo {
    pub seq: u64,
    /// when the frame hit the screen, in the presentation clock
    pub presented: Timestamp,
    /// 0 when the output has no fixed refresh
    pub refresh_ns: u32,
    pub vsync: bool,
    pub zero_copy: bool,
    /// vblanks skipped since the previous presented frame of this burst,
    /// split into those before this frame was started (nothing asked for a
    /// frame) and those after (the frame was late)
    pub idle: u32,
    pub late: u32,
}

pub(crate) fn now() -> Timestamp {
    let ts = rustix::time::clock_gettime(rustix::time::ClockId::Monotonic);
    Timestamp::from_nanos(ts.tv_sec as u64 * 1_000_000_000 + ts.tv_nsec as u64)
}

struct Row {
    window: WindowId,
    seq: u64,
    callback_ms: Option<u32>,
    cpu_start: u64,
    cpu_end: u64,
    present: u64,
}

#[derive(Default)]
struct Burst {
    window: Option<WindowId>,
    cpu_ns: Vec<u64>,
    late: u32,
    idle: u32,
    /// msc and time of the previous presentation
    last: Option<(u64, u64)>,
    refresh_ns: u32,
    /// idle, but feedback for its last frames is still out
    ending: bool,
}

pub(crate) struct FrameStats {
    next_seq: u64,
    rows: VecDeque<Row>,
    file: Option<BufWriter<File>>,
    bursts: Vec<Burst>,
    clock_warned: bool,
}

impl FrameStats {
    pub(crate) fn new(frame_log: Option<&Path>) -> Self {
        let file = frame_log.and_then(|p| match File::create(p) {
            Ok(f) => {
                let mut w = BufWriter::new(f);
                let _ = writeln!(
                    w,
                    "seq\twindow\tcallback_ms\tcpu_start_ns\tcpu_end_ns\tpresent_ns\tpresented_ns\trefresh_ns\tflags\tlate\tidle"
                );
                Some(w)
            }
            Err(e) => {
                warn!("frame log {}: {e}", p.display());
                None
            }
        });
        Self {
            next_seq: 0,
            rows: VecDeque::new(),
            file,
            bursts: Vec::new(),
            clock_warned: false,
        }
    }

    fn burst(&mut self, id: WindowId) -> &mut Burst {
        let i = match self.bursts.iter().position(|b| b.window == Some(id)) {
            Some(i) => i,
            None => {
                self.bursts.push(Burst {
                    window: Some(id),
                    ..Default::default()
                });
                self.bursts.len() - 1
            }
        };
        &mut self.bursts[i]
    }

    pub(crate) fn begin_frame(&mut self, id: WindowId, callback_ms: Option<u32>) -> FrameInfo {
        let seq = self.next_seq;
        self.next_seq += 1;
        let time = now();
        self.burst(id).ending = false;
        self.rows.push_back(Row {
            window: id,
            seq,
            callback_ms,
            cpu_start: time.as_nanos(),
            cpu_end: 0,
            present: 0,
        });
        // feedback that never comes (window unmapped) must not grow this forever
        while self.rows.len() > 256 {
            self.rows.pop_front();
        }
        FrameInfo {
            seq,
            time,
            callback_ms,
        }
    }

    pub(crate) fn end_frame(&mut self, seq: u64) {
        let end = now().as_nanos();
        let Some(row) = self.rows.iter_mut().rev().find(|r| r.seq == seq) else {
            return;
        };
        row.cpu_end = end;
        let (id, cpu) = (row.window, end.saturating_sub(row.cpu_start));
        self.burst(id).cpu_ns.push(cpu);
    }

    pub(crate) fn present_call(&mut self, seq: u64) {
        if let Some(row) = self.rows.iter_mut().rev().find(|r| r.seq == seq) {
            row.present = now().as_nanos();
        }
    }

    pub(crate) fn clock_id(&mut self, clk_id: u32) {
        // CLOCK_MONOTONIC; anything else makes presented times incomparable with ours
        if clk_id != 1 && !self.clock_warned {
            warn!("presentation clock is {clk_id}, not CLOCK_MONOTONIC");
            self.clock_warned = true;
        }
    }

    fn take_row(&mut self, seq: u64) -> Option<Row> {
        let i = self.rows.iter().position(|r| r.seq == seq)?;
        self.rows.remove(i)
    }

    /// a frame the app started but never presented
    pub(crate) fn skipped(&mut self, id: WindowId, seq: u64) {
        self.take_row(seq);
        self.maybe_finish(id);
    }

    /// no wp_presentation: the row is all we'll ever know
    pub(crate) fn unobserved(&mut self, id: WindowId, seq: u64) {
        if let (Some(r), Some(f)) = (self.take_row(seq), self.file.as_mut()) {
            let _ = writeln!(
                f,
                "{}\t{}\t{}\t{}\t{}\t{}\t\t\t\t\t",
                r.seq,
                r.window.0,
                r.callback_ms.map_or(String::new(), |v| v.to_string()),
                r.cpu_start,
                r.cpu_end,
                r.present,
            );
            let _ = f.flush();
        }
        self.maybe_finish(id);
    }

    fn presented(
        &mut self,
        id: WindowId,
        seq: u64,
        presented: u64,
        refresh_ns: u32,
        msc: u64,
        flags: wp_presentation_feedback::Kind,
    ) -> PresentInfo {
        let row = self.take_row(seq);
        let burst = self.burst(id);
        let skipped = match burst.last {
            Some((prev, _)) if msc > prev => (msc - prev - 1) as u32,
            _ => 0,
        };
        let idle = match (burst.last, &row) {
            (Some((_, prev_at)), Some(r)) if refresh_ns > 0 => {
                (r.cpu_start.saturating_sub(prev_at) / refresh_ns as u64).min(skipped as u64) as u32
            }
            _ => 0,
        };
        let late = skipped - idle;
        burst.late += late;
        burst.idle += idle;
        // msc 0 means the output doesn't count vblanks
        burst.last = (msc != 0).then_some((msc, presented));
        burst.refresh_ns = refresh_ns;
        let rows = row.is_some();
        if let (Some(f), Some(r)) = (self.file.as_mut(), row) {
            let _ = writeln!(
                f,
                "{}\t{}\t{}\t{}\t{}\t{}\t{presented}\t{refresh_ns}\t{:#x}\t{late}\t{idle}",
                r.seq,
                r.window.0,
                r.callback_ms.map_or(String::new(), |v| v.to_string()),
                r.cpu_start,
                r.cpu_end,
                r.present,
                flags.bits(),
            );
            let _ = f.flush();
        }
        if rows {
            self.maybe_finish(id);
        }
        PresentInfo {
            seq,
            presented: Timestamp::from_nanos(presented),
            refresh_ns,
            vsync: flags.contains(wp_presentation_feedback::Kind::Vsync),
            zero_copy: flags.contains(wp_presentation_feedback::Kind::ZeroCopy),
            idle,
            late,
        }
    }

    fn discarded(&mut self, id: WindowId, seq: u64) {
        if let (Some(r), Some(f)) = (self.take_row(seq), self.file.as_mut()) {
            let _ = writeln!(
                f,
                "{}\t{}\t{}\t{}\t{}\t{}\t\t\tdiscarded\t\t",
                r.seq,
                r.window.0,
                r.callback_ms.map_or(String::new(), |v| v.to_string()),
                r.cpu_start,
                r.cpu_end,
                r.present,
            );
            let _ = f.flush();
        }
        self.maybe_finish(id);
    }

    /// drawing went idle; the summary waits until the burst's feedback is in
    pub(crate) fn burst_end(&mut self, id: WindowId) {
        self.burst(id).ending = true;
        self.maybe_finish(id);
    }

    fn maybe_finish(&mut self, id: WindowId) {
        if !self.burst(id).ending || self.rows.iter().any(|r| r.window == id) {
            return;
        }
        let fresh = Burst {
            window: Some(id),
            ..Default::default()
        };
        let burst = std::mem::replace(self.burst(id), fresh);
        let mut cpu = burst.cpu_ns;
        if cpu.is_empty() {
            return;
        }
        cpu.sort_unstable();
        // nearest rank
        let pct = |p: usize| cpu[(cpu.len() * p).div_ceil(100).max(1) - 1] as f64 / 1e6;
        let hz = if burst.refresh_ns > 0 {
            1e9 / burst.refresh_ns as f64
        } else {
            0.0
        };
        info!(
            target: "what_platform::frames",
            "{id:?} burst: {} frames, {} late vblanks, {} idle, cpu p50 {:.2} ms p99 {:.2} ms, {hz:.2} Hz",
            cpu.len(),
            burst.late,
            burst.idle,
            pct(50),
            pct(99),
        );
    }
}

impl State {
    pub(crate) fn request_feedback(&mut self, id: WindowId, surface: &WlSurface, seq: u64) {
        self.presentation.present_call(seq);
        match &self.globals.presentation {
            Some(p) => {
                p.feedback(surface, &self.qh, (id, seq));
            }
            None => self.presentation.unobserved(id, seq),
        }
    }
}

impl Dispatch<wp_presentation::WpPresentation, ()> for State {
    fn event(
        state: &mut Self,
        _: &wp_presentation::WpPresentation,
        event: wp_presentation::Event,
        _: &(),
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        if let wp_presentation::Event::ClockId { clk_id } = event {
            state.presentation.clock_id(clk_id);
        }
    }
}

impl Dispatch<wp_presentation_feedback::WpPresentationFeedback, (WindowId, u64)> for State {
    fn event(
        state: &mut Self,
        _: &wp_presentation_feedback::WpPresentationFeedback,
        event: wp_presentation_feedback::Event,
        &(id, seq): &(WindowId, u64),
        _: &Connection,
        _: &QueueHandle<Self>,
    ) {
        match event {
            wp_presentation_feedback::Event::Presented {
                tv_sec_hi,
                tv_sec_lo,
                tv_nsec,
                refresh,
                seq_hi,
                seq_lo,
                flags,
            } => {
                let secs = ((tv_sec_hi as u64) << 32) | tv_sec_lo as u64;
                let presented = secs * 1_000_000_000 + tv_nsec as u64;
                let msc = ((seq_hi as u64) << 32) | seq_lo as u64;
                let flags = match flags {
                    WEnum::Value(k) => k,
                    WEnum::Unknown(bits) => {
                        wp_presentation_feedback::Kind::from_bits_truncate(bits)
                    }
                };
                let info = state
                    .presentation
                    .presented(id, seq, presented, refresh, msc, flags);
                state.events.push(Event::Presented(id, info));
            }
            wp_presentation_feedback::Event::Discarded => state.presentation.discarded(id, seq),
            _ => {}
        }
    }
}
