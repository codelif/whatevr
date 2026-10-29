from whatevr.v2 import messages_pb2 as _messages_pb2
from whatevr.v2 import people_pb2 as _people_pb2
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class ChatFilter(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CHAT_FILTER_UNSPECIFIED: _ClassVar[ChatFilter]
    CHAT_FILTER_ALL: _ClassVar[ChatFilter]
    CHAT_FILTER_DIRECT: _ClassVar[ChatFilter]
    CHAT_FILTER_GROUPS: _ClassVar[ChatFilter]

class ChatType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CHAT_TYPE_UNSPECIFIED: _ClassVar[ChatType]
    CHAT_TYPE_DIRECT: _ClassVar[ChatType]
    CHAT_TYPE_GROUP: _ClassVar[ChatType]
    CHAT_TYPE_COMMUNITY: _ClassVar[ChatType]
    CHAT_TYPE_NEWSLETTER: _ClassVar[ChatType]
    CHAT_TYPE_BROADCAST: _ClassVar[ChatType]
CHAT_FILTER_UNSPECIFIED: ChatFilter
CHAT_FILTER_ALL: ChatFilter
CHAT_FILTER_DIRECT: ChatFilter
CHAT_FILTER_GROUPS: ChatFilter
CHAT_TYPE_UNSPECIFIED: ChatType
CHAT_TYPE_DIRECT: ChatType
CHAT_TYPE_GROUP: ChatType
CHAT_TYPE_COMMUNITY: ChatType
CHAT_TYPE_NEWSLETTER: ChatType
CHAT_TYPE_BROADCAST: ChatType

class ChatsView(_message.Message):
    __slots__ = ("filter", "archived", "latest", "index", "sort")
    FILTER_FIELD_NUMBER: _ClassVar[int]
    ARCHIVED_FIELD_NUMBER: _ClassVar[int]
    LATEST_FIELD_NUMBER: _ClassVar[int]
    INDEX_FIELD_NUMBER: _ClassVar[int]
    SORT_FIELD_NUMBER: _ClassVar[int]
    filter: ChatFilter
    archived: bool
    latest: _messages_pb2.Latest
    index: int
    sort: bytes
    def __init__(self, filter: _Optional[_Union[ChatFilter, str]] = ..., archived: _Optional[bool] = ..., latest: _Optional[_Union[_messages_pb2.Latest, _Mapping]] = ..., index: _Optional[int] = ..., sort: _Optional[bytes] = ...) -> None: ...

class ChatView(_message.Message):
    __slots__ = ("chat_id",)
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    def __init__(self, chat_id: _Optional[str] = ...) -> None: ...

class ChatRow(_message.Message):
    __slots__ = ("id", "name", "type", "avatar_path", "preview", "last_ms", "unread", "marked_unread", "pinned", "archived", "muted", "mute_end_ms", "history_exhausted", "ephemeral_secs", "read_only", "loading_older")
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    TYPE_FIELD_NUMBER: _ClassVar[int]
    AVATAR_PATH_FIELD_NUMBER: _ClassVar[int]
    PREVIEW_FIELD_NUMBER: _ClassVar[int]
    LAST_MS_FIELD_NUMBER: _ClassVar[int]
    UNREAD_FIELD_NUMBER: _ClassVar[int]
    MARKED_UNREAD_FIELD_NUMBER: _ClassVar[int]
    PINNED_FIELD_NUMBER: _ClassVar[int]
    ARCHIVED_FIELD_NUMBER: _ClassVar[int]
    MUTED_FIELD_NUMBER: _ClassVar[int]
    MUTE_END_MS_FIELD_NUMBER: _ClassVar[int]
    HISTORY_EXHAUSTED_FIELD_NUMBER: _ClassVar[int]
    EPHEMERAL_SECS_FIELD_NUMBER: _ClassVar[int]
    READ_ONLY_FIELD_NUMBER: _ClassVar[int]
    LOADING_OLDER_FIELD_NUMBER: _ClassVar[int]
    id: str
    name: str
    type: ChatType
    avatar_path: str
    preview: ChatPreview
    last_ms: int
    unread: int
    marked_unread: bool
    pinned: bool
    archived: bool
    muted: bool
    mute_end_ms: int
    history_exhausted: bool
    ephemeral_secs: int
    read_only: bool
    loading_older: bool
    def __init__(self, id: _Optional[str] = ..., name: _Optional[str] = ..., type: _Optional[_Union[ChatType, str]] = ..., avatar_path: _Optional[str] = ..., preview: _Optional[_Union[ChatPreview, _Mapping]] = ..., last_ms: _Optional[int] = ..., unread: _Optional[int] = ..., marked_unread: _Optional[bool] = ..., pinned: _Optional[bool] = ..., archived: _Optional[bool] = ..., muted: _Optional[bool] = ..., mute_end_ms: _Optional[int] = ..., history_exhausted: _Optional[bool] = ..., ephemeral_secs: _Optional[int] = ..., read_only: _Optional[bool] = ..., loading_older: _Optional[bool] = ...) -> None: ...

class ChatPreview(_message.Message):
    __slots__ = ("text", "from_me", "status")
    TEXT_FIELD_NUMBER: _ClassVar[int]
    FROM_ME_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    text: str
    from_me: bool
    status: _messages_pb2.MessageStatus
    def __init__(self, text: _Optional[str] = ..., from_me: _Optional[bool] = ..., status: _Optional[_Union[_messages_pb2.MessageStatus, str]] = ...) -> None: ...

class ChatMarkRead(_message.Message):
    __slots__ = ("chat_id", "up_to_message_id")
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    UP_TO_MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    up_to_message_id: str
    def __init__(self, chat_id: _Optional[str] = ..., up_to_message_id: _Optional[str] = ...) -> None: ...

class ChatPin(_message.Message):
    __slots__ = ("chat_id", "pinned")
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    PINNED_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    pinned: bool
    def __init__(self, chat_id: _Optional[str] = ..., pinned: _Optional[bool] = ...) -> None: ...

class ChatArchive(_message.Message):
    __slots__ = ("chat_id", "archived")
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    ARCHIVED_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    archived: bool
    def __init__(self, chat_id: _Optional[str] = ..., archived: _Optional[bool] = ...) -> None: ...

class ChatMute(_message.Message):
    __slots__ = ("chat_id", "muted", "duration_ms")
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    MUTED_FIELD_NUMBER: _ClassVar[int]
    DURATION_MS_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    muted: bool
    duration_ms: int
    def __init__(self, chat_id: _Optional[str] = ..., muted: _Optional[bool] = ..., duration_ms: _Optional[int] = ...) -> None: ...

class ChatTyping(_message.Message):
    __slots__ = ("chat_id", "composing", "recording")
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    COMPOSING_FIELD_NUMBER: _ClassVar[int]
    RECORDING_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    composing: bool
    recording: bool
    def __init__(self, chat_id: _Optional[str] = ..., composing: _Optional[bool] = ..., recording: _Optional[bool] = ...) -> None: ...

class ChatRequestOlder(_message.Message):
    __slots__ = ("chat_id",)
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    def __init__(self, chat_id: _Optional[str] = ...) -> None: ...

class ChatRequestOlderResult(_message.Message):
    __slots__ = ("requested",)
    REQUESTED_FIELD_NUMBER: _ClassVar[int]
    requested: bool
    def __init__(self, requested: _Optional[bool] = ...) -> None: ...

class ChatEnsureDirect(_message.Message):
    __slots__ = ("person",)
    PERSON_FIELD_NUMBER: _ClassVar[int]
    person: _people_pb2.Address
    def __init__(self, person: _Optional[_Union[_people_pb2.Address, _Mapping]] = ...) -> None: ...

class ChatEnsureDirectResult(_message.Message):
    __slots__ = ("chat_id",)
    CHAT_ID_FIELD_NUMBER: _ClassVar[int]
    chat_id: str
    def __init__(self, chat_id: _Optional[str] = ...) -> None: ...
