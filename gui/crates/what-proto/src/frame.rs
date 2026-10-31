//! Varint-delimited frames, the socket's only framing (PROTOCOL.md, Transport).

use std::io::{self, Read};

use buffa::Message;
use buffa::bytes::{Buf, Bytes, BytesMut};

use crate::v2;

/// The most one frame may take, its prefix aside.
pub const MAX_FRAME: usize = 16 << 20;

const READ_CHUNK: usize = 64 << 10;

/// Splits frames off a byte stream without copying them out of the buffer.
pub(crate) struct Reader {
    buf: BytesMut,
}

impl Reader {
    pub(crate) fn new() -> Self {
        Reader {
            buf: BytesMut::with_capacity(READ_CHUNK),
        }
    }

    /// The next whole frame body.
    pub(crate) fn next(&mut self, r: &mut impl Read) -> io::Result<Bytes> {
        loop {
            if let Some((len, head)) = prefix(&self.buf)? {
                if self.buf.len() >= head + len {
                    self.buf.advance(head);
                    return Ok(self.buf.split_to(len).freeze());
                }
                self.fill(r, head + len - self.buf.len())?;
            } else {
                self.fill(r, 1)?;
            }
        }
    }

    fn fill(&mut self, r: &mut impl Read, need: usize) -> io::Result<()> {
        let n = self.buf.len();
        // reading into zeroed bytes costs a memset per read, cheaper than
        // the alternatives that need unsafe
        self.buf.resize(n + need.max(READ_CHUNK), 0);
        let got = loop {
            match r.read(&mut self.buf[n..]) {
                Ok(got) => break got,
                Err(e) if e.kind() == io::ErrorKind::Interrupted => {}
                Err(e) => {
                    self.buf.truncate(n);
                    return Err(e);
                }
            }
        };
        self.buf.truncate(n + got);
        if got == 0 {
            return Err(io::ErrorKind::UnexpectedEof.into());
        }
        Ok(())
    }
}

/// The body length and the prefix's own length, None until the prefix is whole.
fn prefix(b: &[u8]) -> io::Result<Option<(usize, usize)>> {
    let mut v = 0u64;
    for (i, &x) in b.iter().take(10).enumerate() {
        v |= u64::from(x & 0x7f) << (7 * i);
        if x & 0x80 == 0 {
            if v > MAX_FRAME as u64 {
                return Err(io::Error::new(
                    io::ErrorKind::InvalidData,
                    format!("a {v} byte frame"),
                ));
            }
            return Ok(Some((v as usize, i + 1)));
        }
    }
    if b.len() >= 10 {
        return Err(io::Error::new(
            io::ErrorKind::InvalidData,
            "a frame prefix past 10 bytes",
        ));
    }
    Ok(None)
}

/// Appends a request as it goes on the wire, prefix included.
pub(crate) fn encode(req: v2::Request, out: &mut Vec<u8>) {
    let f = v2::Frame {
        frame: Some(v2::oneof::frame::Frame::Request(Box::new(req))),
    };
    f.encode_length_delimited(out);
}

/// Field num of a message when it's length-delimited, sliced out of b
/// without a copy. a frame and a response are one oneof each, so this finds
/// the body to decode on its own instead of decoding the wrapper first.
pub(crate) fn field(b: &Bytes, num: u64) -> Option<Bytes> {
    let mut s = &b[..];
    while !s.is_empty() {
        let tag = varint(&mut s)?;
        match tag & 7 {
            0 => {
                varint(&mut s)?;
            }
            1 => s = s.get(8..)?,
            2 => {
                let len = usize::try_from(varint(&mut s)?).ok()?;
                if tag >> 3 == num {
                    let at = b.len() - s.len();
                    return (len <= s.len()).then(|| b.slice(at..at + len));
                }
                s = s.get(len..)?;
            }
            5 => s = s.get(4..)?,
            _ => return None,
        }
    }
    None
}

fn varint(s: &mut &[u8]) -> Option<u64> {
    let mut v = 0u64;
    for i in 0..10 {
        let (&x, rest) = s.split_first()?;
        *s = rest;
        v |= u64::from(x & 0x7f) << (7 * i);
        if x & 0x80 == 0 {
            return Some(v);
        }
    }
    None
}

#[cfg(test)]
mod tests {
    use super::*;

    // hands out its bytes a few at a time, like a socket under load
    struct Trickle<'a>(&'a [u8], usize);

    impl Read for Trickle<'_> {
        fn read(&mut self, out: &mut [u8]) -> io::Result<usize> {
            let n = self.0.len().min(self.1).min(out.len());
            out[..n].copy_from_slice(&self.0[..n]);
            self.0 = &self.0[n..];
            Ok(n)
        }
    }

    fn encoded(id: u64) -> Vec<u8> {
        let mut out = Vec::new();
        encode(
            v2::Request {
                id,
                ..Default::default()
            },
            &mut out,
        );
        out
    }

    #[test]
    fn frames_split_off_however_they_arrive() {
        let (a, b) = (encoded(7), encoded(8));
        let wire = [a.clone(), b.clone()].concat();
        for step in [1, 3, wire.len()] {
            let mut r = Reader::new();
            let mut t = Trickle(&wire, step);
            assert_eq!(r.next(&mut t).unwrap()[..], a[1..]);
            assert_eq!(r.next(&mut t).unwrap()[..], b[1..]);
            assert_eq!(
                r.next(&mut t).unwrap_err().kind(),
                io::ErrorKind::UnexpectedEof
            );
        }
    }

    #[test]
    fn an_oversized_prefix_is_refused() {
        let mut wire = Vec::new();
        buffa::encoding::encode_varint(MAX_FRAME as u64 + 1, &mut wire);
        let err = Reader::new().next(&mut Trickle(&wire, 64)).unwrap_err();
        assert_eq!(err.kind(), io::ErrorKind::InvalidData);
    }

    #[test]
    fn field_finds_the_oneof_body() {
        let body = encoded(300);
        let frame = Bytes::from(body[1..].to_vec());
        let req = field(&frame, 1).unwrap();
        assert_eq!(v2::Request::decode_from_slice(&req).unwrap().id, 300);
        assert!(field(&frame, 2).is_none());
    }
}
