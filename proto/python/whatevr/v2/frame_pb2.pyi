from whatevr.v2 import account_pb2 as _account_pb2
from whatevr.v2 import chats_pb2 as _chats_pb2
from whatevr.v2 import frontends_pb2 as _frontends_pb2
from whatevr.v2 import groups_pb2 as _groups_pb2
from whatevr.v2 import media_pb2 as _media_pb2
from whatevr.v2 import messages_pb2 as _messages_pb2
from whatevr.v2 import notifications_pb2 as _notifications_pb2
from whatevr.v2 import people_pb2 as _people_pb2
from whatevr.v2 import search_pb2 as _search_pb2
from whatevr.v2 import settings_pb2 as _settings_pb2
from whatevr.v2 import stickers_pb2 as _stickers_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ErrorCode(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    ERROR_CODE_UNSPECIFIED: _ClassVar[ErrorCode]
    ERROR_CODE_INVALID_REQUEST: _ClassVar[ErrorCode]
    ERROR_CODE_UNKNOWN_METHOD: _ClassVar[ErrorCode]
    ERROR_CODE_INVALID_PARAMS: _ClassVar[ErrorCode]
    ERROR_CODE_NOT_FOUND: _ClassVar[ErrorCode]
    ERROR_CODE_NOT_LOGGED_IN: _ClassVar[ErrorCode]
    ERROR_CODE_NOT_CONNECTED: _ClassVar[ErrorCode]
    ERROR_CODE_ALREADY_EXISTS: _ClassVar[ErrorCode]
    ERROR_CODE_EXPIRED: _ClassVar[ErrorCode]
    ERROR_CODE_REJECTED: _ClassVar[ErrorCode]
    ERROR_CODE_IO: _ClassVar[ErrorCode]
    ERROR_CODE_INTERNAL: _ClassVar[ErrorCode]
    ERROR_CODE_GUARDED: _ClassVar[ErrorCode]

class Direction(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    DIRECTION_UNSPECIFIED: _ClassVar[Direction]
    DIRECTION_OLDER: _ClassVar[Direction]
    DIRECTION_NEWER: _ClassVar[Direction]
ERROR_CODE_UNSPECIFIED: ErrorCode
ERROR_CODE_INVALID_REQUEST: ErrorCode
ERROR_CODE_UNKNOWN_METHOD: ErrorCode
ERROR_CODE_INVALID_PARAMS: ErrorCode
ERROR_CODE_NOT_FOUND: ErrorCode
ERROR_CODE_NOT_LOGGED_IN: ErrorCode
ERROR_CODE_NOT_CONNECTED: ErrorCode
ERROR_CODE_ALREADY_EXISTS: ErrorCode
ERROR_CODE_EXPIRED: ErrorCode
ERROR_CODE_REJECTED: ErrorCode
ERROR_CODE_IO: ErrorCode
ERROR_CODE_INTERNAL: ErrorCode
ERROR_CODE_GUARDED: ErrorCode
DIRECTION_UNSPECIFIED: Direction
DIRECTION_OLDER: Direction
DIRECTION_NEWER: Direction

class Frame(_message.Message):
    __slots__ = ("request", "response", "event")
    REQUEST_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    EVENT_FIELD_NUMBER: _ClassVar[int]
    request: Request
    response: Response
    event: Event
    def __init__(self, request: _Optional[_Union[Request, _Mapping]] = ..., response: _Optional[_Union[Response, _Mapping]] = ..., event: _Optional[_Union[Event, _Mapping]] = ...) -> None: ...

class Request(_message.Message):
    __slots__ = ("id", "hello", "subscribe", "extend", "unsubscribe", "session_update", "daemon_reconnect", "account_logout", "frontend_set_default", "link_open", "chat_mark_read", "chat_pin", "chat_archive", "chat_mute", "chat_typing", "chat_request_older", "chat_ensure_direct", "send_text", "send_media", "send_sticker", "message_react", "message_edit", "message_revoke", "message_delete", "message_star", "message_pin", "message_forward", "message_mark_played", "message_request_from_phone", "poll_vote", "event_rsvp", "group_join_invite", "message_text", "media_download", "media_stream", "media_cancel_download", "media_read", "media_fetch_profile_picture", "privacy_set", "preferences_set", "self_set_about", "contact_block", "sticker_favorite", "sticker_download", "sticker_pack_install", "sticker_packs_refresh", "notification_dismiss", "search_chats", "search_messages", "search_stickers", "contact_check_phone", "frontend_list")
    ID_FIELD_NUMBER: _ClassVar[int]
    HELLO_FIELD_NUMBER: _ClassVar[int]
    SUBSCRIBE_FIELD_NUMBER: _ClassVar[int]
    EXTEND_FIELD_NUMBER: _ClassVar[int]
    UNSUBSCRIBE_FIELD_NUMBER: _ClassVar[int]
    SESSION_UPDATE_FIELD_NUMBER: _ClassVar[int]
    DAEMON_RECONNECT_FIELD_NUMBER: _ClassVar[int]
    ACCOUNT_LOGOUT_FIELD_NUMBER: _ClassVar[int]
    FRONTEND_SET_DEFAULT_FIELD_NUMBER: _ClassVar[int]
    LINK_OPEN_FIELD_NUMBER: _ClassVar[int]
    CHAT_MARK_READ_FIELD_NUMBER: _ClassVar[int]
    CHAT_PIN_FIELD_NUMBER: _ClassVar[int]
    CHAT_ARCHIVE_FIELD_NUMBER: _ClassVar[int]
    CHAT_MUTE_FIELD_NUMBER: _ClassVar[int]
    CHAT_TYPING_FIELD_NUMBER: _ClassVar[int]
    CHAT_REQUEST_OLDER_FIELD_NUMBER: _ClassVar[int]
    CHAT_ENSURE_DIRECT_FIELD_NUMBER: _ClassVar[int]
    SEND_TEXT_FIELD_NUMBER: _ClassVar[int]
    SEND_MEDIA_FIELD_NUMBER: _ClassVar[int]
    SEND_STICKER_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_REACT_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_EDIT_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_REVOKE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_DELETE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_STAR_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_PIN_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FORWARD_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_MARK_PLAYED_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_REQUEST_FROM_PHONE_FIELD_NUMBER: _ClassVar[int]
    POLL_VOTE_FIELD_NUMBER: _ClassVar[int]
    EVENT_RSVP_FIELD_NUMBER: _ClassVar[int]
    GROUP_JOIN_INVITE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_TEXT_FIELD_NUMBER: _ClassVar[int]
    MEDIA_DOWNLOAD_FIELD_NUMBER: _ClassVar[int]
    MEDIA_STREAM_FIELD_NUMBER: _ClassVar[int]
    MEDIA_CANCEL_DOWNLOAD_FIELD_NUMBER: _ClassVar[int]
    MEDIA_READ_FIELD_NUMBER: _ClassVar[int]
    MEDIA_FETCH_PROFILE_PICTURE_FIELD_NUMBER: _ClassVar[int]
    PRIVACY_SET_FIELD_NUMBER: _ClassVar[int]
    PREFERENCES_SET_FIELD_NUMBER: _ClassVar[int]
    SELF_SET_ABOUT_FIELD_NUMBER: _ClassVar[int]
    CONTACT_BLOCK_FIELD_NUMBER: _ClassVar[int]
    STICKER_FAVORITE_FIELD_NUMBER: _ClassVar[int]
    STICKER_DOWNLOAD_FIELD_NUMBER: _ClassVar[int]
    STICKER_PACK_INSTALL_FIELD_NUMBER: _ClassVar[int]
    STICKER_PACKS_REFRESH_FIELD_NUMBER: _ClassVar[int]
    NOTIFICATION_DISMISS_FIELD_NUMBER: _ClassVar[int]
    SEARCH_CHATS_FIELD_NUMBER: _ClassVar[int]
    SEARCH_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    SEARCH_STICKERS_FIELD_NUMBER: _ClassVar[int]
    CONTACT_CHECK_PHONE_FIELD_NUMBER: _ClassVar[int]
    FRONTEND_LIST_FIELD_NUMBER: _ClassVar[int]
    id: int
    hello: Hello
    subscribe: Subscribe
    extend: Extend
    unsubscribe: Unsubscribe
    session_update: _account_pb2.SessionUpdate
    daemon_reconnect: _account_pb2.DaemonReconnect
    account_logout: _account_pb2.AccountLogout
    frontend_set_default: _frontends_pb2.FrontendSetDefault
    link_open: _frontends_pb2.LinkOpen
    chat_mark_read: _chats_pb2.ChatMarkRead
    chat_pin: _chats_pb2.ChatPin
    chat_archive: _chats_pb2.ChatArchive
    chat_mute: _chats_pb2.ChatMute
    chat_typing: _chats_pb2.ChatTyping
    chat_request_older: _chats_pb2.ChatRequestOlder
    chat_ensure_direct: _chats_pb2.ChatEnsureDirect
    send_text: _messages_pb2.SendText
    send_media: _messages_pb2.SendMedia
    send_sticker: _messages_pb2.SendSticker
    message_react: _messages_pb2.MessageReact
    message_edit: _messages_pb2.MessageEdit
    message_revoke: _messages_pb2.MessageRevoke
    message_delete: _messages_pb2.MessageDelete
    message_star: _messages_pb2.MessageStar
    message_pin: _messages_pb2.MessagePin
    message_forward: _messages_pb2.MessageForward
    message_mark_played: _messages_pb2.MessageMarkPlayed
    message_request_from_phone: _messages_pb2.MessageRequestFromPhone
    poll_vote: _messages_pb2.PollVote
    event_rsvp: _messages_pb2.EventRsvp
    group_join_invite: _messages_pb2.GroupJoinInvite
    message_text: _messages_pb2.MessageText
    media_download: _media_pb2.MediaDownload
    media_stream: _media_pb2.MediaStream
    media_cancel_download: _media_pb2.MediaCancelDownload
    media_read: _media_pb2.MediaRead
    media_fetch_profile_picture: _media_pb2.MediaFetchProfilePicture
    privacy_set: _settings_pb2.PrivacySet
    preferences_set: _settings_pb2.PreferencesSet
    self_set_about: _people_pb2.SelfSetAbout
    contact_block: _people_pb2.ContactBlock
    sticker_favorite: _stickers_pb2.StickerFavorite
    sticker_download: _stickers_pb2.StickerDownload
    sticker_pack_install: _stickers_pb2.StickerPackInstall
    sticker_packs_refresh: _stickers_pb2.StickerPacksRefresh
    notification_dismiss: _notifications_pb2.NotificationDismiss
    search_chats: _search_pb2.SearchChats
    search_messages: _search_pb2.SearchMessages
    search_stickers: _search_pb2.SearchStickers
    contact_check_phone: _search_pb2.ContactCheckPhone
    frontend_list: _frontends_pb2.FrontendList
    def __init__(self, id: _Optional[int] = ..., hello: _Optional[_Union[Hello, _Mapping]] = ..., subscribe: _Optional[_Union[Subscribe, _Mapping]] = ..., extend: _Optional[_Union[Extend, _Mapping]] = ..., unsubscribe: _Optional[_Union[Unsubscribe, _Mapping]] = ..., session_update: _Optional[_Union[_account_pb2.SessionUpdate, _Mapping]] = ..., daemon_reconnect: _Optional[_Union[_account_pb2.DaemonReconnect, _Mapping]] = ..., account_logout: _Optional[_Union[_account_pb2.AccountLogout, _Mapping]] = ..., frontend_set_default: _Optional[_Union[_frontends_pb2.FrontendSetDefault, _Mapping]] = ..., link_open: _Optional[_Union[_frontends_pb2.LinkOpen, _Mapping]] = ..., chat_mark_read: _Optional[_Union[_chats_pb2.ChatMarkRead, _Mapping]] = ..., chat_pin: _Optional[_Union[_chats_pb2.ChatPin, _Mapping]] = ..., chat_archive: _Optional[_Union[_chats_pb2.ChatArchive, _Mapping]] = ..., chat_mute: _Optional[_Union[_chats_pb2.ChatMute, _Mapping]] = ..., chat_typing: _Optional[_Union[_chats_pb2.ChatTyping, _Mapping]] = ..., chat_request_older: _Optional[_Union[_chats_pb2.ChatRequestOlder, _Mapping]] = ..., chat_ensure_direct: _Optional[_Union[_chats_pb2.ChatEnsureDirect, _Mapping]] = ..., send_text: _Optional[_Union[_messages_pb2.SendText, _Mapping]] = ..., send_media: _Optional[_Union[_messages_pb2.SendMedia, _Mapping]] = ..., send_sticker: _Optional[_Union[_messages_pb2.SendSticker, _Mapping]] = ..., message_react: _Optional[_Union[_messages_pb2.MessageReact, _Mapping]] = ..., message_edit: _Optional[_Union[_messages_pb2.MessageEdit, _Mapping]] = ..., message_revoke: _Optional[_Union[_messages_pb2.MessageRevoke, _Mapping]] = ..., message_delete: _Optional[_Union[_messages_pb2.MessageDelete, _Mapping]] = ..., message_star: _Optional[_Union[_messages_pb2.MessageStar, _Mapping]] = ..., message_pin: _Optional[_Union[_messages_pb2.MessagePin, _Mapping]] = ..., message_forward: _Optional[_Union[_messages_pb2.MessageForward, _Mapping]] = ..., message_mark_played: _Optional[_Union[_messages_pb2.MessageMarkPlayed, _Mapping]] = ..., message_request_from_phone: _Optional[_Union[_messages_pb2.MessageRequestFromPhone, _Mapping]] = ..., poll_vote: _Optional[_Union[_messages_pb2.PollVote, _Mapping]] = ..., event_rsvp: _Optional[_Union[_messages_pb2.EventRsvp, _Mapping]] = ..., group_join_invite: _Optional[_Union[_messages_pb2.GroupJoinInvite, _Mapping]] = ..., message_text: _Optional[_Union[_messages_pb2.MessageText, _Mapping]] = ..., media_download: _Optional[_Union[_media_pb2.MediaDownload, _Mapping]] = ..., media_stream: _Optional[_Union[_media_pb2.MediaStream, _Mapping]] = ..., media_cancel_download: _Optional[_Union[_media_pb2.MediaCancelDownload, _Mapping]] = ..., media_read: _Optional[_Union[_media_pb2.MediaRead, _Mapping]] = ..., media_fetch_profile_picture: _Optional[_Union[_media_pb2.MediaFetchProfilePicture, _Mapping]] = ..., privacy_set: _Optional[_Union[_settings_pb2.PrivacySet, _Mapping]] = ..., preferences_set: _Optional[_Union[_settings_pb2.PreferencesSet, _Mapping]] = ..., self_set_about: _Optional[_Union[_people_pb2.SelfSetAbout, _Mapping]] = ..., contact_block: _Optional[_Union[_people_pb2.ContactBlock, _Mapping]] = ..., sticker_favorite: _Optional[_Union[_stickers_pb2.StickerFavorite, _Mapping]] = ..., sticker_download: _Optional[_Union[_stickers_pb2.StickerDownload, _Mapping]] = ..., sticker_pack_install: _Optional[_Union[_stickers_pb2.StickerPackInstall, _Mapping]] = ..., sticker_packs_refresh: _Optional[_Union[_stickers_pb2.StickerPacksRefresh, _Mapping]] = ..., notification_dismiss: _Optional[_Union[_notifications_pb2.NotificationDismiss, _Mapping]] = ..., search_chats: _Optional[_Union[_search_pb2.SearchChats, _Mapping]] = ..., search_messages: _Optional[_Union[_search_pb2.SearchMessages, _Mapping]] = ..., search_stickers: _Optional[_Union[_search_pb2.SearchStickers, _Mapping]] = ..., contact_check_phone: _Optional[_Union[_search_pb2.ContactCheckPhone, _Mapping]] = ..., frontend_list: _Optional[_Union[_frontends_pb2.FrontendList, _Mapping]] = ...) -> None: ...

class Response(_message.Message):
    __slots__ = ("id", "error", "hello", "subscribe", "done", "chat_ensure_direct", "chat_request_older", "send", "message_forward", "group_join_invite", "message_text", "media_stream", "media_read", "media_fetch_profile_picture", "search_chats", "search_messages", "search_stickers", "contact_check_phone", "frontend_list")
    ID_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    HELLO_FIELD_NUMBER: _ClassVar[int]
    SUBSCRIBE_FIELD_NUMBER: _ClassVar[int]
    DONE_FIELD_NUMBER: _ClassVar[int]
    CHAT_ENSURE_DIRECT_FIELD_NUMBER: _ClassVar[int]
    CHAT_REQUEST_OLDER_FIELD_NUMBER: _ClassVar[int]
    SEND_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FORWARD_FIELD_NUMBER: _ClassVar[int]
    GROUP_JOIN_INVITE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_TEXT_FIELD_NUMBER: _ClassVar[int]
    MEDIA_STREAM_FIELD_NUMBER: _ClassVar[int]
    MEDIA_READ_FIELD_NUMBER: _ClassVar[int]
    MEDIA_FETCH_PROFILE_PICTURE_FIELD_NUMBER: _ClassVar[int]
    SEARCH_CHATS_FIELD_NUMBER: _ClassVar[int]
    SEARCH_MESSAGES_FIELD_NUMBER: _ClassVar[int]
    SEARCH_STICKERS_FIELD_NUMBER: _ClassVar[int]
    CONTACT_CHECK_PHONE_FIELD_NUMBER: _ClassVar[int]
    FRONTEND_LIST_FIELD_NUMBER: _ClassVar[int]
    id: int
    error: Error
    hello: HelloResult
    subscribe: SubscribeResult
    done: Done
    chat_ensure_direct: _chats_pb2.ChatEnsureDirectResult
    chat_request_older: _chats_pb2.ChatRequestOlderResult
    send: _messages_pb2.SendResult
    message_forward: _messages_pb2.MessageForwardResult
    group_join_invite: _messages_pb2.GroupJoinInviteResult
    message_text: _messages_pb2.MessageTextResult
    media_stream: _media_pb2.MediaStreamResult
    media_read: _media_pb2.MediaReadResult
    media_fetch_profile_picture: _media_pb2.MediaFetchProfilePictureResult
    search_chats: _search_pb2.SearchChatsResult
    search_messages: _search_pb2.SearchMessagesResult
    search_stickers: _search_pb2.SearchStickersResult
    contact_check_phone: _search_pb2.ContactCheckPhoneResult
    frontend_list: _frontends_pb2.FrontendListResult
    def __init__(self, id: _Optional[int] = ..., error: _Optional[_Union[Error, _Mapping]] = ..., hello: _Optional[_Union[HelloResult, _Mapping]] = ..., subscribe: _Optional[_Union[SubscribeResult, _Mapping]] = ..., done: _Optional[_Union[Done, _Mapping]] = ..., chat_ensure_direct: _Optional[_Union[_chats_pb2.ChatEnsureDirectResult, _Mapping]] = ..., chat_request_older: _Optional[_Union[_chats_pb2.ChatRequestOlderResult, _Mapping]] = ..., send: _Optional[_Union[_messages_pb2.SendResult, _Mapping]] = ..., message_forward: _Optional[_Union[_messages_pb2.MessageForwardResult, _Mapping]] = ..., group_join_invite: _Optional[_Union[_messages_pb2.GroupJoinInviteResult, _Mapping]] = ..., message_text: _Optional[_Union[_messages_pb2.MessageTextResult, _Mapping]] = ..., media_stream: _Optional[_Union[_media_pb2.MediaStreamResult, _Mapping]] = ..., media_read: _Optional[_Union[_media_pb2.MediaReadResult, _Mapping]] = ..., media_fetch_profile_picture: _Optional[_Union[_media_pb2.MediaFetchProfilePictureResult, _Mapping]] = ..., search_chats: _Optional[_Union[_search_pb2.SearchChatsResult, _Mapping]] = ..., search_messages: _Optional[_Union[_search_pb2.SearchMessagesResult, _Mapping]] = ..., search_stickers: _Optional[_Union[_search_pb2.SearchStickersResult, _Mapping]] = ..., contact_check_phone: _Optional[_Union[_search_pb2.ContactCheckPhoneResult, _Mapping]] = ..., frontend_list: _Optional[_Union[_frontends_pb2.FrontendListResult, _Mapping]] = ...) -> None: ...

class Done(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class Error(_message.Message):
    __slots__ = ("code", "message")
    CODE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    code: ErrorCode
    message: str
    def __init__(self, code: _Optional[_Union[ErrorCode, str]] = ..., message: _Optional[str] = ...) -> None: ...

class Hello(_message.Message):
    __slots__ = ("client", "protocol", "frontend_id")
    CLIENT_FIELD_NUMBER: _ClassVar[int]
    PROTOCOL_FIELD_NUMBER: _ClassVar[int]
    FRONTEND_ID_FIELD_NUMBER: _ClassVar[int]
    client: str
    protocol: int
    frontend_id: str
    def __init__(self, client: _Optional[str] = ..., protocol: _Optional[int] = ..., frontend_id: _Optional[str] = ...) -> None: ...

class HelloResult(_message.Message):
    __slots__ = ("daemon", "version", "protocol", "features", "data_dir", "cache_dir")
    DAEMON_FIELD_NUMBER: _ClassVar[int]
    VERSION_FIELD_NUMBER: _ClassVar[int]
    PROTOCOL_FIELD_NUMBER: _ClassVar[int]
    FEATURES_FIELD_NUMBER: _ClassVar[int]
    DATA_DIR_FIELD_NUMBER: _ClassVar[int]
    CACHE_DIR_FIELD_NUMBER: _ClassVar[int]
    daemon: str
    version: str
    protocol: int
    features: _containers.RepeatedScalarFieldContainer[str]
    data_dir: str
    cache_dir: str
    def __init__(self, daemon: _Optional[str] = ..., version: _Optional[str] = ..., protocol: _Optional[int] = ..., features: _Optional[_Iterable[str]] = ..., data_dir: _Optional[str] = ..., cache_dir: _Optional[str] = ...) -> None: ...

class Event(_message.Message):
    __slots__ = ("update", "open_chat", "media_stream_update", "activate")
    UPDATE_FIELD_NUMBER: _ClassVar[int]
    OPEN_CHAT_FIELD_NUMBER: _ClassVar[int]
    MEDIA_STREAM_UPDATE_FIELD_NUMBER: _ClassVar[int]
    ACTIVATE_FIELD_NUMBER: _ClassVar[int]
    update: ViewUpdate
    open_chat: OpenChat
    media_stream_update: _media_pb2.MediaStreamUpdate
    activate: _frontends_pb2.Activate
    def __init__(self, update: _Optional[_Union[ViewUpdate, _Mapping]] = ..., open_chat: _Optional[_Union[OpenChat, _Mapping]] = ..., media_stream_update: _Optional[_Union[_media_pb2.MediaStreamUpdate, _Mapping]] = ..., activate: _Optional[_Union[_frontends_pb2.Activate, _Mapping]] = ...) -> None: ...

class ViewUpdate(_message.Message):
    __slots__ = ("sub", "reset", "changes", "ready", "position")
    SUB_FIELD_NUMBER: _ClassVar[int]
    RESET_FIELD_NUMBER: _ClassVar[int]
    CHANGES_FIELD_NUMBER: _ClassVar[int]
    READY_FIELD_NUMBER: _ClassVar[int]
    POSITION_FIELD_NUMBER: _ClassVar[int]
    sub: int
    reset: bool
    changes: _containers.RepeatedCompositeFieldContainer[Change]
    ready: Ready
    position: Position
    def __init__(self, sub: _Optional[int] = ..., reset: _Optional[bool] = ..., changes: _Optional[_Iterable[_Union[Change, _Mapping]]] = ..., ready: _Optional[_Union[Ready, _Mapping]] = ..., position: _Optional[_Union[Position, _Mapping]] = ...) -> None: ...

class Position(_message.Message):
    __slots__ = ("count", "offset")
    COUNT_FIELD_NUMBER: _ClassVar[int]
    OFFSET_FIELD_NUMBER: _ClassVar[int]
    count: int
    offset: int
    def __init__(self, count: _Optional[int] = ..., offset: _Optional[int] = ...) -> None: ...

class Change(_message.Message):
    __slots__ = ("upsert", "remove")
    UPSERT_FIELD_NUMBER: _ClassVar[int]
    REMOVE_FIELD_NUMBER: _ClassVar[int]
    upsert: Upsert
    remove: Remove
    def __init__(self, upsert: _Optional[_Union[Upsert, _Mapping]] = ..., remove: _Optional[_Union[Remove, _Mapping]] = ...) -> None: ...

class Upsert(_message.Message):
    __slots__ = ("id", "sort", "connection", "login", "sync", "problem", "chat", "message", "typing", "presence", "receipt", "self", "contact", "group", "group_member", "privacy", "preferences", "blocked", "live_location", "sticker", "sticker_pack", "transfer", "notification", "reaction", "poll_vote", "responder")
    ID_FIELD_NUMBER: _ClassVar[int]
    SORT_FIELD_NUMBER: _ClassVar[int]
    CONNECTION_FIELD_NUMBER: _ClassVar[int]
    LOGIN_FIELD_NUMBER: _ClassVar[int]
    SYNC_FIELD_NUMBER: _ClassVar[int]
    PROBLEM_FIELD_NUMBER: _ClassVar[int]
    CHAT_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    TYPING_FIELD_NUMBER: _ClassVar[int]
    PRESENCE_FIELD_NUMBER: _ClassVar[int]
    RECEIPT_FIELD_NUMBER: _ClassVar[int]
    SELF_FIELD_NUMBER: _ClassVar[int]
    CONTACT_FIELD_NUMBER: _ClassVar[int]
    GROUP_FIELD_NUMBER: _ClassVar[int]
    GROUP_MEMBER_FIELD_NUMBER: _ClassVar[int]
    PRIVACY_FIELD_NUMBER: _ClassVar[int]
    PREFERENCES_FIELD_NUMBER: _ClassVar[int]
    BLOCKED_FIELD_NUMBER: _ClassVar[int]
    LIVE_LOCATION_FIELD_NUMBER: _ClassVar[int]
    STICKER_FIELD_NUMBER: _ClassVar[int]
    STICKER_PACK_FIELD_NUMBER: _ClassVar[int]
    TRANSFER_FIELD_NUMBER: _ClassVar[int]
    NOTIFICATION_FIELD_NUMBER: _ClassVar[int]
    REACTION_FIELD_NUMBER: _ClassVar[int]
    POLL_VOTE_FIELD_NUMBER: _ClassVar[int]
    RESPONDER_FIELD_NUMBER: _ClassVar[int]
    id: str
    sort: bytes
    connection: _account_pb2.ConnectionRow
    login: _account_pb2.LoginRow
    sync: _account_pb2.SyncRow
    problem: _account_pb2.ProblemRow
    chat: _chats_pb2.ChatRow
    message: _messages_pb2.MessageRow
    typing: _people_pb2.TypingRow
    presence: _people_pb2.PresenceRow
    receipt: _messages_pb2.ReceiptRow
    self: _people_pb2.SelfRow
    contact: _people_pb2.ContactRow
    group: _groups_pb2.GroupRow
    group_member: _groups_pb2.GroupMemberRow
    privacy: _settings_pb2.PrivacyRow
    preferences: _settings_pb2.PreferencesRow
    blocked: _people_pb2.BlockedRow
    live_location: _messages_pb2.LiveLocationRow
    sticker: _stickers_pb2.StickerRow
    sticker_pack: _stickers_pb2.StickerPackRow
    transfer: _media_pb2.TransferRow
    notification: _notifications_pb2.NotificationRow
    reaction: _messages_pb2.Reaction
    poll_vote: _messages_pb2.PollVoteRow
    responder: _messages_pb2.Responder
    def __init__(self_, id: _Optional[str] = ..., sort: _Optional[bytes] = ..., connection: _Optional[_Union[_account_pb2.ConnectionRow, _Mapping]] = ..., login: _Optional[_Union[_account_pb2.LoginRow, _Mapping]] = ..., sync: _Optional[_Union[_account_pb2.SyncRow, _Mapping]] = ..., problem: _Optional[_Union[_account_pb2.ProblemRow, _Mapping]] = ..., chat: _Optional[_Union[_chats_pb2.ChatRow, _Mapping]] = ..., message: _Optional[_Union[_messages_pb2.MessageRow, _Mapping]] = ..., typing: _Optional[_Union[_people_pb2.TypingRow, _Mapping]] = ..., presence: _Optional[_Union[_people_pb2.PresenceRow, _Mapping]] = ..., receipt: _Optional[_Union[_messages_pb2.ReceiptRow, _Mapping]] = ..., self: _Optional[_Union[_people_pb2.SelfRow, _Mapping]] = ..., contact: _Optional[_Union[_people_pb2.ContactRow, _Mapping]] = ..., group: _Optional[_Union[_groups_pb2.GroupRow, _Mapping]] = ..., group_member: _Optional[_Union[_groups_pb2.GroupMemberRow, _Mapping]] = ..., privacy: _Optional[_Union[_settings_pb2.PrivacyRow, _Mapping]] = ..., preferences: _Optional[_Union[_settings_pb2.PreferencesRow, _Mapping]] = ..., blocked: _Optional[_Union[_people_pb2.BlockedRow, _Mapping]] = ..., live_location: _Optional[_Union[_messages_pb2.LiveLocationRow, _Mapping]] = ..., sticker: _Optional[_Union[_stickers_pb2.StickerRow, _Mapping]] = ..., sticker_pack: _Optional[_Union[_stickers_pb2.StickerPackRow, _Mapping]] = ..., transfer: _Optional[_Union[_media_pb2.TransferRow, _Mapping]] = ..., notification: _Optional[_Union[_notifications_pb2.NotificationRow, _Mapping]] = ..., reaction: _Optional[_Union[_messages_pb2.Reaction, _Mapping]] = ..., poll_vote: _Optional[_Union[_messages_pb2.PollVoteRow, _Mapping]] = ..., responder: _Optional[_Union[_messages_pb2.Responder, _Mapping]] = ...) -> None: ...

class Remove(_message.Message):
    __slots__ = ("id", "replaced_by")
    ID_FIELD_NUMBER: _ClassVar[int]
    REPLACED_BY_FIELD_NUMBER: _ClassVar[int]
    id: str
    replaced_by: str
    def __init__(self, id: _Optional[str] = ..., replaced_by: _Optional[str] = ...) -> None: ...

class Ready(_message.Message):
    __slots__ = ("exhausted",)
    EXHAUSTED_FIELD_NUMBER: _ClassVar[int]
    exhausted: bool
    def __init__(self, exhausted: _Optional[bool] = ...) -> None: ...

class OpenChat(_message.Message):
    __slots__ = ("chat_id",)
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    def __init__(self, chat_id: _Optional[str] = ...) -> None: ...

class Subscribe(_message.Message):
    __slots__ = ("limit", "connection", "login", "sync", "problems", "chats", "chat", "messages", "typing", "presence", "receipts", "self", "contact", "group", "group_members", "privacy", "preferences", "blocklist", "starred", "pinned", "live_locations", "chat_media", "stickers", "sticker_packs", "sticker_pack", "transfers", "notifications", "reactions", "poll_votes", "event_responses")
    LIMIT_FIELD_NUMBER: _ClassVar[int]
    CONNECTION_FIELD_NUMBER: _ClassVar[int]
    LOGIN_FIELD_NUMBER: _ClassVar[int]
    SYNC_FIELD_NUMBER: _ClassVar[int]
    PROBLEMS_FIELD_NUMBER: _ClassVar[int]
    CHATS_FIELD_NUMBER: _ClassVar[int]
    CHAT_FIELD_NUMBER: _ClassVar[int]
    MESSAGES_FIELD_NUMBER: _ClassVar[int]
    TYPING_FIELD_NUMBER: _ClassVar[int]
    PRESENCE_FIELD_NUMBER: _ClassVar[int]
    RECEIPTS_FIELD_NUMBER: _ClassVar[int]
    SELF_FIELD_NUMBER: _ClassVar[int]
    CONTACT_FIELD_NUMBER: _ClassVar[int]
    GROUP_FIELD_NUMBER: _ClassVar[int]
    GROUP_MEMBERS_FIELD_NUMBER: _ClassVar[int]
    PRIVACY_FIELD_NUMBER: _ClassVar[int]
    PREFERENCES_FIELD_NUMBER: _ClassVar[int]
    BLOCKLIST_FIELD_NUMBER: _ClassVar[int]
    STARRED_FIELD_NUMBER: _ClassVar[int]
    PINNED_FIELD_NUMBER: _ClassVar[int]
    LIVE_LOCATIONS_FIELD_NUMBER: _ClassVar[int]
    CHAT_MEDIA_FIELD_NUMBER: _ClassVar[int]
    STICKERS_FIELD_NUMBER: _ClassVar[int]
    STICKER_PACKS_FIELD_NUMBER: _ClassVar[int]
    STICKER_PACK_FIELD_NUMBER: _ClassVar[int]
    TRANSFERS_FIELD_NUMBER: _ClassVar[int]
    NOTIFICATIONS_FIELD_NUMBER: _ClassVar[int]
    REACTIONS_FIELD_NUMBER: _ClassVar[int]
    POLL_VOTES_FIELD_NUMBER: _ClassVar[int]
    EVENT_RESPONSES_FIELD_NUMBER: _ClassVar[int]
    limit: int
    connection: _account_pb2.ConnectionView
    login: _account_pb2.LoginView
    sync: _account_pb2.SyncView
    problems: _account_pb2.ProblemsView
    chats: _chats_pb2.ChatsView
    chat: _chats_pb2.ChatView
    messages: _messages_pb2.MessagesView
    typing: _people_pb2.TypingView
    presence: _people_pb2.PresenceView
    receipts: _messages_pb2.ReceiptsView
    self: _people_pb2.SelfView
    contact: _people_pb2.ContactView
    group: _groups_pb2.GroupView
    group_members: _groups_pb2.GroupMembersView
    privacy: _settings_pb2.PrivacyView
    preferences: _settings_pb2.PreferencesView
    blocklist: _people_pb2.BlocklistView
    starred: _messages_pb2.StarredView
    pinned: _messages_pb2.PinnedView
    live_locations: _messages_pb2.LiveLocationsView
    chat_media: _messages_pb2.ChatMediaView
    stickers: _stickers_pb2.StickersView
    sticker_packs: _stickers_pb2.StickerPacksView
    sticker_pack: _stickers_pb2.StickerPackView
    transfers: _media_pb2.TransfersView
    notifications: _notifications_pb2.NotificationsView
    reactions: _messages_pb2.ReactionsView
    poll_votes: _messages_pb2.PollVotesView
    event_responses: _messages_pb2.EventResponsesView
    def __init__(self_, limit: _Optional[int] = ..., connection: _Optional[_Union[_account_pb2.ConnectionView, _Mapping]] = ..., login: _Optional[_Union[_account_pb2.LoginView, _Mapping]] = ..., sync: _Optional[_Union[_account_pb2.SyncView, _Mapping]] = ..., problems: _Optional[_Union[_account_pb2.ProblemsView, _Mapping]] = ..., chats: _Optional[_Union[_chats_pb2.ChatsView, _Mapping]] = ..., chat: _Optional[_Union[_chats_pb2.ChatView, _Mapping]] = ..., messages: _Optional[_Union[_messages_pb2.MessagesView, _Mapping]] = ..., typing: _Optional[_Union[_people_pb2.TypingView, _Mapping]] = ..., presence: _Optional[_Union[_people_pb2.PresenceView, _Mapping]] = ..., receipts: _Optional[_Union[_messages_pb2.ReceiptsView, _Mapping]] = ..., self: _Optional[_Union[_people_pb2.SelfView, _Mapping]] = ..., contact: _Optional[_Union[_people_pb2.ContactView, _Mapping]] = ..., group: _Optional[_Union[_groups_pb2.GroupView, _Mapping]] = ..., group_members: _Optional[_Union[_groups_pb2.GroupMembersView, _Mapping]] = ..., privacy: _Optional[_Union[_settings_pb2.PrivacyView, _Mapping]] = ..., preferences: _Optional[_Union[_settings_pb2.PreferencesView, _Mapping]] = ..., blocklist: _Optional[_Union[_people_pb2.BlocklistView, _Mapping]] = ..., starred: _Optional[_Union[_messages_pb2.StarredView, _Mapping]] = ..., pinned: _Optional[_Union[_messages_pb2.PinnedView, _Mapping]] = ..., live_locations: _Optional[_Union[_messages_pb2.LiveLocationsView, _Mapping]] = ..., chat_media: _Optional[_Union[_messages_pb2.ChatMediaView, _Mapping]] = ..., stickers: _Optional[_Union[_stickers_pb2.StickersView, _Mapping]] = ..., sticker_packs: _Optional[_Union[_stickers_pb2.StickerPacksView, _Mapping]] = ..., sticker_pack: _Optional[_Union[_stickers_pb2.StickerPackView, _Mapping]] = ..., transfers: _Optional[_Union[_media_pb2.TransfersView, _Mapping]] = ..., notifications: _Optional[_Union[_notifications_pb2.NotificationsView, _Mapping]] = ..., reactions: _Optional[_Union[_messages_pb2.ReactionsView, _Mapping]] = ..., poll_votes: _Optional[_Union[_messages_pb2.PollVotesView, _Mapping]] = ..., event_responses: _Optional[_Union[_messages_pb2.EventResponsesView, _Mapping]] = ...) -> None: ...

class SubscribeResult(_message.Message):
    __slots__ = ("sub", "anchor_id")
    SUB_FIELD_NUMBER: _ClassVar[int]
    ANCHOR_ID_FIELD_NUMBER: _ClassVar[int]
    sub: int
    anchor_id: str
    def __init__(self, sub: _Optional[int] = ..., anchor_id: _Optional[str] = ...) -> None: ...

class Extend(_message.Message):
    __slots__ = ("sub", "count", "direction")
    SUB_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    DIRECTION_FIELD_NUMBER: _ClassVar[int]
    sub: int
    count: int
    direction: Direction
    def __init__(self, sub: _Optional[int] = ..., count: _Optional[int] = ..., direction: _Optional[_Union[Direction, str]] = ...) -> None: ...

class Unsubscribe(_message.Message):
    __slots__ = ("sub",)
    SUB_FIELD_NUMBER: _ClassVar[int]
    sub: int
    def __init__(self, sub: _Optional[int] = ...) -> None: ...
