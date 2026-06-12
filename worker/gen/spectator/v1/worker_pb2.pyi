from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class WorkerState(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    WORKER_STATE_UNSPECIFIED: _ClassVar[WorkerState]
    STOPPED: _ClassVar[WorkerState]
    STARTING: _ClassVar[WorkerState]
    IDLE: _ClassVar[WorkerState]
    SPECTATING: _ClassVar[WorkerState]
    STOPPING: _ClassVar[WorkerState]

class SteamGuardType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    STEAM_GUARD_TYPE_UNSPECIFIED: _ClassVar[SteamGuardType]
    EMAIL: _ClassVar[SteamGuardType]
    MOBILE: _ClassVar[SteamGuardType]
WORKER_STATE_UNSPECIFIED: WorkerState
STOPPED: WorkerState
STARTING: WorkerState
IDLE: WorkerState
SPECTATING: WorkerState
STOPPING: WorkerState
STEAM_GUARD_TYPE_UNSPECIFIED: SteamGuardType
EMAIL: SteamGuardType
MOBILE: SteamGuardType

class WorkerEvent(_message.Message):
    __slots__ = ("worker_id", "ready", "status_update", "steam_guard", "match_id_resolved", "stream_started", "error", "friends_result")
    WORKER_ID_FIELD_NUMBER: _ClassVar[int]
    READY_FIELD_NUMBER: _ClassVar[int]
    STATUS_UPDATE_FIELD_NUMBER: _ClassVar[int]
    STEAM_GUARD_FIELD_NUMBER: _ClassVar[int]
    MATCH_ID_RESOLVED_FIELD_NUMBER: _ClassVar[int]
    STREAM_STARTED_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    FRIENDS_RESULT_FIELD_NUMBER: _ClassVar[int]
    worker_id: str
    ready: WorkerReady
    status_update: StatusUpdate
    steam_guard: SteamGuardRequired
    match_id_resolved: MatchIdResolved
    stream_started: StreamStarted
    error: ErrorEvent
    friends_result: FriendsResult
    def __init__(self, worker_id: _Optional[str] = ..., ready: _Optional[_Union[WorkerReady, _Mapping]] = ..., status_update: _Optional[_Union[StatusUpdate, _Mapping]] = ..., steam_guard: _Optional[_Union[SteamGuardRequired, _Mapping]] = ..., match_id_resolved: _Optional[_Union[MatchIdResolved, _Mapping]] = ..., stream_started: _Optional[_Union[StreamStarted, _Mapping]] = ..., error: _Optional[_Union[ErrorEvent, _Mapping]] = ..., friends_result: _Optional[_Union[FriendsResult, _Mapping]] = ...) -> None: ...

class WorkerReady(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class StatusUpdate(_message.Message):
    __slots__ = ("state",)
    STATE_FIELD_NUMBER: _ClassVar[int]
    state: WorkerState
    def __init__(self, state: _Optional[_Union[WorkerState, str]] = ...) -> None: ...

class SteamGuardRequired(_message.Message):
    __slots__ = ("guard_type",)
    GUARD_TYPE_FIELD_NUMBER: _ClassVar[int]
    guard_type: SteamGuardType
    def __init__(self, guard_type: _Optional[_Union[SteamGuardType, str]] = ...) -> None: ...

class MatchIdResolved(_message.Message):
    __slots__ = ("match_id", "steam_id")
    MATCH_ID_FIELD_NUMBER: _ClassVar[int]
    STEAM_ID_FIELD_NUMBER: _ClassVar[int]
    match_id: int
    steam_id: str
    def __init__(self, match_id: _Optional[int] = ..., steam_id: _Optional[str] = ...) -> None: ...

class StreamStarted(_message.Message):
    __slots__ = ("srt_url",)
    SRT_URL_FIELD_NUMBER: _ClassVar[int]
    srt_url: str
    def __init__(self, srt_url: _Optional[str] = ...) -> None: ...

class ErrorEvent(_message.Message):
    __slots__ = ("code", "message", "fatal")
    CODE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    FATAL_FIELD_NUMBER: _ClassVar[int]
    code: str
    message: str
    fatal: bool
    def __init__(self, code: _Optional[str] = ..., message: _Optional[str] = ..., fatal: _Optional[bool] = ...) -> None: ...

class FriendsResult(_message.Message):
    __slots__ = ("request_id", "friends", "owner_steam_id", "error")
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    FRIENDS_FIELD_NUMBER: _ClassVar[int]
    OWNER_STEAM_ID_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    request_id: str
    friends: _containers.RepeatedCompositeFieldContainer[Friend]
    owner_steam_id: str
    error: ErrorEvent
    def __init__(self, request_id: _Optional[str] = ..., friends: _Optional[_Iterable[_Union[Friend, _Mapping]]] = ..., owner_steam_id: _Optional[str] = ..., error: _Optional[_Union[ErrorEvent, _Mapping]] = ...) -> None: ...

class Friend(_message.Message):
    __slots__ = ("steam_id", "persona_name", "online", "in_match")
    STEAM_ID_FIELD_NUMBER: _ClassVar[int]
    PERSONA_NAME_FIELD_NUMBER: _ClassVar[int]
    ONLINE_FIELD_NUMBER: _ClassVar[int]
    IN_MATCH_FIELD_NUMBER: _ClassVar[int]
    steam_id: str
    persona_name: str
    online: bool
    in_match: bool
    def __init__(self, steam_id: _Optional[str] = ..., persona_name: _Optional[str] = ..., online: _Optional[bool] = ..., in_match: _Optional[bool] = ...) -> None: ...

class Command(_message.Message):
    __slots__ = ("start_spectate", "stop_spectate", "steam_guard", "list_friends")
    START_SPECTATE_FIELD_NUMBER: _ClassVar[int]
    STOP_SPECTATE_FIELD_NUMBER: _ClassVar[int]
    STEAM_GUARD_FIELD_NUMBER: _ClassVar[int]
    LIST_FRIENDS_FIELD_NUMBER: _ClassVar[int]
    start_spectate: StartSpectate
    stop_spectate: StopSpectate
    steam_guard: SubmitSteamGuardCode
    list_friends: ListFriends
    def __init__(self, start_spectate: _Optional[_Union[StartSpectate, _Mapping]] = ..., stop_spectate: _Optional[_Union[StopSpectate, _Mapping]] = ..., steam_guard: _Optional[_Union[SubmitSteamGuardCode, _Mapping]] = ..., list_friends: _Optional[_Union[ListFriends, _Mapping]] = ...) -> None: ...

class StartSpectate(_message.Message):
    __slots__ = ("session_id", "target_steam_id", "steam_username", "steam_password", "sentry_hash")
    SESSION_ID_FIELD_NUMBER: _ClassVar[int]
    TARGET_STEAM_ID_FIELD_NUMBER: _ClassVar[int]
    STEAM_USERNAME_FIELD_NUMBER: _ClassVar[int]
    STEAM_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    SENTRY_HASH_FIELD_NUMBER: _ClassVar[int]
    session_id: str
    target_steam_id: str
    steam_username: str
    steam_password: str
    sentry_hash: bytes
    def __init__(self, session_id: _Optional[str] = ..., target_steam_id: _Optional[str] = ..., steam_username: _Optional[str] = ..., steam_password: _Optional[str] = ..., sentry_hash: _Optional[bytes] = ...) -> None: ...

class ListFriends(_message.Message):
    __slots__ = ("request_id", "steam_username", "steam_password", "sentry_hash")
    REQUEST_ID_FIELD_NUMBER: _ClassVar[int]
    STEAM_USERNAME_FIELD_NUMBER: _ClassVar[int]
    STEAM_PASSWORD_FIELD_NUMBER: _ClassVar[int]
    SENTRY_HASH_FIELD_NUMBER: _ClassVar[int]
    request_id: str
    steam_username: str
    steam_password: str
    sentry_hash: bytes
    def __init__(self, request_id: _Optional[str] = ..., steam_username: _Optional[str] = ..., steam_password: _Optional[str] = ..., sentry_hash: _Optional[bytes] = ...) -> None: ...

class StopSpectate(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class SubmitSteamGuardCode(_message.Message):
    __slots__ = ("code",)
    CODE_FIELD_NUMBER: _ClassVar[int]
    code: str
    def __init__(self, code: _Optional[str] = ...) -> None: ...
