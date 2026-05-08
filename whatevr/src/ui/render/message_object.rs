use std::cell::{Ref, RefCell};

use gtk::{glib, subclass::prelude::*};

use crate::proto;

mod imp {
    use super::*;

    #[derive(Default)]
    pub struct MessageObject {
        pub message: RefCell<proto::Message>,
    }

    #[glib::object_subclass]
    impl ObjectSubclass for MessageObject {
        const NAME: &'static str = "WhatevrMessageObject";
        type Type = super::MessageObject;
    }

    impl ObjectImpl for MessageObject {}
}

glib::wrapper! {
    pub struct MessageObject(ObjectSubclass<imp::MessageObject>);
}

impl MessageObject {
    pub fn new(message: proto::Message) -> Self {
        let obj: Self = glib::Object::new();
        *obj.imp().message.borrow_mut() = message;
        obj
    }

    pub fn message(&self) -> Ref<'_, proto::Message> {
        self.imp().message.borrow()
    }

    pub fn set_message(&self, message: proto::Message) {
        *self.imp().message.borrow_mut() = message;
    }

    pub fn id(&self) -> String {
        self.imp().message.borrow().id.clone()
    }
}
