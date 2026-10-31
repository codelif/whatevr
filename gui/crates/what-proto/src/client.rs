//! The connection to whatevrd: a reader and a writer thread around one unix
//! socket, rings to and from the ui thread, which never touches the socket.

use std::io::Write;
use std::net::Shutdown;
use std::os::unix::net::UnixStream;
use std::path::{Path, PathBuf};
use std::sync::atomic::{AtomicBool, Ordering};
use std::sync::{Arc, Mutex};
use std::thread::{self, JoinHandle};
use std::time::Duration;

use buffa::Message;
use rtrb::{Consumer, Producer, PushError, RingBuffer};

use crate::frame::{self, Reader};
use crate::v2;
use crate::v2::view::{EventOwnedView, HelloResultOwnedView, ResponseOwnedView};

const PROTOCOL: u32 = 2;
const REDIAL: Duration = Duration::from_secs(1);
const HELLO_TIMEOUT: Duration = Duration::from_secs(10);
const INCOMING: usize = 1024;
const OUTGOING: usize = 256;

/// What the daemon sent, in the order it sent it.
pub enum Incoming {
    /// A connection is up. The first thing after connect and after every
    /// Disconnected; subscriptions start over from here.
    Hello(HelloResultOwnedView),
    /// The answer to the request send() numbered id.
    Response(u64, ResponseOwnedView),
    Event(EventOwnedView),
    /// The connection dropped, with every subscription and request on it.
    /// A redial runs every second until the next Hello.
    Disconnected,
    /// The daemon refused this client. Nothing follows.
    Closed(Error),
}

#[derive(Debug, thiserror::Error)]
pub enum Error {
    #[error("whatevrd refused the hello: {0}")]
    Refused(String),
    #[error("whatevrd speaks protocol {0}, this client {PROTOCOL}")]
    Protocol(u32),
}

#[derive(Debug, Clone, Copy, PartialEq, Eq, thiserror::Error)]
pub enum SendError {
    /// No Hello since the last Disconnected.
    #[error("not connected to whatevrd")]
    NotConnected,
    #[error("too many requests queued for whatevrd")]
    Full,
}

/// Where whatevrd listens: WHATEVR_SOCKET, else the user's runtime dir.
pub fn socket_path() -> PathBuf {
    let set = |k| std::env::var_os(k).filter(|v| !v.is_empty());
    if let Some(p) = set("WHATEVR_SOCKET") {
        return p.into();
    }
    // whattui falls back the same way
    let run = set("XDG_RUNTIME_DIR")
        .map(PathBuf::from)
        .unwrap_or_else(|| format!("/run/user/{}", rustix::process::getuid().as_raw()).into());
    run.join("whatevr/whatevrd.sock")
}

struct Shared {
    stop: AtomicBool,
    // the live socket and its connection number. set before the hello, so
    // a drop can cut a hello short; nothing writes on it before the ui saw
    // that hello, as requests carry the number they were sent under
    stream: Mutex<Option<(u64, Arc<UnixStream>)>>,
    // the ui was woken and hasn't drained the ring since
    woken: AtomicBool,
    // the reader is parked on a full ring
    reader_waits: AtomicBool,
}

impl Shared {
    fn cut(&self) {
        if let Some((_, s)) = self.stream.lock().unwrap().take() {
            let _ = s.shutdown(Shutdown::Both);
        }
    }
}

pub struct Client {
    shared: Arc<Shared>,
    incoming: Consumer<(u64, Incoming)>,
    outgoing: Producer<(u64, v2::Request)>,
    // the connection the ui last saw a Hello on
    up: Option<u64>,
    next_id: u64,
    reader: Option<JoinHandle<()>>,
    writer: Option<JoinHandle<()>>,
}

impl Client {
    /// Dials path in the background and says hello as frontend_id. wake runs
    /// on the reader thread when poll has something after returning None.
    pub fn connect(
        path: impl AsRef<Path>,
        frontend_id: &str,
        wake: impl Fn() + Send + 'static,
    ) -> Self {
        let shared = Arc::new(Shared {
            stop: AtomicBool::new(false),
            stream: Mutex::new(None),
            woken: AtomicBool::new(false),
            reader_waits: AtomicBool::new(false),
        });
        let (in_tx, in_rx) = RingBuffer::new(INCOMING);
        let (out_tx, out_rx) = RingBuffer::new(OUTGOING);
        let r = ReaderThread {
            path: path.as_ref().to_owned(),
            frontend_id: frontend_id.to_owned(),
            shared: shared.clone(),
            tx: in_tx,
            wake: Box::new(wake),
        };
        let reader = thread::Builder::new()
            .name("what-proto-read".into())
            .spawn(move || r.run())
            .expect("spawn the reader");
        let s = shared.clone();
        let writer = thread::Builder::new()
            .name("what-proto-write".into())
            .spawn(move || write(&s, out_rx))
            .expect("spawn the writer");
        Client {
            shared,
            incoming: in_rx,
            outgoing: out_tx,
            up: None,
            next_id: 1,
            reader: Some(reader),
            writer: Some(writer),
        }
    }

