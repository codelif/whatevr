use std::{
    cell::{Cell, RefCell},
    collections::{HashMap, VecDeque},
};

use adw::prelude::*;
use gtk::{gdk, gio, glib};

use crate::runtime;

const TEXTURE_CACHE_CAP: usize = 256;

pub struct AvatarDecodeRequest {
    avatar: glib::WeakRef<adw::Avatar>,
    path: String,
    bytes: Vec<u8>,
}

#[derive(Default)]
struct TextureCache {
    entries: HashMap<String, gdk::Texture>,
    order: VecDeque<String>,
}

thread_local! {
    static TEXTURE_CACHE: RefCell<TextureCache> = RefCell::new(TextureCache::default());
    static AVATAR_DECODE_QUEUE: RefCell<VecDeque<AvatarDecodeRequest>> = RefCell::new(VecDeque::new());
    static AVATAR_DECODE_SCHEDULED: Cell<bool> = const { Cell::new(false) };
}

pub fn set_avatar_image(avatar: &adw::Avatar, path: &str) {
    if path.is_empty() {
        avatar.set_custom_image(None::<&gdk::Texture>);
        return;
    }

    match load_texture_cached(path) {
        Some(texture) => avatar.set_custom_image(Some(&texture)),
        None => {
            avatar.set_custom_image(None::<&gdk::Texture>);
            schedule_async_avatar_load(avatar, path.to_string());
        }
    }
}

pub fn cached_texture(path: &str) -> Option<gdk::Texture> {
    TEXTURE_CACHE.with(|cache| cache.borrow().entries.get(path).cloned())
}

fn store_cached_texture(path: String, texture: gdk::Texture) {
    TEXTURE_CACHE.with(|cache| {
        let mut cache = cache.borrow_mut();

        if cache.entries.contains_key(&path) {
            return;
        }

        cache.entries.insert(path.clone(), texture);
        cache.order.push_back(path);

        while cache.order.len() > TEXTURE_CACHE_CAP {
            if let Some(evicted) = cache.order.pop_front() {
                cache.entries.remove(&evicted);
            }
        }
    });
}

fn load_texture_cached(path: &str) -> Option<gdk::Texture> {
    cached_texture(path)
}

fn schedule_async_avatar_load(avatar: &adw::Avatar, path: String) {
    let (tx, rx) = async_channel::bounded::<Option<Vec<u8>>>(1);
    let path_for_task = path.clone();

    runtime::spawn(async move {
        let _ = tx.send(tokio::fs::read(path_for_task).await.ok()).await;
    });

    let avatar_weak = avatar.downgrade();

    glib::MainContext::default().spawn_local(async move {
        let Ok(Some(bytes)) = rx.recv().await else {
            return;
        };

        enqueue_avatar_decode(AvatarDecodeRequest {
            avatar: avatar_weak,
            path,
            bytes,
        });
    });
}

fn enqueue_avatar_decode(request: AvatarDecodeRequest) {
    AVATAR_DECODE_QUEUE.with(|queue| queue.borrow_mut().push_back(request));
    schedule_avatar_decode();
}

fn schedule_avatar_decode() {
    let already_scheduled = AVATAR_DECODE_SCHEDULED.with(|scheduled| {
        if scheduled.get() {
            true
        } else {
            scheduled.set(true);
            false
        }
    });

    if already_scheduled {
        return;
    }

    glib::idle_add_local_once(|| {
        let request = AVATAR_DECODE_QUEUE.with(|queue| queue.borrow_mut().pop_front());

        if let Some(request) = request {
            if let Some(texture) = texture_from_bytes(&request.bytes, 64, 64, true) {
                store_cached_texture(request.path, texture.clone());

                if let Some(avatar) = request.avatar.upgrade() {
                    avatar.set_custom_image(Some(&texture));
                }
            }
        }

        AVATAR_DECODE_SCHEDULED.with(|scheduled| scheduled.set(false));

        let has_more = AVATAR_DECODE_QUEUE.with(|queue| !queue.borrow().is_empty());

        if has_more {
            schedule_avatar_decode();
        }
    });
}

pub fn schedule_async_image_load(
    picture: gtk::Picture,
    path: String,
    display_w: i32,
    display_h: i32,
) {
    let (tx, rx) = async_channel::bounded::<Option<Vec<u8>>>(1);
    let path_for_task = path.clone();

    runtime::spawn(async move {
        let _ = tx.send(tokio::fs::read(path_for_task).await.ok()).await;
    });

    let picture_weak = picture.downgrade();

    glib::MainContext::default().spawn_local(async move {
        let Ok(Some(bytes)) = rx.recv().await else {
            return;
        };

        if let Some(texture) = texture_from_bytes(&bytes, display_w, display_h, false) {
            store_cached_texture(path, texture.clone());

            if let Some(pic) = picture_weak.upgrade() {
                pic.set_paintable(Some(&texture));
            }
        }
    });
}

fn texture_from_bytes(
    bytes: &[u8],
    width: i32,
    height: i32,
    preserve_aspect: bool,
) -> Option<gdk::Texture> {
    let glib_bytes = glib::Bytes::from(bytes);
    let stream = gio::MemoryInputStream::from_bytes(&glib_bytes);

    let pixbuf = gdk::gdk_pixbuf::Pixbuf::from_stream_at_scale(
        &stream,
        width,
        height,
        preserve_aspect,
        None::<&gio::Cancellable>,
    )
    .ok()?;

    Some(gdk::Texture::for_pixbuf(&pixbuf))
}
