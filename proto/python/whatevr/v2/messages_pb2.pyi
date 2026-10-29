from whatevr.v2 import people_pb2 as _people_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class MessageStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    MESSAGE_STATUS_UNSPECIFIED: _ClassVar[MessageStatus]
    MESSAGE_STATUS_PENDING: _ClassVar[MessageStatus]
    MESSAGE_STATUS_SENT: _ClassVar[MessageStatus]
    MESSAGE_STATUS_DELIVERED: _ClassVar[MessageStatus]
    MESSAGE_STATUS_READ: _ClassVar[MessageStatus]
    MESSAGE_STATUS_PLAYED: _ClassVar[MessageStatus]
    MESSAGE_STATUS_FAILED: _ClassVar[MessageStatus]

class LinkPreviewType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    LINK_PREVIEW_TYPE_UNSPECIFIED: _ClassVar[LinkPreviewType]
    LINK_PREVIEW_TYPE_PAGE: _ClassVar[LinkPreviewType]
    LINK_PREVIEW_TYPE_VIDEO: _ClassVar[LinkPreviewType]
    LINK_PREVIEW_TYPE_IMAGE: _ClassVar[LinkPreviewType]
    LINK_PREVIEW_TYPE_PROFILE: _ClassVar[LinkPreviewType]
    LINK_PREVIEW_TYPE_PAYMENT_LINKS: _ClassVar[LinkPreviewType]
    LINK_PREVIEW_TYPE_PLACEHOLDER: _ClassVar[LinkPreviewType]