    /// Queues a request and returns the id its Response will carry.
    pub fn send(&mut self, method: v2::oneof::request::Method) -> Result<u64, SendError> {
        let conn = self.up.ok_or(SendError::NotConnected)?;
        let id = self.next_id;
        let req = v2::Request {
            id,
            method: Some(method),
        };
        self.outgoing
            .push((conn, req))
            .map_err(|_| SendError::Full)?;
        self.next_id += 1;
        if let Some(w) = &self.writer {
            w.thread().unpark();
        }
        Ok(id)
    }

    /// The next thing the daemon sent, None once drained. after None, the
    /// wake closure runs again when there's more.
    pub fn poll(&mut self) -> Option<Incoming> {
        let got = match self.incoming.pop() {
            Ok(x) => x,
            Err(_) => {
                // clear first, then look again: a push in between either
                // lands here or wakes
                self.shared.woken.store(false, Ordering::SeqCst);
                self.incoming.pop().ok()?
            }
        };
        if self.shared.reader_waits.swap(false, Ordering::SeqCst)
            && let Some(r) = &self.reader
        {
            r.thread().unpark();
        }
        let (conn, inc) = got;
        match inc {
            Incoming::Hello(_) => self.up = Some(conn),
            Incoming::Disconnected | Incoming::Closed(_) => self.up = None,
            _ => {}
        }
        Some(inc)
    }
}

impl Drop for Client {
    fn drop(&mut self) {
        self.shared.stop.store(true, Ordering::SeqCst);
        self.shared.cut();
        for t in [self.reader.take(), self.writer.take()]
            .into_iter()
            .flatten()
        {
            t.thread().unpark();
            let _ = t.join();
        }
    }
}

struct ReaderThread {
    path: PathBuf,
    frontend_id: String,
    shared: Arc<Shared>,
    tx: Producer<(u64, Incoming)>,
    wake: Box<dyn Fn() + Send>,
}

enum Hello {
    Up(HelloResultOwnedView),
    Lost(std::io::Error),
    Refused(Error),
}

impl ReaderThread {
    fn stopped(&self) -> bool {
        self.shared.stop.load(Ordering::SeqCst)
    }

    fn run(mut self) {
        let mut conn = 0u64;
        let mut said = false;
        while !self.stopped() {
            let s = match UnixStream::connect(&self.path) {
                Ok(s) => Arc::new(s),
                Err(e) => {
                    if !said {
                        log::info!(
                            "whatevrd at {}: {e}, redialing every {REDIAL:?}",
                            self.path.display()
                        );
                        said = true;
                    }
                    thread::park_timeout(REDIAL);
                    continue;
                }
            };
            said = false;
            conn += 1;
            *self.shared.stream.lock().unwrap() = Some((conn, s.clone()));
            if self.stopped() {
                break;
            }
            let mut r = Reader::new();
            match self.hello(&s, &mut r) {
                Hello::Up(h) => {
                    if !self.push(conn, Incoming::Hello(h)) {
                        break;
                    }
                }
                Hello::Lost(e) => {
                    log::warn!("whatevrd hello: {e}");
                    self.shared.cut();
                    thread::park_timeout(REDIAL);
                    continue;
                }
                Hello::Refused(e) => {
                    self.shared.cut();
                    self.push(conn, Incoming::Closed(e));
                    return;
                }
            }
            let why = loop {
                let body = match r.next(&mut &*s) {
                    Ok(b) => b,
                    Err(e) => break e.to_string(),
                };
                let inc = if let Some(b) = frame::field(&body, 2) {
                    match ResponseOwnedView::decode(b) {
                        Ok(v) => Incoming::Response(v.view().id, v),
                        Err(e) => break format!("a response: {e}"),
                    }
                } else if let Some(b) = frame::field(&body, 3) {
                    match EventOwnedView::decode(b) {
                        Ok(v) => Incoming::Event(v),
                        Err(e) => break format!("an event: {e}"),
                    }
                } else {
                    break "a frame that is neither a response nor an event".into();
                };
                if !self.push(conn, inc) {
                    return;
                }
            };
            self.shared.cut();
            if self.stopped() {
                break;
            }
            log::warn!("whatevrd connection lost: {why}");
            if !self.push(conn, Incoming::Disconnected) {
                break;
            }
            thread::park_timeout(REDIAL);
        }
    }

