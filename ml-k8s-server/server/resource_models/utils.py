import logging
import uuid

json_header = "application/json"


CPU_BASE_MAGNITUDE = 1000

logger = logging.getLogger(__name__)
NB_ADMIN_USER = uuid.UUID(int=0)

k8s_memory_factors = {
    "m": 1 / 1000,  # milli
    "u": 1 / (1000 * 1000),  # micro
    "n": 1 / (1000 * 1000 * 1000),  # nano
    "K": 1000,
    "M": 1000 * 1000,
    "G": 1000 * 1000 * 1000,
    "T": 1000 * 1000 * 1000 * 1000,
    "P": 1000 * 1000 * 1000 * 1000 * 1000,
    "E": 1000 * 1000 * 1000 * 1000 * 1000 * 1000,
    "k": 1024,
    "Ki": 1024,
    "Mi": 1024 * 1024,
    "Gi": 1024 * 1024 * 1024,
    "Ti": 1024 * 1024 * 1024 * 1024,
    "Pi": 1024 * 1024 * 1024 * 1024 * 1024,
    "Ei": 1024 * 1024 * 1024 * 1024 * 1024 * 1024,
}


def convert_memory_unit_to_bytes(unit: str) -> int:
    if [i for i in k8s_memory_factors.keys() if unit.endswith(i)]:
        # unit matched with k8s memory factors
        for key, value in k8s_memory_factors.items():
            if unit.endswith(key):
                standard_unit = int(float(unit.rstrip(key)) * value)
                break
    elif unit.isnumeric():
        standard_unit = int(unit)
    else:
        raise ValueError(f"memory unit is not correct {unit}")
    return standard_unit


def convert_cpu_unit_to_cores(unit: str | float | int) -> float:
    if isinstance(unit, int):
        unit = float(unit)
    if isinstance(unit, str):
        if unit.endswith("m"):
            value = unit.rstrip("m")
            standard_unit: float = int(value) / CPU_BASE_MAGNITUDE
        elif float(unit) or float(unit) == 0:
            standard_unit = round(float(unit), 2)
        else:
            raise ValueError(f"cpu unit is not correct {unit}")
    elif isinstance(unit, float):
        standard_unit = unit
    else:
        raise ValueError(f"cpu unit is not correct {unit}")
    return standard_unit


def normalize_cpu(unit: str | float | int | None) -> float | None:
    decimal_place: int = 2
    if unit is None:
        return None
    if isinstance(unit, int):
        unit = float(unit)
    if isinstance(unit, str):
        if unit.endswith("m"):
            value = unit.rstrip("m")
            standard_unit: float = int(value) / CPU_BASE_MAGNITUDE
        elif float(unit) or float(unit) == 0:
            standard_unit = float(unit)
        else:
            raise ValueError(f"cpu unit is not correct {unit}")
    elif isinstance(unit, float):
        standard_unit = unit
    else:
        raise ValueError(f"cpu unit is not correct {unit}")
    # rounding cores
    rounded_unit: float = round(standard_unit, decimal_place)
    # to convert 0.001 to 0.01
    if rounded_unit == 0 and standard_unit != 0:
        return 1 / (10 ** float(decimal_place))
    return rounded_unit
