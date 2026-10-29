//! the allocator every binary installs:
//! `#[global_allocator] static A: what_core::alloc::Allocator = what_core::alloc::Allocator;`
//! rows are built on worker threads and freed on the ui thread; glibc took
//! ~20 us a row for that, mimalloc ~2.5.

pub use mimalloc::MiMalloc as Allocator;
