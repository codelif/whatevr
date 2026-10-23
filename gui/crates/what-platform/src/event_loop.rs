//! one epoll for everything: the wayland socket, key repeat, app fds and app timers.

use std::io;
use std::os::fd::{AsFd, BorrowedFd, OwnedFd};
use std::time::Duration;

use rustix::event::epoll;
use rustix::time::{Itimerspec, TimerfdClockId, TimerfdFlags, TimerfdTimerFlags, Timespec};
use what_core::Token;

// top two bits of epoll data say who owns the fd; app tokens get the remaining 62
const KIND_SHIFT: u32 = 62;
const USER_FD: u64 = 0;
const USER_TIMER: u64 = 1 << KIND_SHIFT;
const INTERNAL: u64 = 2 << KIND_SHIFT;
const PAYLOAD: u64 = (1 << KIND_SHIFT) - 1;

pub(crate) const WAYLAND: u64 = INTERNAL;
pub(crate) const REPEAT: u64 = INTERNAL | 1;

#[derive(Clone, Copy, Debug)]
pub(crate) enum Ready {
    Wayland,
    Repeat,
    Fd(Token),
    Timer(Token),
}

pub(crate) struct EventLoop {
    epoll: OwnedFd,
    timers: Vec<(Token, OwnedFd)>,
    buf: Vec<epoll::Event>,
}

impl EventLoop {
    pub(crate) fn new() -> io::Result<Self> {
        Ok(Self {
            epoll: epoll::create(epoll::CreateFlags::CLOEXEC)?,
            timers: Vec::new(),
            buf: Vec::with_capacity(32),
        })
    }

    pub(crate) fn add_internal(&self, fd: BorrowedFd<'_>, id: u64) -> io::Result<()> {
        epoll::add(
            &self.epoll,
            fd,
            epoll::EventData::new_u64(id),
            epoll::EventFlags::IN,
        )?;
        Ok(())
    }

    /// wayland wants EPOLLOUT only while a flush hit a full socket
    pub(crate) fn set_wayland_writable(
        &self,
        fd: BorrowedFd<'_>,
        writable: bool,
    ) -> io::Result<()> {
        let mut flags = epoll::EventFlags::IN;
        if writable {
            flags |= epoll::EventFlags::OUT;
        }
        epoll::modify(&self.epoll, fd, epoll::EventData::new_u64(WAYLAND), flags)?;
        Ok(())
    }

    pub(crate) fn add_fd(&self, fd: BorrowedFd<'_>, token: Token) -> io::Result<()> {
        let data = checked(token)? | USER_FD;
        epoll::add(
            &self.epoll,
            fd,
            epoll::EventData::new_u64(data),
            epoll::EventFlags::IN,
        )?;
        Ok(())
    }

    pub(crate) fn remove_fd(&self, fd: BorrowedFd<'_>) -> io::Result<()> {
        epoll::delete(&self.epoll, fd)?;
        Ok(())
    }

    pub(crate) fn set_timer(&mut self, token: Token, after: Duration) -> io::Result<()> {
        let data = checked(token)? | USER_TIMER;
        let idx = match self.timers.iter().position(|(t, _)| *t == token) {
            Some(i) => i,
            None => {
                let fd = timerfd()?;
                epoll::add(
                    &self.epoll,
                    &fd,
                    epoll::EventData::new_u64(data),
                    epoll::EventFlags::IN,
                )?;
                self.timers.push((token, fd));
                self.timers.len() - 1
            }
        };
        // a zero it_value disarms, so the shortest real timer is 1ns
        arm(
            &self.timers[idx].1,
            after.max(Duration::from_nanos(1)),
            Duration::ZERO,
        )
    }

    pub(crate) fn cancel_timer(&mut self, token: Token) -> io::Result<()> {
        if let Some(i) = self.timers.iter().position(|(t, _)| *t == token) {
            let (_, fd) = self.timers.swap_remove(i);
            epoll::delete(&self.epoll, &fd)?;
        }
        Ok(())
    }

    /// blocks until something is ready, or `timeout` passes (none = forever)
    pub(crate) fn wait(
        &mut self,
        timeout: Option<Duration>,
        out: &mut Vec<Ready>,
    ) -> io::Result<()> {
        let ts = timeout.map(|d| Timespec {
            tv_sec: d.as_secs() as _,
            tv_nsec: d.subsec_nanos() as _,
        });
        self.buf.clear();
        loop {
            match epoll::wait(
                &self.epoll,
                rustix::buffer::spare_capacity(&mut self.buf),
                ts.as_ref(),
            ) {
                Ok(_) => break,
                Err(rustix::io::Errno::INTR) => continue,
                Err(e) => return Err(e.into()),
            }
        }
        for ev in &self.buf {
            let data = ev.data.u64();
            let token = Token(data & PAYLOAD);
            match data & !PAYLOAD {
                USER_FD => out.push(Ready::Fd(token)),
                USER_TIMER => {
                    if let Some((_, fd)) = self.timers.iter().find(|(t, _)| *t == token) {
                        drain(fd.as_fd());
                    }
                    out.push(Ready::Timer(token));
                }
                _ if data == WAYLAND => out.push(Ready::Wayland),
                _ if data == REPEAT => out.push(Ready::Repeat),
                _ => {}
            }
        }
        Ok(())
    }
}

fn checked(token: Token) -> io::Result<u64> {
    if token.0 > PAYLOAD {
        return Err(io::Error::new(
            io::ErrorKind::InvalidInput,
            "token needs more than 62 bits",
        ));
    }
    Ok(token.0)
}

pub(crate) fn timerfd() -> io::Result<OwnedFd> {
    Ok(rustix::time::timerfd_create(
        TimerfdClockId::Monotonic,
        TimerfdFlags::NONBLOCK | TimerfdFlags::CLOEXEC,
    )?)
}

pub(crate) fn arm(fd: &OwnedFd, value: Duration, interval: Duration) -> io::Result<()> {
    let spec = |d: Duration| Timespec {
        tv_sec: d.as_secs() as _,
        tv_nsec: d.subsec_nanos() as _,
    };
    rustix::time::timerfd_settime(
        fd,
        TimerfdTimerFlags::empty(),
        &Itimerspec {
            it_interval: spec(interval),
            it_value: spec(value),
        },
    )?;
    Ok(())
}

/// returns how many times the timer expired since the last read
pub(crate) fn drain(fd: BorrowedFd<'_>) -> u64 {
    let mut buf = [0u8; 8];
    match rustix::io::read(fd, &mut buf) {
        Ok(8) => u64::from_ne_bytes(buf),
        _ => 0,
    }
}
