import re

import bcrypt
from Cryptodome.Cipher import AES

from config import Configs

AES_KEY = Configs.NUDGEBEE_ENCRYPTION_KEY
MEMORY_PATTERN = re.compile(r"^(\d+(?:\.\d+)?)\s*([KMGT]i?B?|B)$", re.IGNORECASE)


def decrypt(encrypted_msg):
    if not encrypted_msg:
        return ""
    key = bytes.fromhex(AES_KEY)
    data = bytes.fromhex(encrypted_msg)
    cipher = AES.new(key, AES.MODE_GCM, data[:12])  # nonce
    dec = cipher.decrypt_and_verify(data[12:-16], data[-16:])  # ciphertext, tag
    return dec.decode("UTF-8")


def validate_key(key: str, hashed_key: str) -> bool:
    """
    Validate a key against a hashed key.

    :param key: Plain text password to validate.
    :param hashed_key: Hashed password to compare against.
    :return: True if the password is valid, False otherwise.
    """
    try:
        # bcrypt requires the password and hash to be in bytes
        return bcrypt.checkpw(key.encode("utf-8"), hashed_key.encode("utf-8"))
    except ValueError:
        # Handle case where hash is not in the expected format
        return False


def parse_size_to_gb(size_str: str) -> float:
    """
    Parse a human-readable size string (e.g. "512MiB", "1.5 TB", "1024")
    and return the size in GiB (i.e. bytes / 2**30).
    """
    size_str = size_str.strip()
    match = MEMORY_PATTERN.match(size_str)

    if match:
        value_str, unit = match.groups()
        value = float(value_str)
        unit = unit.upper()

        # if the user passed "Gi" (or "M", etc.) without the "B", tack it on
        if not unit.endswith("B"):
            unit += "B"

        # number of bytes per unit
        unit_to_bytes = {
            "B": 1,
            "KB": 10**3,
            "KIB": 2**10,
            "MB": 10**6,
            "MIB": 2**20,
            "GB": 10**9,
            "GIB": 2**30,
            "TB": 10**12,
            "TIB": 2**40,
        }

        bytes_ = value * unit_to_bytes[unit]
    else:
        # no unit → treat as bytes
        bytes_ = float(size_str)

    # convert bytes to GiB
    gib = bytes_ / (1024**3)
    return gib


def round_bytes_to_gb(bytes_value: float) -> int:
    gigabyte: int = 1024**3  # 1 GB = 1024^3 bytes
    # Calculate the number of gigabytes
    gb_value: float = bytes_value / gigabyte
    # Round to the nearest whole number
    rounded_gb: int = round(gb_value)
    if rounded_gb == 0:
        return 1
    return rounded_gb
