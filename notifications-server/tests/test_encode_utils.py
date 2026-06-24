import enum
import json
import uuid
from datetime import datetime
from decimal import Decimal
from unittest.mock import MagicMock, patch

import pytest

from notifications_server.utils.encode_utils import (
    MAX_PORT,
    MIN_PORT,
    ModelEncoder,
    as_dict,
    gen_id,
    is_valid_port,
    singleton,
)


def _encode(obj):
    return json.loads(json.dumps(obj, cls=ModelEncoder))


class TestModelEncoder:
    def test_datetime_serialized_as_isoformat(self):
        dt = datetime(2026, 6, 23, 10, 30, 0)
        assert _encode({"at": dt}) == {"at": dt.isoformat()}

    def test_enum_serialized_as_value(self):
        class Color(enum.Enum):
            RED = "red"

        assert _encode({"c": Color.RED}) == {"c": "red"}

    def test_decimal_serialized_as_float(self):
        assert _encode({"n": Decimal("1.5")}) == {"n": 1.5}

    def test_uuid_serialized_as_str(self):
        u = uuid.uuid4()
        assert _encode({"id": u}) == {"id": str(u)}

    def test_bytes_serialized_as_decoded_str(self):
        assert _encode({"b": b"hello"}) == {"b": "hello"}

    def test_set_serialized_as_list(self):
        result = _encode({"s": {1, 2, 3}})
        assert sorted(result["s"]) == [1, 2, 3]

    def test_object_with_to_dict_uses_to_dict(self):
        class Widget:
            def to_dict(self):
                return {"kind": "widget"}

        assert _encode({"w": Widget()}) == {"w": {"kind": "widget"}}

    def test_strings_round_trip_verbatim(self):
        # json handles str natively, so default() is never invoked for string
        # values — the isinstance(obj, str) branch is effectively dead under
        # json.dumps. Both JSON-looking and plain strings are emitted as-is.
        assert _encode({"payload": '{"a": 1}'}) == {"payload": '{"a": 1}'}
        assert _encode({"name": "plain text"}) == {"name": "plain text"}

    def test_unsupported_type_raises_type_error(self):
        class Opaque:
            pass

        with pytest.raises(TypeError):
            json.dumps({"x": Opaque()}, cls=ModelEncoder)


def test_gen_id_returns_unique_uuid_strings():
    a, b = gen_id(), gen_id()
    assert a != b
    # Parses as a valid UUID and is the canonical string form.
    assert str(uuid.UUID(a)) == a


class TestSingleton:
    def test_returns_same_instance_each_call(self):
        @singleton
        class Service:
            pass

        assert Service() is Service()

    def test_constructor_args_only_applied_on_first_call(self):
        @singleton
        class Holder:
            def __init__(self, value):
                self.value = value

        first = Holder(1)
        second = Holder(2)
        assert first is second
        assert second.value == 1  # second call's arg is ignored


def test_as_dict_maps_column_attrs():
    # Mimic the SQLAlchemy inspect(obj).mapper.column_attrs shape that as_dict
    # walks, without standing up a real mapped model.
    class Row:
        id = 7
        name = "alpha"

    col_id = MagicMock()
    col_id.key = "id"
    col_name = MagicMock()
    col_name.key = "name"

    inspected = MagicMock()
    inspected.mapper.column_attrs = [col_id, col_name]

    with patch("notifications_server.utils.encode_utils.inspect", return_value=inspected):
        assert as_dict(Row()) == {"id": 7, "name": "alpha"}


class TestIsValidPort:
    def test_boundary_values_are_valid(self):
        assert is_valid_port(MIN_PORT) is True
        assert is_valid_port(MAX_PORT) is True

    def test_numeric_string_is_valid(self):
        assert is_valid_port("8080") is True

    def test_out_of_range_is_invalid(self):
        assert is_valid_port(MIN_PORT - 1) is False
        assert is_valid_port(MAX_PORT + 1) is False

    def test_bool_is_rejected(self):
        # bool is an int subclass; int(True) == 1 would otherwise pass.
        assert is_valid_port(True) is False
        assert is_valid_port(False) is False

    def test_non_numeric_string_is_invalid(self):
        assert is_valid_port("not-a-port") is False

    def test_non_int_str_types_are_invalid(self):
        assert is_valid_port(None) is False
        assert is_valid_port(80.0) is False
        assert is_valid_port([80]) is False