    fn hello(&self, s: &UnixStream, r: &mut Reader) -> Hello {
        let client = if self.frontend_id.is_empty() {
            "what-proto"
        } else {
            &self.frontend_id
        };
        let hello = v2::Hello {
            client: client.into(),
            protocol: PROTOCOL,
            frontend_id: self.frontend_id.clone(),
        };
        let req = v2::Request {
            id: 0,
            method: Some(v2::oneof::request::Method::Hello(Box::new(hello))),
        };
        let mut out = Vec::new();
        frame::encode(req, &mut out);
        let body = s
            .set_read_timeout(Some(HELLO_TIMEOUT))
            .and_then(|_| (&*s).write_all(&out))
            .and_then(|_| r.next(&mut &*s))
            .and_then(|b| s.set_read_timeout(None).map(|_| b));
        let body = match body {
            Ok(b) => b,
            Err(e) => return Hello::Lost(e),
        };
        let Some(resp) = frame::field(&body, 2) else {
            return Hello::Lost(std::io::Error::other(
                "the first frame was not the hello's answer",
            ));
        };
        if let Some(e) = frame::field(&resp, 2) {
            let msg = v2::Error::decode_from_slice(&e)
                .map(|e| e.message)
                .unwrap_or_default();
            return Hello::Refused(Error::Refused(msg));
        }
        let h = frame::field(&resp, 10).unwrap_or_default();
        match HelloResultOwnedView::decode(h) {
            Ok(h) if h.view().protocol == PROTOCOL => Hello::Up(h),
            Ok(h) => Hello::Refused(Error::Protocol(h.view().protocol)),
            Err(e) => Hello::Lost(std::io::Error::other(e)),
        }
    }

    /// Hands x to the ui, parking while the ring is full. false once stopped.
    fn push(&mut self, conn: u64, x: Incoming) -> bool {
        let mut x = (conn, x);
        loop {
            match self.tx.push(x) {
                Ok(()) => break,
                Err(PushError::Full(back)) => x = back,
            }
            // say so, then try once more: a pop in between either made
            // room or sees the flag and unparks
            self.shared.reader_waits.store(true, Ordering::SeqCst);
            match self.tx.push(x) {
                Ok(()) => break,
                Err(PushError::Full(back)) => x = back,
            }
            if self.stopped() {
                return false;
            }
            thread::park();
            if self.stopped() {
                return false;
            }
        }
        if !self.shared.woken.swap(true, Ordering::SeqCst) {
            (self.wake)();
        }
        true
    }
}

fn write(shared: &Shared, mut rx: Consumer<(u64, v2::Request)>) {
    let mut buf = Vec::new();
    while !shared.stop.load(Ordering::SeqCst) {
        let Ok((conn, req)) = rx.pop() else {
            thread::park();
            continue;
        };
        let live = shared.stream.lock().unwrap().clone();
        let Some((now, s)) = live.filter(|(now, _)| *now == conn) else {
            // sent on a connection that's gone
            continue;
        };
        buf.clear();
        frame::encode(req, &mut buf);
        // everything else queued for this connection goes in the same write
        while let Ok(&(c, _)) = rx.peek() {
            if c != now {
                break;
            }
            let (_, req) = rx.pop().expect("peeked");
            frame::encode(req, &mut buf);
        }
        if let Err(e) = (&*s).write_all(&buf) {
            log::warn!("whatevrd write: {e}");
            let _ = s.shutdown(Shutdown::Both);
        }
    }
}

#[cfg(test)]
mod tests {
    use std::os::unix::net::UnixListener;
    use std::sync::mpsc;

    use super::*;
    use v2::oneof::{event, frame::Frame, request::Method, response};

    // a whatevrd that says what the test tells it to
    struct Fake {
        s: UnixStream,
        r: Reader,
    }

    impl Fake {
        fn accept(l: &UnixListener) -> Self {
            Fake {
                s: l.accept().unwrap().0,
                r: Reader::new(),
            }
        }

        fn request(&mut self) -> v2::Request {
            let body = self.r.next(&mut self.s).unwrap();
            match v2::Frame::decode_from_slice(&body).unwrap().frame {
                Some(Frame::Request(r)) => *r,
                other => panic!("not a request: {other:?}"),
            }
        }

