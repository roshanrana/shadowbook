from google.protobuf.internal import containers as _containers
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Iterable as _Iterable, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Money(_message.Message):
    __slots__ = ("minor", "currency", "scale")
    MINOR_FIELD_NUMBER: _ClassVar[int]
    CURRENCY_FIELD_NUMBER: _ClassVar[int]
    SCALE_FIELD_NUMBER: _ClassVar[int]
    minor: int
    currency: str
    scale: int
    def __init__(self, minor: _Optional[int] = ..., currency: _Optional[str] = ..., scale: _Optional[int] = ...) -> None: ...

class Entry(_message.Message):
    __slots__ = ("entry_id", "account_id", "amount")
    ENTRY_ID_FIELD_NUMBER: _ClassVar[int]
    ACCOUNT_ID_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    entry_id: int
    account_id: str
    amount: Money
    def __init__(self, entry_id: _Optional[int] = ..., account_id: _Optional[str] = ..., amount: _Optional[_Union[Money, _Mapping]] = ...) -> None: ...

class PostingEvent(_message.Message):
    __slots__ = ("posting_id", "principal", "kind", "business_date", "value_date", "posted_at", "entries", "reverses_posting_id")
    POSTING_ID_FIELD_NUMBER: _ClassVar[int]
    PRINCIPAL_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    BUSINESS_DATE_FIELD_NUMBER: _ClassVar[int]
    VALUE_DATE_FIELD_NUMBER: _ClassVar[int]
    POSTED_AT_FIELD_NUMBER: _ClassVar[int]
    ENTRIES_FIELD_NUMBER: _ClassVar[int]
    REVERSES_POSTING_ID_FIELD_NUMBER: _ClassVar[int]
    posting_id: str
    principal: str
    kind: str
    business_date: str
    value_date: str
    posted_at: str
    entries: _containers.RepeatedCompositeFieldContainer[Entry]
    reverses_posting_id: str
    def __init__(self, posting_id: _Optional[str] = ..., principal: _Optional[str] = ..., kind: _Optional[str] = ..., business_date: _Optional[str] = ..., value_date: _Optional[str] = ..., posted_at: _Optional[str] = ..., entries: _Optional[_Iterable[_Union[Entry, _Mapping]]] = ..., reverses_posting_id: _Optional[str] = ...) -> None: ...