class Rsvp(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    RSVP_UNSPECIFIED: _ClassVar[Rsvp]
    RSVP_GOING: _ClassVar[Rsvp]
    RSVP_NOT_GOING: _ClassVar[Rsvp]
    RSVP_MAYBE: _ClassVar[Rsvp]

class InteractiveSource(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    INTERACTIVE_SOURCE_UNSPECIFIED: _ClassVar[InteractiveSource]
    INTERACTIVE_SOURCE_BUTTONS: _ClassVar[InteractiveSource]
    INTERACTIVE_SOURCE_LIST: _ClassVar[InteractiveSource]
    INTERACTIVE_SOURCE_TEMPLATE: _ClassVar[InteractiveSource]
    INTERACTIVE_SOURCE_INTERACTIVE: _ClassVar[InteractiveSource]
    INTERACTIVE_SOURCE_CAROUSEL: _ClassVar[InteractiveSource]

class InteractiveButtonKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    INTERACTIVE_BUTTON_KIND_UNSPECIFIED: _ClassVar[InteractiveButtonKind]
    INTERACTIVE_BUTTON_KIND_URL: _ClassVar[InteractiveButtonKind]
    INTERACTIVE_BUTTON_KIND_CALL: _ClassVar[InteractiveButtonKind]
    INTERACTIVE_BUTTON_KIND_COPY: _ClassVar[InteractiveButtonKind]
    INTERACTIVE_BUTTON_KIND_REPLY: _ClassVar[InteractiveButtonKind]
    INTERACTIVE_BUTTON_KIND_OTHER: _ClassVar[InteractiveButtonKind]

class OrderStatus(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ORDER_STATUS_UNSPECIFIED: _ClassVar[OrderStatus]
    ORDER_STATUS_INQUIRY: _ClassVar[OrderStatus]
    ORDER_STATUS_ACCEPTED: _ClassVar[OrderStatus]
    ORDER_STATUS_DECLINED: _ClassVar[OrderStatus]

class PaymentKind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PAYMENT_KIND_UNSPECIFIED: _ClassVar[PaymentKind]
    PAYMENT_KIND_REQUEST: _ClassVar[PaymentKind]
    PAYMENT_KIND_SENT: _ClassVar[PaymentKind]
    PAYMENT_KIND_INVITE: _ClassVar[PaymentKind]

class CallOutcome(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CALL_OUTCOME_UNSPECIFIED: _ClassVar[CallOutcome]
    CALL_OUTCOME_CONNECTED: _ClassVar[CallOutcome]
    CALL_OUTCOME_MISSED: _ClassVar[CallOutcome]
    CALL_OUTCOME_FAILED: _ClassVar[CallOutcome]
    CALL_OUTCOME_REJECTED: _ClassVar[CallOutcome]
    CALL_OUTCOME_ACCEPTED_ELSEWHERE: _ClassVar[CallOutcome]
    CALL_OUTCOME_ONGOING: _ClassVar[CallOutcome]
    CALL_OUTCOME_SILENCED: _ClassVar[CallOutcome]

class SystemType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SYSTEM_TYPE_UNSPECIFIED: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_JOIN: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_LEAVE: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_PROMOTE: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_DEMOTE: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_NAME: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_TOPIC: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_PHOTO: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_LOCKED: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_ANNOUNCE: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_APPROVAL: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_INVITE_LINK: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_LINK: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_UNLINK: _ClassVar[SystemType]
    SYSTEM_TYPE_GROUP_DELETE: _ClassVar[SystemType]
    SYSTEM_TYPE_EPHEMERAL: _ClassVar[SystemType]
    SYSTEM_TYPE_IDENTITY_CHANGE: _ClassVar[SystemType]
MESSAGE_STATUS_UNSPECIFIED: MessageStatus
MESSAGE_STATUS_PENDING: MessageStatus
MESSAGE_STATUS_SENT: MessageStatus
MESSAGE_STATUS_DELIVERED: MessageStatus
MESSAGE_STATUS_READ: MessageStatus
MESSAGE_STATUS_PLAYED: MessageStatus
MESSAGE_STATUS_FAILED: MessageStatus
LINK_PREVIEW_TYPE_UNSPECIFIED: LinkPreviewType
LINK_PREVIEW_TYPE_PAGE: LinkPreviewType
LINK_PREVIEW_TYPE_VIDEO: LinkPreviewType
LINK_PREVIEW_TYPE_IMAGE: LinkPreviewType
LINK_PREVIEW_TYPE_PROFILE: LinkPreviewType
LINK_PREVIEW_TYPE_PAYMENT_LINKS: LinkPreviewType
LINK_PREVIEW_TYPE_PLACEHOLDER: LinkPreviewType
RSVP_UNSPECIFIED: Rsvp
RSVP_GOING: Rsvp
RSVP_NOT_GOING: Rsvp
RSVP_MAYBE: Rsvp
INTERACTIVE_SOURCE_UNSPECIFIED: InteractiveSource
INTERACTIVE_SOURCE_BUTTONS: InteractiveSource
INTERACTIVE_SOURCE_LIST: InteractiveSource
INTERACTIVE_SOURCE_TEMPLATE: InteractiveSource
INTERACTIVE_SOURCE_INTERACTIVE: InteractiveSource
INTERACTIVE_SOURCE_CAROUSEL: InteractiveSource
INTERACTIVE_BUTTON_KIND_UNSPECIFIED: InteractiveButtonKind
INTERACTIVE_BUTTON_KIND_URL: InteractiveButtonKind
INTERACTIVE_BUTTON_KIND_CALL: InteractiveButtonKind
INTERACTIVE_BUTTON_KIND_COPY: InteractiveButtonKind
INTERACTIVE_BUTTON_KIND_REPLY: InteractiveButtonKind
INTERACTIVE_BUTTON_KIND_OTHER: InteractiveButtonKind
ORDER_STATUS_UNSPECIFIED: OrderStatus
ORDER_STATUS_INQUIRY: OrderStatus
ORDER_STATUS_ACCEPTED: OrderStatus
ORDER_STATUS_DECLINED: OrderStatus
PAYMENT_KIND_UNSPECIFIED: PaymentKind
PAYMENT_KIND_REQUEST: PaymentKind
PAYMENT_KIND_SENT: PaymentKind
PAYMENT_KIND_INVITE: PaymentKind
CALL_OUTCOME_UNSPECIFIED: CallOutcome
CALL_OUTCOME_CONNECTED: CallOutcome
CALL_OUTCOME_MISSED: CallOutcome
CALL_OUTCOME_FAILED: CallOutcome
CALL_OUTCOME_REJECTED: CallOutcome
CALL_OUTCOME_ACCEPTED_ELSEWHERE: CallOutcome
CALL_OUTCOME_ONGOING: CallOutcome
CALL_OUTCOME_SILENCED: CallOutcome
SYSTEM_TYPE_UNSPECIFIED: SystemType
SYSTEM_TYPE_GROUP_JOIN: SystemType
SYSTEM_TYPE_GROUP_LEAVE: SystemType
SYSTEM_TYPE_GROUP_PROMOTE: SystemType
SYSTEM_TYPE_GROUP_DEMOTE: SystemType
SYSTEM_TYPE_GROUP_NAME: SystemType
SYSTEM_TYPE_GROUP_TOPIC: SystemType
SYSTEM_TYPE_GROUP_PHOTO: SystemType
SYSTEM_TYPE_GROUP_LOCKED: SystemType
SYSTEM_TYPE_GROUP_ANNOUNCE: SystemType
SYSTEM_TYPE_GROUP_APPROVAL: SystemType
SYSTEM_TYPE_GROUP_INVITE_LINK: SystemType
SYSTEM_TYPE_GROUP_LINK: SystemType
SYSTEM_TYPE_GROUP_UNLINK: SystemType
SYSTEM_TYPE_GROUP_DELETE: SystemType
SYSTEM_TYPE_EPHEMERAL: SystemType
SYSTEM_TYPE_IDENTITY_CHANGE: SystemType

class MessagesView(_message.Message):
    __slots__ = ("chat_id", "latest", "unread", "message_id", "sort", "index")
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    LATEST_FIELD_NUMBER: _ClassVar[int]
    UNREAD_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    SORT_FIELD_NUMBER: _ClassVar[int]
    INDEX_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    latest: Latest
    unread: Unread
    message_id: str
    sort: bytes
    index: int
    def __init__(self, chat_id: _Optional[str] = ..., latest: _Optional[_Union[Latest, _Mapping]] = ..., unread: _Optional[_Union[Unread, _Mapping]] = ..., message_id: _Optional[str] = ..., sort: _Optional[bytes] = ..., index: _Optional[int] = ...) -> None: ...

class Latest(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class Unread(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class StarredView(_message.Message):
    __slots__ = ("chat_id",)
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    def __init__(self, chat_id: _Optional[str] = ...) -> None: ...

class PinnedView(_message.Message):
    __slots__ = ("chat_id",)
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    def __init__(self, chat_id: _Optional[str] = ...) -> None: ...

class ChatMediaView(_message.Message):
    __slots__ = ("chat_id",)
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    def __init__(self, chat_id: _Optional[str] = ...) -> None: ...

class ReceiptsView(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class ReceiptRow(_message.Message):
    __slots__ = ("person", "delivered_ms", "read_ms", "played_ms")
    PERSON_FIELD_NUMBER: _ClassVar[int]
    DELIVERED_MS_FIELD_NUMBER: _ClassVar[int]
    READ_MS_FIELD_NUMBER: _ClassVar[int]
    PLAYED_MS_FIELD_NUMBER: _ClassVar[int]
    person: _people_pb2.Person
    delivered_ms: int
    read_ms: int
    played_ms: int
    def __init__(self, person: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., delivered_ms: _Optional[int] = ..., read_ms: _Optional[int] = ..., played_ms: _Optional[int] = ...) -> None: ...

class ReactionsView(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class PollVotesView(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class PollVoteRow(_message.Message):
    __slots__ = ("option", "person", "t_ms")
    OPTION_FIELD_NUMBER: _ClassVar[int]
    PERSON_FIELD_NUMBER: _ClassVar[int]
    T_MS_FIELD_NUMBER: _ClassVar[int]
    option: int
    person: _people_pb2.Person
    t_ms: int
    def __init__(self, option: _Optional[int] = ..., person: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., t_ms: _Optional[int] = ...) -> None: ...

class EventResponsesView(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class LiveLocationsView(_message.Message):
    __slots__ = ("chat_id",)
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    def __init__(self, chat_id: _Optional[str] = ...) -> None: ...

class LiveLocationRow(_message.Message):
    __slots__ = ("message_id", "sender", "started_ms", "expires_ms", "updated_ms", "location")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    SENDER_FIELD_NUMBER: _ClassVar[int]
    STARTED_MS_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_MS_FIELD_NUMBER: _ClassVar[int]
    UPDATED_MS_FIELD_NUMBER: _ClassVar[int]
    LOCATION_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    sender: _people_pb2.Person
    started_ms: int
    expires_ms: int
    updated_ms: int
    location: Location
    def __init__(self, message_id: _Optional[str] = ..., sender: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., started_ms: _Optional[int] = ..., expires_ms: _Optional[int] = ..., updated_ms: _Optional[int] = ..., location: _Optional[_Union[Location, _Mapping]] = ...) -> None: ...

class MessageRow(_message.Message):
    __slots__ = ("id", "chat_id", "chat_name", "sender", "from_me", "t_ms", "status", "fallback", "text", "mentions", "reply_to", "reactions", "edited", "revoked", "revoked_by", "starred", "forwarded", "forwarded_many", "pinned_until_ms", "edit_until_ms", "kept", "view_once", "error", "text_truncated", "reaction_counts", "text_body", "image", "video", "gif", "voice", "audio", "document", "video_note", "sticker", "location", "live_location", "contacts", "poll", "group_invite", "event", "album", "interactive", "product", "order", "payment", "sticker_pack", "call_log", "system", "waiting", "unsupported")
    ID_FIELD_NUMBER: _ClassVar[int]
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    CHAT_NAME_FIELD_NUMBER: _ClassVar[int]
    SENDER_FIELD_NUMBER: _ClassVar[int]
    FROM_ME_FIELD_NUMBER: _ClassVar[int]
    T_MS_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    FALLBACK_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    MENTIONS_FIELD_NUMBER: _ClassVar[int]
    REPLY_TO_FIELD_NUMBER: _ClassVar[int]
    REACTIONS_FIELD_NUMBER: _ClassVar[int]
    EDITED_FIELD_NUMBER: _ClassVar[int]
    REVOKED_FIELD_NUMBER: _ClassVar[int]
    REVOKED_BY_FIELD_NUMBER: _ClassVar[int]
    STARRED_FIELD_NUMBER: _ClassVar[int]
    FORWARDED_FIELD_NUMBER: _ClassVar[int]
    FORWARDED_MANY_FIELD_NUMBER: _ClassVar[int]
    PINNED_UNTIL_MS_FIELD_NUMBER: _ClassVar[int]
    EDIT_UNTIL_MS_FIELD_NUMBER: _ClassVar[int]
    KEPT_FIELD_NUMBER: _ClassVar[int]
    VIEW_ONCE_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    TEXT_TRUNCATED_FIELD_NUMBER: _ClassVar[int]
    REACTION_COUNTS_FIELD_NUMBER: _ClassVar[int]
    TEXT_BODY_FIELD_NUMBER: _ClassVar[int]
    IMAGE_FIELD_NUMBER: _ClassVar[int]
    VIDEO_FIELD_NUMBER: _ClassVar[int]
    GIF_FIELD_NUMBER: _ClassVar[int]
    VOICE_FIELD_NUMBER: _ClassVar[int]
    AUDIO_FIELD_NUMBER: _ClassVar[int]
    DOCUMENT_FIELD_NUMBER: _ClassVar[int]
    VIDEO_NOTE_FIELD_NUMBER: _ClassVar[int]
    STICKER_FIELD_NUMBER: _ClassVar[int]
    LOCATION_FIELD_NUMBER: _ClassVar[int]
    LIVE_LOCATION_FIELD_NUMBER: _ClassVar[int]
    CONTACTS_FIELD_NUMBER: _ClassVar[int]
    POLL_FIELD_NUMBER: _ClassVar[int]
    GROUP_INVITE_FIELD_NUMBER: _ClassVar[int]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    ALBUM_FIELD_NUMBER: _ClassVar[int]
    INTERACTIVE_FIELD_NUMBER: _ClassVar[int]
    PRODUCT_FIELD_NUMBER: _ClassVar[int]
    ORDER_FIELD_NUMBER: _ClassVar[int]
    PAYMENT_FIELD_NUMBER: _ClassVar[int]
    STICKER_PACK_FIELD_NUMBER: _ClassVar[int]
    CALL_LOG_FIELD_NUMBER: _ClassVar[int]
    SYSTEM_FIELD_NUMBER: _ClassVar[int]
    WAITING_FIELD_NUMBER: _ClassVar[int]
    UNSUPPORTED_FIELD_NUMBER: _ClassVar[int]
    id: str
    chat_id: str
    chat_name: str
    sender: _people_pb2.Person
    from_me: bool
    t_ms: int
    status: MessageStatus
    fallback: str
    text: str
    mentions: _containers.RepeatedCompositeFieldContainer[Mention]
    reply_to: Quote
    reactions: _containers.RepeatedCompositeFieldContainer[Reaction]
    edited: bool
    revoked: bool
    revoked_by: _people_pb2.Person
    starred: bool
    forwarded: bool
    forwarded_many: bool
    pinned_until_ms: int
    edit_until_ms: int
    kept: bool
    view_once: bool
    error: str
    text_truncated: bool
    reaction_counts: _containers.RepeatedCompositeFieldContainer[ReactionCount]
    text_body: Text
    image: Image
    video: Video
    gif: Gif
    voice: Voice
    audio: Audio
    document: Document
    video_note: VideoNote
    sticker: Sticker
    location: Location
    live_location: LiveLocation
    contacts: Contacts
    poll: Poll
    group_invite: GroupInvite
    event: ScheduledEvent
    album: Album
    interactive: Interactive
    product: Product
    order: Order
    payment: Payment
    sticker_pack: StickerPackShare
    call_log: CallLog
    system: System
    waiting: Waiting
    unsupported: Unsupported
    def __init__(self, id: _Optional[str] = ..., chat_id: _Optional[str] = ..., chat_name: _Optional[str] = ..., sender: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., from_me: _Optional[bool] = ..., t_ms: _Optional[int] = ..., status: _Optional[_Union[MessageStatus, str]] = ..., fallback: _Optional[str] = ..., text: _Optional[str] = ..., mentions: _Optional[_Iterable[_Union[Mention, _Mapping]]] = ..., reply_to: _Optional[_Union[Quote, _Mapping]] = ..., reactions: _Optional[_Iterable[_Union[Reaction, _Mapping]]] = ..., edited: _Optional[bool] = ..., revoked: _Optional[bool] = ..., revoked_by: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., starred: _Optional[bool] = ..., forwarded: _Optional[bool] = ..., forwarded_many: _Optional[bool] = ..., pinned_until_ms: _Optional[int] = ..., edit_until_ms: _Optional[int] = ..., kept: _Optional[bool] = ..., view_once: _Optional[bool] = ..., error: _Optional[str] = ..., text_truncated: _Optional[bool] = ..., reaction_counts: _Optional[_Iterable[_Union[ReactionCount, _Mapping]]] = ..., text_body: _Optional[_Union[Text, _Mapping]] = ..., image: _Optional[_Union[Image, _Mapping]] = ..., video: _Optional[_Union[Video, _Mapping]] = ..., gif: _Optional[_Union[Gif, _Mapping]] = ..., voice: _Optional[_Union[Voice, _Mapping]] = ..., audio: _Optional[_Union[Audio, _Mapping]] = ..., document: _Optional[_Union[Document, _Mapping]] = ..., video_note: _Optional[_Union[VideoNote, _Mapping]] = ..., sticker: _Optional[_Union[Sticker, _Mapping]] = ..., location: _Optional[_Union[Location, _Mapping]] = ..., live_location: _Optional[_Union[LiveLocation, _Mapping]] = ..., contacts: _Optional[_Union[Contacts, _Mapping]] = ..., poll: _Optional[_Union[Poll, _Mapping]] = ..., group_invite: _Optional[_Union[GroupInvite, _Mapping]] = ..., event: _Optional[_Union[ScheduledEvent, _Mapping]] = ..., album: _Optional[_Union[Album, _Mapping]] = ..., interactive: _Optional[_Union[Interactive, _Mapping]] = ..., product: _Optional[_Union[Product, _Mapping]] = ..., order: _Optional[_Union[Order, _Mapping]] = ..., payment: _Optional[_Union[Payment, _Mapping]] = ..., sticker_pack: _Optional[_Union[StickerPackShare, _Mapping]] = ..., call_log: _Optional[_Union[CallLog, _Mapping]] = ..., system: _Optional[_Union[System, _Mapping]] = ..., waiting: _Optional[_Union[Waiting, _Mapping]] = ..., unsupported: _Optional[_Union[Unsupported, _Mapping]] = ...) -> None: ...

class Mention(_message.Message):
    __slots__ = ("person", "start", "end")
    PERSON_FIELD_NUMBER: _ClassVar[int]
    START_FIELD_NUMBER: _ClassVar[int]
    END_FIELD_NUMBER: _ClassVar[int]
    person: _people_pb2.Person
    start: int
    end: int
    def __init__(self, person: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., start: _Optional[int] = ..., end: _Optional[int] = ...) -> None: ...

class Quote(_message.Message):
    __slots__ = ("message_id", "sender", "text", "thumbnail_path")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    SENDER_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    THUMBNAIL_PATH_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    sender: _people_pb2.Person
    text: str
    thumbnail_path: str
    def __init__(self, message_id: _Optional[str] = ..., sender: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., text: _Optional[str] = ..., thumbnail_path: _Optional[str] = ...) -> None: ...

class Reaction(_message.Message):
    __slots__ = ("emoji", "sender", "t_ms")
    EMOJI_FIELD_NUMBER: _ClassVar[int]
    SENDER_FIELD_NUMBER: _ClassVar[int]
    T_MS_FIELD_NUMBER: _ClassVar[int]
    emoji: str
    sender: _people_pb2.Person
    t_ms: int
    def __init__(self, emoji: _Optional[str] = ..., sender: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., t_ms: _Optional[int] = ...) -> None: ...

class ReactionCount(_message.Message):
    __slots__ = ("emoji", "count", "mine")
    EMOJI_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    MINE_FIELD_NUMBER: _ClassVar[int]
    emoji: str
    count: int
    mine: bool
    def __init__(self, emoji: _Optional[str] = ..., count: _Optional[int] = ..., mine: _Optional[bool] = ...) -> None: ...

class Media(_message.Message):
    __slots__ = ("mime", "width", "height", "size_bytes", "duration_ms", "thumbnail_path", "path", "downloading", "download_error")
    MIME_FIELD_NUMBER: _ClassVar[int]
    WIDTH_FIELD_NUMBER: _ClassVar[int]
    HEIGHT_FIELD_NUMBER: _ClassVar[int]
    SIZE_BYTES_FIELD_NUMBER: _ClassVar[int]
    DURATION_MS_FIELD_NUMBER: _ClassVar[int]
    THUMBNAIL_PATH_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    DOWNLOADING_FIELD_NUMBER: _ClassVar[int]
    DOWNLOAD_ERROR_FIELD_NUMBER: _ClassVar[int]
    mime: str
    width: int
    height: int
    size_bytes: int
    duration_ms: int
    thumbnail_path: str
    path: str
    downloading: bool
    download_error: str
    def __init__(self, mime: _Optional[str] = ..., width: _Optional[int] = ..., height: _Optional[int] = ..., size_bytes: _Optional[int] = ..., duration_ms: _Optional[int] = ..., thumbnail_path: _Optional[str] = ..., path: _Optional[str] = ..., downloading: _Optional[bool] = ..., download_error: _Optional[str] = ...) -> None: ...

class Text(_message.Message):
    __slots__ = ("link_preview",)
    LINK_PREVIEW_FIELD_NUMBER: _ClassVar[int]
    link_preview: LinkPreview
    def __init__(self, link_preview: _Optional[_Union[LinkPreview, _Mapping]] = ...) -> None: ...

class LinkPreview(_message.Message):
    __slots__ = ("url", "host", "title", "description", "type", "thumbnail_path", "thumbnail_width", "thumbnail_height")
    URL_FIELD_NUMBER: _ClassVar[int]
    HOST_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    THUMBNAIL_PATH_FIELD_NUMBER: _ClassVar[int]
    THUMBNAIL_WIDTH_FIELD_NUMBER: _ClassVar[int]
    THUMBNAIL_HEIGHT_FIELD_NUMBER: _ClassVar[int]
    url: str
    host: str
    title: str
    description: str
    type: LinkPreviewType
    thumbnail_path: str
    thumbnail_width: int
    thumbnail_height: int
    def __init__(self, url: _Optional[str] = ..., host: _Optional[str] = ..., title: _Optional[str] = ..., description: _Optional[str] = ..., type: _Optional[_Union[LinkPreviewType, str]] = ..., thumbnail_path: _Optional[str] = ..., thumbnail_width: _Optional[int] = ..., thumbnail_height: _Optional[int] = ...) -> None: ...

class Image(_message.Message):
    __slots__ = ("media",)
    MEDIA_FIELD_NUMBER: _ClassVar[int]
    media: Media
    def __init__(self, media: _Optional[_Union[Media, _Mapping]] = ...) -> None: ...

class Video(_message.Message):
    __slots__ = ("media",)
    MEDIA_FIELD_NUMBER: _ClassVar[int]
    media: Media
    def __init__(self, media: _Optional[_Union[Media, _Mapping]] = ...) -> None: ...

class Gif(_message.Message):
    __slots__ = ("media",)
    MEDIA_FIELD_NUMBER: _ClassVar[int]
    media: Media
    def __init__(self, media: _Optional[_Union[Media, _Mapping]] = ...) -> None: ...

class Voice(_message.Message):
    __slots__ = ("media", "waveform", "played")
    MEDIA_FIELD_NUMBER: _ClassVar[int]
    WAVEFORM_FIELD_NUMBER: _ClassVar[int]
    PLAYED_FIELD_NUMBER: _ClassVar[int]
    media: Media
    waveform: bytes
    played: bool
    def __init__(self, media: _Optional[_Union[Media, _Mapping]] = ..., waveform: _Optional[bytes] = ..., played: _Optional[bool] = ...) -> None: ...

class Audio(_message.Message):
    __slots__ = ("media",)
    MEDIA_FIELD_NUMBER: _ClassVar[int]
    media: Media
    def __init__(self, media: _Optional[_Union[Media, _Mapping]] = ...) -> None: ...

class Document(_message.Message):
    __slots__ = ("media", "filename", "page_count")
    MEDIA_FIELD_NUMBER: _ClassVar[int]
    FILENAME_FIELD_NUMBER: _ClassVar[int]
    PAGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    media: Media
    filename: str
    page_count: int
    def __init__(self, media: _Optional[_Union[Media, _Mapping]] = ..., filename: _Optional[str] = ..., page_count: _Optional[int] = ...) -> None: ...

class VideoNote(_message.Message):
    __slots__ = ("media",)
    MEDIA_FIELD_NUMBER: _ClassVar[int]
    media: Media
    def __init__(self, media: _Optional[_Union[Media, _Mapping]] = ...) -> None: ...

class Sticker(_message.Message):
    __slots__ = ("media", "sticker_id", "animated", "lottie")
    MEDIA_FIELD_NUMBER: _ClassVar[int]
    STICKER_ID_FIELD_NUMBER: _ClassVar[int]
    ANIMATED_FIELD_NUMBER: _ClassVar[int]
    LOTTIE_FIELD_NUMBER: _ClassVar[int]
    media: Media
    sticker_id: str
    animated: bool
    lottie: bool
    def __init__(self, media: _Optional[_Union[Media, _Mapping]] = ..., sticker_id: _Optional[str] = ..., animated: _Optional[bool] = ..., lottie: _Optional[bool] = ...) -> None: ...

class Location(_message.Message):
    __slots__ = ("lat", "lng", "name", "address", "url", "accuracy_m", "map")
    LAT_FIELD_NUMBER: _ClassVar[int]
    LNG_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    ADDRESS_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    ACCURACY_M_FIELD_NUMBER: _ClassVar[int]
    MAP_FIELD_NUMBER: _ClassVar[int]
    lat: float
    lng: float
    name: str
    address: str
    url: str
    accuracy_m: int
    map: Media
    def __init__(self, lat: _Optional[float] = ..., lng: _Optional[float] = ..., name: _Optional[str] = ..., address: _Optional[str] = ..., url: _Optional[str] = ..., accuracy_m: _Optional[int] = ..., map: _Optional[_Union[Media, _Mapping]] = ...) -> None: ...

class LiveLocation(_message.Message):
    __slots__ = ("location", "active", "started_ms", "expires_ms", "updated_ms", "speed_mps", "heading_deg", "point_count")
    LOCATION_FIELD_NUMBER: _ClassVar[int]
    ACTIVE_FIELD_NUMBER: _ClassVar[int]
    STARTED_MS_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_MS_FIELD_NUMBER: _ClassVar[int]
    UPDATED_MS_FIELD_NUMBER: _ClassVar[int]
    SPEED_MPS_FIELD_NUMBER: _ClassVar[int]
    HEADING_DEG_FIELD_NUMBER: _ClassVar[int]
    POINT_COUNT_FIELD_NUMBER: _ClassVar[int]
    location: Location
    active: bool
    started_ms: int
    expires_ms: int
    updated_ms: int
    speed_mps: float
    heading_deg: int
    point_count: int
    def __init__(self, location: _Optional[_Union[Location, _Mapping]] = ..., active: _Optional[bool] = ..., started_ms: _Optional[int] = ..., expires_ms: _Optional[int] = ..., updated_ms: _Optional[int] = ..., speed_mps: _Optional[float] = ..., heading_deg: _Optional[int] = ..., point_count: _Optional[int] = ...) -> None: ...

class Contacts(_message.Message):
    __slots__ = ("display_name", "cards")
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    CARDS_FIELD_NUMBER: _ClassVar[int]
    display_name: str
    cards: _containers.RepeatedCompositeFieldContainer[ContactCard]
    def __init__(self, display_name: _Optional[str] = ..., cards: _Optional[_Iterable[_Union[ContactCard, _Mapping]]] = ...) -> None: ...

class ContactCard(_message.Message):
    __slots__ = ("display_name", "org", "title", "birthday", "phones", "emails", "urls", "addresses", "vcard")
    DISPLAY_NAME_FIELD_NUMBER: _ClassVar[int]
    ORG_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    BIRTHDAY_FIELD_NUMBER: _ClassVar[int]
    PHONES_FIELD_NUMBER: _ClassVar[int]
    EMAILS_FIELD_NUMBER: _ClassVar[int]
    URLS_FIELD_NUMBER: _ClassVar[int]
    ADDRESSES_FIELD_NUMBER: _ClassVar[int]
    VCARD_FIELD_NUMBER: _ClassVar[int]
    display_name: str
    org: str
    title: str
    birthday: str
    phones: _containers.RepeatedCompositeFieldContainer[ContactField]
    emails: _containers.RepeatedCompositeFieldContainer[ContactField]
    urls: _containers.RepeatedCompositeFieldContainer[ContactField]
    addresses: _containers.RepeatedCompositeFieldContainer[ContactField]
    vcard: str
    def __init__(self, display_name: _Optional[str] = ..., org: _Optional[str] = ..., title: _Optional[str] = ..., birthday: _Optional[str] = ..., phones: _Optional[_Iterable[_Union[ContactField, _Mapping]]] = ..., emails: _Optional[_Iterable[_Union[ContactField, _Mapping]]] = ..., urls: _Optional[_Iterable[_Union[ContactField, _Mapping]]] = ..., addresses: _Optional[_Iterable[_Union[ContactField, _Mapping]]] = ..., vcard: _Optional[str] = ...) -> None: ...

class ContactField(_message.Message):
    __slots__ = ("label", "value", "person")
    LABEL_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    PERSON_FIELD_NUMBER: _ClassVar[int]
    label: str
    value: str
    person: _people_pb2.Person
    def __init__(self, label: _Optional[str] = ..., value: _Optional[str] = ..., person: _Optional[_Union[_people_pb2.Person, _Mapping]] = ...) -> None: ...

class Poll(_message.Message):
    __slots__ = ("question", "options", "selectable", "quiz", "allow_add_option", "ends_ms", "voters", "self_voted")
    QUESTION_FIELD_NUMBER: _ClassVar[int]
    OPTIONS_FIELD_NUMBER: _ClassVar[int]
    SELECTABLE_FIELD_NUMBER: _ClassVar[int]
    QUIZ_FIELD_NUMBER: _ClassVar[int]
    ALLOW_ADD_OPTION_FIELD_NUMBER: _ClassVar[int]
    ENDS_MS_FIELD_NUMBER: _ClassVar[int]
    VOTERS_FIELD_NUMBER: _ClassVar[int]
    SELF_VOTED_FIELD_NUMBER: _ClassVar[int]
    question: str
    options: _containers.RepeatedCompositeFieldContainer[PollOption]
    selectable: int
    quiz: bool
    allow_add_option: bool
    ends_ms: int
    voters: int
    self_voted: bool
    def __init__(self, question: _Optional[str] = ..., options: _Optional[_Iterable[_Union[PollOption, _Mapping]]] = ..., selectable: _Optional[int] = ..., quiz: _Optional[bool] = ..., allow_add_option: _Optional[bool] = ..., ends_ms: _Optional[int] = ..., voters: _Optional[int] = ..., self_voted: _Optional[bool] = ...) -> None: ...

class PollOption(_message.Message):
    __slots__ = ("index", "name", "voters", "self_voted", "votes")
    INDEX_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    VOTERS_FIELD_NUMBER: _ClassVar[int]
    SELF_VOTED_FIELD_NUMBER: _ClassVar[int]
    VOTES_FIELD_NUMBER: _ClassVar[int]
    index: int
    name: str
    voters: _containers.RepeatedCompositeFieldContainer[Voter]
    self_voted: bool
    votes: int
    def __init__(self, index: _Optional[int] = ..., name: _Optional[str] = ..., voters: _Optional[_Iterable[_Union[Voter, _Mapping]]] = ..., self_voted: _Optional[bool] = ..., votes: _Optional[int] = ...) -> None: ...

class Voter(_message.Message):
    __slots__ = ("person", "t_ms")
    PERSON_FIELD_NUMBER: _ClassVar[int]
    T_MS_FIELD_NUMBER: _ClassVar[int]
    person: _people_pb2.Person
    t_ms: int
    def __init__(self, person: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., t_ms: _Optional[int] = ...) -> None: ...

class GroupInvite(_message.Message):
    __slots__ = ("chat_id", "code", "expires_ms", "name", "caption", "thumbnail_path", "subject", "topic", "member_count", "joined", "resolve_error")
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    CODE_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_MS_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    CAPTION_FIELD_NUMBER: _ClassVar[int]
    THUMBNAIL_PATH_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    TOPIC_FIELD_NUMBER: _ClassVar[int]
    MEMBER_COUNT_FIELD_NUMBER: _ClassVar[int]
    JOINED_FIELD_NUMBER: _ClassVar[int]
    RESOLVE_ERROR_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    code: str
    expires_ms: int
    name: str
    caption: str
    thumbnail_path: str
    subject: str
    topic: str
    member_count: int
    joined: bool
    resolve_error: str
    def __init__(self, chat_id: _Optional[str] = ..., code: _Optional[str] = ..., expires_ms: _Optional[int] = ..., name: _Optional[str] = ..., caption: _Optional[str] = ..., thumbnail_path: _Optional[str] = ..., subject: _Optional[str] = ..., topic: _Optional[str] = ..., member_count: _Optional[int] = ..., joined: _Optional[bool] = ..., resolve_error: _Optional[str] = ...) -> None: ...

class ScheduledEvent(_message.Message):
    __slots__ = ("name", "description", "starts_ms", "ends_ms", "canceled", "join_link", "location", "extra_guests_allowed", "call", "reminder_offset_ms", "responders", "going", "self_response", "self_guests", "maybe", "not_going")
    NAME_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    STARTS_MS_FIELD_NUMBER: _ClassVar[int]
    ENDS_MS_FIELD_NUMBER: _ClassVar[int]
    CANCELED_FIELD_NUMBER: _ClassVar[int]
    JOIN_LINK_FIELD_NUMBER: _ClassVar[int]
    LOCATION_FIELD_NUMBER: _ClassVar[int]
    EXTRA_GUESTS_ALLOWED_FIELD_NUMBER: _ClassVar[int]
    CALL_FIELD_NUMBER: _ClassVar[int]
    REMINDER_OFFSET_MS_FIELD_NUMBER: _ClassVar[int]
    RESPONDERS_FIELD_NUMBER: _ClassVar[int]
    GOING_FIELD_NUMBER: _ClassVar[int]
    SELF_RESPONSE_FIELD_NUMBER: _ClassVar[int]
    SELF_GUESTS_FIELD_NUMBER: _ClassVar[int]
    MAYBE_FIELD_NUMBER: _ClassVar[int]
    NOT_GOING_FIELD_NUMBER: _ClassVar[int]
    name: str
    description: str
    starts_ms: int
    ends_ms: int
    canceled: bool
    join_link: str
    location: Location
    extra_guests_allowed: bool
    call: bool
    reminder_offset_ms: int
    responders: _containers.RepeatedCompositeFieldContainer[Responder]
    going: int
    self_response: Rsvp
    self_guests: int
    maybe: int
    not_going: int
    def __init__(self, name: _Optional[str] = ..., description: _Optional[str] = ..., starts_ms: _Optional[int] = ..., ends_ms: _Optional[int] = ..., canceled: _Optional[bool] = ..., join_link: _Optional[str] = ..., location: _Optional[_Union[Location, _Mapping]] = ..., extra_guests_allowed: _Optional[bool] = ..., call: _Optional[bool] = ..., reminder_offset_ms: _Optional[int] = ..., responders: _Optional[_Iterable[_Union[Responder, _Mapping]]] = ..., going: _Optional[int] = ..., self_response: _Optional[_Union[Rsvp, str]] = ..., self_guests: _Optional[int] = ..., maybe: _Optional[int] = ..., not_going: _Optional[int] = ...) -> None: ...

class Responder(_message.Message):
    __slots__ = ("person", "response", "extra_guests", "t_ms")
    PERSON_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    EXTRA_GUESTS_FIELD_NUMBER: _ClassVar[int]
    T_MS_FIELD_NUMBER: _ClassVar[int]
    person: _people_pb2.Person
    response: Rsvp
    extra_guests: int
    t_ms: int
    def __init__(self, person: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., response: _Optional[_Union[Rsvp, str]] = ..., extra_guests: _Optional[int] = ..., t_ms: _Optional[int] = ...) -> None: ...

class Album(_message.Message):
    __slots__ = ("expected", "items")
    EXPECTED_FIELD_NUMBER: _ClassVar[int]
    ITEMS_FIELD_NUMBER: _ClassVar[int]
    expected: int
    items: _containers.RepeatedCompositeFieldContainer[MessageRow]
    def __init__(self, expected: _Optional[int] = ..., items: _Optional[_Iterable[_Union[MessageRow, _Mapping]]] = ...) -> None: ...

class Interactive(_message.Message):
    __slots__ = ("source", "title", "subtitle", "body", "footer", "thumbnail_path", "document_name", "buttons", "sections", "list_label", "cards")
    SOURCE_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    SUBTITLE_FIELD_NUMBER: _ClassVar[int]
    BODY_FIELD_NUMBER: _ClassVar[int]
    FOOTER_FIELD_NUMBER: _ClassVar[int]
    THUMBNAIL_PATH_FIELD_NUMBER: _ClassVar[int]
    DOCUMENT_NAME_FIELD_NUMBER: _ClassVar[int]
    BUTTONS_FIELD_NUMBER: _ClassVar[int]
    SECTIONS_FIELD_NUMBER: _ClassVar[int]
    LIST_LABEL_FIELD_NUMBER: _ClassVar[int]
    CARDS_FIELD_NUMBER: _ClassVar[int]
    source: InteractiveSource
    title: str
    subtitle: str
    body: str
    footer: str
    thumbnail_path: str
    document_name: str
    buttons: _containers.RepeatedCompositeFieldContainer[InteractiveButton]
    sections: _containers.RepeatedCompositeFieldContainer[InteractiveSection]
    list_label: str
    cards: _containers.RepeatedCompositeFieldContainer[Interactive]
    def __init__(self, source: _Optional[_Union[InteractiveSource, str]] = ..., title: _Optional[str] = ..., subtitle: _Optional[str] = ..., body: _Optional[str] = ..., footer: _Optional[str] = ..., thumbnail_path: _Optional[str] = ..., document_name: _Optional[str] = ..., buttons: _Optional[_Iterable[_Union[InteractiveButton, _Mapping]]] = ..., sections: _Optional[_Iterable[_Union[InteractiveSection, _Mapping]]] = ..., list_label: _Optional[str] = ..., cards: _Optional[_Iterable[_Union[Interactive, _Mapping]]] = ...) -> None: ...

class InteractiveButton(_message.Message):
    __slots__ = ("kind", "label", "url", "phone", "copy", "id", "live")
    KIND_FIELD_NUMBER: _ClassVar[int]
    LABEL_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    PHONE_FIELD_NUMBER: _ClassVar[int]
    COPY_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    LIVE_FIELD_NUMBER: _ClassVar[int]
    kind: InteractiveButtonKind
    label: str
    url: str
    phone: str
    copy: str
    id: str
    live: bool
    def __init__(self, kind: _Optional[_Union[InteractiveButtonKind, str]] = ..., label: _Optional[str] = ..., url: _Optional[str] = ..., phone: _Optional[str] = ..., copy: _Optional[str] = ..., id: _Optional[str] = ..., live: _Optional[bool] = ...) -> None: ...

class InteractiveSection(_message.Message):
    __slots__ = ("title", "rows")
    TITLE_FIELD_NUMBER: _ClassVar[int]
    ROWS_FIELD_NUMBER: _ClassVar[int]
    title: str
    rows: _containers.RepeatedCompositeFieldContainer[InteractiveRow]
    def __init__(self, title: _Optional[str] = ..., rows: _Optional[_Iterable[_Union[InteractiveRow, _Mapping]]] = ...) -> None: ...

class InteractiveRow(_message.Message):
    __slots__ = ("id", "title", "description")
    ID_FIELD_NUMBER: _ClassVar[int]
    TITLE_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    id: str
    title: str
    description: str
    def __init__(self, id: _Optional[str] = ..., title: _Optional[str] = ..., description: _Optional[str] = ...) -> None: ...

class Money(_message.Message):
    __slots__ = ("thousandths", "currency")
    THOUSANDTHS_FIELD_NUMBER: _ClassVar[int]
    CURRENCY_FIELD_NUMBER: _ClassVar[int]
    thousandths: int
    currency: str
    def __init__(self, thousandths: _Optional[int] = ..., currency: _Optional[str] = ...) -> None: ...

class Product(_message.Message):
    __slots__ = ("title", "description", "body", "footer", "thumbnail_path", "price", "sale_price", "product_id", "retailer_id", "url", "image_count", "seller")
    TITLE_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    BODY_FIELD_NUMBER: _ClassVar[int]
    FOOTER_FIELD_NUMBER: _ClassVar[int]
    THUMBNAIL_PATH_FIELD_NUMBER: _ClassVar[int]
    PRICE_FIELD_NUMBER: _ClassVar[int]
    SALE_PRICE_FIELD_NUMBER: _ClassVar[int]
    PRODUCT_ID_FIELD_NUMBER: _ClassVar[int]
    RETAILER_ID_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    IMAGE_COUNT_FIELD_NUMBER: _ClassVar[int]
    SELLER_FIELD_NUMBER: _ClassVar[int]
    title: str
    description: str
    body: str
    footer: str
    thumbnail_path: str
    price: Money
    sale_price: Money
    product_id: str
    retailer_id: str
    url: str
    image_count: int
    seller: _people_pb2.Person
    def __init__(self, title: _Optional[str] = ..., description: _Optional[str] = ..., body: _Optional[str] = ..., footer: _Optional[str] = ..., thumbnail_path: _Optional[str] = ..., price: _Optional[_Union[Money, _Mapping]] = ..., sale_price: _Optional[_Union[Money, _Mapping]] = ..., product_id: _Optional[str] = ..., retailer_id: _Optional[str] = ..., url: _Optional[str] = ..., image_count: _Optional[int] = ..., seller: _Optional[_Union[_people_pb2.Person, _Mapping]] = ...) -> None: ...

class Order(_message.Message):
    __slots__ = ("title", "thumbnail_path", "order_id", "item_count", "status", "total", "seller")
    TITLE_FIELD_NUMBER: _ClassVar[int]
    THUMBNAIL_PATH_FIELD_NUMBER: _ClassVar[int]
    ORDER_ID_FIELD_NUMBER: _ClassVar[int]
    ITEM_COUNT_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    TOTAL_FIELD_NUMBER: _ClassVar[int]
    SELLER_FIELD_NUMBER: _ClassVar[int]
    title: str
    thumbnail_path: str
    order_id: str
    item_count: int
    status: OrderStatus
    total: Money
    seller: _people_pb2.Person
    def __init__(self, title: _Optional[str] = ..., thumbnail_path: _Optional[str] = ..., order_id: _Optional[str] = ..., item_count: _Optional[int] = ..., status: _Optional[_Union[OrderStatus, str]] = ..., total: _Optional[_Union[Money, _Mapping]] = ..., seller: _Optional[_Union[_people_pb2.Person, _Mapping]] = ...) -> None: ...

class Payment(_message.Message):
    __slots__ = ("kind", "amount", "note", "expires_ms", "service", "requested_from")
    KIND_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    NOTE_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_MS_FIELD_NUMBER: _ClassVar[int]
    SERVICE_FIELD_NUMBER: _ClassVar[int]
    REQUESTED_FROM_FIELD_NUMBER: _ClassVar[int]
    kind: PaymentKind
    amount: Money
    note: str
    expires_ms: int
    service: str
    requested_from: _people_pb2.Person
    def __init__(self, kind: _Optional[_Union[PaymentKind, str]] = ..., amount: _Optional[_Union[Money, _Mapping]] = ..., note: _Optional[str] = ..., expires_ms: _Optional[int] = ..., service: _Optional[str] = ..., requested_from: _Optional[_Union[_people_pb2.Person, _Mapping]] = ...) -> None: ...

class StickerPackShare(_message.Message):
    __slots__ = ("pack_id", "name", "publisher", "description", "caption", "count", "installable", "installed")
    PACK_ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    PUBLISHER_FIELD_NUMBER: _ClassVar[int]
    DESCRIPTION_FIELD_NUMBER: _ClassVar[int]
    CAPTION_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    INSTALLABLE_FIELD_NUMBER: _ClassVar[int]
    INSTALLED_FIELD_NUMBER: _ClassVar[int]
    pack_id: str
    name: str
    publisher: str
    description: str
    caption: str
    count: int
    installable: bool
    installed: bool
    def __init__(self, pack_id: _Optional[str] = ..., name: _Optional[str] = ..., publisher: _Optional[str] = ..., description: _Optional[str] = ..., caption: _Optional[str] = ..., count: _Optional[int] = ..., installable: _Optional[bool] = ..., installed: _Optional[bool] = ...) -> None: ...

class CallLog(_message.Message):
    __slots__ = ("video", "outcome", "duration_ms", "group", "scheduled", "voice_chat", "participants")
    VIDEO_FIELD_NUMBER: _ClassVar[int]
    OUTCOME_FIELD_NUMBER: _ClassVar[int]
    DURATION_MS_FIELD_NUMBER: _ClassVar[int]
    GROUP_FIELD_NUMBER: _ClassVar[int]
    SCHEDULED_FIELD_NUMBER: _ClassVar[int]
    VOICE_CHAT_FIELD_NUMBER: _ClassVar[int]
    PARTICIPANTS_FIELD_NUMBER: _ClassVar[int]
    video: bool
    outcome: CallOutcome
    duration_ms: int
    group: bool
    scheduled: bool
    voice_chat: bool
    participants: int
    def __init__(self, video: _Optional[bool] = ..., outcome: _Optional[_Union[CallOutcome, str]] = ..., duration_ms: _Optional[int] = ..., group: _Optional[bool] = ..., scheduled: _Optional[bool] = ..., voice_chat: _Optional[bool] = ..., participants: _Optional[int] = ...) -> None: ...

class System(_message.Message):
    __slots__ = ("type", "text", "actor", "names", "overflow", "value", "on", "ephemeral_secs", "about_self")
    TYPE_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    ACTOR_FIELD_NUMBER: _ClassVar[int]
    NAMES_FIELD_NUMBER: _ClassVar[int]
    OVERFLOW_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    ON_FIELD_NUMBER: _ClassVar[int]
    EPHEMERAL_SECS_FIELD_NUMBER: _ClassVar[int]
    ABOUT_SELF_FIELD_NUMBER: _ClassVar[int]
    type: SystemType
    text: str
    actor: _people_pb2.Person
    names: _containers.RepeatedCompositeFieldContainer[_people_pb2.Person]
    overflow: int
    value: str
    on: bool
    ephemeral_secs: int
    about_self: bool
    def __init__(self, type: _Optional[_Union[SystemType, str]] = ..., text: _Optional[str] = ..., actor: _Optional[_Union[_people_pb2.Person, _Mapping]] = ..., names: _Optional[_Iterable[_Union[_people_pb2.Person, _Mapping]]] = ..., overflow: _Optional[int] = ..., value: _Optional[str] = ..., on: _Optional[bool] = ..., ephemeral_secs: _Optional[int] = ..., about_self: _Optional[bool] = ...) -> None: ...

class Waiting(_message.Message):
    __slots__ = ("first_seen_ms", "retry_at_ms", "requests", "asked_phone", "never")
    FIRST_SEEN_MS_FIELD_NUMBER: _ClassVar[int]
    RETRY_AT_MS_FIELD_NUMBER: _ClassVar[int]
    REQUESTS_FIELD_NUMBER: _ClassVar[int]
    ASKED_PHONE_FIELD_NUMBER: _ClassVar[int]
    NEVER_FIELD_NUMBER: _ClassVar[int]
    first_seen_ms: int
    retry_at_ms: int
    requests: int
    asked_phone: bool
    never: str
    def __init__(self, first_seen_ms: _Optional[int] = ..., retry_at_ms: _Optional[int] = ..., requests: _Optional[int] = ..., asked_phone: _Optional[bool] = ..., never: _Optional[str] = ...) -> None: ...

class Unsupported(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class SendText(_message.Message):
    __slots__ = ("chat_id", "text", "reply_to", "mentions", "key")
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    REPLY_TO_FIELD_NUMBER: _ClassVar[int]
    MENTIONS_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    text: str
    reply_to: str
    mentions: _containers.RepeatedCompositeFieldContainer[_people_pb2.Address]
    key: str
    def __init__(self, chat_id: _Optional[str] = ..., text: _Optional[str] = ..., reply_to: _Optional[str] = ..., mentions: _Optional[_Iterable[_Union[_people_pb2.Address, _Mapping]]] = ..., key: _Optional[str] = ...) -> None: ...

class SendMedia(_message.Message):
    __slots__ = ("chat_id", "path", "caption", "reply_to", "mentions", "as_document", "view_once", "key")
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    CAPTION_FIELD_NUMBER: _ClassVar[int]
    REPLY_TO_FIELD_NUMBER: _ClassVar[int]
    MENTIONS_FIELD_NUMBER: _ClassVar[int]
    AS_DOCUMENT_FIELD_NUMBER: _ClassVar[int]
    VIEW_ONCE_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    path: str
    caption: str
    reply_to: str
    mentions: _containers.RepeatedCompositeFieldContainer[_people_pb2.Address]
    as_document: bool
    view_once: bool
    key: str
    def __init__(self, chat_id: _Optional[str] = ..., path: _Optional[str] = ..., caption: _Optional[str] = ..., reply_to: _Optional[str] = ..., mentions: _Optional[_Iterable[_Union[_people_pb2.Address, _Mapping]]] = ..., as_document: _Optional[bool] = ..., view_once: _Optional[bool] = ..., key: _Optional[str] = ...) -> None: ...

class SendSticker(_message.Message):
    __slots__ = ("chat_id", "sticker_id", "reply_to", "key")
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    STICKER_ID_FIELD_NUMBER: _ClassVar[int]
    REPLY_TO_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    sticker_id: str
    reply_to: str
    key: str
    def __init__(self, chat_id: _Optional[str] = ..., sticker_id: _Optional[str] = ..., reply_to: _Optional[str] = ..., key: _Optional[str] = ...) -> None: ...

class SendResult(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class MessageReact(_message.Message):
    __slots__ = ("message_id", "emoji")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    EMOJI_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    emoji: str
    def __init__(self, message_id: _Optional[str] = ..., emoji: _Optional[str] = ...) -> None: ...

class MessageEdit(_message.Message):
    __slots__ = ("message_id", "text")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    text: str
    def __init__(self, message_id: _Optional[str] = ..., text: _Optional[str] = ...) -> None: ...

class MessageRevoke(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class MessageDelete(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class MessageStar(_message.Message):
    __slots__ = ("message_id", "starred")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    STARRED_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    starred: bool
    def __init__(self, message_id: _Optional[str] = ..., starred: _Optional[bool] = ...) -> None: ...

class MessagePin(_message.Message):
    __slots__ = ("message_id", "pinned", "duration_ms")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    PINNED_FIELD_NUMBER: _ClassVar[int]
    DURATION_MS_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    pinned: bool
    duration_ms: int
    def __init__(self, message_id: _Optional[str] = ..., pinned: _Optional[bool] = ..., duration_ms: _Optional[int] = ...) -> None: ...

class MessageForward(_message.Message):
    __slots__ = ("message_id", "chat_ids", "key")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    CHAT_IDS_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    chat_ids: _containers.RepeatedScalarFieldContainer[str]
    key: str
    def __init__(self, message_id: _Optional[str] = ..., chat_ids: _Optional[_Iterable[str]] = ..., key: _Optional[str] = ...) -> None: ...

class MessageForwardResult(_message.Message):
    __slots__ = ("message_ids",)
    MESSAGE_IDS_FIELD_NUMBER: _ClassVar[int]
    message_ids: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, message_ids: _Optional[_Iterable[str]] = ...) -> None: ...

class MessageMarkPlayed(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class MessageRequestFromPhone(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class PollVote(_message.Message):
    __slots__ = ("message_id", "option_indexes")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    OPTION_INDEXES_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    option_indexes: _containers.RepeatedScalarFieldContainer[int]
    def __init__(self, message_id: _Optional[str] = ..., option_indexes: _Optional[_Iterable[int]] = ...) -> None: ...

class MessageText(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class MessageTextResult(_message.Message):
    __slots__ = ("text", "mentions")
    TEXT_FIELD_NUMBER: _ClassVar[int]
    MENTIONS_FIELD_NUMBER: _ClassVar[int]
    text: str
    mentions: _containers.RepeatedCompositeFieldContainer[Mention]
    def __init__(self, text: _Optional[str] = ..., mentions: _Optional[_Iterable[_Union[Mention, _Mapping]]] = ...) -> None: ...

class EventRsvp(_message.Message):
    __slots__ = ("message_id", "response", "extra_guests")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    EXTRA_GUESTS_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    response: Rsvp
    extra_guests: int
    def __init__(self, message_id: _Optional[str] = ..., response: _Optional[_Union[Rsvp, str]] = ..., extra_guests: _Optional[int] = ...) -> None: ...

class GroupJoinInvite(_message.Message):
    __slots__ = ("message_id",)
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    def __init__(self, message_id: _Optional[str] = ...) -> None: ...

class GroupJoinInviteResult(_message.Message):
    __slots__ = ("chat_id",)
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    def __init__(self, chat_id: _Optional[str] = ...) -> None: ...
