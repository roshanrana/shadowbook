from shadowbook.v1 import posting_pb2 as _posting_pb2
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from typing import ClassVar as _ClassVar, Mapping as _Mapping, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class MovementEvent(_message.Message):
    __slots__ = ("message_id", "account_id", "amount", "business_date", "value_date", "posted_at", "kind")
    MESSAGE_ID_FIELD_NUMBER: _ClassVar[int]
    ACCOUNT_ID_FIELD_NUMBER: _ClassVar[int]
    AMOUNT_FIELD_NUMBER: _ClassVar[int]
    BUSINESS_DATE_FIELD_NUMBER: _ClassVar[int]
    VALUE_DATE_FIELD_NUMBER: _ClassVar[int]
    POSTED_AT_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    message_id: str
    account_id: str
    amount: _posting_pb2.Money
    business_date: str
    value_date: str
    posted_at: str
    kind: str
    def __init__(self, message_id: _Optional[str] = ..., account_id: _Optional[str] = ..., amount: _Optional[_Union[_posting_pb2.Money, _Mapping]] = ..., business_date: _Optional[str] = ..., value_date: _Optional[str] = ..., posted_at: _Optional[str] = ..., kind: _Optional[str] = ...) -> None: ...