        fn send(&mut self, f: Frame) {
            let mut out = Vec::new();
            v2::Frame { frame: Some(f) }.encode_length_delimited(&mut out);
            self.s.write_all(&out).unwrap();
        }

        fn respond(&mut self, id: u64, result: response::Result) {
            self.send(Frame::Response(Box::new(v2::Response {
                id,
                result: Some(result),
            })));
        }

        fn hello(&mut self, protocol: u32) -> v2::Hello {
            let req = self.request();
            let Some(Method::Hello(h)) = req.method else {
                panic!("not a hello first")
            };
            let res = v2::HelloResult {
                daemon: "fake".into(),
                protocol,
                ..Default::default()
            };
            self.respond(req.id, response::Result::Hello(Box::new(res)));
            *h
        }
    }

    fn setup(name: &str) -> (UnixListener, Client, mpsc::Receiver<()>, PathBuf) {
        let dir = std::env::temp_dir().join(format!("what-proto-{}-{name}", std::process::id()));
        let _ = std::fs::remove_dir_all(&dir);
        std::fs::create_dir_all(&dir).unwrap();
        let path = dir.join("d.sock");
        let l = UnixListener::bind(&path).unwrap();
        let (tx, rx) = mpsc::channel();
        let c = Client::connect(&path, "in.codelif.whatevr", move || {
            let _ = tx.send(());
        });
        (l, c, rx, dir)
    }

    fn next(c: &mut Client, wake: &mpsc::Receiver<()>) -> Incoming {
        loop {
            if let Some(x) = c.poll() {
                return x;
            }
            wake.recv_timeout(Duration::from_secs(5)).expect("no wake");
        }
    }

    #[test]
    fn hello_events_answers_and_a_redial() {
        let (l, mut c, wake, dir) = setup("redial");
        assert_eq!(
            c.send(Method::Unsubscribe(Box::default())),
            Err(SendError::NotConnected)
        );
        let mut d = Fake::accept(&l);
        let h = d.hello(2);
        assert_eq!(
            (h.protocol, h.frontend_id.as_str()),
            (2, "in.codelif.whatevr")
        );
        let update = v2::ViewUpdate {
            sub: 5,
            ..Default::default()
        };
        d.send(Frame::Event(Box::new(v2::Event {
            event: Some(event::Event::Update(Box::new(update))),
        })));
        assert!(matches!(next(&mut c, &wake), Incoming::Hello(h) if h.view().daemon == "fake"));
        let Incoming::Event(e) = next(&mut c, &wake) else {
            panic!("not the event")
        };
        assert!(
            matches!(&e.view().event, Some(v2::view::oneof::event::Event::Update(u)) if u.sub == 5)
        );

        let id = c
            .send(Method::Unsubscribe(Box::new(v2::Unsubscribe { sub: 5 })))
            .unwrap();
        let req = d.request();
        assert_eq!(req.id, id);
        d.respond(id, response::Result::Done(Box::default()));
        assert!(matches!(next(&mut c, &wake), Incoming::Response(got, _) if got == id));

        drop(d);
        assert!(matches!(next(&mut c, &wake), Incoming::Disconnected));
        assert_eq!(
            c.send(Method::Unsubscribe(Box::default())),
            Err(SendError::NotConnected)
        );
        let mut d = Fake::accept(&l);
        d.hello(2);
        assert!(matches!(next(&mut c, &wake), Incoming::Hello(_)));
        let id2 = c.send(Method::Unsubscribe(Box::default())).unwrap();
        assert_eq!((d.request().id, id2), (id2, id + 1));
        drop(c);
        let _ = std::fs::remove_dir_all(dir);
    }

    #[test]
    fn a_refused_hello_closes() {
        let (l, mut c, wake, dir) = setup("refused");
        let mut d = Fake::accept(&l);
        let req = d.request();
        let err = v2::Error {
            message: "no".into(),
            ..Default::default()
        };
        d.respond(req.id, response::Result::Error(Box::new(err)));
        assert!(matches!(next(&mut c, &wake), Incoming::Closed(Error::Refused(m)) if m == "no"));
        let (l2, mut c2, wake2, dir2) = setup("protocol");
        let mut d2 = Fake::accept(&l2);
        d2.hello(3);
        assert!(matches!(
            next(&mut c2, &wake2),
            Incoming::Closed(Error::Protocol(3))
        ));
        drop((l, l2, c, c2));
        let _ = (std::fs::remove_dir_all(dir), std::fs::remove_dir_all(dir2));
    }
}
